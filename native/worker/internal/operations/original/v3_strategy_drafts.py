"""Selected original rules; source I/O is supplied by the host as frozen inputs."""
from __future__ import annotations
from hxc_lifecycle_strategy import DEFAULT_THRESHOLDS as HXC_DEFAULT_THRESHOLDS
from hxc_lifecycle_strategy import BRANCHES as HXC_BRANCHES, SCENE_SKILLS as HXC_SCENE_SKILLS

OPERATING_FIELDS = ("primary_metric", "secondary_metrics", "trigger_rule", "cooldown_rule",
                    "execution_notes", "case_ref", "review_requirement", "observation_days", "lifecycle_thresholds",
                    "owner_role", "external_identity", "material_rule", "exit_rule", "branch_mapping")

METRIC_KEYS = frozenset({"delivery_done", "open_blockers", "return_visit", "satisfaction", "d7_use",
                         "coach_30d", "wecom_30d", "profile_ready", "renew_upgrade", "entitlement_review",
                         "return_reply", "new_need", "product_review", "service_reentry", "reopen", "repurchase",
                         "questionnaire", "hxc_open", "new_review", "resolved_review", "overdue_review"})

def derive_service_relationship_tier(item: dict) -> str:
  """Reuse a published tier without version gating; only legacy empty rows derive."""
  row = item if isinstance(item, dict) else {}
  published = str(row.get("service_relationship_tier") or "")
  if published:
    return published
  profile = row.get("profile") if isinstance(row.get("profile"), dict) else {}
  products = row.get("active_product_keys") if isinstance(row.get("active_product_keys"), list) else []
  order = row.get("order_summary") if isinstance(row.get("order_summary"), dict) else {}
  product_text = " ".join(str(value or "") for value in products + (order.get("product_keys") if isinstance(order.get("product_keys"), list) else []))
  purchase_history = str(row.get("purchase_history") or "").strip().lower()
  current_status = str(row.get("current_service_status") or row.get("service_status") or "").strip().lower()
  entitlement = str(row.get("entitlement_status") or "").strip().lower()
  order_status = str(row.get("order_status") or "").strip().lower()
  has_profile = any(str(profile.get(field) or "").strip() for field in (
    "identity_stage", "income_stage", "industry", "business_shape", "business_stage", "business_focus", "pain_point",
  ))
  paid_count = int(order.get("paid_count") or 0)
  has_purchase = bool(
    product_text or paid_count
    or purchase_history not in {"", "none", "unknown"}
    or order_status not in {"", "unknown"}
    or entitlement not in {"", "unknown"}
  )
  conflict = any(token in f"{entitlement} {current_status} {order_status} {purchase_history}" for token in ("conflict", "争议", "冲突", "unknown", "待核")) and has_purchase
  active = current_status in {"active", "current", "paid", "verified", "在服", "有效", "服务期"} or bool(row.get("service_active"))
  expired = current_status in {"expired", "过期", "到期", "inactive", "已结束"} or any(token in product_text.lower() for token in ("expired", "过期", "到期", "已结束"))
  high_ticket = any(token in product_text.lower() for token in ("founder_ai_upgrade", "opc_fde_camp", "annual_transformation", "workshop4999", "fde", "联盟", "高客单", "升级营", "实战营"))
  if conflict and has_purchase:
    return "needs_verification"
  # An empty order projection is not evidence of no purchase when the source
  # itself was unavailable. Keep the user in the verification layer until the
  # orders/entitlements source has completed a successful sync.
  if row.get("order_source_available") is False and not has_purchase:
    return "needs_verification"
  if high_ticket and active:
    return "high_ticket_active"
  if has_purchase and active:
    return "entry_active"
  if has_purchase and expired:
    return "high_ticket_historical" if high_ticket else "entry_expired"
  if has_profile and not active:
    return "profile_no_current_service"
  if not has_profile and not has_purchase:
    return "low_profile_no_current_service"
  return "needs_verification"

def product_families_for_item(item: dict) -> tuple[set[str], set[str]]:
  """Return current and historical product families from the public order projection."""
  row = item if isinstance(item, dict) else {}
  order = row.get("order_summary") if isinstance(row.get("order_summary"), dict) else {}
  raw_keys = []
  for value in (row.get("active_product_keys"), order.get("product_keys")):
    if isinstance(value, list):
      raw_keys.extend(str(entry).strip() for entry in value if str(entry).strip())
  aliases = {"legacySubscription": "soak299"}
  current_keys = {"soak299", "founder_ai_upgrade", "opc_fde_camp", "alliance"}
  normalized = {aliases.get(key, key) for key in raw_keys}
  status = str(row.get("current_service_status") or row.get("service_status") or "").lower()
  active = bool(row.get("active_product_keys")) or status in {"active", "current", "paid", "verified", "在服", "有效", "服务期"} or row.get("service_active") is True
  current = {key for key in normalized if key in current_keys} if active else set()
  historical = normalized - current if active else normalized
  return current, historical

def audience_filter_result(item: dict, audience_filter: dict, require_segment: bool = True,
                           allow_identity_issues: bool = False) -> str:
  """Return an empty string for a match, otherwise a stable exclusion reason."""
  exact_refs = audience_filter.get("exact_safe_user_refs") or []
  if exact_refs and str(item.get("safe_user_ref") or "") not in exact_refs:
    return "not_in_exact_campaign_list"
  if audience_filter.get("population") == "canonical_archive":
    if item.get("identity_conflict") is True and not allow_identity_issues:
      return "identity_conflict"
    if not str(item.get("safe_user_ref") or "").strip():
      return "identity_unresolved"
    if item.get("identity_resolved") is not True and not allow_identity_issues:
      return "identity_unresolved"
    if item.get("do_not_contact") is True or item.get("contact_preference") == "do_not_contact":
      return "do_not_contact"
    if item.get("manual_handoff") is True or item.get("high_risk") is True:
      return "manual_handoff_or_high_risk"
    if item.get("service_only") is True:
      return "service_only_suppressed"
    current_products, historical_products = product_families_for_item(item)
    selected_products = set(audience_filter.get("product_families") or [])
    if selected_products and not selected_products.intersection(current_products):
      return "product_family_not_matched"
    selected_historical = set(audience_filter.get("historical_product_families") or [])
    if selected_historical and not selected_historical.intersection(historical_products):
      return "historical_product_family_not_matched"
    target_product = audience_filter.get("target_product_key") or ""
    if target_product:
      if item.get("entitlement_status") != "verified":
        return "product_entitlement_unverified"
      order_summary = item.get("order_summary") if isinstance(item.get("order_summary"), dict) else {}
      try:
        refund_count = max(0, int(order_summary.get("refund_count") or 0))
      except (TypeError, ValueError):
        refund_count = 1
      if order_summary.get("has_refund") is True or refund_count > 0:
        return "product_purchase_requires_manual_review"
      active_products = item.get("active_product_keys") if isinstance(item.get("active_product_keys"), list) else []
      purchased_products = order_summary.get("product_keys") if isinstance(order_summary.get("product_keys"), list) else []
      if target_product in active_products or target_product in purchased_products:
        return "existing_product_purchase"
    signals = item.get("strategy_signals") if isinstance(item.get("strategy_signals"), dict) else {}
    selected_segments = audience_filter.get("audience_segments") or []
    if require_segment and not selected_segments and not exact_refs:
      return "audience_segment_required"
    if selected_segments and not any(bool(signals.get(segment)) for segment in selected_segments):
      return "audience_segment_not_matched"
  elif not str(item.get("canonical_user_id") or "").strip():
    return "identity_unresolved"
  relationship_tiers = audience_filter.get("service_relationship_tiers") or []
  if relationship_tiers and str(item.get("service_relationship_tier") or "") not in relationship_tiers:
    return "service_relationship_tier_excluded"
  tiers = audience_filter.get("service_readiness_tiers") or []
  if tiers and str(item.get("service_readiness_tier") or "") not in tiers:
    return "service_readiness_tier_excluded"
  need_state = audience_filter.get("need_state", "any")
  has_need = bool(str(item.get("current_need") or "").strip())
  if need_state == "present" and not has_need:
    return "current_need_missing"
  if need_state == "missing" and has_need:
    return "current_need_present"
  context_state = audience_filter.get("relationship_context", "any")
  has_context = item.get("relationship_context_available") is True
  if context_state == "present" and not has_context:
    return "relationship_context_missing"
  if context_state == "missing" and has_context:
    return "relationship_context_present"
  return ""

def normalize_operating_config(value: dict | None) -> dict:
  """Versioned operating contract; old strategy releases have no fabricated KPI."""
  if value is None:
    return {}
  if not isinstance(value, dict) or set(value) - set(OPERATING_FIELDS):
    raise ValueError("unsupported operating configuration")
  primary = str(value.get("primary_metric") or "").strip()
  secondaries = value.get("secondary_metrics") or []
  if primary and primary not in METRIC_KEYS:
    raise ValueError("unknown primary metric")
  if not isinstance(secondaries, list) or any(str(item) not in METRIC_KEYS for item in secondaries):
    raise ValueError("unknown secondary metric")
  secondaries = list(dict.fromkeys(str(item) for item in secondaries if str(item) != primary))
  if len(secondaries) > 4:
    raise ValueError("too many secondary metrics")
  config = {field: str(value.get(field) or "").strip() for field in (
    "trigger_rule", "cooldown_rule", "execution_notes", "case_ref", "review_requirement",
    "owner_role", "external_identity", "material_rule", "exit_rule")}
  if any(len(item) > 600 for item in config.values()):
    raise ValueError("operating configuration is too long")
  try:
    days = int(value.get("observation_days") or 7)
  except (TypeError, ValueError):
    raise ValueError("invalid observation period") from None
  if not 1 <= days <= 180:
    raise ValueError("invalid observation period")
  result = {"primary_metric": primary, "secondary_metrics": secondaries, **config, "observation_days": days}
  if "lifecycle_thresholds" in value:
    raw = value["lifecycle_thresholds"]
    if not isinstance(raw, dict) or set(raw) - set(HXC_DEFAULT_THRESHOLDS):
      raise ValueError("invalid lifecycle thresholds")
    try:
      thresholds = {key: int(number) for key, number in raw.items()}
    except (TypeError, ValueError):
      raise ValueError("invalid lifecycle thresholds") from None
    if any(number < 0 or number > 365 for number in thresholds.values()):
      raise ValueError("lifecycle threshold out of range")
    result["lifecycle_thresholds"] = thresholds
  if "branch_mapping" in value:
    routing = value["branch_mapping"]
    if not isinstance(routing, dict) or set(routing) - set(HXC_BRANCHES):
      raise ValueError("invalid lifecycle branch mapping")
    normalized = {}
    for branch, rule in routing.items():
      if not isinstance(rule, dict) or set(rule) != {"action", "scene", "skill_key", "material_requirement"}:
        raise ValueError("invalid lifecycle branch rule")
      action = str(rule["action"])
      scene = str(rule["scene"])
      skill = str(rule["skill_key"])
      material = str(rule["material_requirement"])
      if action != HXC_BRANCHES[branch][0] or HXC_SCENE_SKILLS.get(scene) != skill or material not in {"none", "verified_current"}:
        raise ValueError("incompatible lifecycle branch rule")
      normalized[branch] = {"action": action, "scene": scene, "skill_key": skill, "material_requirement": material}
    result["branch_mapping"] = normalized
  return result
