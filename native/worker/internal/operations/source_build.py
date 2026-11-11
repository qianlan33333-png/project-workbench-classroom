import os,json,subprocess,sys
from pathlib import Path
from source_common import load
root,settings=load();build=root/'original';private=build/'var/private-data';data=build/'public/data';data.mkdir(parents=True,exist_ok=True,mode=0o700)
if not json.loads((root/'raw/unified/read-receipt.json').read_text()).get('complete'):raise RuntimeError('unified_source_incomplete')
env={**os.environ,**settings['buildEnv'],'DARU_ENV_FILE':'/dev/null','NODE_OPTIONS':'--max-old-space-size='+str(settings.get('nodeHeapMiB',3300)),'DARU_PUBLIC_DATA_DIR':str(data),'DARU_PRIVATE_DATA_DIR':str(private),'DARU_RAW_HXC_PATH':str(root/'raw/hxc-latest.json'),'DARU_RAW_SCRM_PATH':str(root/'raw/no-legacy-scrm.json'),'DARU_RAW_QIANLAN_PATH':str(root/'raw/no-legacy-qianlan.json'),'DARU_PRODUCTION_SOURCE_FACTS_PATH':str(root/'raw/crm-production-source.json'),'DARU_WAREHOUSE_CANONICAL_PATH':str(private/'warehouse-canonical.json'),'DARU_CONVERSATION_PROFILE_CANDIDATES_PATH':str(private/'conversation-profile-candidates.json'),'DARU_QUESTIONNAIRE_ASSESSMENT_PATH':str(private/'questionnaire-assessment.json'),'DARU_WORKBENCH_PATH':str(data/'workbench_live.json'),'DARU_SERVICE_PACK_DIR':str(build/'var/service-packs'),'DARU_HXC_SOURCE_GATE_PATH':str(build/'var/hxc-source-gate.json'),'DARU_HXC_SOURCE_GATE_ENFORCED':'1','DARU_VAR_DIR':str(build/'var'),'DARU_RELATIONSHIP_V1_CACHE_DIR':str(build/'var/relationship-v1-cache'),'DARU_ORDER_LEDGER_PATH':str(private/'user-order-ledger.json'),'DARU_USER_SOURCE_DETAIL_LEDGER_PATH':str(private/'user-source-detail-ledger.json'),'DARU_USER_SOURCE_DETAIL_RECORDS_PATH':str(private/'user-source-detail-records.ndjson'),'DARU_STRATEGY_AUDIENCE_USERS_PATH':str(private/'strategy-audience-users.json')}
with (root/'raw/build-stdout-private.log').open('wb')as stdout,(root/'raw/build-stderr-private.log').open('wb')as stderr:
 os.chmod(stdout.name,0o600);os.chmod(stderr.name,0o600)
 r=subprocess.run([settings['node'],str(build/'ops/build-workbench.mjs')],cwd=build,env=env,stdout=stdout,stderr=stderr,timeout=1800)
print(json.dumps({'original_build_exit':r.returncode,'new_environment':True}),flush=True)
if r.returncode:raise SystemExit(r.returncode)
