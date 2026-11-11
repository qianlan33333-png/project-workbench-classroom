"""Read original execution evidence only; never invoke schema initializers."""
import json,sys,sqlite3,os
from pathlib import Path
from datetime import datetime
from zoneinfo import ZoneInfo
root=Path('/opt/daru-v3-warehouse-candidate');sys.path.insert(0,str(root/'ops'))
from routine_read_model import select_source
now=datetime.now(ZoneInfo('Asia/Shanghai'))
refs={}
def accept(user):
 ref=user.get('workbench_user_ref')or user.get('safe_user_ref')
 if ref:refs[ref]={k:user.get(k)for k in ['customer_id','_hxcAccountIds','_mergedCustomerIDs']}
select_source(root/'var/private-data/canonical_users_live.json',{'users':accept})

db=sqlite3.connect((root/'var/daru.sqlite').resolve().as_uri()+'?mode=ro',uri=True);db.row_factory=sqlite3.Row;db.execute('PRAGMA query_only=ON');db.execute('BEGIN')
# Explicitly prohibit SQL writes, including CREATE IF NOT EXISTS in old load().
deny={sqlite3.SQLITE_INSERT,sqlite3.SQLITE_UPDATE,sqlite3.SQLITE_DELETE,sqlite3.SQLITE_CREATE_TABLE,sqlite3.SQLITE_DROP_TABLE,sqlite3.SQLITE_ALTER_TABLE,sqlite3.SQLITE_ATTACH,sqlite3.SQLITE_DETACH}
db.set_authorizer(lambda action,*_:sqlite3.SQLITE_DENY if action in deny else sqlite3.SQLITE_OK)
actions={}
for row in db.execute("SELECT action_id,canonical_user_id,week,product_key,status,reason,result_json FROM action_logs WHERE action_type='agent_user_action'"):
 value={k:row[k]for k in ['action_id','week','product_key','status','reason']};result=json.loads(row['result_json']or'{}')
 for key in ['handoff_context','matter_ref','strategy_key','branch','source_snapshot_id']:
  if key in result:value[key]=result[key]
 actions.setdefault(row['canonical_user_id'],[]).append(value)
reviews={}
for row in db.execute('SELECT safe_user_ref,kind,payload_json FROM hxc_service_fact_reviews'):reviews.setdefault(row['safe_user_ref'],{})[row['kind']]=json.loads(row['payload_json'])
import sku99_ledger,sku68_operations,sku299_renewal,sku299_owner_service
copy=sku99_ledger.copy_index(db,list(refs),now,receipts_root=root)
slots={};sku299_renewal.load_slots(db,slots)
legacy=sku68_operations.legacy_v3_successful_cycles()
if legacy is None:raise RuntimeError('original_legacy_history_unavailable')
files=['var/hxc-source-gate.json','var/order-beneficiary-reconciliation.json','var/private-data/order-service-clock/anchors.json','var/private-data/confirmed-operation-policy.json','var/private-data/hxc-identity-mappings.json','var/hxc-profile-merge-approvals.json','var/hxc-need-classifications.json','var/private-data/sku299-owner-editorial-20261021.json','var/newcomer-completion-pending.json','var/private-data/sku299-owner-service-overrides.json','var/private-data/hxc-current-entitlements-observed.json','var/private-data/hxc-subscription-activations-observed.json','ops/sku299_owner_service_policy.json','ops/product_service_scope_policy.json','ops/sku99_assets.json','ops/sku299_assets.json','docs/product-operation-packs/sku-202608052341/v1/renewal-policy.v1.json']
files+=['var/service-packs/weekly-review-pack-latest.json','var/service-packs/content-service-pack-latest.json']
assets={p:json.loads((root/p).read_text())for p in files if (root/p).is_file()}
from hxc_communication_context import open_records
holds=open_records(db)
value={'handoffs':holds,'read_only':True,'complete':True,'source_as_of':now.isoformat(),'refs':refs,'actions':actions,'fact_reviews':reviews,'copy_index':copy,'legacy_successful_cycles':sorted(legacy),'renewal_slots':slots.get('sku299_renewal_slots',{}),'previous_cycle_ends':slots.get('sku299_previous_cycle_ends',{}),'owner_policy':sku299_owner_service.load_policy(root),'assets':assets,'asset_reads':{p:'present'if p in assets else'absent_in_original'for p in files},'strategies':json.loads((root/'var/strategy-releases-v3.json').read_text())}
json.dump(value,sys.stdout,ensure_ascii=False,default=lambda x:sorted(x)if isinstance(x,set)else str(x))
db.close()
