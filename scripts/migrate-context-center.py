#!/usr/bin/env python3
"""Import source configuration as data; never execute exported code or enable old runners.
Credentials are read/written only in the caller's protected migration directory.
"""
import argparse, ast, base64, hashlib, io, json, os, pathlib, secrets, urllib.request, urllib.error, urllib.parse, zipfile

P=argparse.ArgumentParser();P.add_argument('--source',required=True);P.add_argument('--private-dir',required=True);P.add_argument('--target-cli');P.add_argument('--apply',action='store_true');P.add_argument('--enable-new-schedules',action='store_true');P.add_argument('--overwrite-existing',action='store_true');P.add_argument('--plugin-url',default='https://dsh.qianlan333.cloud/workbench-components/mcp');P.add_argument('--asset-root',default='/var/lib/workbench-context-plugin/assets');args=P.parse_args()
source=pathlib.Path(args.source).resolve();private=pathlib.Path(args.private_dir).resolve();private.mkdir(parents=True,exist_ok=True);private.chmod(0o700)
def digest(raw):return hashlib.sha256(raw).hexdigest()
def read(p,default=None):
 try:return json.loads(p.read_text())
 except FileNotFoundError:return default

def secret_write(p,value):p.write_text(json.dumps(value,ensure_ascii=False,indent=2));p.chmod(0o600)

def env_values():
 env={}
 for p in (source/'private-connections').glob('*.env'):
  for line in p.read_text().splitlines():
   if '=' in line and not line.lstrip().startswith('#'):
    k,v=line.split('=',1);env[k.strip()]=v.strip().strip('"').strip("'")
 env.update(read(source/'private-connections/runtime-environment.json',{}))
 return env

def source_operations():
 tree=ast.parse((source/'ops/local-api.py').read_text());ops={}
 # HTTP GET is not proof of no side effects. Only source-audited routes may be
 # used by read-only experts; all other routes need an explicit owner action.
 pure_reads={'/api/local/session','/api/local/v3/opportunity-pool','/api/local/v3/customer-report-archive','/api/local/v3/operation-evaluation'}
 for cls in tree.body:
  if not isinstance(cls,ast.ClassDef):continue
  for f in cls.body:
   if not isinstance(f,ast.FunctionDef) or f.name not in ('do_GET','do_POST'):continue
   method=f.name[3:]
   for n in ast.walk(f):
    if not isinstance(n,ast.Constant) or not isinstance(n.value,str) or not n.value.startswith('/api/local/') or len(n.value)>150:continue
    route=n.value
    if route.endswith('/'):continue  # Dynamic path families are recorded as source-owned, not guessed.
    key=route.removeprefix('/api/local/').replace('/','_').replace('-','_')+'_'+method.lower()
    prepare=method=='POST' and route in ['/api/local/copy/generate','/api/local/agent/copy-request','/api/local/diagnosis/generate','/api/local/automation/product-activation/preview']
    pure=method=='GET' and route in pure_reads
    ops[key]={'method':method,'path':route,'description':route.removeprefix('/api/local/'),'ownerOnly':not pure and not prepare,'effect':'read' if pure else 'prepare' if prepare else 'execute'}
 # Stable operation aliases used by the shared tools and documentation.
 aliases={'opportunity_pool':'v3_opportunity_pool_get','copy_generate':'copy_generate_post','agent_copy_request':'agent_copy_request_post'}
 for alias,key in aliases.items():
  if key in ops:ops[alias]=ops[key]
 return ops

manifest=read(source/'prompts/runtime/manifest.json',{})
active=set()
def refs(v):
 if isinstance(v,dict):
  for x in v.values():refs(x)
 elif isinstance(v,list):
  for x in v:refs(x)
 elif isinstance(v,str) and v.endswith(('.json','.md')):active.add(v)
refs(manifest)
archived=set(manifest.get('archive_only_resources') or [])
entries=[]
for directory in ['prompts','docs/product-operation-packs','docs/user-operations-agent']:
 for p in sorted((source/directory).rglob('*')):
  if p.is_file() and not p.is_symlink() and p.suffix in ['.json','.md']:
   raw=p.read_bytes();entries.append({'path':str(p.relative_to(source)),'sha256':digest(raw),'bytes':len(raw),'kind':'prompt' if directory=='prompts' else 'policy','activeReference':p.name in active and p.name not in archived,'imported':True,'executionOwner':'source-compatible' if directory=='prompts' else 'configuration'})
for name in ['strategy-releases-v3.json','strategy-drafts-v3.json','hxc-source-gate.json','content-service-strategy-latest.json','prompt-asset-versions.json','user-operations-agent/runtime-private.json','hxc-profile-merge-approvals.json']:
 p=source/'var'/name
 if p.is_file():entries.append({'path':str(p.relative_to(source)),'sha256':digest(p.read_bytes()),'bytes':p.stat().st_size,'kind':'private-runtime' if 'runtime-private' in name else 'strategy','imported':True,'executionOwner':'source-compatible'})
agent_base=read(source/'docs/user-operations-agent/runtime.v1.json',{})
agent_private=read(source/agent_base.get('private_runtime_config','nonexistent'),{})
private_ref=agent_base.get('private_runtime_config')
if private_ref and (source/private_ref).is_file():
 p=source/private_ref;entries.append({'path':private_ref,'sha256':digest(p.read_bytes()),'bytes':p.stat().st_size,'kind':'private-runtime','imported':True,'executionOwner':agent_private.get('execution_owner',agent_base.get('execution_owner'))})
known_secrets=[v for k,v in env_values().items() if len(v)>=12 and any(x in k.upper() for x in ['PASSWORD','TOKEN','API_KEY','SECRET'])]
for entry in entries:
 raw=(source/entry['path']).read_text(errors='replace')
 if any(secret in raw for secret in known_secrets):entry['kind']='private-config'
operations=source_operations()
report={'version':'context-migration.v1','source':'original-context-center','sourceApplicationStillRunning':True,'entries':entries,'configurationCount':len(entries),'operationCount':len(operations),'promptManifestSha256':digest((source/'prompts/runtime/manifest.json').read_bytes()),'agentMode':agent_private.get('mode',agent_base.get('mode')),'agentExecutionOwner':agent_private.get('execution_owner',agent_base.get('execution_owner')),'sourceTimers':read(source/'migration-timers.json',[]),'dependencies':{'publishedData':'original source read-only snapshot','complexOperations':'original API via compatibility plugin; original business gates remain authoritative','newDataAndBatchJobs':'independent plugin/MySQL'},'automaticSendEnabledByMigration':False,'newRunnersForOriginalTimersEnabled':False}
secret_write(private/'migration-manifest.json',report)
secret_write(private/'operation-catalog.json',operations)
if not args.apply:
 print(json.dumps({'planned':True,'configurations':len(entries),'operations':len(operations),'report':str(private/'migration-manifest.json')},ensure_ascii=False));raise SystemExit(0)
if not args.target_cli:raise SystemExit('--target-cli required for apply')
cli=read(pathlib.Path(args.target_cli));base=cli['url'].rstrip('/');auth='Basic '+base64.b64encode(('owner:'+cli['password']).encode()).decode()

def api(path,body=None,method=None):
 req=urllib.request.Request(base+'/api/v1'+path,data=None if body is None else json.dumps(body,ensure_ascii=False).encode(),method=method or ('GET' if body is None else 'POST'),headers={'Authorization':auth,'Content-Type':'application/json'})
 try:
  with urllib.request.urlopen(req,timeout=45) as r:return json.load(r)
 except urllib.error.HTTPError as e:
  # Do not print response payloads containing business data or private parameters.
  raise RuntimeError('target API failed at '+path+' HTTP '+str(e.code)) from None

def save_file(project_id,path,content):
 base_hash=''
 try:
  current=api('/projects/'+project_id+'/file?path='+urllib.parse.quote(path)+'&view=1')
  if current.get('content')==content:return
  base_hash=current.get('hash','')
 except RuntimeError as e:
  if 'HTTP 404' not in str(e):raise
 return api('/projects/'+project_id+'/file',{'path':path,'content':content,'baseHash':base_hash},'PUT')

def upload_expert(name,description,instructions,component_key,existing=None):
 skill='context-'+component_key.replace('.','-')
 if existing:skill=existing.get('skillName') or skill
 content='---\nname: '+skill+'\ndisplay-name: '+json.dumps(name,ensure_ascii=False)+'\ndescription: '+json.dumps(description,ensure_ascii=False)+'\ncomponent-tools: '+json.dumps([component_key])+'\n---\n\n'+instructions
 out=io.BytesIO()
 with zipfile.ZipFile(out,'w',zipfile.ZIP_DEFLATED) as z:z.writestr('SKILL.md',content)
 boundary='workbench-migration-'+secrets.token_hex(12);chunks=[]
 if existing:
  for key,value in [('id',existing['id']),('baseDigest',existing['digest'])]:chunks.append(('--'+boundary+'\r\nContent-Disposition: form-data; name="'+key+'"\r\n\r\n'+value+'\r\n').encode())
 chunks.append(('--'+boundary+'\r\nContent-Disposition: form-data; name="file"; filename="expert.zip"\r\nContent-Type: application/zip\r\n\r\n').encode()+out.getvalue()+b'\r\n');chunks.append(('--'+boundary+'--\r\n').encode())
 req=urllib.request.Request(base+'/api/v1/experts/import',data=b''.join(chunks),headers={'Authorization':auth,'Content-Type':'multipart/form-data; boundary='+boundary})
 with urllib.request.urlopen(req,timeout=45) as r:return json.load(r)

env=env_values();db=read(private/'mysql.json');bindings=read(private/'bindings.json',{})
base_instructions='''你负责当前项目的原上下文中心工作。先读取项目中的“原中心配置/迁移清单.json”，区分发布版、草稿、停用配置和兼容依赖。专家是方法包，原业务事实保存在项目/数据插件，不写入全局方法。\n使用list_components查目录，通过call_component调用当前项目组件。已保存的固定任务可按运行配置执行。查看read_component_results或component_status获取真实结果，不把受理当成功。\n数据或身份缺失时输出待核，不猜测、不自动合并身份，不覆盖订单和权益事实。营销计划互斥，必要服务独立保留；连续运营窗口仍按已确认配置，不自行写死两三天。\n批量生成每批最多2000条，先取得原策略有效名单，不把全库身份清单当运营资格；优先保留已成功结果，失败项另处理，用户上下文彼此隔离。\n原中心兼容操作继续使用原校验和提示词工程。准备/生成不代表发送授权。发送、审批、修改原配置须本人页面确认；不绕过禁用状态、人工接手和原业务门禁。\n原中心仍运行，不能开启第二套原定时或重复发送。结果不明先核对原回执，不能换编号重发。\n'''
families=[
 ('data','用户上下文维护','数据治理专家','context.data','sync_contexts',{}),
 ('batch','活动与批量生成','活动运营专家','context.batch','configuration',{}),
 ('service','服务计划与跟进','服务运营专家','context.service','configuration',{}),
 ('recommend','关键用户推荐','用户推荐专家','context.recommend','recommend_users',{}),
 ('reports','报告与内容素材','报告内容专家','context.reports','configuration',{}),
 ('groups','群运营','群运营专家','context.groups','configuration',{}),
 ('analysis','经营分析与历史核查','经营分析专家','context.analysis','legacy_read',{'operation':'v3_operation_evaluation_get'}),
]
projects=api('/projects');experts=api('/experts');plugins=api('/plugins');installed=[];grants=[];preserved=[];previous_plugin=read(private/'plugin-config.json',{})
for key,title,expert_name,component_key,default_action,parameters in families:
 name='上下文中心 · '+title
 saved_project=bindings.get(key,{}).get('projectId')
 p=next((p for p in projects if p['id']==saved_project),None) if saved_project else next((p for p in projects if 'context-migration-'+key in (p.get('aliases') or [])),None)
 if saved_project and p is None:raise RuntimeError('bound migration project no longer exists: '+key)
 if p and ('context-migration-'+key not in (p.get('aliases') or []) or p.get('archived')):raise RuntimeError('bound migration project is archived or ownership alias changed: '+key)
 p=p or api('/projects',{'name':name,'description':'原中心配置迁移；数据与生成任务由插件承接，复杂业务经兼容动作复用原服务。来源与依赖见项目内迁移清单。','aliases':['context-migration-'+key]})
 expert_name='上下文中心 · '+expert_name
 if bindings.get(key,{}).get('expertId') and not any(e['id']==bindings[key]['expertId'] for e in experts):raise RuntimeError('bound expert no longer exists: '+key)
 ex=next((e for e in experts if e['id']==bindings.get(key,{}).get('expertId')),None) or next((e for e in experts if e.get('skillName')=='context-'+component_key.replace('.','-')),None)
 if not ex or args.overwrite_existing:ex=upload_expert(expert_name,'沿原已确认规则组织'+title,base_instructions+'\n当前职责：'+title+'。\n',component_key,ex)
 if not saved_project or args.overwrite_existing:p=api('/projects/'+p['id'],{**p,'expertIds':list(dict.fromkeys([*(p.get('expertIds') or []),ex['id']])),'defaultExpertId':ex['id']},'PUT')
 token=bindings.get(key,{}).get('token') or secrets.token_urlsafe(40);binding_id='context-'+key
 actions=['query_contexts','list_user_refs','legacy_read','legacy_prepare','legacy_action','configuration','read_configuration','batch_results','status','lookup','cancel','retry']
 if key=='data':actions+=['sync_contexts']
 if key=='batch':actions+=['batch_generate']
 if key=='recommend':actions+=['recommend_users']
 labels={'sync_contexts':'同步并清洗上下文','query_contexts':'查询已发布上下文：user_ref精确引用，可选limit≤20和offset','list_user_refs':'读取身份清单：limit≤2000、offset；不是运营资格','batch_generate':'批量生成≤2000条：items为引用或{item_key,input}；原方法用operation+parameters，通用生成用instruction；不发送','legacy_read':'原中心纯读：operation操作键+parameters参数对象；报告需safe_user_ref','legacy_prepare':'沿原方案准备内容：operation+parameters，身份/场景/内容版本仍经原规则校验','legacy_action':'原系统操作：operation+parameters；先展示待确认卡，须本人确认；结果不明不重发','configuration':'查看迁移状态','read_configuration':'读取原配置：name为迁移清单路径，可用offset分页','recommend_users':'筛选并推荐关键用户','batch_results':'分页查看批次结果：task_ref取后台任务jobRef，可选limit≤50、offset','status':'查询任务状态','lookup':'查回受理结果','cancel':'请求停止','retry':'继续失败或已停止任务：task_ref取jobRef；仅补未成功项，不适用原系统动作'}
 spec={'key':component_key,'version':'1.0.0','actions':[{'name':a,'description':labels[a],'requiresOwner':a=='legacy_action','readOnly':a in ['query_contexts','list_user_refs','legacy_read','configuration','read_configuration','batch_results','recommend_users']} for a in actions if a not in ['status','lookup','cancel']], 'statusAction':'status','lookupAction':'lookup','cancelAction':'cancel'}
 if bindings.get(key,{}).get('pluginId') and not any(x['id']==bindings[key]['pluginId'] for x in plugins):raise RuntimeError('bound component no longer exists: '+key)
 bound_plugin=bindings.get(key,{}).get('pluginId')
 old=next((x for x in plugins if x['id']==bound_plugin),{}) if bound_plugin else next((x for x in plugins if x.get('component',{}).get('key')==component_key and x.get('projectId')==p['id']),{})
 if old and (old.get('projectId')!=p['id'] or not old.get('component')):raise RuntimeError('bound component ownership or type changed: '+key)
 if old and not args.overwrite_existing:
  if not bindings.get(key,{}).get('token'):raise RuntimeError('existing component needs original protected bindings: '+key)
  plugin=old;preserved.append({'family':key,'kept':'component actions, endpoint and enabled state'})
 else:plugin=api('/plugins',{'id':old.get('id',''),'name':title+'工具','description':'独立插件；原复杂业务保留明确兼容依赖','projectId':p['id'],'transport':'http','url':args.plugin_url,'headers':{'Authorization':'Bearer '+token},'enabled':True,'component':spec})
 sid=bindings.get(key,{}).get('sessionId') or p['defaultSessionId']
 if not saved_project or args.overwrite_existing:api('/sessions/'+sid,{'expertId':ex['id']},'PUT')
 old_config={}
 try:old_config=api('/sessions/'+sid+'/component-config')
 except RuntimeError as e:
  if 'HTTP 404' not in str(e):raise
 run_config={'sessionId':sid,'revision':old_config.get('revision',0),'pluginId':plugin['id'],'pluginVersion':'1.0.0','action':default_action,'arguments':parameters,'expertId':ex['id'],'expertVersion':ex['digest'],'ruleVersion':'canonical.clean.v1' if key=='data' else '', 'mode':'expert' if key=='recommend' else 'program','goal':'根据原候选规则与证据推荐当前关键用户，说明理由、来源和下一步；不扩大名单，不发送消息。' if key=='recommend' else ''}
 if not old_config or args.overwrite_existing:
  run_config=api('/sessions/'+sid+'/component-config',run_config,'PUT')
 else:
  if {k:v for k,v in old_config.items() if k!='revision'}!={k:v for k,v in run_config.items() if k!='revision'}:preserved.append({'family':key,'kept':'existing Session parameters, goal and pinned versions differ from import defaults'})
  run_config=old_config
 schedule_id=bindings.get(key,{}).get('scheduleId')
 if key in ['data','recommend']:
  existing_schedule=next((s for s in api('/schedules?projectId='+p['id']) if s['id']==schedule_id),None) if schedule_id else None
  if schedule_id and not existing_schedule:raise RuntimeError('bound migration schedule no longer exists: '+key)
  if not existing_schedule:
   existing_schedule=api('/schedules',{'projectId':p['id'],'sessionId':sid,'name':'迁移后：'+('只读拉取与清洗' if key=='data' else '关键用户推荐'),'expertId':ex['id'],'instruction':'按固定配置执行，结果返回原会话','component':run_config,'cron':'20 */6 * * *' if key=='data' else '10 9 * * *','timezone':'Asia/Shanghai','enabled':args.enable_new_schedules})
  schedule_id=existing_schedule['id']
 save_file(p['id'],'原中心配置/迁移清单.json',json.dumps(report,ensure_ascii=False,indent=2))
 # Original prompt/config facts live in project files, never global expert packages.
 if key=='data':
  for entry in entries:
   if entry['kind'].startswith('private'):continue
   path=source/entry['path'];body=path.read_text()
   if len(body.encode())>1500000:continue
   target='原中心配置/'+entry['path']
   save_file(p['id'],target,body)
 prior_grant=next((g for g in previous_plugin.get('grants',[]) if g.get('id')==binding_id),None)
 grants.append(prior_grant if prior_grant and not args.overwrite_existing else {'id':binding_id,'token':token,'projectId':p['id'],'actions':actions,'operations':list(operations)})
 bindings[key]={'token':token,'projectId':p['id'],'sessionId':sid,'expertId':ex['id'],'pluginId':plugin['id'],'componentKey':component_key,'scheduleId':schedule_id}
 installed.append({'capability':title,'projectId':p['id'],'sessionId':sid,'expertId':ex['id'],'pluginId':plugin['id'],'defaultAction':default_action,'cronOwnership':'source-owned; only separate new sync/recommendation schedules','newScheduleId':schedule_id})
 secret_write(private/'bindings.json',bindings)
# The browser gateway validates both htpasswd and the application role. The
# captured personal-admin account was verified against both existing gates.
if env.get('DARU_PERSONAL_ADMIN_USER') and env.get('DARU_PERSONAL_ADMIN_PASSWORD'):
 source_user=env['DARU_PERSONAL_ADMIN_USER'];source_password=env['DARU_PERSONAL_ADMIN_PASSWORD']
elif env.get('DARU_ADMIN_PASSWORD'):
 source_user=env.get('DARU_ADMIN_USER') or 'owner';source_password=env['DARU_ADMIN_PASSWORD']
elif env.get('DARU_AUTH_PASSWORD'):
 source_user=env.get('DARU_AUTH_USER') or 'daru';source_password=env['DARU_AUTH_PASSWORD']
else:raise RuntimeError('source authentication not available; cannot configure compatibility')

endpoint=(env.get('ARK_API_BASE_URL') or 'https://ark.cn-beijing.volces.com/api/v3').rstrip('/')
if not endpoint.endswith('/chat/completions'):endpoint+='/chat/completions'
config={'generationConcurrency':2,'listen':'127.0.0.1:3123','mysqlDSN':db['user']+':'+db['password']+'@tcp(127.0.0.1:3306)/'+db['database']+'?charset=utf8mb4&parseTime=true','sourceBase':'https://agent.lhbl.com.cn','sourceAuth':'browser_session','sourceUser':source_user,'sourcePassword':source_password,'sourcePath':'/data/canonical_users_live.json','requiredFields':['safe_user_ref','id','snapshot_id','archive_version','order_source_available','order_history','_orderSummary','operational_excluded'],'trimFields':['CRM展示名','微信昵称','企微昵称','黄小璨昵称'],'idField':'safe_user_ref','model':{'endpoint':endpoint,'key':env.get('ARK_API_KEY',''),'name':env.get('ARK_MODEL','')},'grants':grants,'operations':operations,'assetsDir':args.asset_root}
secret_write(private/'plugin-config.json',config);secret_write(private/'installed-map.json',installed);secret_write(private/'preserved-customizations.json',preserved)
print(json.dumps({'applied':True,'projects':len(installed),'configurations':len(entries),'operations':len(operations),'mapping':str(private/'installed-map.json'),'originalAutomaticJobsEnabled':False},ensure_ascii=False))
