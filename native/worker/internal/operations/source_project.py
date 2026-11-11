import json,os,sys,hashlib,importlib.util,subprocess
from pathlib import Path
from source_common import load
root,settings=load();original=root/'original';private=original/'var/private-data';sys.path.insert(0,str(original/'ops'))
from routine_read_model import select_source
from agent_user_scheduler import safe_ref
spec=importlib.util.spec_from_file_location('conversation_projection',original/'ops/export-conversation-profile-candidates.py');module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
profiles=json.loads((root/'raw/original-profiles.json').read_text());projected={};complete=True;people=[]
def user(row):
 global complete
 source=str(row.get('unionid')or row.get('UnionID')or row['customer_id']);ref=safe_ref(source)
 people.append({'customer_id':str(row['customer_id']),'unionid':row.get('unionid')or row.get('UnionID')or''})
 original_profile=profiles.get(ref)
 if not original_profile:return
 result=original_profile['result'];fields={k:item for k,value in (result.get('fields')or{}).items()if k in module.FIELDS and (item:=module.safe_field(value))}
 stated=module.crm_user_statements(result.get('fields')or{},row.get('_warehouseSourceRecords')or{})
 complete=complete and module.statement_sources_complete(result.get('fields')or{},stated)
 if fields or stated:projected[ref]={'fields':fields,'crm_user_statements':stated,'updated_at':original_profile['updated_at']}
header=select_source(private/'warehouse-canonical.json',{'users':user},('hxc_identity_resolution','source_snapshot_id'))
(private/'conversation-profile-candidates.json').write_text(json.dumps({'version':1,'count':len(projected),'profiles':projected,'crm_statement_sources_complete':complete},ensure_ascii=False))
users=[];select_source(root/'raw/hxc-latest.json',{'users':users.append})
# Identity joins and the opaque HXC reference retain their original Node functions.
node=Path(settings['node'])
js="""import fs from 'node:fs';import crypto from 'node:crypto';import {joinWarehouseHxc} from './ops/warehouse-hxc-projection.mjs';import {relationshipSafeUserRef} from './ops/relationship-safe-ref.mjs';
const data=JSON.parse(fs.readFileSync(0,'utf8'));const joined=joinWarehouseHxc(data.people,data.users,data.resolution);let links={};let conflicts=new Set();
for(const row of joined.rows){if(row._hxcAccountIds.length!==1)continue;const uid=row._hxcAccountIds[0];const source=relationshipSafeUserRef({'黄小璨用户ID':uid});if(!source)throw Error('relationship_reference_unconfigured');
const target=row._workbenchStableRef||'usr_'+crypto.createHash('sha256').update(String(row.unionid||row.customer_id)).digest('hex').slice(0,12);
if(links[source]&&links[source]!==target)conflicts.add(source);else links[source]=target;}
for(const source of conflicts)delete links[source];console.log(JSON.stringify({links,stats:joined.stats}));"""
env={**os.environ,**settings['buildEnv']}
r=subprocess.run([str(node),'--input-type=module','-e',js],cwd=original,env=env,input=json.dumps({'people':people,'users':users,'resolution':header['hxc_identity_resolution']}),text=True,capture_output=True)
if r.returncode:
 from source_common import write_json
 write_json(root/'raw/project-node-error.json',{'stderr':r.stderr})
 raise RuntimeError('original_identity_projection_failed')
links=json.loads(r.stdout);items={}
with (root/'raw/unified/context-all.jsonl').open()as stream:
 for line in stream:
  row=json.loads(line)
  if row.get('deleted')or row.get('change_kind')=='delete':continue
  target=links['links'].get(row.get('safe_user_ref'))
  if not target:continue
  questionnaire=row.get('questionnaire')or{};assessment=row.get('ai_operator_assessment')or{}
  if not questionnaire.get('submission_count')and not assessment:continue
  items[row['safe_user_ref']]={'label':'AI 学习＋应用落地能力评估','workbench_user_ref':target,'source':'hxc_unified_snapshot.context-all','source_updated_at':row.get('source_updated_at')or row.get('updated_at')or'','questionnaire':questionnaire,'assessment':assessment,'questionnaire_form_attribution':'unknown','assessment_evidence_state':assessment.get('evidence_state')or'unavailable'}
(private/'questionnaire-assessment.json').write_text(json.dumps({'version':'ai-learning-application-assessment.v1','identity_snapshot_id':header['source_snapshot_id'],'source':'hxc_unified_snapshot.context-all','count':len(items),'items':items},ensure_ascii=False))
(root/'raw/projected-account-links.json').write_text(json.dumps(links))
print(json.dumps({'conversation_profiles':len(projected),'statement_sources_complete':complete,'assessments':len(items),'identity':links['stats']}))
