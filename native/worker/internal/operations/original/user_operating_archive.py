"""Selected original pure rules; I/O and mutation entry points are intentionally excluded."""
from __future__ import annotations
from datetime import datetime, timezone


from typing import Any


ARCHIVE_VERSION = "user-operating-archive.v1"


FACT_STATES = frozenset({
  "user_confirmed", "human_confirmed", "observed_active", "observed_candidate",
  "model_candidate", "unsupported", "revoked", "unknown",
})


CORE_FACT_FIELDS = frozenset({
  "identity_stage", "income_stage", "industry", "business_shape", "business_stage",
  "ai_stage", "attention_direction", "confirmed_self_view", "current_need",
  "business_focus", "pain_scene", "product_interest", "decision_timing",
  "decision_role", "relationship_note", "order_refund_status", "contact_preference",
})


SENSITIVE_KEYS = frozenset({
  "phone", "mobile", "phone_hash", "unionid", "external_userid", "openid",
  "content", "chat_content", "raw_chat", "cookie", "token", "secret",
})


def _now() -> str:
  return datetime.now(timezone.utc).astimezone().isoformat(timespec="seconds")


def _text(value: Any) -> str:
  return str(value or "").strip()


def _list(value: Any) -> list:
  return value if isinstance(value, list) else []


def _state(value: Any) -> str:
  candidate = _text(value)
  return candidate if candidate in FACT_STATES else "unknown"


def _source(value: Any) -> str:
  return _text(value) or "unknown"


def _safe_value(value: Any) -> Any:
  if isinstance(value, dict):
    return {k: _safe_value(v) for k, v in value.items() if str(k).lower() not in SENSITIVE_KEYS}
  if isinstance(value, list):
    return [_safe_value(v) for v in value]
  return value


def normalize_fact(field: str, value: Any, metadata: dict | None = None) -> dict:
  """Return one auditable, source-tagged fact without sensitive identifiers."""
  meta = metadata if isinstance(metadata, dict) else {}
  return {
    "field": _text(field),
    "value": _safe_value(value),
    "state": _state(meta.get("state")),
    "source": _source(meta.get("source")),
    "source_ref": _text(meta.get("source_ref")),
    "confidence": _text(meta.get("confidence")) or "unknown",
    "observed_at": _text(meta.get("observed_at")),
    "updated_at": _text(meta.get("updated_at")) or _text(meta.get("observed_at")),
    "expires_at": _text(meta.get("expires_at")),
    "sensitivity": _text(meta.get("sensitivity")) or "normal",
    "revision": int(meta.get("revision") or 1),
  }


def _facts_by_field(raw: Any) -> dict[str, dict]:
  rows: list[dict] = []
  if isinstance(raw, dict):
    for field, value in raw.items():
      if isinstance(value, dict) and "value" in value:
        rows.append(normalize_fact(field, value.get("value"), value))
      else:
        rows.append(normalize_fact(field, value, {"state": "unknown", "source": "unknown"}))
  elif isinstance(raw, list):
    for item in raw:
      if not isinstance(item, dict):
        continue
      field = _text(item.get("field"))
      if field:
        rows.append(normalize_fact(field, item.get("value"), item))
  return {row["field"]: row for row in rows if row["field"]}


def _freshness_status(value: Any) -> str:
  status = _text(value)
  return status if status in {"fresh", "stale", "missing", "unknown"} else "unknown"


def _service_relationship(commercial: dict, service: dict) -> dict:
  commercial = commercial if isinstance(commercial, dict) else {}
  service = service if isinstance(service, dict) else {}
  orders = _list(commercial.get("orders"))
  paid_count = int(commercial.get("paid_count") or 0)
  refund_count = int(commercial.get("refund_count") or 0)
  if orders:
    paid_count = max(paid_count, sum(1 for row in orders if isinstance(row, dict) and row.get("paid") is True))
    refund_count = max(refund_count, sum(1 for row in orders if isinstance(row, dict) and row.get("refunded") is True))
  purchase_history = _text(service.get("purchase_history"))
  if purchase_history not in {"none", "historical", "current", "refunded", "unknown"}:
    if refund_count and paid_count and refund_count >= paid_count:
      purchase_history = "refunded"
    elif paid_count:
      purchase_history = "current" if service.get("current_service_status") == "active" else "historical"
    else:
      purchase_history = "unknown" if commercial.get("available") is False else "none"
  current_status = _text(service.get("current_service_status"))
  if current_status not in {"active", "expired", "pending", "unknown"}:
    current_status = "active" if service.get("active") is True else "unknown"
  entitlement = _text(service.get("entitlement_status"))
  if entitlement not in {"verified", "disputed", "unknown"}:
    entitlement = "disputed" if refund_count else "unknown"
  return {
    "purchase_history": purchase_history,
    "current_service_status": current_status,
    "entitlement_status": entitlement,
    "active_products": [_text(item) for item in _list(service.get("active_products")) if _text(item)],
    "service_period": _safe_value(service.get("service_period") or {}),
    "paid_count": paid_count,
    "refund_count": refund_count,
    "latest_paid_at": _text(commercial.get("latest_paid_at")),
    "source": _source(service.get("source") or commercial.get("source")),
    "updated_at": _text(service.get("updated_at") or commercial.get("updated_at")),
  }


def determine_service_action(archive: dict) -> dict:
  """Choose an operational action, never a product recommendation."""
  safety = archive.get("safety") if isinstance(archive.get("safety"), dict) else {}
  relationship = archive.get("relationship_service") if isinstance(archive.get("relationship_service"), dict) else {}
  quality = archive.get("data_quality") if isinstance(archive.get("data_quality"), dict) else {}
  if safety.get("do_not_contact") is True or safety.get("contact_preference") == "do_not_contact":
    return {"action": "do_not_contact", "reason_code": "contact_prohibited", "manual_review_required": False}
  if safety.get("manual_handoff") is True or safety.get("high_risk") is True:
    return {"action": "high_intent_handoff", "reason_code": "manual_handoff_or_high_risk", "manual_review_required": True}
  if relationship.get("entitlement_status") == "disputed" or relationship.get("purchase_history") == "refunded":
    return {"action": "high_intent_handoff", "reason_code": "refund_or_entitlement_dispute", "manual_review_required": True}
  if any(value in {"stale", "missing"} for value in (quality.get("freshness") or {}).values()):
    return {"action": "data_hold", "reason_code": "source_data_stale_or_missing", "manual_review_required": True}
  if relationship.get("current_service_status") == "active" or relationship.get("purchase_history") == "current":
    return {"action": "entitlement_service", "reason_code": "active_service_relationship", "manual_review_required": True}
  if safety.get("service_only") is True:
    return {"action": "service_only", "reason_code": "service_only_preference", "manual_review_required": True}
  needs = archive.get("needs") if isinstance(archive.get("needs"), dict) else {}
  if not needs.get("current_need"):
    return {"action": "onboarding", "reason_code": "core_need_missing", "manual_review_required": True}
  return {"action": "nurture", "reason_code": "continue_value_service", "manual_review_required": True}


def build_user_operating_archive(payload: dict | None = None) -> dict:
  payload = payload if isinstance(payload, dict) else {}
  facts = _facts_by_field(payload.get("facts") or payload.get("profile_facts"))
  profile = payload.get("profile") if isinstance(payload.get("profile"), dict) else {}
  for field in CORE_FACT_FIELDS:
    if field not in facts and profile.get(field) not in (None, ""):
      facts[field] = normalize_fact(field, profile.get(field), {
        "state": "unknown", "source": "profile_projection",
      })
  value = lambda key: facts.get(key, {}).get("value", "")
  freshness_input = payload.get("freshness") if isinstance(payload.get("freshness"), dict) else {}
  freshness = {str(k): _freshness_status(v) for k, v in freshness_input.items()}
  gaps = [_text(item) for item in _list(payload.get("gaps")) if _text(item)]
  if not value("identity_stage"):
    gaps.append("identity_stage")
  if not value("current_need"):
    gaps.append("current_need")
  if not value("industry"):
    gaps.append("industry")
  relationship = payload.get("relationship") if isinstance(payload.get("relationship"), dict) else {}
  safety = payload.get("safety") if isinstance(payload.get("safety"), dict) else {}
  commercial = payload.get("commercial") if isinstance(payload.get("commercial"), dict) else {}
  service = _service_relationship(commercial, payload.get("service_relationship") or {})
  archive = {
    "archive_version": ARCHIVE_VERSION,
    "snapshot_version": int(payload.get("snapshot_version") or 1),
    "safe_user_ref": _text(payload.get("safe_user_ref")),
    "generated_at": _text(payload.get("generated_at")) or _now(),
    "overview": {
      "display_name": _text(profile.get("display_name")),
      "identity_stage": _safe_value(value("identity_stage")),
      "industry": _safe_value(value("industry")),
      "business_shape": _safe_value(value("business_shape")),
      "business_stage": _safe_value(value("business_stage")),
      "ai_stage": _safe_value(value("ai_stage")),
    },
    "needs": {
      "current_need": _safe_value(value("current_need")),
      "business_focus": _safe_value(value("business_focus")),
      "pain_scene": _safe_value(value("pain_scene")),
      "attention_direction": _safe_value(value("attention_direction")),
      "decision_timing": _safe_value(value("decision_timing")),
      "decision_role": _safe_value(value("decision_role")),
      "gaps": sorted(set(gaps)),
    },
    "relationship_service": {
      **service,
      "trust_state": _text(relationship.get("trust_state")) or "待判断",
      "trust_confidence": _text(relationship.get("trust_confidence")) or "low",
      "intimacy_state": _text(relationship.get("intimacy_state")) or "待判断",
      "intimacy_confidence": _text(relationship.get("intimacy_confidence")) or "low",
      "owner_display_name": _text(payload.get("owner_display_name")),
      "activity_segment": _text(payload.get("activity_segment")) or "unknown",
      "manual_handoff": bool(safety.get("manual_handoff")),
    },
    "evidence": [
      _safe_value(item) for item in _list(payload.get("evidence"))
      if isinstance(item, dict) and not any(key in SENSITIVE_KEYS for key in item)
    ][:20],
    "facts": list(facts.values())[:100],
    "safety": {
      "contact_preference": _text(safety.get("contact_preference")) or "unknown",
      "do_not_contact": bool(safety.get("do_not_contact")),
      "service_only": bool(safety.get("service_only")),
      "manual_handoff": bool(safety.get("manual_handoff")),
      "high_risk": bool(safety.get("high_risk")),
    },
    "data_quality": {
      "freshness": freshness,
      "gaps": sorted(set(gaps)),
      "identity_mapping_status": _text(payload.get("identity_mapping_status")) or "unknown",
      "relationship_mapping_status": _text(payload.get("relationship_mapping_status")) or "unknown",
      "questionnaire_status": _text(payload.get("questionnaire_status")) or "unknown",
      "profile_data_status": _text(payload.get("profile_data_status")) or "partial",
    },
  }
  archive["service_recommendation"] = determine_service_action(archive)
  archive["policy_inputs"] = {
    "product_policy": _text(payload.get("product_policy")) or "external_versioned_policy",
    "service_policy": _text(payload.get("service_policy")) or "service-action-v1",
  }
  archive["automatic_send_allowed"] = False
  archive["scrm_write_allowed"] = False
  archive["raw_chat_exposed"] = False
  return archive
