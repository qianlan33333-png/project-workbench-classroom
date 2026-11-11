"""Bulk read of the fields consumed by the existing operator profile view."""
import collections
from contextlib import closing
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import sys
from types import SimpleNamespace

ROOT = Path("/opt/daru-v3-warehouse-candidate")
sys.path[:0] = ["/opt/workbench-operations-source", str(ROOT / "ops")]

from crm_warehouse import env_file_values

settings = env_file_values("/etc/daru-ops-console.env")
for key in ("PROMPT_DATABASE_URL", "DARU_CANONICAL_INDEX_DIR", "DARU_HXC_UNIFIED_SNAPSHOT_PATH",
            "DARU_HXC_PROFILE_MERGE_APPROVAL_PATH"):
    if settings.get(key):
        os.environ[key] = settings[key]
os.environ["DARU_CANONICAL_READ_MODE"] = "indexed"

import canonical_archive_index as archive_index
import conversation_intelligence as conversation
import directory
import fde_campaign_audience as fde
import hxc_unified_snapshot as hxc
import operator_followup as followup
import operator_contact_view as contact
import published_profile_corrections as corrections
from workbench_profile_overrides import _connect as connect_profiles, _row as override_row


class SerialPool:
    """All inputs are already frozen. Avoid creating threads for each profile."""

    def __init__(self, **_):
        pass

    def __enter__(self):
        return self

    def __exit__(self, *_):
        pass

    def map(self, action, items):
        return map(action, items)


def open_archive(path):
    signature = archive_index.signature(path)
    folder = Path(os.environ.get("DARU_CANONICAL_INDEX_DIR", ROOT / "var/canonical-read-index"))
    return archive_index.connect(folder / (archive_index.cache_key(signature) + ".sqlite"))


def source_snapshot(db):
    return json.loads(db.execute("SELECT value FROM metadata WHERE key='snapshot_id'").fetchone()[0])


def prompt_rows():
    overrides = collections.defaultdict(list)
    history = collections.defaultdict(list)
    profiles = {}
    sessions = collections.defaultdict(list)
    with connect_profiles() as db:
        db.execute("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
        for row in db.execute("""
            SELECT safe_user_ref,field,value,revision,source,updated_by,reason,updated_at
            FROM workbench_profile_overrides ORDER BY safe_user_ref,field
        """):
            overrides[row[0]].append(override_row(row))
        for ref, field, value, at in db.execute("""
            SELECT safe_user_ref,field,new_value,changed_at FROM (
                SELECT safe_user_ref,field,new_value,changed_at,
                       row_number() OVER (PARTITION BY safe_user_ref ORDER BY changed_at DESC,id DESC) AS n
                FROM workbench_profile_override_history
            ) rows WHERE n<=20 ORDER BY safe_user_ref,n
        """):
            history[ref].append({"field": field, "new_value": value, "changed_at": at.isoformat()})
        for ref, snapshot, result, model, at in db.execute("""
            SELECT p.safe_user_ref,p.source_snapshot_id,COALESCE(q.result,p.result),COALESCE(q.model,p.model),p.updated_at
            FROM workbench_conversation_profiles_v1 p
            LEFT JOIN workbench_conversation_profiles_polished_v1 q
              ON q.safe_user_ref=p.safe_user_ref AND q.source_hash=p.source_hash
              AND q.source_snapshot_id=p.source_snapshot_id AND q.result->>'polish_version'=%s
        """, (conversation.POLISH_VERSION,)):
            if result.get("version") == conversation.CURRENT_VERSION:
                profiles[ref] = {"source_snapshot_id": snapshot, **result, "model": model,
                                 "generated_at": at.isoformat()}
        for ref, channel, at, result in db.execute("""
            SELECT safe_user_ref,source_channel,occurred_at,result FROM (
                SELECT safe_user_ref,source_channel,occurred_at,result,
                       row_number() OVER (PARTITION BY safe_user_ref ORDER BY occurred_at DESC) AS n
                FROM workbench_conversation_sessions_v1 WHERE result->>'version'=%s
            ) rows WHERE n<=20 ORDER BY safe_user_ref,n
        """, (conversation.CURRENT_VERSION,)):
            sessions[ref].append({"source_channel": channel, "occurred_at": at, **result})
    return overrides, history, profiles, sessions


def pending_rows():
    path = ROOT / "var/daru.sqlite"
    with closing(sqlite3.connect(path.as_uri() + "?mode=ro", uri=True)) as db:
        return {ref: json.loads(body) for ref, body in db.execute("""
            SELECT canonical_user_id,result_json FROM (
                SELECT canonical_user_id,result_json,
                       row_number() OVER (PARTITION BY canonical_user_id ORDER BY updated_at DESC) AS n
                FROM action_logs WHERE action_type='agent_user_handling' AND status NOT IN ('done','cancelled')
            ) WHERE n=1
        """)}


def relationship_rows(db):
    archive = hxc.current_archive()
    aliases = archive.get("identity_aliases") or {}
    by_target = collections.defaultdict(list)
    by_source = collections.Counter()
    for row in hxc._common_rows(db, "account-links"):
        if row.get("mapping_status") != "exact":
            continue
        target = row.get("workbench_user_ref")
        if target in aliases:
            target = hxc.resolve_reference(target, archive)
        by_target[target].append(row.get("_db_safe_user_ref"))
        by_source[row.get("safe_user_ref")] += 1
    kind = "context-all" if db.execute("SELECT 1 FROM resources WHERE resource='context-all'").fetchone() else "context"
    contexts = collections.defaultdict(list)
    for source, payload in db.execute("SELECT safe_user_ref,payload_json FROM items WHERE resource=?", (kind,)):
        contexts[source].append(json.loads(payload))
    return by_target, by_source, contexts


def export(probe_refs=()):
    canonical = ROOT / "public/data/canonical_users_live.json"
    private = ROOT / "var/private-data/canonical_users_live.json"
    workbench = ROOT / "public/data/workbench_live.json"
    hxc_users = ROOT / "var/private-data/source-snapshots/hxc/hxc-latest.json"
    inputs = (canonical, private, workbench, hxc_users)
    signatures = tuple(archive_index.signature(path) for path in inputs)
    with closing(open_archive(canonical)) as public, closing(open_archive(private)) as internal:
        snapshot = source_snapshot(public)
        if source_snapshot(internal) != snapshot or json.loads(workbench.read_text()).get("snapshot_id") != snapshot:
            raise ValueError("directory_archive_version_mismatch")
        private_positions = {}
        for ref, position in internal.execute("SELECT workbench_ref,position FROM users"):
            if not ref or ref in private_positions:
                raise ValueError("directory_private_identity_mismatch")
            private_positions[ref] = position
        overrides, history, profiles, sessions = prompt_rows()
        pending = pending_rows()
        operator = followup.operator_name()
        numbers, observed = contact._contacts(str(hxc_users), contact.source_signature(hxc_users))
        try:
            with closing(hxc._connect(hxc.database_path())) as db:
                db.execute("BEGIN")
                targets, source_counts, contexts = relationship_rows(db)
        except FileNotFoundError as error:
            raise ValueError("directory_relationship_source_unavailable") from error
        proof = [[ref, item["field"], item["value"], item["revision"], item["reason"], item["updated_at"]]
                 for ref in sorted(overrides) for item in overrides[ref] if item["field"] in corrections.FIELDS]
        correction_revision = hashlib.sha256(json.dumps(proof, ensure_ascii=False,
                                                        separators=(",", ":")).encode()).hexdigest()
        # A missing or stale assessment is the same unavailable source the page
        # reports. Capture that state and do not substitute an older assessment.
        try:
            assessment = fde.read_judgments(ROOT, snapshot, correction_revision).get("users") or {}
        except ValueError:
            assessment = None

        directory.concurrent = SimpleNamespace(futures=SimpleNamespace(ThreadPoolExecutor=SerialPool))
        scanned = set()
        report = collections.Counter()
        missing_sources = collections.Counter()
        old_reader = directory.reader() if probe_refs else None
        field_mismatches = 0
        digest = hashlib.sha256()
        content_digests = {}
        for position, ref, listing, search, body in public.execute("""
            SELECT b.position,b.safe_ref,b.body,b.search,u.body FROM browser b
            JOIN users u ON u.position=b.position ORDER BY b.position
        """):
            if ref in scanned or not ref:
                raise ValueError("directory_duplicate_ref")
            scanned.add(ref)
            if probe_refs and ref not in probe_refs:
                continue
            user = json.loads(body)
            if user.get("safe_user_ref") != ref:
                raise ValueError("directory_identity_mismatch")
            if ref not in private_positions:
                raise ValueError("directory_private_identity_mismatch")
            private_body = internal.execute("SELECT body FROM users WHERE position=?",
                                            (private_positions[ref],)).fetchone()[0]
            private_user = json.loads(private_body)
            account = str(private_user.get("黄小璨用户ID") or "")
            phones = numbers.get(account, set())
            if not account:
                user["operator_contact"] = {"status": "missing"}
            elif len(phones) > 1:
                user["operator_contact"] = {"status": "conflict", "observed_at": observed}
            else:
                phone = next(iter(phones), "")
                user["operator_contact"] = {"phone": phone, "observed_at": observed,
                                            "source": "精确关联的黄小璨账号资料",
                                            "status": "available" if phone else "missing"}
            manual = {entry["field"]: entry for entry in overrides.get(ref, [])}
            current = {}
            for field, names in followup.RAW_FIELDS.items():
                edited = manual.get(field)
                value = edited["value"] if edited else next(
                    (followup.safe_text(private_user.get(name), 1000) for name in names
                     if followup.safe_text(private_user.get(name), 1000)), "")
                current[field] = {"value": value, "revision": edited["revision"] if edited else 0,
                                  "source": "人工修订" if edited else "已发布档案"}
            sources = targets.get(ref, [])
            relation = {"mapped": False}
            if len(sources) > 1 or (sources and source_counts[sources[0]] != 1):
                relation = None
            elif sources:
                values = contexts[sources[0]]
                if len(values) > 1:
                    relation = None
                else:
                    relation = {"mapped": True, "merge_approved": hxc._approved_merge(ref),
                                "merge_rule_version": hxc.MERGE_RULE,
                                "context": hxc._classify_needs({**hxc._brief_context(values[0]), **values[0]}) if values else None}
            frozen = {
                "archive/user": {"user": user, "resolved_ref": ref, "snapshot_id": snapshot},
                "profile-overrides": {"items": overrides.get(ref, []), "history": history.get(ref, [])},
                "conversation-intelligence": {"profile": profiles.get(ref), "sessions": sessions.get(ref, [])},
                "operator-followup": {"operator": operator, "current": current, "pending": pending.get(ref)},
                "hxc-unified/user": relation,
                "fde-campaign-assessment": {"assessment": assessment.get(ref), "snapshot_id": snapshot}
                if assessment is not None else None,
            }

            def read(route, *_, **__):
                value = frozen[route]
                if value is None:
                    raise ValueError("original_supplement_unavailable")
                return value

            detail = directory.profile(read, {"id": ref, "snapshot_id": snapshot})
            if old_reader:
                original = directory.profile(old_reader, {"id": ref, "snapshot_id": snapshot})
                if original != detail:
                    different = sorted(set(g["title"] for g in original.get("groups", [])
                                           if g not in detail.get("groups", [])))
                    print(json.dumps({"different_groups": different, "original_missing": original.get("missingSources"),
                                      "bulk_missing": detail["missingSources"]}, ensure_ascii=False), file=sys.stderr)
                    field_mismatches += 1
            fields = {"position": position, "search": json.loads(search or "[]"),
                      "nickname": directory.nickname(json.loads(listing)),
                      "detailNickname": detail["nickname"], "groups": detail["groups"],
                      "missingSources": detail["missingSources"], "sourceSnapshot": snapshot}
            for group in fields["groups"]:
                for entry in group["fields"]:
                    report[group["title"] + "/" + entry["label"]] += bool(entry["value"])
            missing_sources.update(fields["missingSources"])
            if not probe_refs:
                line = (json.dumps({"id": ref, "fields": fields}, ensure_ascii=False, separators=(",", ":")) + "\n").encode()
                digest.update(line)
                canonical = json.dumps([ref, fields], ensure_ascii=False, sort_keys=True,
                                       separators=(",", ":")).encode()
                content_digests[ref] = hashlib.sha256(canonical).hexdigest()
                sys.stdout.buffer.write(line)
        if scanned != private_positions.keys() or len(scanned) != public.execute("SELECT COUNT(*) FROM browser").fetchone()[0]:
            raise ValueError("directory_population_incomplete")
        if tuple(archive_index.signature(path) for path in inputs) != signatures:
            raise ValueError("directory_source_changed")
    sys.stdout.flush()
    content = hashlib.sha256()
    for ref, value in sorted(content_digests.items()):
        content.update(f"{ref}:{value}\n".encode())
    print(json.dumps({"snapshot": snapshot, "count": len(scanned), "sha256": digest.hexdigest(),
                      "contentSha256": content.hexdigest(),
                      "fields": report, "missingSources": missing_sources,
                      "probeMismatches": field_mismatches}, ensure_ascii=False), file=sys.stderr)


if __name__ == "__main__":
    export(tuple(sys.argv[1:]))
