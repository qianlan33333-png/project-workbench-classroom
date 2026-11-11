"""Original pure audience projection; source IO is supplied by the host."""
import re
from types import SimpleNamespace
import v3_strategy_drafts

def relationship_service_readiness(services, profile: dict, blocked: bool=False) -> dict:
    """Turn verified profile coverage into one conservative service tier."""
    values = profile if isinstance(profile, dict) else {}
    known_context = [field for field in services.SERVICE_CONTEXT_FIELDS if bool(values.get(field))]
    has_need = bool(values.get('current_need'))
    if blocked:
        tier = 'blocked'
        reason = '用户已进入免打扰，不生成主动触达内容。'
    elif has_need and known_context:
        tier = 'personalized_ready'
        reason = '已有明确需求，并至少有一项背景信息可以交叉核对。'
    elif has_need or len(known_context) >= 2:
        tier = 'contextual_service'
        reason = '有部分需求或背景上下文，只做保守承接，不扩大推断。'
    else:
        tier = 'low_information'
        reason = '缺少可交叉核对的需求和背景，只做价值服务或一次轻量确认。'
    missing = [field for field in ('current_need', *services.SERVICE_CONTEXT_FIELDS) if not values.get(field)]
    return {'tier': tier, 'reason': reason, 'known_context_fields': known_context, 'has_confirmed_need': has_need, 'missing_fields': missing}

def archive_need_context(services, value) -> str:
    """Expose only a short profile topic, never a contact detail or chat transcript."""
    raw = ' '.join(str(value or '').split())
    if not raw:
        return ''
    sensitive = re.compile('(?:[A-Z0-9._%+-]+@[A-Z0-9.-]+\\.[A-Z]{2,})|(?:\\+?86[-\\s]?)?1[3-9]\\d{9}|\\b\\d{7,}\\b|(?:union.?id|external.?userid|手机号|微信号|身份证)', re.IGNORECASE)
    if sensitive.search(raw):
        return ''
    return services.service_safe_topic(raw)

def service_safe_topic(value) -> str:
  """Keep a service-relevant topic without exposing a whole profile or sensitive facts."""
  raw = " ".join(str(value or "").replace("用户最近主动表达：", "").split())
  if not raw:
    return ""
  patterns = [
    r"核心诉求(?:是|为)\s*([^；。]{3,38})",
    r"主要(?:诉求|关注|落点)(?:是|为|还是|在于)?\s*([^；。]{3,38})",
    r"正在纠结\s*([^；。]{3,36})",
    r"存在\s*([^；。]{3,30})短板",
    r"我想做\s*([^；。？]{3,28})",
  ]
  sensitive = r"收入|营收|家庭|婚姻|孩子|带娃|情绪|疾病|负债|求职|现居|年龄|\d+\s*岁"
  for pattern in patterns:
    match = re.search(pattern, raw)
    if not match:
      continue
    topic = match.group(1).strip(" ，。；：‘’“”\"'")
    if topic and not re.search(sensitive, topic):
      return topic[:48].rstrip("，。；：")
  profile_markers = ["是资深", "是成熟", "是处于", "年收入", "现居", "拥有", "核心诉求", "短板"]
  if len(raw) <= 48 and not re.search(sensitive, raw) and not any(marker in raw for marker in profile_markers):
    return raw
  return ""

def project(row, normalized_filter):
    services=SimpleNamespace(SERVICE_CONTEXT_FIELDS=("identity_stage","income_stage","industry","business_shape","business_stage","long_term_memory"),service_safe_topic=service_safe_topic)
    services.relationship_service_readiness=lambda profile,blocked:relationship_service_readiness(services,profile,blocked)
    safe_ref=str(row.get("safe_user_ref")or"").strip()
    identity = row.get('_identityMatch') if isinstance(row.get('_identityMatch'), dict) else {}
    safety = row.get('allocation_safety') if isinstance(row.get('allocation_safety'), dict) else {}
    summary = row.get('hxc_summary') if isinstance(row.get('hxc_summary'), dict) else {}
    order_summary = row.get('order_summary') if isinstance(row.get('order_summary'), dict) else {}
    current_need = 'present' if row.get('has_current_need') is True else ''
    source_profile = row.get('profile') if isinstance(row.get('profile'), dict) else {}
    profile = {'identity_stage': source_profile.get('identity_stage'), 'income_stage': source_profile.get('income_stage'), 'industry': source_profile.get('industry'), 'business_shape': source_profile.get('business_shape'), 'business_stage': source_profile.get('business_stage'), 'business_focus': source_profile.get('business_focus'), 'pain_point': source_profile.get('pain_point'), 'long_term_memory': 'present' if summary.get('has_memory') is True else '', 'current_need': current_need}
    current_need_text = archive_need_context(services, profile.get('business_focus') or profile.get('pain_point'))
    relationship_context_available = bool(current_need or any((profile.get(field) for field in services.SERVICE_CONTEXT_FIELDS)) or row.get('relationship_context_available') is True or summary.get('has_memory') or (int(summary.get('user_msg_7d') or summary.get('msg_7d') or 0) > 0))
    identity_resolved = identity.get('confidence') == 'high' and (identity.get('matched_by') == 'verified_identity' or (normalized_filter.get('exact_safe_user_refs') and row.get('customer_mapping_status') == 'resolved'))
    contact_preference = str(safety.get('contact_policy') or 'unknown').strip().lower()
    identity_conflict = str(row.get('customer_mapping_status') or '').lower() == 'conflict'
    signals = row.get('strategy_signals') if isinstance(row.get('strategy_signals'), dict) else {}
    readiness = services.relationship_service_readiness(profile, bool(safety.get('is_do_not_contact')))
    display_name = str(row.get('name') or '待识别用户').strip()
    item = {'safe_user_ref': safe_ref, 'name': display_name, 'profile': profile, 'current_need': current_need, 'current_need_text': current_need_text, 'user_topic': ' / '.join((str(profile.get(field) or '').strip() for field in ('business_stage', 'business_shape', 'industry') if str(profile.get(field) or '').strip())), 'identity_resolved': identity_resolved, 'identity_conflict': identity_conflict, 'contact_preference': contact_preference, 'do_not_contact': bool(safety.get('is_do_not_contact')), 'service_only': bool(safety.get('service_only')) or contact_preference == 'service_only', 'manual_handoff': bool(safety.get('manual_handoff')), 'high_risk': bool(safety.get('high_risk')), 'relationship_context_available': relationship_context_available, 'service_readiness_tier': readiness['tier'], 'service_readiness_reason': readiness['reason'], 'relationship_context_sources': [source for source, present in (('confirmed_need', bool(current_need)), ('profile', any((profile.get(field) for field in services.SERVICE_CONTEXT_FIELDS))), ('hxc_summary', bool(summary.get('has_memory') or summary.get('user_msg_7d') or summary.get('msg_7d')))) if present], 'identity_status': 'exact' if identity_resolved else 'unresolved', 'strategy_signals': signals, 'manual_review_required': bool(signals.get('manual_review')) or contact_preference in {'', 'unknown'}, 'product_key': str(signals.get('product_key') or 'unknown'), 'active_product_keys': row.get('active_product_keys') if isinstance(row.get('active_product_keys'), list) else [], 'entitlement_status': str(row.get('entitlement_status') or 'unknown'), 'service_relationship_tier': str(row.get('service_relationship_tier') or ''), 'service_relationship_tier_version': str(row.get('service_relationship_tier_version') or ''), 'service_relationship_reason': str(row.get('service_relationship_reason') or ''), 'order_source_available': row.get('order_source_available') is True, 'order_summary': {'paid_count': int(order_summary.get('paid_count') or 0), 'refund_count': int(order_summary.get('refund_count') or 0), 'has_refund': bool(order_summary.get('has_refund')), 'product_keys': order_summary.get('product_keys') if isinstance(order_summary.get('product_keys'), list) else []}, 'source': str(row.get('source') or 'workbench')}
    item['service_relationship_tier'] = v3_strategy_drafts.derive_service_relationship_tier(item)
    return item
