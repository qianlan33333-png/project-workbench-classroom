#!/usr/bin/env python3
"""Expose the original plan and archive reads to the scoped workbench account."""
import base64
import concurrent.futures
import hashlib
import json
import re
import sqlite3
import stat
import sys
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timedelta
from pathlib import Path
from zoneinfo import ZoneInfo


SAFE = re.compile(r"[A-Za-z0-9_-]{1,96}")
WEEK = re.compile(r"\d{4}-W\d{2}")
RUN = re.compile(r"copybatch-[a-f0-9]{32}")
DIGEST = re.compile(r"[a-f0-9]{64}")
CURSOR = re.compile(r"[A-Za-z0-9_=-]{1,1000}")
ROOT = "http://127.0.0.1:8797/api/local/v3/"
ORIGINAL = Path("/opt/daru-v3-warehouse-candidate")
SHANGHAI = ZoneInfo("Asia/Shanghai")
ORIGINAL_SEND_FLAG = Path("/etc/workbench-operations-source/allow-crm-handoff")
LABELS = {
    "identity": "身份／角色", "industry": "行业", "income_stage": "收入／营收阶段",
    "business_shape": "业务形态", "business_stage": "业务阶段", "business_focus": "业务焦点",
    "pain_point": "主要卡点", "current_need": "当前需求", "business_background": "业务背景",
    "personal_background": "个人背景", "communication_preference": "沟通偏好",
    "ai_application_stage": "AI 应用阶段", "other_important_info": "其他重要信息",
}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def reader(allow_edit=False):
    env = {}
    for line in Path("/etc/daru-ops-console.env").read_text().splitlines():
        if "=" in line and not line.lstrip().startswith("#"):
            key, value = line.split("=", 1)
            env[key] = value.strip().strip('"').strip("'")
    def authorization(user, password):
        return "Basic " + base64.b64encode((env[user] + ":" + env[password]).encode()).decode()

    operator_auth = authorization("DARU_AUTH_USER", "DARU_AUTH_PASSWORD")
    opener = urllib.request.build_opener(NoRedirect())

    def read(path, query=None, payload=None, timeout=18, image=False):
        auth = operator_auth
        if path in ("copy-package-edit", "copy-package-review"):
            if not allow_edit or payload is None:
                raise ValueError("original edit authority is required")
            auth = authorization("DARU_ADMIN_USER", "DARU_ADMIN_PASSWORD")
        request = urllib.request.Request(ROOT + path + ("?" + urllib.parse.urlencode(query) if query else ""),
                                         data=json.dumps(payload).encode() if payload is not None else None,
                                         headers={"Authorization": auth, "Content-Type": "application/json"})
        try:
            with opener.open(request, timeout=timeout) as response:
                if image:
                    limit = 720 * 1024
                    body = response.read(limit + 1)
                    if response.headers.get_content_type() != "image/jpeg" or not body.startswith(b"\xff\xd8\xff") or len(body) > limit:
                        raise ValueError("original card image is unavailable")
                    return {"jpeg": base64.b64encode(body).decode("ascii")}
                limit = (40 if path == "copy-package" else 12) * 1024 * 1024
                data = response.read(limit + 1)
        except urllib.error.HTTPError as err:
            if err.code == 409 and path.startswith("archive/"):
                return {"ok": False, "state": "source_changed"}
            if err.code in (400, 409) and path == "codex-copy-jobs":
                result = json.loads(err.read(8192))
                if payload.get("action") == "test_candidates" and result.get("error") == "current_actionable_package_required":
                    return {"ok": True, "items": []}
            if err.code == 409 and path == "copy-package-edit":
                result = json.loads(err.read(8192))
                if result.get("error_code") in ("copy_package_changed", "copy_already_handed_off", "copy_not_editable"):
                    return {"state": "source_changed"}
            raise
        if len(data) > limit:
            raise ValueError("source budget exceeded")
        result = json.loads(data)
        if result.get("ok") is not True:
            raise ValueError("original source rejected the read")
        return result
    return read


def text(value):
    if isinstance(value, str):
        return value.strip()[:2000]
    if isinstance(value, bool):
        return "是" if value else "否"
    if isinstance(value, (int, float)):
        return str(value)
    if isinstance(value, list):
        return "、".join(text(item) for item in value[:20] if isinstance(item, (str, int, float)))[:2000]
    if isinstance(value, dict):
        return "；".join(filter(None, (text(value.get(k)) for k in ("self_description", "strengths", "growth_edges"))))[:2000]
    return ""


def fields_field(label, value, source, data=None):
    data = data or {}
    references = data.get("evidence_refs") or []
    return {"label": label, "value": text(value), "source": text(data.get("source")) or source,
            "observedAt": text(data.get("observed_at") or data.get("updated_at") or data.get("source_at") or data.get("collected_at")),
            "sourceRef": text(data.get("source_ref") or data.get("evidence_ref") or references),
            "state": text(data.get("evidence_status") or data.get("state") or data.get("status"))}


def nickname(user):
    return next((text(user.get(k)) for k in ("企微昵称", "黄小璨昵称", "CRM展示名", "源姓名（昵称）") if text(user.get(k))), "")


def profiles(read, params):
    query = {"filter": "all", "scope": "all", "limit": 20, "q": params.get("q", "").strip()}
    if len(query["q"]) > 120:
        raise ValueError("query too long")
    for key, pattern in (("cursor", CURSOR), ("snapshot_id", SAFE)):
        value = params.get(key, "")
        if value and not pattern.fullmatch(value):
            raise ValueError("invalid archive cursor")
        if value:
            query[key] = value
    out = read("archive/users", query)
    if out.get("state") == "source_changed":
        return {"state": "source_changed", "records": []}
    records = [{"id": row["safe_user_ref"], "nickname": nickname(row)} for row in out["items"]]
    return {"state": "ready", "records": records, "total": out["total"], "snapshot": out["snapshot_id"], "next": out["next_cursor"]}


def profile_lookup(read, params):
    external = params.get("externalUserId", "")
    if not re.fullmatch(r"[A-Za-z0-9_-]{1,191}", external):
        raise ValueError("invalid external contact identity")
    # Reuse the original center's already-published read index. Never build an
    # index or scan/reimport the source JSON in this latency-sensitive read.
    sys.path.insert(0, str(ORIGINAL / "ops"))
    import canonical_archive_index as index
    source = ORIGINAL / "var/private-data/canonical_users_live.json"
    signature = index.signature(source)
    path = ORIGINAL / "var/canonical-read-index" / (index.cache_key(signature) + ".sqlite")
    from contextlib import closing
    with closing(index.connect(path)) as db:
        metadata = {k: json.loads(v) for k, v in db.execute("SELECT key,value FROM metadata WHERE key IN ('__read_index','snapshot_id')")}
        if metadata.get("__read_index", {}).get("source_signature") != signature:
            raise ValueError("archive index is stale")
        rows = db.execute("SELECT safe_ref FROM users WHERE json_extract(body,'$.external_userid')=? LIMIT 2", (external,)).fetchall()
    if len(rows) > 1:
        raise ValueError("ambiguous external contact identity")
    result = {"externalUserId": external, "records": []}
    if rows:
        doc = profile(read, {"id": rows[0][0], "snapshot_id": metadata["snapshot_id"]})
        if doc.get("state") != "ready" or index.signature(source) != signature:
            raise ValueError("archive generation changed")
        result["records"] = [{"id": doc["id"], "fields": {"nickname": doc.get("nickname", ""),
            "profile_fields": {"snapshot": doc["snapshot"], "groups": doc["groups"], "missingSources": doc.get("missingSources", [])}}}]
    return result


def profile(read, params):
    ref, snapshot = params.get("id", ""), params.get("snapshot_id", "")
    if not SAFE.fullmatch(ref) or (snapshot and not SAFE.fullmatch(snapshot)):
        raise ValueError("invalid archive identity")
    query = {"safe_user_ref": ref}
    out = read("archive/user", {**query, "snapshot_id": snapshot})
    if out.get("state") == "source_changed":
        return {"state": "source_changed"}
    user = out["user"]
    if out["resolved_ref"] != ref or user["safe_user_ref"] != ref:
        raise ValueError("archive identity changed; reload the directory")

    def supplement(route):
        try:
            value = read(route, query, timeout=12)
            if value.get("safe_user_ref", ref) != ref:
                return None
            return value
        except (OSError, ValueError):
            return None

    routes = ("profile-overrides", "conversation-intelligence", "hxc-unified/user", "operator-followup", "fde-campaign-assessment")
    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
        loaded = dict(zip(routes, pool.map(supplement, routes)))
    if (loaded["fde-campaign-assessment"] or {}).get("snapshot_id", out["snapshot_id"]) != out["snapshot_id"]:
        loaded["fde-campaign-assessment"] = None
    overrides = (loaded["profile-overrides"] or {}).get("items") or []
    manual = {row.get("field"): row for row in overrides if isinstance(row, dict) and text(row.get("value"))}
    conversation = loaded["conversation-intelligence"] or {}
    projected = conversation.get("profile") or {}
    owner_followup = loaded["operator-followup"] or {}
    current = owner_followup.get("current") or {}
    candidates = (user.get("_conversationFieldCandidates") or {}).get("fields") or {}
    stored = (user.get("_profileFieldCandidates") or {}).get("candidates") or {}
    provenance = user.get("_profileFactProvenance") or {}
    archive = "上下文中心发布档案"
    contact = user.get("operator_contact") or {}
    hxc = user.get("_hxcSummary") or {}
    wecom = user.get("_wecomInteraction") or {}
    coach = user.get("_hxcCoachInteraction") or {}
    display = nickname(user)
    entitlement = user.get("entitlement_status")
    if entitlement in (None, "", "unknown"):
        entitlement = hxc.get("membership_status")
    relationship_tier = user.get("service_relationship_tier")
    relationship_label = {"entry_active": "299／泛入口在服", "entry_expired": "299／泛入口已过期",
                          "high_ticket_active": "高客单在服", "high_ticket_expired": "高客单已过期"}.get(relationship_tier, relationship_tier)
    basic = [fields_field("手机号", contact.get("phone"), "本人联系方式来源", contact),
             fields_field("企微昵称", user.get("企微昵称"), "企业微信／CRM"),
             fields_field("企微首次添加", user.get("企微首次添加时间"), "CRM 企微客户关系"),
             fields_field("黄小璨昵称", user.get("黄小璨昵称"), "黄小璨账号"),
             fields_field("建议对外称呼", (manual.get("preferred_salutation") or {}).get("value") or user.get("黄小璨昵称") or display, "档案称呼"),
             fields_field("黄小璨 AI 已开通", hxc.get("account_present"), "黄小璨账号", {"observed_at": hxc.get("account_created_at")}),
             fields_field("黄小璨会员权益", entitlement, "黄小璨会员记录"),
             fields_field("当前服务关系", relationship_label, "CRM／HXC 权益"),
             fields_field("企微人工深入会话", interaction(wecom, "deep_sessions_all", "deep_sessions_30d"), "企微双向会话"),
             fields_field("黄小璨 AI 深度咨询会话", interaction(coach, "coach_meaningful_sessions_all", "coach_meaningful_sessions_30d"), "黄小璨 AI coach")]
    raw_keys = {"identity": ("身份阶段",), "industry": ("行业",), "income_stage": ("收入/营收阶段", "收入阶段"),
                "business_shape": ("业务形态",), "business_stage": ("业务阶段",), "business_focus": ("业务焦点",),
                "pain_point": ("核心痛苦场景",), "current_need": ("当前需求",)}
    for key, label in LABELS.items():
        published = next((user[k] for k in raw_keys.get(key, ()) if text(user.get(k))), "")
        observed = provenance.get("identity_stage" if key == "identity" else key) or {}
        edited = candidate_for(key, manual)
        if edited:
            basic.append(fields_field(label, edited["value"], "工作台人工确认", {**edited, "state": "user_confirmed"}))
        elif published:
            basic.append(fields_field(label, published, archive, observed))
        else:
            option = candidate_for(key, current, (projected.get("fields") or projected), candidates, stored)
            if not option:
                option = session_candidate(key, conversation.get("sessions") or [])
            basic.append(fields_field(label, option.get("value"), "互动提炼 · 待核线索", {"state": "observed_candidate", **option}))

    relationship = loaded["hxc-unified/user"] or {}
    historic = []
    historic_labels = {"identity": "身份／角色", "business": "业务形态", "business_stage": "业务阶段",
                       "ai_stage": "AI 应用阶段", "communication_preference": "沟通偏好", "self_view": "历史自我认知",
                       "topic_interests": "关注话题", "industry": "历史行业信息", "income": "历史收入阶段"}
    if relationship.get("mapped") and relationship.get("merge_approved") and relationship.get("merge_rule_version") == "hxc-profile-merge.v1":
        context = relationship.get("context") or {}
        for item in context.get("profile_facts") or []:
            label = historic_labels.get(item.get("field_key"))
            if label and text(item.get("value")):
                historic.append(fields_field(label, item["value"], "黄小璨关系中心 · 精确关联 · 历史资料", item))
        for item in context.get("focuses") or []:
            if item.get("title") and item.get("source_ref"):
                historic.append(fields_field("关系中心关注事项", item["title"], "黄小璨关系中心 · 精确关联", item))

    service = [fields_field("当前服务关系", relationship_label, "CRM／HXC 权益"),
               fields_field("当前产品", user.get("active_product_keys"), "CRM 当前产品"),
               fields_field("历史购买", (user.get("_orderSummary") or {}).get("product_labels"), "CRM 已归档订单"),
               fields_field("服务到期时间", hxc.get("service_expires_at"), "黄小璨会员记录")]
    for order in (user.get("order_history") or [])[:20]:
        service.append(fields_field("历史订单", order.get("product_label") or order.get("product_name"), "原订单来源",
                                    {"observed_at": order.get("actual_paid_at") or order.get("paid_at"), "source_ref": order.get("order_ref")}))
    growth = user.get("operating_growth") or {}
    product = user.get("product_fit") or {}
    diagnosis = [fields_field(label, growth.get(key), archive) for key, label in
                 (("target", "目标用户"), ("potential", "潜力判断"), ("intent", "产品意向"), ("reason", "判断依据"))]
    diagnosis.extend(fields_field(label, user.get(key), archive) for key, label in
                     (("记忆摘要", "互动提炼摘要"), ("最近一次问题", "最近一次问题"), ("诊断问题", "诊断问题")))
    fit = [fields_field(label, product.get(key), "原产品适配记录") for key, label in
           (("recommended_product_key", "当前适配方向"), ("recommendation_status", "适配状态"),
            ("recommendation_reason_summary", "推荐依据"), ("missing_questions", "待核问题"))]
    assessment = (loaded["fde-campaign-assessment"] or {}).get("assessment") or {}
    product_judgment = assessment.get("fde_product_need_recognition") or {}
    for key, label in (("long_term", "长期发展"), ("current_business", "当下业务"), ("ai_learning_and_pitfalls", "AI 学习与避坑")):
        area = product_judgment.get(key) or {}
        for field, caption in (("situation", "用户情况"), ("product_help", "产品能帮什么"), ("reason", "判断依据")):
            if text(area.get(field)):
                fit.append(fields_field(label + " · " + caption, area[field], "本期产品判断", {"evidence_refs": area.get("evidence")}))
    fit.extend(extra_fields(product_judgment, "本期产品判断"))
    followup = [fields_field("经办", owner_followup.get("operator"), "运营跟进记录")] if owner_followup.get("operator") else []
    for key, label in (("current_need", "现在想解决什么"), ("pain_point", "目前卡在哪里"),
                       ("business_focus", "业务焦点"), ("communication_preference", "沟通偏好")):
        entry = current.get(key)
        if isinstance(entry, dict):
            if text(entry.get("value") or entry.get("text")):
                followup.append(fields_field(label, entry.get("value") or entry.get("text"), "运营跟进记录", entry))
        elif text(entry):
            followup.append(fields_field(label, entry, "运营跟进记录"))
    followup.extend(extra_fields(owner_followup.get("pending"), "待处理跟进"))
    sessions = [fields_field("互动提炼 · 待核", row.get("summary"), text(row.get("source_channel")) or "原会话提炼",
                             {"observed_at": row.get("occurred_at"), "state": "observed_candidate"})
                for row in (conversation.get("sessions") or [])[:20] if isinstance(row, dict) and text(row.get("summary"))]
    history = [fields_field(LABELS.get(row.get("field"), "档案修订"), row.get("new_value"), "人工修订历史",
                            {"observed_at": row.get("changed_at")})
               for row in ((loaded["profile-overrides"] or {}).get("history") or [])[:20] if isinstance(row, dict)]
    groups = [("基础信息", basic), ("运营跟进", followup), ("用户诊断", diagnosis), ("商品与服务期", service),
              ("产品适配", fit), ("关系中心历史资料", historic), ("互动提炼记录", sessions), ("档案修订历史", history)]
    return {"state": "ready", "id": ref, "nickname": display, "snapshot": out["snapshot_id"],
            "groups": [{"title": title, "fields": items} for title, items in groups if items],
            "missingSources": [route for route, value in loaded.items() if value is None]}


def candidate_for(key, *sources):
    aliases = {"identity": ("identity", "identity_stage"), "business_shape": ("business_shape", "business_model", "business"),
               "ai_application_stage": ("ai_application_stage", "ai_stage")}
    for source in sources:
        if not isinstance(source, dict):
            continue
        for alias in aliases.get(key, (key,)):
            item = source.get(alias)
            if isinstance(item, list):
                item = next((value for value in item if isinstance(value, dict) and text(value.get("value"))), None)
            if isinstance(item, dict) and text(item.get("value") or item.get("text")):
                return {**item, "value": item.get("value") or item.get("text")}
            if isinstance(item, str) and text(item):
                return {"value": item}
    return {}


def session_candidate(key, sessions):
    categories = {"business_background": ("business_info",), "personal_background": ("personal_info",),
                  "pain_point": ("pain_point",), "communication_preference": ("communication_preference",),
                  "current_need": ("need_business", "need_personal", "need_ai", "recent_goal")}.get(key, ())
    for session in sorted(sessions, key=lambda row: row.get("occurred_at") or "", reverse=True):
        for insight in session.get("insights") or []:
            value = text(insight.get("value"))
            if (insight.get("category") in categories and insight.get("evidence_refs") and 0 < len(value) <= 65
                    and (key != "communication_preference" or insight.get("status") == "user_stated")):
                return {**insight, "value": value, "observed_at": session.get("occurred_at"),
                        "source": (session.get("source_channel") or "原会话") + "提炼 · 待核"}
    return {}


def interaction(source, all_key, month_key):
    if all_key not in source and month_key not in source:
        return ""
    return f"历史 {source.get(all_key, '—')} 次 · 近 30 天 {source.get(month_key, '—')} 次"


def extra_fields(data, source):
    if not isinstance(data, dict):
        return []
    labels = {"summary": "结论", "status": "状态", "reason": "依据", "need": "当前需求", "current_need": "当前需求",
              "blocker": "目前卡点", "next_step": "下一步", "previous_followup": "之前怎么跟的", "owner": "经办",
              "one_question": "待核问题", "one_action": "下一步行动"}
    rows = [fields_field(label, data[key], source) for key, label in labels.items() if key in data and text(data[key])]
    if isinstance(data.get("next_step"), dict):
        rows.extend(extra_fields(data["next_step"], source))
    return rows


def catalog(read, params):
    out = read("work-packages", {"summary": 1}, timeout=70)
    assert WEEK.fullmatch(out["week"])
    package_keys = ("package_id", "label", "scene_label", "strategy_key", "strategy_version", "candidate_count",
                    "actionable_count", "paused_count", "ready_for_generation", "state", "generated_count", "approved_count", "run_id")
    slot_keys = ("strategy_key", "scene", "label", "matched_count", "outbound_count", "waiting_count",
                 "population_label", "waiting_label", "reason")
    return {"week": out["week"], "activityAsOf": out.get("activity_as_of"),
            "packages": [{k: row.get(k) for k in package_keys} for row in out["packages"]],
            "slots": [{k: row.get(k) for k in slot_keys} for row in out["slots"]]}


def package_identity(params):
    package, week = params.get("package", ""), params.get("week", "")
    if not SAFE.fullmatch(package) or not package.startswith("weekpkg-") or not WEEK.fullmatch(week):
        raise ValueError("invalid work package")
    return package, week


def candidate_records(read, package, week):
    # In the original endpoint this fixed action returns the current distinct
    # candidates before the branches that create drafts or request approvals.
    out = read("codex-copy-jobs", payload={"action": "test_candidates", "package_id": package, "week": week}, timeout=70)
    records = [{"id": row["safe_user_ref"], "nickname": text(row.get("name")), "reason": text(row.get("reason"))}
               for row in out["items"]]
    if len(records) != len({row["id"] for row in records}):
        raise ValueError("duplicate identity in the original candidate response")
    revision = hashlib.sha256(json.dumps(records, sort_keys=True, ensure_ascii=False).encode()).hexdigest()
    return records, revision


def members(read, params):
    package, week = package_identity(params)
    after = params.get("after") or "0"
    if not re.fullmatch(r"\d{1,6}", after):
        raise ValueError("invalid work package page")
    records, revision = candidate_records(read, package, week)
    if params.get("revision") not in (None, "", revision):
        return {"state": "source_changed", "records": []}
    start = int(after)
    return {"state": "ready", "records": records[start:start + 20], "total": len(records), "revision": revision,
            "rosterDigest": roster_digest(records),
            "next": str(start + 20) if start + 20 < len(records) else ""}


def roster_digest(records):
    return hashlib.sha256(json.dumps(sorted(row["id"] for row in records)).encode()).hexdigest()


def current_package(read, params):
    package, week = package_identity(params)
    current = catalog(read, {})
    if current["week"] != week:
        raise ValueError("original work package week changed")
    row = next((row for row in current["packages"] if row["package_id"] == package), None)
    if row is None:
        raise ValueError("original work package is unavailable")
    return row


def package_run(read, params, *, preflight=False):
    run = package_binding(read, params)
    detail = read("copy-package", {"run_id": run, "operator_view": 1, "preflight": int(preflight)}, timeout=72)["package"]
    if detail["run_id"] != run or detail["is_sample"]:
        raise ValueError("original run identity changed")
    return detail


def package_binding(read, params):
    package, week = package_identity(params)
    run = params.get("run", "")
    if not RUN.fullmatch(run):
        raise ValueError("invalid original run")
    # The summary binds the run to its parent without loading raw generation
    # requests. A caller may reopen an older run of this exact work package.
    summary = read("copy-packages", {"week": week, "summary": 1}, timeout=72)
    if not any(row.get("run_id") == run and row.get("parent_package_id") == package and row.get("week") == week
               and not row.get("is_sample") and not row.get("test_package") for row in summary["items"]):
        raise ValueError("original run is outside this work package")
    return run


def package_history(read, params):
    week = params.get("week", "")
    if not WEEK.fullmatch(week):
        raise ValueError("invalid original work package week")
    rows = read("copy-packages", {"week": week, "summary": 1}, timeout=72)["items"]
    return {"runs": [
        {"package_id": item["parent_package_id"], "run_id": item["run_id"], "label": item["label"],
         "week": week, "createdAt": item.get("created_at"), "total": item.get("total_count")}
        for item in rows if not item.get("is_sample") and not item.get("test_package")
        and (item.get("parent_package_id") or "").startswith("weekpkg-")
        and RUN.fullmatch(item.get("run_id") or "") and item.get("week") == week
    ]}


def verified_receipts(detail):
    delivery = detail.get("delivery_summary") or {}
    handoff = detail.get("handoff") or {}
    plan = delivery.get("plan_id")
    keys = set(handoff.get("selected_item_keys") or [])
    if (type(plan) is not int or plan <= 0 or not keys or delivery.get("digest") != handoff.get("digest")
            or delivery.get("handoff_id") != handoff.get("id") or delivery.get("requested_count") != len(keys)):
        return {}
    path = ORIGINAL / "var" / "operator-delivery-receipts" / f"plan-{plan}.json"
    try:
        if path.is_symlink() or path.stat().st_size > 2_000_000:
            return {}
        data = json.loads(path.read_text())
    except (OSError, ValueError):
        return {}
    items = data.get("items") or {}
    if (data.get("version") != "verified-push-receipts.v1" or data.get("read_only") is not True
            or data.get("run_id") != detail["run_id"] or data.get("plan_id") != plan or set(items) != keys):
        return {}
    return items


def delivery_label(receipt, handoff_status):
    if receipt.get("provider_delivery_status") == 1 and receipt.get("query_errcode") == 0 and receipt.get("provider_sent_at") and receipt.get("msgid_sha256"):
        return "已发送", text(receipt["provider_sent_at"])
    if receipt.get("local_execution_state") == "final_failed" and not receipt.get("provider_sent_at"):
        return "发送失败", "CRM 已返回明确失败"
    if handoff_status in ("mobile_pending", "provider_accepted"):
        return "待发送", "CRM 已受理，等待手机确认或发送回执"
    if handoff_status in ("unknown", "blocked", "final_failed"):
        return "待发送", "交接需要核对，请查看原 CRM 回执"
    return "待发送", "已确认，等待 CRM 回执"


def already_delivered_slots(read, detail):
    """Use the source ledger only to find the exact duplicate slot's receipt."""
    pending = {item["item_key"]: item.get("safe_user_ref") for item in detail["items"] if not item.get("text")}
    if not pending:
        return {}
    db = ORIGINAL / "var" / "daru.sqlite"
    previous = {}
    try:
        with sqlite3.connect(db.as_uri() + "?mode=ro", uri=True, timeout=2) as conn:
            requests = conn.execute("SELECT item_key,request_json FROM copy_batch_items WHERE run_id=?", (detail["run_id"],))
            for key, value in requests:
                ref = pending.get(key)
                if not ref:
                    continue
                request = json.loads(value)
                slot = (request.get("sku_pack_binding") or {}).get("slot_key")
                if not slot or request.get("safe_user_ref") != ref:
                    continue
                original = conn.execute(
                    "SELECT run_id,item_key FROM sku299_generation_slots WHERE slot_key=? AND user_ref=? AND run_id<>?",
                    (slot, ref, detail["run_id"]),
                ).fetchone()
                if original and RUN.fullmatch(original[0]):
                    previous[key] = original
    except (sqlite3.Error, ValueError):
        return {}
    if not previous:
        return {}
    # Keep the original package read bounded. An unknown or missing receipt
    # leaves the current draft held, never labeled sent.
    out = {}
    for run in list(dict.fromkeys(run for run, _ in previous.values()))[:10]:
        try:
            old = read("copy-package", {"run_id": run, "operator_view": 1}, timeout=4)["package"]
        except (OSError, ValueError):
            continue
        if old["run_id"] != run or old["is_sample"]:
            continue
        receipts = verified_receipts(old)
        old_items = {item["item_key"]: item for item in old["items"]}
        for key, (old_run, old_key) in previous.items():
            item = old_items.get(old_key) or {}
            if old_run == run and old_key in receipts and item.get("safe_user_ref") == pending[key]:
                status, receipt = delivery_label(receipts[old_key], None)
                if status == "已发送":
                    plan = old["delivery_summary"]["plan_id"]
                    out[key] = {"message": item.get("text") or "", "progress": f"同一服务节点已发送 · CRM 计划 {plan} · {receipt}"}
    return out


def formal_status(detail):
    # The original HTTP job status may resume a lost worker. This lookup
    # deliberately reads the persisted result without restarting any action.
    db = ORIGINAL / "var" / "daru.sqlite"
    try:
        with sqlite3.connect(db.as_uri() + "?mode=ro", uri=True, timeout=2) as conn:
            row = conn.execute("""SELECT status FROM action_logs
                WHERE action_type='copy_formal_preparation'
                  AND json_extract(result_json,'$.source_run_id')=?
                  AND json_extract(result_json,'$.source_digest')=?
                ORDER BY updated_at DESC LIMIT 1""", (detail["run_id"], detail["digest"])).fetchone()
    except sqlite3.Error:
        return ""
    return row[0] if row and row[0] in ("running", "failed") else ""


def copy_judgment(item):
    def mapping(value):
        return value if isinstance(value, dict) else {}

    card = mapping(item.get("judgment"))
    service = mapping(card.get("service_decision"))
    material = mapping(card.get("approved_material"))
    support = mapping(card.get("service_support_decision"))
    groups = [
        ("为什么给他发", [card.get("goal"), service.get("this_round_service") or card.get("this_round_service")]),
        ("本次要完成什么", [service.get("concrete_support") or card.get("specific_support"), card.get("single_next_step")]),
        ("我们知道他什么", [mapping(card.get("need_signal")).get("summary"), mapping(card.get("mainline_decision")).get("this_action")]),
        ("为什么推荐这个", [material.get("title"), support.get("focus_mode"), mapping(card.get("content_relevance")).get("reason")]),
        ("这次怎么聊", [card.get("relationship_posture"), mapping(card.get("posture_decision")).get("basis")]),
        ("需要核对什么", [item.get("operator_note"), card.get("operator_review_note"), card.get("must_not_claim")]),
    ]
    facts = []
    background = card.get("background_for_understanding") or card.get("profile_focus")
    for fact in background[:20] if isinstance(background, list) else []:
        if isinstance(fact, dict) and isinstance(fact.get("value"), str):
            status = ("当前已确认" if fact.get("current_confirmed") else "历史背景" if fact.get("historical")
                      else "推测，引用前确认" if fact.get("inferred") else "来源线索，当前是否适用需核对")
            facts.append({"text": text(fact["value"]), "time": text(fact.get("source_at")), "status": status})
    return {"sections": [{"title": title, "lines": [clean for value in values if (clean := text(value))]} for title, values in groups],
            "facts": facts} if card or item.get("operator_note") else None


CARD_IMAGES = ("/api/local/v3/customer-report-archive-image", "/api/local/v3/personal-report-image")


def card_image_url(card):
    url = urllib.parse.urlsplit(card.get("preview_url") or "")
    if card.get("type") == "image" and not (url.scheme or url.netloc or url.fragment) and url.path in CARD_IMAGES:
        return url
    return None


def copy_attachments(item):
    return [{"index": index, "type": text(card.get("type")), "title": text(card.get("title")),
             "purpose": text(card.get("purpose")), "page": text(card.get("page")), "note": text(card.get("note")),
             "verified": card.get("verified") is True, "image": card_image_url(card) is not None}
            for index, card in enumerate(item.get("attachments") or [])]


def package_card(read, params):
    detail = package_run(read, params)
    index = params.get("card", "")
    if detail["digest"] != params.get("digest") or not re.fullmatch(r"\d{1,2}", index):
        raise ValueError("card preview revision changed")
    item = next((x for x in detail["items"] if x["item_key"] == params.get("item")), None)
    cards = (item or {}).get("attachments") or []
    if int(index) >= len(cards) or not (url := card_image_url(cards[int(index)])):
        raise ValueError("card preview is unavailable")
    return read(url.path.removeprefix("/api/local/v3/"), urllib.parse.parse_qsl(url.query), image=True)


def package_detail(read, params, *, detail=None):
    if not params.get("run"):
        run = current_package(read, params).get("run_id")
        if not run:
            return {"state": "not_generated"}
        params = {**params, "run": run}
    detail = detail if detail is not None else package_run(read, params)
    item_key = params.get("item", "")
    if item_key:
        if not SAFE.fullmatch(item_key):
            raise ValueError("invalid original copy item")
        detail = {**detail, "items": [item for item in detail["items"] if item["item_key"] == item_key]}
    delivery = detail.get("delivery_summary") or {}
    handoff = detail.get("handoff") or {}
    review = detail.get("review") or {}
    formal = formal_status(detail)
    receipts = verified_receipts(detail)
    duplicates = already_delivered_slots(read, detail)
    handed_off = set(handoff.get("selected_item_keys") or [])
    reviewed = set(review.get("selected_item_keys") or [])
    rows = []
    for item in detail["items"]:
        key, ref = item["item_key"], item.get("safe_user_ref")
        if not SAFE.fullmatch(key) or not ref or not SAFE.fullmatch(ref):
            continue
        message = item.get("text") or ""
        usable = bool(item.get("usable") and message)
        if key in handed_off:
            status, progress = delivery_label(receipts.get(key) or {}, delivery.get("status"))
        elif key in duplicates:
            status, progress = "已发送", duplicates[key]["progress"]
            message = duplicates[key]["message"]
        elif key in reviewed and review.get("decision") == "approved":
            status, progress = "待发送", "原中心已确认，等待交接"
        elif review:
            status, progress = "已暂缓", "该条未纳入已确认的发送名单"
        elif formal and usable:
            status, progress = ("待核对", "原中心正在校验本人确认") if formal == "running" else ("待核对", "原中心审核失败，请核对原包")
        else:
            status, progress = ("待核对" if usable else "待生成"), text(item.get("note"))
        row = {"key": key, "id": ref, "nickname": text(item.get("name")), "status": status}
        if item_key:
            row.update(message=message, progress=progress, revision=item.get("edit_revision", 0), edited=item.get("edited") is True,
                       editable=bool(usable and item.get("reviewable") and not handoff and formal != "running"),
                       judgment=copy_judgment(item), attachments=copy_attachments(item))
        rows.append(row)
    return {"state": "ready", "run": detail["run_id"], "digest": detail["digest"], "rows": rows,
            **({"rosterDigest": roster_digest(rows)} if not item_key else {}),
            "reviewed": bool(review), "handoff": bool(handoff), "formal": formal, "plan": delivery.get("plan_id")}


def package_prepare(read, params):
    package, week = package_identity(params)
    row = current_package(read, params)
    records, revision = candidate_records(read, package, week)
    day = params.get("day")
    if day and (day != datetime.now(SHANGHAI).date().isoformat() or row["strategy_key"] != params.get("strategy")):
        return {"state": "source_changed"}
    if day and not records:
        if row["actionable_count"] != 0:
            raise ValueError("candidate source is not ready")
        return {"state": "empty"}
    if (not day and revision != params.get("revision")) or not 1 <= len(records) <= 2500:
        return {"state": "source_changed"}
    ids = {item["id"] for item in records}
    run = "" if day else row.get("run_id")
    if run:
        detail = package_run(read, {**params, "run": run}, preflight=True)
        if {x.get("safe_user_ref") for x in detail["items"]} == ids:
            if detail.get("review") or detail.get("handoff"):
                return {"state": "source_changed"}
        else:
            run = ""
    if not run:
        if not row["ready_for_generation"]:
            return {"state": "source_changed"}
        result = read("codex-copy-jobs", payload={"action": "prepare", "package_id": package, "week": week,
                      "selected_user_refs": sorted(ids)}, timeout=100)
        run = result["run_id"]
        if result["count"] != len(ids) or result["automatic_send_allowed"] is not False:
            raise ValueError("original generation scope changed")
    if not RUN.fullmatch(run):
        raise ValueError("invalid original generation run")
    if day:
        scope = daily_scope(read, {**params, "run": run}, records)
        return bind_daily_drafts(read, params, records, run, len(ids), scope)
    return {"state": "ready", "run": run, "total": len(ids)}


def bind_daily_drafts(read, params, records, run, total, scope):
    previous = {(row["reuseRun"], row["reusePackage"], row["reuseWeek"]) for row in scope if "reuseRun" in row}
    binding = {}
    if previous:
        if len(previous) != 1 or any("reuseRun" not in row for row in scope):
            raise ValueError("today's eligible drafts span multiple original runs")
        run, package, week = previous.pop()
        binding = {"package": package, "week": week}
        rebound = {**params, **binding, "run": run, "currentWeek": params["week"], "currentPackage": params["package"]}
        detail = package_run(read, rebound, preflight=True)
        total = len(detail["items"])
        deadlines = {(row["id"], row["reuseItem"]): source_time(row["expiresAt"]) for row in scope}
        scope = [row for row in daily_scope(read, rebound, records, detail=detail) if (row["id"], row["key"]) in deadlines]
        ready = {row["item_key"] for row in detail["items"] if row.get("usable") and row.get("reviewable") and row.get("text")}
        if {(row["id"], row["key"]) for row in scope} != deadlines.keys() or not {row["key"] for row in scope} <= ready:
            raise ValueError("existing original drafts changed")
        for row in scope:
            row["existing"] = True
            end = min(filter(None, (deadlines[(row["id"], row["key"])], source_time(row["expiresAt"]))), default=None)
            row["expiresAt"] = end.isoformat() if end else None
    return {"state": "ready" if scope else "empty", "run": run, "total": total, "scope": scope, **binding}


def source_time(value):
    if not isinstance(value, str) or not value:
        return None
    result = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if result.tzinfo is None:
        raise ValueError("source timestamp has no timezone")
    return result


def newcomer_deadline(request, now):
    binding = request.get("sku_pack_binding") or {}
    if binding.get("primary_identity") == "299":
        paid = source_time(binding.get("paid_at"))
        bounds = {"D2": (24, 96), "D4": (96, 168), "D7": (168, 192)}.get(binding.get("node"))
        if binding.get("node") == "D0" or binding.get("skip_newcomer") is True:
            return None
        if not paid or not bounds or binding.get("skip_newcomer") is not False:
            raise ValueError("invalid original 299 newcomer clock")
        start, end = (paid + timedelta(hours=hour) for hour in bounds)
    elif binding.get("window_start") and binding.get("window_end"):
        start, end = source_time(binding["window_start"]), source_time(binding["window_end"])
    else:
        # Other source policies must publish their clock before being scheduled.
        raise ValueError("unverified original newcomer window")
    if entitlement_end := source_time(binding.get("entitlement_end")):
        end = min(end, entitlement_end)
    return end if start <= now < end else None


def service_window(binding, now):
    start = source_time(binding.get("window_start"))
    limits = [value for value in (source_time(binding.get("window_end")),
                                   source_time(binding.get("entitlement_end"))) if value]
    end = min(limits, default=None)
    return (not start or start <= now) and (not end or now < end), end


def daily_scope(read, params, records, *, now=None, detail=None):
    now = now or datetime.now(SHANGHAI)
    if params["day"] != now.astimezone(SHANGHAI).date().isoformat():
        raise ValueError("original business date expired")
    preparing = detail is None
    detail = detail if detail is not None else package_run(read, params, preflight=True)
    if detail.get("review") or detail.get("handoff") or formal_status(detail):
        return []
    records = {row["id"]: row for row in records}
    if not detail["items"] or (preparing and {row.get("safe_user_ref") for row in detail["items"]} != records.keys()):
        raise ValueError("original run does not match the daily roster")
    scope = []
    db = ORIGINAL / "var" / "daru.sqlite"
    with sqlite3.connect(db.as_uri() + "?mode=ro", uri=True, timeout=4) as conn:
        frozen = conn.execute("SELECT item_key,request_json FROM copy_batch_items WHERE run_id=? ORDER BY item_index,item_key",
                              (detail["run_id"],)).fetchall()
        if len(frozen) != len(detail["items"]) or {row["item_key"] for row in detail["items"]} != {key for key, _ in frozen}:
            raise ValueError("original item identities changed")
        receipts = {}
        for offset, (key, raw) in enumerate(frozen):
            request = json.loads(raw)
            ref = request["safe_user_ref"]
            if ref not in records:
                continue
            record = records[ref]
            if request.get("strategy_key") != params["strategy"]:
                raise ValueError("original strategy changed")
            binding = request.get("sku_pack_binding") or {}
            reuse = {}
            if params["strategy"] == "hxc:onboarding":
                end = newcomer_deadline(request, now)
                if end is None:
                    continue
            else:
                task = conn.execute("""SELECT json_extract(result_json,'$.scheduled_for') FROM action_logs
                    WHERE week=? AND canonical_user_id=? AND action_type='agent_user_action'
                      AND todo_type='hxc_lifecycle' AND status='pending'
                      AND json_extract(result_json,'$.strategy_key')=?""",
                    (params.get("currentWeek") or params["week"], ref, params["strategy"])).fetchone()
                if not task or not re.fullmatch(r"\d{4}-\d{2}-\d{2}", task[0] or ""):
                    raise ValueError("original schedule is unavailable")
                if task[0] > params["day"]:
                    continue
                active, end = service_window(binding, now)
                if not active:
                    continue
            if binding.get("primary_identity") == "299" and binding.get("slot_key"):
                prior = conn.execute("""SELECT run_id,item_key,state,handoff_id,sent_at FROM sku299_generation_slots
                     WHERE slot_key=? AND user_ref=? AND cycle_key=? AND node=? AND state!='cancelled'""",
                     (binding["slot_key"], ref, binding["cycle_key"], binding["node"])).fetchone()
                if prior:
                    old_run, old_key, state, handoff, sent = prior
                    if not RUN.fullmatch(old_run):
                        raise ValueError("invalid original receipt reference")
                    if old_run != detail["run_id"] and old_run not in receipts:
                        receipts[old_run] = read("copy-package", {"run_id": old_run, "operator_view": 1}, timeout=8)["package"]
                    old = receipts.get(old_run, detail)
                    old_item = next((item for item in old["items"] if item["item_key"] == old_key and item.get("safe_user_ref") == ref), None)
                    if old["run_id"] != old_run or old_item is None:
                        raise ValueError("original receipt identity mismatch")
                    if (old_key in verified_receipts(old) or
                            old_key in ((old.get("handoff") or {}).get("selected_item_keys") or []) or
                            state != "draft" or handoff or sent):
                        continue
                    if old_run != detail["run_id"]:
                        if not preparing:
                            continue
                        if old.get("review") or old.get("handoff") or formal_status(old):
                            continue
                        if not (old_item.get("usable") and old_item.get("reviewable") and old_item.get("text")):
                            raise ValueError("existing original draft is unavailable")
                        saved = conn.execute("""SELECT i.request_json,r.config_json FROM copy_batch_items i
                            JOIN copy_batch_runs r ON i.run_id=r.run_id WHERE i.run_id=? AND i.item_key=?""",
                                             (old_run, old_key)).fetchone()
                        if not saved:
                            raise ValueError("existing original draft binding is missing")
                        old_request = json.loads(saved[0])
                        old_config = json.loads(saved[1])
                        old_package = old_config.get("parent_package_id") or old_config.get("package_id") or ""
                        old_week = old_config.get("week") or ""
                        package_identity({"package": old_package, "week": old_week})
                        old_binding = old_request.get("sku_pack_binding") or {}
                        if (type(request.get("strategy_version")) is not int or request["strategy_version"] < 1
                                or not request.get("skill_key")
                                or any(old_request.get(field) != request.get(field) for field in ("strategy_key", "strategy_version", "skill_key"))
                                or any(old_binding.get(field) != binding.get(field) for field in ("slot_key", "cycle_key", "node"))):
                            raise ValueError("existing original draft uses a different node or method")
                        reuse = {"reuseRun": old_run, "reuseItem": old_key, "reusePackage": old_package, "reuseWeek": old_week}
            scope.append({"offset": offset, "key": key, "id": ref, "reason": record["reason"],
                          "expiresAt": end.isoformat() if end else None, "expert": text(request.get("skill_key")),
                          "strategyVersion": request.get("strategy_version"), **reuse})
    return scope


def package_daily(read, params):
    detail = package_run(read, params, preflight=True)
    out = package_detail(read, params, detail=detail)
    if not (out["reviewed"] or out["handoff"] or out["formal"]):
        package, week = package_identity({"package": params.get("currentPackage") or params["package"],
                                          "week": params.get("currentWeek") or params["week"]})
        records, _ = candidate_records(read, package, week)
        keys = {row["key"] for row in daily_scope(read, params, records, detail=detail)}
        out["rows"] = [row for row in out["rows"] if row["key"] in keys]
    return out


def daily_generation_open(params, run):
    now = datetime.now(SHANGHAI)
    if params["day"] != now.date().isoformat():
        return False
    db = ORIGINAL / "var" / "daru.sqlite"
    with sqlite3.connect(db.as_uri() + "?mode=ro", uri=True, timeout=4) as conn:
        if params["action"] == "export":
            row = conn.execute("""SELECT request_json FROM copy_batch_items
                WHERE run_id=? ORDER BY item_index,item_key LIMIT 1 OFFSET ?""", (run, int(params["offset"]))).fetchone()
        else:
            row = conn.execute("SELECT request_json FROM copy_batch_items WHERE run_id=? AND item_key=?",
                               (run, params["item_key"])).fetchone()
        if not row:
            raise ValueError("original daily item changed")
        request = json.loads(row[0])
        if request.get("strategy_key") != params["strategy"]:
            raise ValueError("original daily item changed")
        binding = request.get("sku_pack_binding") or {}
        if params["strategy"] == "hxc:onboarding":
            if newcomer_deadline(request, now) is None:
                return False
        elif not service_window(binding, now)[0]:
            return False
        if binding.get("primary_identity") == "299" and binding.get("slot_key"):
            claim = conn.execute("""SELECT run_id,state,handoff_id,sent_at FROM sku299_generation_slots
                WHERE slot_key=? AND user_ref=? AND cycle_key=? AND node=? AND state!='cancelled'""",
                (binding["slot_key"], request["safe_user_ref"], binding["cycle_key"], binding["node"])).fetchone()
            if claim and (claim[0] != run or claim[1] != "draft" or claim[2] or claim[3]):
                return False
        return True


def package_generate(read, params):
    run = package_binding(read, params)
    if params.get("day") and not daily_generation_open(params, run):
        return {"state": "held", "automatic_send_allowed": False}
    if params.get("action") == "export":
        return read("codex-copy-jobs", payload={"action": "export", "run_id": run, "offset": int(params["offset"]), "limit": 1}, timeout=220)
    if (params.get("action") == "import" and SAFE.fullmatch(params.get("item_key", ""))
            and DIGEST.fullmatch(params.get("request_hash", "")) and DIGEST.fullmatch(params.get("prompt_sha256", ""))):
        return read("codex-copy-jobs", payload={**{key: params[key] for key in ("item_key", "request_hash", "prompt_sha256", "output", "model")},
                    "action": "import", "run_id": run}, timeout=220)
    raise ValueError("invalid original generation operation")


def package_edit(read, params):
    if params.get("day"):
        visible = package_daily(read, params)
        if visible["reviewed"] or visible["handoff"] or visible["formal"] or not visible["rows"]:
            return {"state": "source_changed"}
    else:
        try:
            current = current_package(read, params)
        except ValueError:
            return {"state": "source_changed"}
        if current.get("run_id") != params.get("run"):
            return {"state": "source_changed"}
    detail = package_run(read, params)
    item_key, revision = params.get("item", ""), params.get("revision", "")
    body = params.get("text", "").strip()
    if not SAFE.fullmatch(item_key) or not re.fullmatch(r"\d{1,9}", revision) or not 2 <= len(body) <= 5000:
        raise ValueError("invalid final copy")
    item = next((x for x in detail["items"] if x["item_key"] == item_key), None)
    if (detail.get("handoff") or formal_status(detail) == "running" or not item or not item.get("reviewable")
            or not item.get("usable") or detail["digest"] != params.get("digest") or item.get("edit_revision") != int(revision)):
        return {"state": "source_changed"}
    expires = source_time(params.get("expiresAt"))
    if expires and datetime.now(SHANGHAI) >= expires:
        return {"state": "source_changed"}
    if item["text"] == body:
        return {"state": "saved", "digest": detail["digest"], "revision": int(revision)}
    result = read("copy-package-edit", payload={"run_id": detail["run_id"], "item_key": item_key, "digest": detail["digest"],
                  "expected_revision": int(revision), "text": body}, timeout=40)
    if result.get("state") == "source_changed":
        return result
    if result.get("automatic_send_allowed") is not False or result.get("revision") != int(revision) + 1:
        raise ValueError("original edit result is unavailable")
    return {"state": "saved", "digest": result["digest"], "revision": result["revision"]}


def original_send_binding():
    if not ORIGINAL_SEND_FLAG.exists():
        return None
    status = ORIGINAL_SEND_FLAG.lstat()
    if status.st_uid != 0 or not stat.S_ISREG(status.st_mode) or status.st_mode & 0o022:
        return None
    return json.loads(ORIGINAL_SEND_FLAG.read_text())


def matching_handoff(detail, keys):
    review, handoff = detail.get("review") or {}, detail.get("handoff") or {}
    return (review.get("decision") == "approved" and
            set(review.get("selected_item_keys") or []) == set(keys) and
            handoff.get("digest") == detail["digest"] and
            set(handoff.get("selected_item_keys") or []) == set(keys))


def package_confirm(read, params):
    if (not params.get("staff") or not params.get("transport") or original_send_binding() != {
            "staff": params["staff"], "transport": params["transport"],
    }):
        raise ValueError("original CRM handoff is disabled")
    detail = package_run(read, params, preflight=True)
    if detail["digest"] != params.get("digest"):
        return {"state": "source_changed"}
    keys = json.loads(params["keys"])
    if (not isinstance(keys, list) or not keys or len(keys) > 2500
            or any(not isinstance(key, str) or not SAFE.fullmatch(key) for key in keys)
            or len(keys) != len(set(keys))):
        raise ValueError("invalid review selection")
    review, handoff = detail.get("review") or {}, detail.get("handoff") or {}
    if handoff or review:
        if review.get("decision") != "approved" or set(review.get("selected_item_keys") or []) != set(keys):
            return {"state": "source_changed"}
        if matching_handoff(detail, keys):
            return {"state": "recorded"}
        return {"state": "needs_check"}
    if formal_status(detail):
        return {"state": "needs_check"}
    package, week = package_identity({"package": params.get("currentPackage") or params["package"],
                                      "week": params.get("currentWeek") or params["week"]})
    records, _ = candidate_records(read, package, week)
    current = {row["key"] for row in daily_scope(read, params, records, detail=detail)}
    usable = {row["item_key"] for row in detail["items"] if row.get("usable") and row.get("text")}
    if not set(keys) <= current & usable:
        return {"state": "source_changed"}
    expires = source_time(params.get("expiresAt"))
    if expires and datetime.now(SHANGHAI) >= expires:
        return {"state": "source_changed"}
    result = read("copy-package-review", payload={
        "run_id": detail["run_id"], "digest": detail["digest"], "decision": "approved",
        "excluded_item_keys": sorted({row["item_key"] for row in detail["items"]} - set(keys)),
        "handoff_on_approval": True,
    }, timeout=70)
    if result.get("automatic_send_allowed") is not False:
        raise ValueError("original confirmation outcome unavailable")
    return {"state": "recorded" if matching_handoff({**result, "digest": detail["digest"]}, keys) else "needs_check"}


if __name__ == "__main__":
    execute = sys.argv[1:] == ["execute"]
    limit = 160000 if execute else 8192
    raw = sys.stdin.buffer.read(limit + 1)
    if len(raw) > limit:
        raise ValueError("directory request exceeds the budget")
    value = json.loads(raw)
    params = value.get("params") or {}
    if not isinstance(params, dict) or not all(isinstance(v, str) for v in params.values()):
        raise ValueError("invalid directory parameters")
    views = ({"package_prepare": package_prepare, "package_generate": package_generate,
              "package_edit": package_edit, "package_confirm": package_confirm}
             if execute else {"profiles": profiles, "profile": profile, "profile_lookup": profile_lookup, "catalog": catalog, "members": members,
                              "package": package_detail, "package_daily": package_daily,
                              "package_history": package_history, "package_card": package_card})
    result = views[value["view"]](reader(allow_edit=execute and value["view"] in ("package_edit", "package_confirm")), params)
    print(json.dumps(result, ensure_ascii=False, separators=(",", ":")))
