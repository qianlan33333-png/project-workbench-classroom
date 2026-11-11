"""Selected original rules; source I/O is supplied by the host as frozen inputs."""
from __future__ import annotations
from order_time_contract import service_time
from datetime import date, datetime, timedelta
import hashlib
from hxc_routine_policy import stamp
from zoneinfo import ZoneInfo

POLICY_VERSION = "hxc-lifecycle.v4"

DEFAULT_THRESHOLDS = {"paid_expiry_days": 7, "gift_expiry_days": 7,
                      "trial_expiry_days": 0, "low_use_days": 30, "long_silent_days": 90,
                      "recovery_cooldown_days": 30}

STRATEGIES = {
    "hxc:first_profile": ("首次建档与体验引导", "建立可用档案并引导一次真实体验", "profile_ready"),
    "hxc:known_no_rights": ("有档案、无当前权益维护", "恢复联系并确认当前需求", "return_reply"),
    "hxc:onboarding": ("新人 D0–D7 承接", "完成开通、首次有效聊天和基础档案建立", "d7_use"),
    "hxc:ongoing": ("在服价值服务", "持续获得价值并保持自然互动", "coach_30d"),
    "hxc:expiry": ("到期承接", "核对已获得的价值和后续服务选择", "reopen"),
    "hxc:recovery": ("过期与长期沉默关系恢复", "低压恢复关系并重新了解需求", "return_reply"),
}

BRANCHES = {
    "purchase_account_unlinked": ("conditional_first_use", "new_user_onboarding", "new_user_onboarding"),
    "unknown_profile": ("profile_check", "personal_followup", "personal_followup"),
    "known_background": ("relationship_check", "personal_followup", "personal_followup"),
    "activation_time_review": ("verify_activation", "personal_followup", "personal_followup"),
    "open_blocked": ("verify_activation", "personal_followup", "personal_followup"),
    "not_opened": ("help_open", "new_user_onboarding", "new_user_onboarding"),
    "opened_unused": ("first_use", "new_user_onboarding", "new_user_onboarding"),
    "used_once": ("respond_need", "personal_followup", "personal_followup"),
    "d7_review": ("review_progress", "personal_followup", "personal_followup"),
    "unanswered_issue": ("respond_issue", "personal_followup", "personal_followup"),
    "normal_service": ("service_value", "personal_followup", "personal_followup"),
    "low_use": ("restore_use", "activation_recall", "activation_recall"),
    "weekly_review_ready": ("review_progress_service", "weekly_review", "weekly_review"),
    "content_ready": ("recommend_content", "content_service", "content_service"),
    "recall_ready": ("recall_with_new_value", "activation_recall", "activation_recall"),
    "service_start_review": ("verify_activation", "personal_followup", "personal_followup"),
    "account_review": ("verify_activation", "personal_followup", "personal_followup"),
    "rights_review": ("verify_activation", "personal_followup", "personal_followup"),
    "usage_unknown": ("verify_usage", "personal_followup", "personal_followup"),
    "paid_expiry": ("review_renewal", "personal_followup", "personal_followup"),
    "gift_expiry": ("review_paid_option", "personal_followup", "personal_followup"),
    "expired": ("relationship_recovery", "activation_recall", "activation_recall"),
    "long_silent": ("low_pressure_reconnect", "activation_recall", "activation_recall"),
}

BRANCH_ROUTING_DEFAULTS = {
    branch: {"action": action, "scene": scene, "skill_key": skill, "material_requirement": "none"}
    for branch, (action, scene, skill) in BRANCHES.items()
}

SCENE_SKILLS = {
    "personal_followup": "personal_followup",
    "new_user_onboarding": "new_user_onboarding",
    "activation_recall": "activation_recall",
    "weekly_review": "weekly_review",
    "content_service": "content_service",
}

BRANCH_LABELS = {
    "purchase_account_unlinked": "已购买公开课，黄小璨账号未精确关联；条件式确认入口与使用",
    "unknown_profile": "基础资料待补", "known_background": "有可核历史关系，待确认近况",
    "activation_time_review": "近期权益候选缺注册和问卷日期",
    "open_blocked": "开通状态待核", "not_opened": "权益已生效，账号尚未开通",
    "opened_unused": "已开通，待完成首次有效互动", "used_once": "已有首次有效互动",
    "d7_review": "新人第七天进展复核", "unanswered_issue": "用户新问题优先回应",
    "weekly_review_ready": "周复盘回访", "content_ready": "内容推荐", "recall_ready": "新内容召回",
    "service_start_review": "注册或问卷日期待补", "account_review": "小程序账号待精确关联", "rights_review": "当前权益待核",
    "normal_service": "常态服务准备", "low_use": "内容候选准备中",
    "usage_unknown": "有效使用来源待核", "paid_expiry": "实付权益临期",
    "gift_expiry": "赠送权益临期", "expired": "权益已明确结束",
    "long_silent": "有历史关系且长期未见有效互动",
}

INTERNAL_ACTIONS = {"profile_check", "verify_activation", "verify_usage", "review_progress"}

def _date(value):
    if not value:
        return None
    try:
        parsed = datetime.fromisoformat(str(value).replace("Z", "+00:00"))
        return parsed.astimezone(ZoneInfo("Asia/Shanghai")).date() if parsed.tzinfo else parsed.date()
    except ValueError:
        try:
            return date.fromisoformat(str(value)[:10])
        except ValueError:
            return None

def _first_grant(evidence):
    grants = [grant for grant in (evidence or {}).get("grants") or [] if isinstance(grant, dict)]
    grants.sort(key=lambda grant: str(grant.get("granted_at") or ""), reverse=True)
    return grants[0] if grants else {}

def _profile_known(user):
    return any(str(user.get(key) or "").strip() not in {"", "待确认", "未知", "[protected]"}
               for key in ("身份阶段", "行业", "业务形态", "业务阶段", "业务焦点", "核心痛苦场景"))

def _blocked(user):
    safety = user.get("allocation_safety") or {}
    return (safety.get("is_do_not_contact") is True
            or str(safety.get("contact_policy") or "").lower() in {"blocked", "do_not_contact", "opt_out"}
            or safety.get("manual_handoff") is True or safety.get("high_risk") is True)

def _first_paid_unlinked_68(user, current):
    """Purchase day is a provisional contact window, never proof of HXC rights."""
    hxc = user.get("_hxcSummary") or {}
    if hxc.get("account_present") is True or user.get("黄小璨用户ID") or user.get("_hxcAccountIds"):
        return None
    # A CRM customer ID alone is not proof that the WeCom operator can reach
    # this person. The original add event and nickname are required here.
    if not user.get("企微首次添加时间") or not str(user.get("企微昵称") or "").strip():
        return None
    orders = [o for o in (user.get("order_history") or user.get("_orderHistory") or [])
              if isinstance(o, dict) and o.get("is_paid") is True and o.get("is_refunded") is False]
    hxc_orders = []
    for order in orders:
        code = str(order.get("product_code") or "")
        if code not in {"31314141", "334465678"} and order.get("product_key") != "soak299":
            continue
        paid = stamp(service_time(order))
        if paid:
            hxc_orders.append((paid, code, order))
    if not hxc_orders:
        return None
    hxc_orders.sort(key=lambda item: item[0])
    paid, code, order = hxc_orders[0]
    if code not in {"31314141", "334465678"} or not timedelta(0) <= current - paid < timedelta(days=30):
        return None
    order_ref = str(order.get("order_ref") or "")
    if not order_ref:
        return None
    return {"paid_at": paid.isoformat(), "product_code": code,
            "order_evidence_digest": hashlib.sha256(order_ref.encode()).hexdigest(),
            "purchase_days": int((current-paid).total_seconds() // 86400)}

def branch_route(branch, configured=None):
    """Published routing can change the main scene, never the safety/action gate."""
    base = BRANCH_ROUTING_DEFAULTS[branch]
    selected = (configured or {}).get(branch) or {}
    return {**base, **selected}

def current_service_tier(user, evidence=None):
    """Historical refunds warn; only current-service or identity conflicts block.

    Compatibility for v4 frozen archives: lift its refund-only classification
    only with an exact current account, an unexpired current service and a
    current active grant. Never turn missing rights into inferred rights.
    """
    tier = str(user.get("service_relationship_tier") or "")
    hxc = user.get("_hxcSummary") or {}
    statuses = [hxc.get("service_status"), user.get("current_service_status"),
                user.get("service_status"), user.get("entitlement_status")]
    conflict = (user.get("_customerIdentityConflict") or user.get("identity_conflict")
                or user.get("customer_mapping_status") != "resolved"
                or user.get("current_service_refunded") is True
                or user.get("current_entitlement_revoked") is True
                or any(any(x in str(v or "").lower() for x in
                           ("conflict", "disputed", "refund", "revoked", "冲突", "争议", "退款", "撤销")) for v in statuses))
    if conflict:
        return "needs_verification"
    # Old commercial labels cannot override an exact, currently usable HXC
    # service. A paid low-ticket order alone never supplies missing rights.
    if (tier in {'needs_verification','high_ticket_historical','high_ticket_expired'}
            and hxc.get('service_status')=='active'
            and (evidence or {}).get('routine_account_exact')is True):
        now=(evidence or {}).get('_evaluation_now')or datetime.now(ZoneInfo('Asia/Shanghai'))
        end=stamp(hxc.get('service_expires_at'))
        paid_low=any(str(o.get('product_code'))in {'31314141','334465678'}
            and o.get('is_paid')is True and o.get('is_refunded')is False
            and (t:=stamp(service_time(o)))and t<=now
            for o in user.get('order_history')or user.get('_orderHistory')or[])
        current_grant=any((t:=stamp(g.get('granted_at')))and t<=now
            and (e:=stamp(g.get('expires_at')))and e>now
            and g.get('status')in {None,'active','enabled','granted'}
            for g in ((evidence or {}).get('grants')or[])+((evidence or {}).get('source_product_grants')or[]))
        # A new exact-account readback can precede the daily grant projection.
        # Its real source date/expiry is sufficient; no manual active flag or
        # stale observation can fill a missing grant.
        from sku68_operations import current_expiry_verified
        observed=(evidence or {}).get('current_hxc_entitlement')or{}
        observed_at=stamp(observed.get('as_of'))
        current_grant=current_grant or bool(observed_at and timedelta(0)<=now-observed_at<=timedelta(hours=24)
            and current_expiry_verified(evidence or{},end))
        if paid_low and end and end>now and current_grant:return 'entry_active'
    order = user.get("_orderSummary") or user.get("order_summary") or {}
    reason = str(user.get("service_relationship_reason") or "")
    if (tier == "needs_verification" and user.get("service_relationship_tier_version") == "service-relationship.v4"
            and reason == "身份、退款或权益事实存在冲突。" and order.get("has_refund") is True
            and hxc.get("service_status") == "active"
            and (evidence or {}).get("routine_account_exact") is True):
        # The v4 reason conflated unrelated historical refunds with rights.
        # Both subscription and grant must be current; differing end dates
        # do not block ordinary service, but cannot support an expiry reminder.
        end = stamp(hxc.get("service_expires_at"))
        now = (evidence or {}).get("_evaluation_now") or datetime.now(ZoneInfo("Asia/Shanghai"))
        if end and end > now and any(stamp(g.get("expires_at")) and stamp(g.get("expires_at")) > now
                and stamp(g.get("granted_at")) and stamp(g.get("granted_at")) <= now
                and g.get("status") in (None, "active", "enabled", "granted")
                for g in ((evidence or {}).get("grants") or []) + ((evidence or {}).get("source_product_grants") or []) if isinstance(g, dict)):
            return "entry_active"
    return tier

def evaluate(user, evidence=None, *, today=None, thresholds=None):
    """Return one HXC service decision with source-backed reasons, or a no-touch reason.

    Unknown entitlement, first-use or activity evidence is never interpreted as a negative event.
    """
    current = today or datetime.now(ZoneInfo("Asia/Shanghai"))
    if not isinstance(current, datetime):
        current = datetime.combine(current, datetime.now(ZoneInfo("Asia/Shanghai")).time(), ZoneInfo("Asia/Shanghai"))
    elif current.tzinfo is None:
        current = current.replace(tzinfo=ZoneInfo("Asia/Shanghai"))
    now = current.astimezone(ZoneInfo("Asia/Shanghai")).date()
    limits = {**DEFAULT_THRESHOLDS, **(thresholds or {})}
    ref = str(user.get("safe_user_ref") or "")
    if not ref.startswith("usr_") or user.get("customer_mapping_status") != "resolved":
        return {"eligible": False, "reason": "identity_not_exact"}
    if _blocked(user):
        return {"eligible": False, "reason": "contact_or_handoff_blocked"}
    from sku299_owner_service import apply as apply_owner_service
    user,evidence,admission=apply_owner_service(user,evidence,current)
    if admission and admission['status'] in {'manual_account_link','identity_conflict','current_service_revoked'}:
        return {'eligible':False,'reason':admission['reason'],'service_admission':admission}
    if admission and admission['status']=='inactive':
        return {'eligible':False,'outbound_candidate':False,'reason':'legacy499_recovery_pool_only','service_admission':admission,'recovery_subtype':admission.get('recovery_subtype')}
    tier = current_service_tier(user, dict(evidence or {}, _evaluation_now=current))
    if tier == "needs_verification":
        return {"eligible": False, "reason": "other_service_or_fact_review"}
    hxc = user.get("_hxcSummary") or {}
    coach = user.get("_hxcCoachInteraction") or {}
    grant = _first_grant(evidence)
    grant_start, grant_end = _date(grant.get("granted_at")), _date(grant.get("expires_at"))
    # Reuse the server-side product-correlated source join before an expired
    # account label can trigger another product's recovery flow.
    from sku68_operations import primary_299, orders
    premium=primary_299(user,evidence or {},current)
    paid299=[o for o in orders(user) if (str(o.get('product_code'))=='202608052341'
             or o.get('product_key')=='soak299') and stamp(service_time(o)) and stamp(service_time(o))<=current]
    if premium:
        grant_end=_date(premium['expires_at'])
    else:
        # The recorded current account date is canonical; old grant mismatch
        # is informational, not a second expiry veto.
        grant_end=_date(hxc.get('service_expires_at')) or grant_end
    status = str(hxc.get("service_status") or "")
    entitlement = str(user.get("entitlement_status") or "")
    active = status == "active" or (status not in {"expired", "not_started"} and tier == "entry_active" and entitlement == "active")
    expired = status == "expired" or tier == "entry_expired"
    if premium:
        active,expired=True,False
    ended299=[_date(g.get('expires_at')) for g in ((evidence or {}).get('grants') or [])+((evidence or {}).get('source_product_grants') or [])
        if isinstance(g,dict) and _date(g.get('expires_at')) and _date(g['expires_at'])<=now
        and (g.get('product_key')=='soak299' or str(g.get('product_code'))=='202608052341'
             or g.get('category') in {'paid_299','soak299','paid_365d','paid_annual'}
             or g.get('product_service_tag')=='深度思考群用户')]
    if expired and not premium and paid299 and not (admission and admission['status']=='expired') and (not ended299 or any(_date(service_time(o))>max(ended299) for o in paid299)):
        explicit=any(str(o.get('product_code'))=='202608052341' or str(o.get('product_code')or'').startswith('wxshop-') for o in paid299)
        return {"eligible": False, "reason": "current_paid_299_entitlement_pending" if explicit else "299_entitlement_attribution_requires_review", "service_sync_required": explicit}
    skip_newcomer=(evidence or {}).get('skip_299_newcomer') is True
    if tier in {"high_ticket_active", "high_ticket_historical"} and not (status == "active" or status == "expired"):
        return {"eligible": False, "reason": "other_service_or_fact_review"}
    # Newcomer SOP uses registration, then questionnaire; entitlement events stay separate.
    actual_start = stamp((evidence or {}).get("first_service_activated_at"))
    anchor = stamp((evidence or {}).get('onboarding_anchor_at')) or stamp((evidence or {}).get('account_created_at')) or stamp((evidence or {}).get('questionnaire_recorded_at')) or actual_start
    start = anchor.date() if anchor else None
    elapsed = current - anchor if anchor else None
    days = int(elapsed.total_seconds() // 86400) if elapsed is not None else None
    # A new rights record is a newcomer *candidate*, never proof of first use.
    recorded_starts = [stamp(g.get("granted_at")) for g in (evidence or {}).get("grants") or []]
    earliest_record = min([t for t in recorded_starts if t], default=None)
    recent_rights_candidate = bool(not anchor and earliest_record and timedelta(0) <= current-earliest_record < timedelta(days=7))
    recent_login = stamp((evidence or {}).get("first_login_at"))
    recent_without_start = bool(not anchor and recent_login and timedelta(0) <= current-recent_login < timedelta(days=7))
    rights_type = "gift" if grant.get("category") in {"gift_30d", "trial_7d", "gift", "trial"} else "paid" if any(
        order.get("product_key") == "soak299" and order.get("is_paid") is True and order.get("is_refunded") is not True
        for order in user.get("order_history") or [] if isinstance(order, dict)) else "unknown"
    if admission and admission['status'] in {'active','expired'}:rights_type='paid' if admission.get('original_order_refs') else 'gift'
    last_user_reply = _date(user.get("last_user_reply_at"))
    unresolved = user.get("unresolved_user_issue") is True or bool(
        last_user_reply and _date(user.get("last_outreach_at")) and
        last_user_reply > _date(user.get("last_outreach_at")))
    completion_due=(evidence or {}).get('onboarding_completion_due') is True and not skip_newcomer
    purchase_unlinked = _first_paid_unlinked_68(user, current)
    if purchase_unlinked:
        purchase_unlinked["questionnaire_confirmed"] = bool((evidence or {}).get("surveys"))
    if purchase_unlinked and tier not in {"high_ticket_active", "high_ticket_historical", "high_ticket_expired"}:
        strategy, branch = "hxc:onboarding", "purchase_account_unlinked"
    elif completion_due:
        strategy, branch = 'hxc:onboarding', 'd7_review'
    elif active:
        if unresolved:
            strategy, branch = "hxc:ongoing", "unanswered_issue"
        elif (evidence or {}).get("routine_account_exact") is False:
            strategy, branch = "hxc:ongoing", "account_review"
        elif hxc.get("service_status") not in {"active", "expired"}:
            strategy, branch = "hxc:ongoing", "rights_review"
        elif recent_rights_candidate and not skip_newcomer:
            strategy, branch = "hxc:onboarding", "activation_time_review"
        elif (recent_without_start or (anchor and anchor > current)) and not skip_newcomer:
            strategy, branch = "hxc:ongoing", "service_start_review"
        elif elapsed is not None and timedelta(0) <= elapsed < timedelta(days=7) and not skip_newcomer:
            strategy = "hxc:onboarding"
            if hxc.get("account_present") is not True:
                branch = "not_opened" if status == "active" else "open_blocked"
            elif (evidence or {}).get("onboarding_conversation_observed") is True:
                branch = "used_once"
            elif not isinstance(coach.get("coach_meaningful_sessions_all"), int):
                branch = "opened_unused" if hxc.get("user_msg_7d") == 0 else "open_blocked"
            else:
                branch = "used_once" if coach["coach_meaningful_sessions_all"] > 0 else "opened_unused"
        elif grant_end and 0 <= (grant_end - now).days <= (
                limits["trial_expiry_days"] if grant.get("category") == "trial_7d" else
                limits["gift_expiry_days"] if rights_type == "gift" else limits["paid_expiry_days"]):
            strategy, branch = "hxc:expiry", "gift_expiry" if rights_type == "gift" else "paid_expiry"
        else:
            strategy = "hxc:ongoing"
            last_use = _date(coach.get("last_meaningful_coach_at"))
            segment=((evidence or {}).get('activity') or {}).get('activity_segment')
            if segment in {'A','B','C','D'}:
                branch = "normal_service"  # Published ABCD owns support intensity.
            elif not isinstance(coach.get("coach_meaningful_sessions_30d"), int):
                branch = "usage_unknown"
            elif last_use and (now - last_use).days >= limits["low_use_days"]:
                branch = "low_use"
            elif coach["coach_meaningful_sessions_30d"] == 0 and start and (now - start).days >= limits["low_use_days"]:
                branch = "low_use"
            else:
                branch = "normal_service"
    elif expired:
        strategy, branch = "hxc:recovery", "expired"
    elif tier in {"low_profile_no_current_service", "profile_no_current_service", "historical_other_purchase"}:
        if _profile_known(user) or tier == "historical_other_purchase" or any(o.get("is_paid") is True and o.get("is_refunded") is not True for o in (user.get("order_history") or user.get("_orderHistory") or []) if isinstance(o, dict)):
            strategy, branch = "hxc:known_no_rights", "known_background"
        else:
            strategy, branch = "hxc:first_profile", "unknown_profile"
        last_contact = _date(user.get("CRM最后触达"))
        if last_contact and (now - last_contact).days >= limits["long_silent_days"] and _profile_known(user):
            strategy, branch = "hxc:recovery", "long_silent"
    else:
        return {"eligible": False, "reason": "entitlement_or_service_unknown"}
    action, scene, skill = BRANCHES[branch]
    # Missing reliable start/expiry or open status cannot become an outbound claim.
    # Mirror owns the first-day welcome. Keep an internal workbench decision so
    # the dashboard can show the handoff gap without generating a second A0.
    mirror_day0 = (strategy == "hxc:onboarding" and not completion_due and
                   (days == 0 or (branch == "purchase_account_unlinked" and
                                  purchase_unlinked.get("purchase_days") == 0)))
    internal = (mirror_day0 or (action in INTERNAL_ACTIONS and not (branch=='d7_review' and completion_due)) or strategy in {"hxc:first_profile", "hxc:known_no_rights"}
                or (strategy == "hxc:expiry" and rights_type == "unknown")
                or branch in {"normal_service", "low_use", "service_start_review", "account_review", "rights_review"}  # Prepare or verify before any outbound draft.
                or (strategy == "hxc:recovery" and (
                    not _date(user.get("last_outreach_at")) or
                    (now - _date(user.get("last_outreach_at"))).days < limits["recovery_cooldown_days"])))
    preparation_reason = ""
    if internal:
        if mirror_day0: preparation_reason = "mirror_day0_welcome_owned_externally"
        elif strategy == "hxc:first_profile": preparation_reason = "welfare_flow_not_enabled"
        elif strategy == "hxc:known_no_rights": preparation_reason = "relationship_plan_required"
        elif strategy == "hxc:recovery":
            preparation_reason = "outreach_history_missing" if not _date(user.get("last_outreach_at")) else "recovery_cooldown"
        elif branch in {"activation_time_review", "service_start_review", "open_blocked"}: preparation_reason = "first_activation_missing"
        elif branch == "account_review": preparation_reason = "account_mapping_review"
        elif branch == "rights_review" or strategy == "hxc:expiry" and rights_type == "unknown": preparation_reason = "entitlement_review"
        else: preparation_reason = "service_material_required"
    return {"eligible": True, "safe_user_ref": ref, "product_service_key": "hxc",
            "strategy_key": strategy, "policy_version": POLICY_VERSION, "branch": branch,
            "action": action, "scene": scene, "skill_key": skill,
            "history_refund_warning": bool((user.get("_orderSummary") or user.get("order_summary") or {}).get("has_refund")),
            "effective_service_tier": tier, "service_admission": admission,
            "manual_review_required": True, "outbound_candidate": not internal, "preparation_reason": preparation_reason,
            "account_created_at": (evidence or {}).get("account_created_at") or "",
            "first_login_at": (evidence or {}).get("first_login_at") or "",
            "questionnaire_recorded_at": (evidence or {}).get("questionnaire_recorded_at") or "",
            "why_now": ("前七天已结束：完整七天资料已到，生成阶段总结" if completion_due else
                        "已核订单满七天且账号未精确关联：仅准备一次条件式服务确认，后续转人工核账号" if
                        branch == "purchase_account_unlinked" and purchase_unlinked.get("purchase_days", 0) >= 7 else
                        f"{STRATEGIES[strategy][0]}：{BRANCH_LABELS[branch]}"),
            "onboarding_phase": "completion_pending" if completion_due else
                                "historical_one_time" if branch == "purchase_account_unlinked" and purchase_unlinked.get("purchase_days", 0) >= 7 else "active",
            "evidence_refs": [value for value in (grant.get("evidence"), user.get("snapshot_id")) if isinstance(value, str) and value][:3],
            "rights_type": rights_type, "entitlement_state": "active" if active else "expired" if expired else "unverified",
            "activation_candidate_at": earliest_record.isoformat() if recent_rights_candidate else "",
            "activation_state": "needs_verification" if recent_rights_candidate else "confirmed" if anchor else "unknown",
            "rights_start": actual_start.isoformat() if actual_start else "",
            "first_service_activated_at": actual_start.isoformat() if actual_start else "",
            "onboarding_anchor_at": anchor.isoformat() if anchor else "",
            "onboarding_anchor_basis": (evidence or {}).get("onboarding_anchor_basis") or "legacy_actual_activation",
            "onboarding_dates_agree": (evidence or {}).get("onboarding_dates_agree"),
            "onboarding_day": days if days is not None else purchase_unlinked.get("purchase_days") if branch == "purchase_account_unlinked" else None,
            "activity_segment": ((evidence or {}).get("activity") or {}).get("activity_segment", "unknown"),
            "activity_as_of": ((evidence or {}).get("activity") or {}).get("as_of"),
            "provisional_purchase": purchase_unlinked if branch == "purchase_account_unlinked" else {},
            "rights_end": grant.get("expires_at") if grant_end else ""}
