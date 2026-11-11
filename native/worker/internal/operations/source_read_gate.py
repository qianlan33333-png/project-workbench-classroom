"""Final original-side read: no schema setup, task mutation or CRM submission."""
import json,sys,sqlite3,os
from contextlib import closing
from pathlib import Path
from datetime import datetime,timedelta
from zoneinfo import ZoneInfo
os.environ['DARU_CANONICAL_READ_MODE']='stream'
root=Path('/opt/daru-v3-warehouse-candidate');sys.path.insert(0,str(root/'ops'))
from sku99_ledger import copy_index
from hxc_communication_context import open_records, blocks, handoff
from agent_user_scheduler import safe_ref
items=json.load(sys.stdin)
if not isinstance(items,list)or not 1<=len(items)<=5000:raise ValueError('gate_scope_missing')
selected={r['ref']:r for r in items}
if len(selected)!=len(items)or not all(isinstance(k,str)and isinstance(v.get('slot'),str)and v['slot'] for k,v in selected.items()):raise ValueError('gate_scope_invalid')
now=datetime.now(ZoneInfo('Asia/Shanghai'))
blocked={}
from hxc_unified_snapshot import _brief_context, _approved_merge, HXC_REF
from authoritative_archive_links import read_links
from user_reference import current_archive, resolve_reference
from hxc_profile_archive_merge import approved_hxc_archive_fields
from hxc_sync_bounds import get_json, Budget
from hxc_report_delivery import material as report_material
archive=current_archive()
links=read_links(root,archive)
budget=Budget(seconds=220)
values={}
for line in Path('/etc/daru-ops-console.env').read_text().splitlines():
    if '='in line and not line.lstrip().startswith('#'):
        k,v=line.split('=',1);values[k.strip()]=v.strip().strip(chr(34)).strip(chr(39))
source_counts={}
links_by_ref={}
for link in links:
    if link.get('mapping_status')!='exact':continue
    target=resolve_reference(link.get('workbench_user_ref'),archive)
    links_by_ref.setdefault(target,[]).append(link)
    source=link.get('safe_user_ref');source_counts[source]=source_counts.get(source,0)+1
source_by_ref={}
for item in items:
    if item.get('product')!='hxc' or(not item.get('rights')and item.get('account_optional')is True):continue
    ref=item['ref'];matches=links_by_ref.get(resolve_reference(ref,archive))or[]
    source=matches[0].get('_db_safe_user_ref')if len(matches)==1 else None
    if not isinstance(source,str)or not HXC_REF.fullmatch(source)or source_counts.get(source)!=1:
        raise ValueError('current_hxc_identity_ambiguous')
    source_by_ref[ref]=source

def read_current_contexts(wanted):
    url='https://ip.lhbl.com.cn/api/ops/private-domain/v1/sync'
    token=values['HXC_PRIVATE_DOMAIN_API_TOKEN']
    contexts={};pin=None
    def current_page(page):
        nonlocal pin
        at=datetime.fromisoformat(str(page.get('data_as_of')or'').replace('Z','+00:00'))
        if at.tzinfo is None or not -300<=(datetime.now(at.tzinfo)-at).total_seconds()<=10800:raise ValueError('current_hxc_context_stale')
        mark=page.get('watermark',page.get('snapshot_watermark'))
        if isinstance(mark,bool)or not str(mark).isdigit():raise ValueError('current_hxc_watermark_invalid')
        stamp=(str(mark),page['data_as_of'])
        if pin is not None and pin!=stamp:raise ValueError('current_hxc_context_changed')
        pin=stamp
        return page
    def add(row):
        source=row.get('safe_user_ref')
        if source not in wanted:return
        if source in contexts or row.get('deleted')is True or row.get('change_kind')=='delete':raise ValueError('current_hxc_context_unavailable')
        contexts[source]=row
    if len(wanted)<=20:
        for source in wanted:
            page=current_page(get_json(url,token,{'resource':'context','contract_version':'relationship.context-all.v1','safe_user_ref':source,'limit':2},attempts=1,budget=budget))
            rows=page.get('items')
            if (page.get('ok')is not True or page.get('contract_version')!='relationship.context-all.v1' or page.get('resource')!='context'
                or page.get('publication_state')!='current_projection' or not isinstance(rows,list)
                or page.get('total')!=len(rows) or len(rows)>1 or page.get('next_cursor')or page.get('has_more')is True
                or any(r.get('safe_user_ref')!=source for r in rows)):
                raise ValueError('current_hxc_context_unavailable')
            for row in rows:add(row)
    else:
        # The original frozen-page reader bounds a large batch by pages, not
        # one HTTP request per recipient. Retain only this batch's subjects.
        from hxc_unified_sync_store import resource_params, _fetch_page, _batches
        def getter(params):
            page=current_page(get_json(url,token,params,attempts=2,budget=budget))
            if page.get('publication_state')!='current_projection':raise ValueError('current_hxc_context_unavailable')
            return page
        page,params=_fetch_page(getter,resource_params('context-all'),budget)
        for batch,_ in _batches(getter,'context-all',params,page,budget):
            for row in batch:add(row)
    if wanted:
        status=get_json(url,token,{'resource':'sync-status'},attempts=1,budget=budget)
        head=next((h for h in status.get('resources',[])if h.get('resource')=='context-all'),{})
        if head.get('status')!='published' or head.get('contract_version')!='relationship.context-all.v1' or (str(head.get('watermark')),head.get('data_as_of'))!=pin:
            raise ValueError('current_hxc_context_changed')
    return contexts
contexts=read_current_contexts(set(source_by_ref.values()))
def current_context(ref):
    row=contexts.get(source_by_ref[ref])
    context=_brief_context(row)if row else {}
    fields=approved_hxc_archive_fields({'mapped':True,'merge_approved':_approved_merge(ref),'context':context})
    return {'mapped':bool(row),'context':context,'archive_additions':fields}
from hxc_service_binding import verify as verify_service, capture as capture_service
now=datetime.now(ZoneInfo('Asia/Shanghai'))
for item in items:
    if item.get('product')!='hxc':continue
    if not item.get('rights')and item.get('account_optional')is True:continue
    view=current_context(item['ref']);context=view.get('context')or{}
    data={'safe_user_ref':item['ref'],'profile_context':{'archive_fields':view.get('archive_additions')or{}}}
    if not item.get('service_binding'):
        if item.get('report') or capture_service(data,now=now)is not None:blocked[item['ref']]='service_binding_changed'
        continue
    try:verify_service(item['service_binding'],data,now=now)
    except ValueError:
        blocked[item['ref']]='current_service_guidance_changed';continue
    if not item.get('report'):continue
    reports=context.get('growth_reports')or[]
    if not view.get('mapped')or len(reports)!=1:
        blocked[item['ref']]='current_report_unavailable';continue
    report=reports[0];pin=item['report']
    try:delivery=report_material(report,week=now.strftime('%G-W%V'))
    except (ValueError,TypeError,KeyError):delivery=None
    if (set(pin)!={'report_ref','version','content_digest'}or any(report.get(k)!=v for k,v in pin.items())
        or not delivery or delivery.get('base_target_path')!=item.get('page')
        or delivery.get('body_sha256')!=item.get('body_sha256')):
        blocked[item['ref']]='current_report_changed'
db=sqlite3.connect((root/'var/daru.sqlite').resolve().as_uri()+'?mode=ro',uri=True)
db.row_factory=sqlite3.Row;db.execute('PRAGMA query_only=ON');db.execute('BEGIN')
from routine_read_model import select_source
current={}
def take(row):
    ref=row.get('workbench_user_ref')or row.get('safe_user_ref')
    if ref:
        current[ref]={k:row.get(k)for k in ('customer_id','_mergedCustomerIDs','_hxcAccountIds','operational_excluded','do_not_contact','contact_preference','high_risk','manual_handoff','handoff_context','safety','allocation_safety','archive')}
        if not current[ref]['_hxcAccountIds']and row.get('黄小璨用户ID'):current[ref]['_hxcAccountIds']=[str(row['黄小璨用户ID'])]
select_source(root/'var/private-data/canonical_users_live.json',{'users':take})
reviews={}
for row in db.execute('SELECT safe_user_ref,kind,payload_json FROM hxc_service_fact_reviews'):
    reviews.setdefault(row['safe_user_ref'],{})[row['kind']]=json.loads(row['payload_json'])
aliases=original_aliases({ref:current.get(ref)or{}for ref in selected},current)
reverse={}
for ref,item in selected.items():
    if contact_prohibited(current.get(ref)or{}):blocked[ref]='current_contact_prohibited'
    if item.get('fact_reviews')!=fact_reviews_fingerprint(reviews,set(aliases[ref]+(item.get('aliases')or[ref]))):blocked[ref]='original_fact_reviews_changed'
    if str((current.get(ref)or{}).get('customer_id'))!=str(item['customer_id']):blocked[ref]='original_identity_changed'
    if item.get('product')=='hxc':
        accounts={str(x)for x in(current.get(ref)or{}).get('_hxcAccountIds')or[]}
        if not isinstance(item.get('rights'),dict)or accounts!=set(item['rights']):blocked[ref]='original_account_identity_changed'
    for alias in set(aliases[ref]+(item.get('aliases')or[ref])):
        if alias in reverse and reverse[alias]!=ref:raise ValueError('original_identity_alias_ambiguous')
        reverse[alias]=ref
index=copy_index(db,list(reverse),now,receipts_root=root)
for alias,rows in index.items():
    ref=reverse[alias]
    for row in rows:
        binding=row.get('binding')or{}
        if (row.get('slot_key')or binding.get('slot_key'))==selected[ref]['slot']:blocked[ref]='original_action_already_owned'
        if row.get('handoff')and row.get('state')not in ('sent','cancelled'):blocked[ref]='original_plan_outcome_pending'
        if row.get('state')=='sent':
            try:at=datetime.fromisoformat(row['sent_at'].replace('Z','+00:00'))
            except (KeyError,ValueError,AttributeError):blocked[ref]='original_send_time_unverified';continue
            if at.tzinfo is None or now-at<timedelta(hours=24):blocked[ref]='actual_send_cooldown_24h'
holds=open_records(db)
for hold in holds:
    ref=reverse.get(hold.get('workbench_user_ref'))
    if ref and blocks(hold.get('handoff_context'),product_key=selected[ref].get('product')or'',matter_ref=selected[ref].get('matter')or''):blocked[ref]='original_human_handoff_active'
for ref,item in selected.items():
    user=current.get(ref)or{}
    held=user.get('manual_handoff')is True or any((user.get(k)or{}).get('manual_handoff')is True for k in ('safety','allocation_safety'))
    if held:
        scope=user.get('handoff_context')
        known=[h for h in holds if reverse.get(h.get('workbench_user_ref'))==ref]
        if not scope or not any(handoff(scope)==handoff(h.get('handoff_context'))for h in known):scope=None
        if blocks(scope,product_key=item.get('product')or'',matter_ref=item.get('matter')or''):blocked[ref]='original_human_handoff_active'
latest={}
for row in db.execute("SELECT canonical_user_id,todo_type,status,result_json FROM action_logs WHERE action_type='copy_review' ORDER BY created_at DESC,action_id DESC"):
    ref=reverse.get(safe_ref(row['canonical_user_id']))
    if ref is None:continue
    body=json.loads(row['result_json']or'{}')
    key=(ref,row['canonical_user_id'],body.get('batch_id')or'',body.get('candidate_id')or row['todo_type'])
    latest.setdefault(key,row['status'])
for key,status in latest.items():
    if status=='manual_handoff':blocked[key[0]]='original_human_handoff_active'
print(json.dumps({'ready':not blocked,'blocked_count':len(blocked)},ensure_ascii=False))
db.close()
