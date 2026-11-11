"""Selected original rules; source I/O is supplied by the host as frozen inputs."""
import hashlib
import json
import re
from datetime import datetime

VERSION = 'relationship.growth-report.v1'

BUSINESS = ('direction', 'customer', 'model', 'product', 'acquisition', 'delivery', 'management')

INNER = ('motivation', 'uncertainty', 'boundaries', 'stability')

AI = ('ai', 'value', 'workflow', 'assets', 'evaluation', 'delivery')

LABELS = {
    'business': dict(zip(BUSINESS, ('业务方向', '客户需求理解', '商业模式与价值主张', '产品设计', '获客与成交', '交付与复用', '经营与协作'))),
    'inner': dict(zip(INNER, ('底层动力', '面对不确定性', '关系与责任边界', '内核稳定性'))),
    'ai': dict(zip(AI, ('AI认知与应用判断', '业务洞察与价值定义', '人机协作与流程重构', '知识数据与能力沉淀', '效果验证与业务迭代', '组织协同与持续经营'))),
}

def digest(value):
    return hashlib.sha256(json.dumps(value, ensure_ascii=False, sort_keys=True,
        separators=(',', ':'), default=str).encode()).hexdigest()

def validate_handoff(value):
    if (not isinstance(value, dict) or value.get('schema_version') not in {VERSION,'relationship.growth-report.v2'}
            or value.get('report_type') != 'commercial-personal-ai-growth'
            or value.get('review_status') != 'accepted' or not value.get('report_ref')
            or type(value.get('version')) is not int or value['version'] < 1
            or value.get('derived_not_user_fact') is not True or not value.get('rule_version')):
        raise ValueError('growth_report_handoff_invalid')
    # HXC batch preparation is hidden until explicit publication. Review acceptance
    # alone must never promote that cache into a channel-service report.
    metadata = value.get('metadata') or {}
    if (any(value.get(k) in ('hidden', 'draft', 'withdrawn', 'expired', 'deleted', 'superseded')
            for k in ('status', 'visibility', 'publication_status'))
            or value.get('preparation_state') == 'prepared_not_delivered'
            or isinstance(metadata, dict) and metadata.get('preparation_state') == 'prepared_not_delivered'):
        raise ValueError('growth_report_not_published')
    content = value.get('content')
    if (not isinstance(content, dict) or digest(content) != value.get('content_digest')
            or content.get('schema_version') != value.get('schema_version')
            or content.get('report_type') != value['report_type']):
        raise ValueError('growth_report_content_invalid')
    for name, keys in [('business', BUSINESS), ('inner', INNER), ('ai', AI)]:
        if set(content.get(name) or {}) != set(keys):
            raise ValueError('growth_report_dimensions_invalid')
        if any(content[name][key].get('name') != label for key, label in LABELS[name].items()):
            raise ValueError('growth_report_dimension_names_invalid')
    try:
        if datetime.fromisoformat(value['source_at'].replace('Z', '+00:00')).tzinfo is None:
            raise ValueError
    except (KeyError, ValueError, TypeError, AttributeError):
        raise ValueError('growth_report_source_time_invalid') from None
    def inspect(node):
        if isinstance(node, dict):
            if any(k in node for k in ('owner_id', 'evidence_keys', 'basis', 'raw_chat', 'operator_note')):
                raise ValueError('growth_report_private_content')
            for item in node.values():
                inspect(item)
        elif isinstance(node, list):
            for item in node:
                inspect(item)
    inspect(content)
    for node in content['inner'].values():
        if 'score' in node:
            raise ValueError('growth_report_inner_not_numeric')
    cycle = value.get('service_cycle')
    if cycle is not None:
        if (not isinstance(cycle, dict) or cycle.get('version') != 'growth-report-service-cycle.v1'
                or cycle.get('mode') not in {'unavailable','first_report','monthly_update','monthly_recap','weekly_continuation'}
                or not re.fullmatch(r'\d{4}-(?:0[1-9]|1[0-2])', str(cycle.get('period') or ''))
                or cycle.get('automatic_send_allowed') is not False
                or cycle.get('merge_with_weekly') is not True):
            raise ValueError('growth_report_service_cycle_invalid')
        inspect(cycle)
    return value
