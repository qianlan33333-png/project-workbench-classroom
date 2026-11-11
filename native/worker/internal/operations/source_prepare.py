"""Project the original clean result and read-only ledgers into four resources.

All rule functions remain in the original code bundle. This module only replaces
the old filesystem/SQLite source joins with frozen JSONL inputs for MySQL.
"""
from collections import Counter, defaultdict
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import sys
from source_common import load, write_json, original_aliases, original_reviews, alias_execution_requires_review, original_pack_redlines, generation_profile, generation_method

root, settings = load()
original = root / 'original'
raw = root / 'raw'
private = original / 'var/private-data'
sys.path.insert(0, str(original / 'ops'))
from routine_read_model import select_source
import hxc_routine_policy as routine
from hxc_operating_events import actual_sends
from copy_profile_context import compact_projection
from agent_user_scheduler import safe_ref

history = json.loads((raw / 'original-history.json').read_text())
generation = json.loads((raw / 'original-generation.json').read_text())
methods = json.loads((raw / 'original-methods.json').read_text())
writing = json.loads((raw / 'original-writing.json').read_text())
independent = os.environ.get('WORKBENCH_OPERATIONS_INDEPENDENT') == '1'
if writing.get('read_only') is not True:raise ValueError('writing_source_unverified')
takeovers=json.loads((raw/'original-handoffs.json').read_text())
if takeovers.get('complete')is not True or takeovers.get('read_only')is not True or not isinstance(takeovers.get('handoffs'),list):
    raise ValueError('original_copy_review_handoffs_incomplete')
if history.get('complete') is not True or history.get('read_only') is not True:
    raise ValueError('original_history_incomplete')
for filename in ['weekly-review-pack-latest.json','content-service-pack-latest.json']:
    path='var/service-packs/'+filename
    state=(history.get('asset_reads')or{}).get(path)
    if state not in ('present','absent_in_original') or (state=='present')!=(path in history['assets']):
        raise ValueError('original_pack_read_unverified')
rows = []
header = select_source(private / 'canonical_users_live.json', {'users': rows.append},
                       ('snapshot_id', 'generated_at', 'crm_source_snapshot_id', 'count'))
snapshot = header['snapshot_id']
users = {u.get('workbench_user_ref') or u.get('safe_user_ref'): u for u in rows}
aliases_by_ref=original_aliases(users,history['refs'])
audiences={}
audience_header=select_source(private/'strategy-audience-users.json',
    {'users':lambda row:audiences.__setitem__(row['safe_user_ref'],row)},('snapshot_id',))
if audience_header.get('snapshot_id')!=snapshot:
    raise ValueError('original_audience_snapshot_mismatch')
if not snapshot or len(users) != len(rows) or len(rows) != header['count']:
    raise ValueError('canonical_identity_or_count_invalid')
evidence = json.loads((original / 'public/data/activation_evidence_live.json').read_text())
workbench = json.loads((original / 'public/data/workbench_live.json').read_text())
if evidence['snapshot_id'] != snapshot or workbench['snapshot_id'] != snapshot:
    raise ValueError('original_clean_snapshot_mismatch')

# The original compact source reader keeps its exact activity, rights and date
# semantics. The link is local to this private collection, never on a source host.
source_link = private / 'source-snapshots/hxc/hxc-latest.json'
source_link.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
if not source_link.exists():
    source_link.symlink_to(raw / 'hxc-latest.json')
static = routine._static_sources(original)
if static['complete'] is not True:
    raise ValueError('full_hxc_activity_coverage_required')

def active(resource):
    with (raw / 'unified' / (resource + '.jsonl')).open() as stream:
        for line in stream:
            item = json.loads(line)
            if not item.get('deleted') and item.get('change_kind') != 'delete':
                yield item

# The current original OneID/HMAC projection is authoritative. Legacy API
# account-links are retained for audit but cannot transfer one person's facts.
links = json.loads((raw / 'projected-account-links.json').read_text())['links']
reverse = Counter(links.values())
links = {source: ref for source, ref in links.items() if reverse[ref] == 1}
catalog = {str(x.get('content_id')): x for x in active('catalog')}
catalog_by_key = {str(x.get('content_type')) + ':' + str(x.get('content_id')): x
                  for x in active('catalog')}
reviews = {k: list(v) for k, v in static['reviews'].items()}
unified_reviews = defaultdict(list)
growth = {}
content_history = defaultdict(set)

def exact_account(source):
    ref = links.get(source)
    user = users.get(ref) or {}
    ids = {str(v) for v in user.get('_hxcAccountIds') or []}
    if not ids and user.get('黄小璨用户ID'):
        ids = {str(user['黄小璨用户ID'])}
    return (ref, next(iter(ids))) if user.get('customer_mapping_status') == 'resolved' and len(ids) == 1 else (None, None)

for item in active('weekly-reviews'):
    ref, uid = exact_account(item.get('safe_user_ref'))
    if ref and item.get('summary') and item.get('base_target_path'):
        unified_reviews[uid].append(dict(item, id=item.get('review_id'), user_id=uid,
            created_at=item.get('generated_at'), source_ref=item.get('resource_key'), unified_published=True))
reviews.update(unified_reviews)
from hxc_growth_report import validate_handoff
contexts_by_ref=defaultdict(list)
for item in active('context-all'):
    ref,uid=exact_account(item.get('safe_user_ref'))
    if ref:contexts_by_ref[ref].append(item)
ambiguous_contexts={ref for ref,items in contexts_by_ref.items()if len(items)!=1}
for ref,items in contexts_by_ref.items():
    if ref in ambiguous_contexts:continue
    item=items[0];ref,uid=exact_account(item.get('safe_user_ref'))
    reports = item.get('growth_reports') or []
    if not ref or len(reports) != 1:
        continue
    try:
        validate_handoff(reports[0])
    except (ValueError, TypeError):
        continue
    growth[uid] = reports[0]
for item in active('content-activity'):
    ref = links.get(item.get('safe_user_ref'))
    if (ref and item.get('attribution_status') == 'attributed'
            and item.get('behavior_type') in {'view','effective_view','chat_start','layer_done','takeaway','pushed'}
            and item.get('content_type') and item.get('content_id')):
        content_history[ref].add((str(item['content_type']), str(item['content_id'])))

# Same offset, snapshot, identity and actual-send interpretation as load_sources.
ledger = json.loads((private / 'user-source-detail-ledger.json').read_text())
if ledger.get('snapshot_id') != snapshot:
    raise ValueError('crm_correspondence_snapshot_mismatch')
correspondence = {}
with (private / 'user-source-detail-records.ndjson').open('rb') as stream:
    metadata = json.loads(stream.readline())
    if metadata.get('snapshot_id') != snapshot or metadata.get('generated_at') != ledger.get('generated_at'):
        raise ValueError('crm_correspondence_record_generation_mismatch')
    for ref, entry in ledger['user_index'].items():
        user = users.get(ref) or {}
        if not (user.get('_hxcAccountIds') or user.get('黄小璨用户ID')):
            continue
        offset, size = entry.get('offset'), entry.get('bytes')
        if not isinstance(offset, int) or not isinstance(size, int) or offset < 0 or not 2 <= size <= 128*1024*1024:
            raise ValueError('crm_correspondence_offset_invalid')
        stream.seek(offset)
        blob = stream.read(size)
        if len(blob) != size or not blob.endswith(b'\n'):
            raise ValueError('crm_correspondence_record_truncated')
        detail = json.loads(blob)
        if detail.get('safe_user_ref') != ref:
            raise ValueError('crm_correspondence_identity_mismatch')
        correspondence[ref] = actual_sends(detail, now=routine.stamp(ledger['generated_at']))
sends = {k: list(v) for k,v in static['sends'].items()}
for ref, events in correspondence.items():
    ids = users[ref].get('_hxcAccountIds') or ([users[ref]['黄小璨用户ID']] if users[ref].get('黄小璨用户ID') else [])
    if len(ids) == 1:
        sends.setdefault(str(ids[0]), []).extend(events)
entitlement_events = defaultdict(list)
for event in static['raw'].get('entitlement_events') or []:
    entitlement_events[str(event.get('user_id') or '')].append(event)

todos = defaultdict(list)
for todo in workbench.get('todos_candidates_top50') or workbench.get('todos_top20') or []:
    ref = todo.get('safe_user_ref') or safe_ref(todo.get('canonical_user_id'))
    if ref not in users:
        raise ValueError('priority_todo_identity_missing')
    todos[ref].append(todo)
# Mechanical HXC potential rotation is retired by the original product scope
# filter. It must not be revived merely to inflate migration candidate counts.

def json_default(value):
    if isinstance(value, datetime):
        return value.isoformat()
    if isinstance(value, (set, tuple)):
        return sorted(value)
    raise TypeError(type(value).__name__)

files = {}
def emit(name, records):
    path = root / (name + '.jsonl')
    count = 0
    maximum = 0
    partial = path.with_suffix('.partial')
    with partial.open('wb') as stream:
        partial.chmod(0o600)
        for record in records:
            encoded = (json.dumps(record, ensure_ascii=False, default=json_default, allow_nan=False, separators=(',',':'))+'\n').encode()
            if len(encoded)>240000:
                raise ValueError('source_record_exceeds_existing_resource_limit_'+name)
            stream.write(encoded)
            maximum = max(maximum, len(encoded))
            count += 1
    partial.replace(path)
    with path.open('rb') as stream:
        files[name] = {'sha256': hashlib.file_digest(stream, 'sha256').hexdigest(), 'count': count, 'maxRecordBytes': maximum}

def keyed(index, keys):
    return {k: index[k] for k in keys if k in index}

contexts={}
if independent:
    from source_profile import profile_rows
    directory_positions = profile_rows(root, users, contexts_by_ref, links, snapshot, history['assets'])
else:
    directory_positions = users

def profiles():
    for ref in directory_positions:
        user = users[ref]
        user = {k:v for k,v in user.items() if k not in ('_qianlanUserMessages','_hxcRecentUserMessages','_warehouseSourceRecords')}
        # Audience preview fields are the original build's published facts.
        # Do not make a second segmentation model from private free text.
        public=audiences.get(ref)or{}
        if not public and user.get('operational_excluded')is not True:
            raise ValueError('original_audience_user_missing')
        user['audience_source']=public
        for key in ('profile','has_current_need','relationship_context_available','hxc_summary',
                    'hxc_need_evidence','order_summary','strategy_signals'):
            if key in public:user[key]=public[key]
        user = dict(user, safe_user_ref=ref, source_snapshot_id=snapshot,
                    identity_resolved=user.get('customer_mapping_status') == 'resolved',
                    identity_conflict=bool(user.get('_customerIdentityConflict') or user.get('identity_conflict') or ref in ambiguous_contexts),
                    priority_todos=todos[ref], priority_todos_complete=True)
        user['_orderSummary']={k:v for k,v in (user.get('_orderSummary')or{}).items()
            if k in ('paid_count','refund_count','has_refund','product_keys','latest_paid_at','total_paid')}
        ids = [str(x) for x in user.get('_hxcAccountIds') or []]
        if not ids and user.get('黄小璨用户ID'):
            ids = [str(user['黄小璨用户ID'])]
        compact = compact_projection((generation.get(ref) or {}).get('result') or {}, 'content_service')
        per = {key: keyed(static[key], ids) for key in ('accounts','metrics','histories','seen','product_grants_by_user','current_entitlements_by_user','user_message_times')}
        per.update(users={}, as_of=static['raw']['synced_at'], complete=True,
                   packs=original_pack_redlines(history['assets'],ids), reviews=keyed(reviews, ids), sends=keyed(sends, ids),
                   growth_reports=keyed(growth, ids), correspondence=keyed(correspondence,[ref]),
                   catalog={}, content_history_by_ref=keyed(content_history,[ref]),
                   entitlement_events=[], entitlement_events_by_user=keyed(entitlement_events,ids),
                   completion_requests=keyed((history['assets'].get('var/newcomer-completion-pending.json') or {}).get('requests',{}),[ref]),
                   crm_send_coverage='partial_archive' if ref in correspondence else 'unavailable')
        # Keep rule inputs intact; only the downstream model profile is compact.
        user['generation_profile']=generation_profile(user,compact)
        if (generation.get(ref)or{}).get('salutation'):
            user['generation_profile']['salutation']=generation[ref]['salutation']
        contexts[ref]={'complete':True,'snapshot_id':snapshot,'sources':per,
            'private_from_record':True,
            'catalog_from_shared':True,'fact_reviews':{ref:original_reviews(history['fact_reviews'],aliases_by_ref[ref])},
            'evidence':(evidence.get('users') or {}).get(ref,{}), 'profile':{'conversation':compact},
            'editorial_assignments':history['assets'].get('var/private-data/sku299-owner-editorial-20261021.json') or {}}
        yield {'id':ref,'fields':{'raw':user}}

def histories():
    # Original action IDs are hashed from the same stable reference. Preserve
    # exact old aliases on a committed customer merge; never join by a name.
    holds=defaultdict(list)
    for hold in [*history.get('handoffs',[]),*takeovers['handoffs']]:
        holds[hold.get('workbench_user_ref')].append(hold)
    for ref, user in users.items():
        aliases=aliases_by_ref[ref]
        original_actions=[]
        for alias in aliases:
            original_actions.extend(history['actions'].get(alias,[]))
        yield {'id':ref,'fields':{'complete':True,'actions':original_actions,'hxc_ledger_complete':True,
            'hxc_context':contexts[ref],
            'identity_lineage_requires_review':alias_execution_requires_review(history,ref,aliases),
            'contact_history_scope':{'crm':'complete_original_archive_contract','original_executor':'local_history_only' if independent else 'complete_read_only_ledger','original_aliases':aliases,'unarchived_channels':'unknown','absence_means_never_contacted':False},
            'handoffs':[dict(h,workbench_user_ref=ref) for alias in aliases for h in holds[alias]],
            'copy_index':{ref:[v for alias in aliases for v in history['copy_index'].get(alias,[])]},
            'renewal_slots':{ref:[v for alias in aliases for v in history['renewal_slots'].get(alias,[])]},
            'previous_cycle_ends':{ref:[v for alias in aliases for v in history['previous_cycle_ends'].get(alias,[])]},
            'legacy_successful_cycles':[v for v in history['legacy_successful_cycles'] if any(alias in v for alias in aliases)],
            'source_as_of':history['source_as_of']}}

emit('profiles',profiles())
emit('history',histories())
assets=history['assets']
operation_inputs={'writing_reference':writing.get('newcomer_reference'),'product_service_scope_policy':assets['ops/product_service_scope_policy.json'],
    'sku99_assets':assets['ops/sku99_assets.json'],'sku299_assets':assets['ops/sku299_assets.json'],
    'renewal_policy':assets['docs/product-operation-packs/sku-202608052341/v1/renewal-policy.v1.json'],
    'shared_sources':{'source_snapshot_id':snapshot,'catalog':{}}}
catalog_chunks=[]
chunk=[]
size=0
for item in catalog_by_key.values():
    item_size=len(json.dumps(item,ensure_ascii=False).encode())
    if chunk and size+item_size>180000:
        catalog_chunks.append(chunk);chunk=[];size=0
    chunk.append(item);size+=item_size
if chunk:catalog_chunks.append(chunk)
emit('strategies',[{'id':key,'fields':value} for key,value in history['strategies'].items()]+[
    {'id':'owner-service-policy','fields':{'policy':history['owner_policy']}},
    {'id':'original-operation-inputs','fields':operation_inputs}]+[
    {'id':'original-catalog-'+str(i).zfill(4),'fields':{'source_snapshot_id':snapshot,'catalog_entries':chunk}}
    for i,chunk in enumerate(catalog_chunks)]+[
    {'id':'original-writing-'+str(i).zfill(4),'fields':{'path':path,'asset':asset}}
    for i,(path,asset) in enumerate(sorted(writing['assets'].items()))])
emit('methods', ({'id': key, 'fields': {'method': resources if independent and isinstance(resources, dict)
                                      else generation_method(key, resources)}}
                 for key, resources in methods.items()))
report={'raw_sources_read':True,'new_environment_cleaning':True,'finished_archive_imported':False,
    'original_copy_review_handoffs':len(takeovers['handoffs']),
    'snapshot_id':snapshot,'archive_count':len(users),'original_strategies':len(history['strategies']),
    'hxc':json.loads((raw/'hxc-read-receipt.json').read_text()),
    'crm':json.loads((raw/'crm-read-receipt.json').read_text()),
    'unified':json.loads((raw/'unified/read-receipt.json').read_text()),
    'source_times':{'cleaned_at':header['generated_at'],'history_as_of':history['source_as_of']},
    'identity':json.loads((raw/'projected-account-links.json').read_text())['stats'],
    'remaining_uncertainty':['CRM 联系历史仅限原归档覆盖','多源水位各自保留，未匹配的新账号不借用他人资料']}
write_json(root/'native-result.json',{'directory':str(root),'files':files,'report':report})
print(json.dumps({'archive_count':len(users),'relations':files},ensure_ascii=False))
