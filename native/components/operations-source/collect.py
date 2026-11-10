#!/usr/bin/env python3
"""Read a bounded, approved group through the original archive and HXC GETs."""
import argparse
import base64
import datetime
import hashlib
import json
import os
import re
import sqlite3
import tempfile
import urllib.parse
import urllib.request
from pathlib import Path

FIELDS = {
    "对外称呼": ("企微昵称", "CRM展示名", "源姓名（昵称）"),
    "身份／角色": ("身份阶段",),
    "收入／营收阶段": ("收入/营收阶段", "收入阶段"),
    "行业": ("行业",),
    "业务形态": ("业务形态",),
    "业务阶段": ("业务阶段",),
    "业务焦点": ("业务焦点",),
    "主要卡点": ("核心痛苦场景",),
    "当前需求": ("当前需求",),
}
SAFE_REF = re.compile(r"[A-Za-z0-9_-]{1,96}")
BEIJING = datetime.timezone(datetime.timedelta(hours=8))


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def reader(origin, auth):
    url = urllib.parse.urlsplit(origin)
    if (
        url.username or url.password or url.path not in ("", "/")
        or url.query or url.fragment or not url.netloc
        or (url.scheme != "https" and not (url.scheme == "http" and url.hostname == "127.0.0.1"))
    ):
        raise ValueError("invalid source origin")
    opener = urllib.request.build_opener(NoRedirect())

    def read(path, ref):
        query = urllib.parse.urlencode({"safe_user_ref": ref, "limit": 3})
        request = urllib.request.Request(
            origin.rstrip("/") + "/api/local/v3/" + path + "?" + query,
            headers={"Authorization": auth},
        )
        with opener.open(request, timeout=20) as response:
            body = response.read(4 * 1024 * 1024 + 1)
        if len(body) > 4 * 1024 * 1024:
            raise ValueError("source response exceeded the read budget")
        result = json.loads(body)
        if result.get("ok") is not True:
            raise ValueError("source rejected the request")
        return result
    return read


def profile_text(profile, keys):
    for key in keys:
        value = profile.get(key)
        if isinstance(value, str) and value.strip():
            return value.strip()[:500]
    return ""


def select_fields(value, keys):
    return {key: value[key] for key in keys if key in value}


def collect(read, ref):
    archive = read("archive/user", ref)
    profile = archive["user"]
    if archive["resolved_ref"] != ref or profile["safe_user_ref"] != ref:
        raise ValueError("archive resolved a different identity")
    hxc = read("hxc-unified/user", ref)
    if hxc.get("mapped") is not True or not hxc.get("context"):
        raise ValueError("no unique HXC mapping")
    context = hxc["context"]
    if context.get("deleted") or not context.get("account_state", {}).get("created_at"):
        raise ValueError("inactive or incomplete HXC source")
    fields = {label: profile_text(profile, keys) for label, keys in FIELDS.items()}
    evidence = {
        "hxc_account": select_fields(context["account_state"], ("created_at", "first_login_at", "last_login_at", "onboard_status", "assessment_status")),
        "hxc_usage": select_fields(context.get("activity_window", {}), ("window_basis", "source_complete", "user_messages_7d", "content_opens_7d", "content_conversations_7d", "last_user_message_at")),
        "hxc_questionnaire": select_fields(context.get("questionnaire", {}), ("status", "submission_count")),
        "archive_profile": {key: value for key, value in fields.items() if value},
        "archive_contact": {
            "excluded": profile.get("operational_excluded"),
            "last_contact": profile_text(profile, ("CRM最后触达",)),
            "safety": select_fields(profile.get("allocation_safety") or {}, ("contact_policy", "is_do_not_contact", "service_only", "manual_handoff")),
        },
    }
    joined = datetime.datetime.fromisoformat(context["account_state"]["created_at"])
    if joined.tzinfo is None:
        raise ValueError("source account timestamp has no time zone")
    today = datetime.datetime.now(BEIJING).date()
    joined_date = joined.astimezone(BEIJING).date()
    evidence["hxc_account"].update({
        "joined_beijing_date": joined_date.isoformat(),
        "days_since_joining": (today - joined_date).days,
        "days_since_joining_as_of": today.isoformat(),
    })
    summary = profile.get("_orderSummary") or {}
    orders = select_fields(summary, ("verified_purchase_count", "paid_count", "has_refund"))
    if orders and profile.get("order_source_available") is True:
        evidence["archive_orders"] = orders
    materials = read("hxc-unified/materials", ref)
    if materials.get("mapped") is True:
        for item in materials.get("items", [])[:3]:
            title, summary = item.get("title"), item.get("content_summary")
            if title and summary:
                key = "material_" + str(len([k for k in evidence if k.startswith("material_")]) + 1)
                evidence[key] = {"title": str(title)[:160], "summary": str(summary)[:600], "version": item.get("content_version")}
    return {"id": ref, "fields": {
        "nickname": fields["对外称呼"], "profile_fields": fields, "evidence": evidence,
        "source_watermarks": {"archive": archive["snapshot_id"], "hxc": hxc["checked_at"]},
    }}


def discover_exact(snapshot, limit):
    uri = Path(snapshot).resolve().as_uri() + "?mode=ro"
    with sqlite3.connect(uri, uri=True) as db:
        db.execute("PRAGMA query_only=ON")
        rows = db.execute("""
            SELECT json_extract(l.payload_json,'$.workbench_user_ref') AS ref
            FROM items c JOIN items l ON c.safe_user_ref=l.safe_user_ref
            WHERE c.resource='context-all' AND l.resource='account-links'
              AND json_extract(l.payload_json,'$.mapping_status')='exact'
              AND length(ref)>0
            GROUP BY ref HAVING count(*)=1
            ORDER BY max(json_extract(c.payload_json,'$.account_state.created_at')) DESC,ref
            LIMIT ?
        """, (limit,))
        return [row[0] for row in rows]


def approved_refs(path, limit):
    refs = path.read_text().split()
    if not refs or len(refs) > limit or len(set(refs)) != len(refs) or not all(SAFE_REF.fullmatch(ref) for ref in refs):
        raise ValueError("invalid or out of scope archive references")
    return refs


def authorized_body(body, path, limit):
    approved = set(approved_refs(path, limit))
    records = json.loads(body)["records"]
    if not records or any(row["id"] not in approved for row in records):
        raise ValueError("source operation no longer within the approved references")
    return body


def main():
    args = argparse.ArgumentParser(description=__doc__)
    source = args.add_mutually_exclusive_group(required=True)
    source.add_argument("--refs", type=Path, help="one approved archive ref per line")
    source.add_argument("--discover", type=Path, help="operator-only pilot selection from the original HXC snapshot")
    args.add_argument("--limit", type=int, default=12)
    args.add_argument("--operator-env", type=Path, help="read existing local operator credentials on the standard server")
    args.add_argument("--operation-key")
    args.add_argument("--state-dir", type=Path)
    opts = args.parse_args()
    if not 1 <= opts.limit <= 50:
        args.error("limit must be 1–50")
    cached = None
    if opts.operation_key or opts.state_dir:
        if not opts.state_dir or not SAFE_REF.fullmatch(opts.operation_key or "") or not opts.refs:
            args.error("machine refresh requires an operation key, state directory and approved references")
        cached = opts.state_dir / (opts.operation_key + ".json")
        if cached.is_file():
            print(authorized_body(cached.read_text(), opts.refs, opts.limit))
            return
    origin = os.environ.get("WORKBENCH_OPERATIONS_ARCHIVE_ORIGIN", "http://127.0.0.1:8797")
    auth = os.environ.get("WORKBENCH_OPERATIONS_ARCHIVE_AUTH")
    if opts.operator_env:
        if origin != "http://127.0.0.1:8797":
            args.error("the standard server operator credentials are local only")
        env = {}
        for line in opts.operator_env.read_text().splitlines():
            if "=" in line and not line.lstrip().startswith("#"):
                key, value = line.split("=", 1)
                env[key] = value.strip().strip('"').strip("'")
        credentials = env["DARU_AUTH_USER"] + ":" + env["DARU_AUTH_PASSWORD"]
        auth = "Basic " + base64.b64encode(credentials.encode()).decode()
    if not auth:
        args.error("an authenticated source is required")
    refs = approved_refs(opts.refs, opts.limit) if opts.refs else discover_exact(opts.discover, opts.limit)
    if not refs or len(refs) > opts.limit or len(set(refs)) != len(refs) or not all(SAFE_REF.fullmatch(ref) for ref in refs):
        args.error("invalid or out of scope archive references")
    read = reader(origin, auth)
    # Publish a complete bundle only after every original lookup succeeded.
    result = [collect(read, ref) for ref in refs]
    versions = {row["fields"]["source_watermarks"]["archive"] for row in result}
    hxc_versions = {row["fields"]["source_watermarks"]["hxc"] for row in result}
    if len(versions) != 1 or len(hxc_versions) != 1:
        raise ValueError("source publication changed during collection")
    if cached:
        data = json.dumps(result, ensure_ascii=False, separators=(",", ":"))
        result_body = json.dumps({
            "watermark": hashlib.sha256(data.encode()).hexdigest(), "records": result,
        }, ensure_ascii=False, separators=(",", ":"))
        if len(result_body.encode()) > 900_000:
            raise ValueError("source output budget exceeded")
        authorized_body(result_body, opts.refs, opts.limit)
        fd, temp = tempfile.mkstemp(dir=opts.state_dir)
        try:
            with os.fdopen(fd, "w") as handle:
                handle.write(result_body)
                handle.flush()
                os.fsync(handle.fileno())
            try:
                os.link(temp, cached)
            except FileExistsError:
                result_body = cached.read_text()
        finally:
            os.unlink(temp)
        print(authorized_body(result_body, opts.refs, opts.limit))
        return
    for row in result:
        print(json.dumps(row, ensure_ascii=False, separators=(",", ":")))


if __name__ == "__main__":
    main()
