"""Selected original rules; source I/O is supplied by the host as frozen inputs."""
from copy import deepcopy
from datetime import datetime, timedelta
from urllib.parse import urlsplit, parse_qs
import re
import hashlib, json

def material(report, *, week=None):
    from hxc_growth_report import validate_handoff
    validate_handoff(report)
    m = deepcopy(report.get('delivery_material') or {})
    selection = report.get('weekly_selection') or {}
    if week:
        monday = datetime.strptime(week+'-1', '%G-W%V-%u').date()
        if (selection.get('version') != 'growth-report-weekly-selection.v1'
                or selection.get('primary_service') != 'growth_report'
                or selection.get('replace_weekly_review') is not True
                or selection.get('period_start') != monday.isoformat()
                or selection.get('period_end') != (monday+timedelta(days=6)).isoformat()
                or selection.get('automatic_send_allowed') is not False):
            return None
    path = urlsplit(str(m.get('base_target_path') or ''))
    query = parse_qs(path.query, keep_blank_values=True)
    if (m.get('purpose') != 'growth_report' or m.get('content_type') != 'link'
            or m.get('content_id') != report['report_ref']
            or m.get('report_version') != report['version']
            or m.get('content_digest') != report['content_digest']
            or m.get('requires_dispatch_signature') is not True
            or path.scheme or path.netloc or path.fragment
            or path.path != '/pages/outcome/outcome' or set(query) != {'id'}
            or len(query['id']) != 1 or not re.fullmatch(r'[A-Za-z0-9_-]{1,64}', query['id'][0])):
        raise ValueError('growth_report_delivery_material_invalid')
    body=report.get('body_markdown') or json.dumps(report['content'],ensure_ascii=False)
    priority = report['content']['overview']['priority']['text']
    return dict(m, kind='content', summary=priority, specific_help=priority,
                source_ref=report['report_ref'], body_coverage='current_hxc_report',
                key_judgments=[x.get('text') for x in report['content'].get('insights') or [] if x.get('text')],
                version=report['version'], body_catalog_version=report['version'],
                report_ref={k: report[k] for k in ('report_ref','version','content_digest')},
                user_body=body, content_body=body, body_status='ready', body_chars=len(body),
                body_sha256=hashlib.sha256(body.encode()).hexdigest(),
                body_source_path='hxc-current-report:'+report['report_ref'],
                body_version_binding=str(report['version'])+':'+report['content_digest'],
                material_rule='这是同一份综合评估报告，不称作每周学习总结；只讲当前重点和一项支持，不宣称用户已确认。',
                support_options=['看报告并补充变化','回学习管家确认重点或继续讨论','本人选择深度咨询'])
