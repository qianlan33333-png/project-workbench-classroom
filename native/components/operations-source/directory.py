#!/usr/bin/env python3
"""Expose the original plan and archive reads to the scoped workbench account."""
import base64
import concurrent.futures
import hashlib
import json
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path


SAFE = re.compile(r"[A-Za-z0-9_-]{1,96}")
WEEK = re.compile(r"\d{4}-W\d{2}")
CURSOR = re.compile(r"[A-Za-z0-9_=-]{1,1000}")
ROOT = "http://127.0.0.1:8797/api/local/v3/"
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


def reader():
    env = {}
    for line in Path("/etc/daru-ops-console.env").read_text().splitlines():
        if "=" in line and not line.lstrip().startswith("#"):
            key, value = line.split("=", 1)
            env[key] = value.strip().strip('"').strip("'")
    auth = base64.b64encode((env["DARU_AUTH_USER"] + ":" + env["DARU_AUTH_PASSWORD"]).encode()).decode()
    opener = urllib.request.build_opener(NoRedirect())

    def read(path, query=None, payload=None, timeout=18):
        request = urllib.request.Request(ROOT + path + ("?" + urllib.parse.urlencode(query) if query else ""),
                                         data=json.dumps(payload).encode() if payload is not None else None,
                                         headers={"Authorization": "Basic " + auth, "Content-Type": "application/json"})
        try:
            with opener.open(request, timeout=timeout) as response:
                data = response.read(12 * 1024 * 1024 + 1)
        except urllib.error.HTTPError as err:
            if err.code == 409 and path.startswith("archive/"):
                return {"ok": False, "state": "source_changed"}
            if err.code == 400 and path == "codex-copy-jobs":
                result = json.loads(err.read(8192))
                if result.get("error") == "current_actionable_package_required":
                    return {"ok": True, "items": []}
            raise
        if len(data) > 12 * 1024 * 1024:
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
                    "actionable_count", "paused_count", "ready_for_generation", "state", "generated_count", "approved_count")
    slot_keys = ("strategy_key", "scene", "label", "matched_count", "outbound_count", "waiting_count",
                 "population_label", "waiting_label", "reason")
    return {"week": out["week"], "activityAsOf": out.get("activity_as_of"),
            "packages": [{k: row.get(k) for k in package_keys} for row in out["packages"]],
            "slots": [{k: row.get(k) for k in slot_keys} for row in out["slots"]]}


def members(read, params):
    package, week = params.get("package", ""), params.get("week", "")
    if not SAFE.fullmatch(package) or not package.startswith("weekpkg-") or not WEEK.fullmatch(week):
        raise ValueError("invalid work package")
    after = params.get("after") or "0"
    if not re.fullmatch(r"\d{1,6}", after):
        raise ValueError("invalid work package page")
    # In the original endpoint this fixed action returns the current distinct
    # candidates before the branches that create drafts or request approvals.
    out = read("codex-copy-jobs", payload={"action": "test_candidates", "package_id": package, "week": week}, timeout=70)
    records = [{"id": row["safe_user_ref"], "nickname": text(row.get("name")), "reason": text(row.get("reason"))}
               for row in out["items"]]
    if len(records) != len({row["id"] for row in records}):
        raise ValueError("duplicate identity in the original candidate response")
    revision = hashlib.sha256(json.dumps(records, sort_keys=True, ensure_ascii=False).encode()).hexdigest()
    if params.get("revision") not in (None, "", revision):
        return {"state": "source_changed", "records": []}
    start = int(after)
    return {"state": "ready", "records": records[start:start + 20], "total": len(records), "revision": revision,
            "next": str(start + 20) if start + 20 < len(records) else ""}


if __name__ == "__main__":
    raw = sys.stdin.buffer.read(8193)
    if len(raw) > 8192:
        raise ValueError("directory request exceeds the budget")
    value = json.loads(raw)
    params = value.get("params") or {}
    if not isinstance(params, dict) or not all(isinstance(v, str) for v in params.values()):
        raise ValueError("invalid directory parameters")
    views = {"profiles": profiles, "profile": profile, "catalog": catalog, "members": members}
    result = views[value["view"]](reader(), params)
    print(json.dumps(result, ensure_ascii=False, separators=(",", ":")))
