import sys,json,subprocess,shlex,os,hashlib
from pathlib import Path
from datetime import datetime,timezone
from source_common import load, write_json, read_proxy, direct_hxc_reader
from source_ssh import connection
root,settings=load();out=root/'raw/unified';out.mkdir(mode=0o700,exist_ok=True)
sys.path.insert(0,str(root/'original/ops'))
import hxc_unified_sync_store as original
from hxc_sync_bounds import Budget
proxy='''import json,pathlib,sys,urllib.request,urllib.parse,urllib.error
v={}
for line in pathlib.Path('/etc/daru-ops-console.env').read_text().splitlines():
 if '='in line and not line.lstrip().startswith('#'):
  k,x=line.split('=',1);v[k.strip()]=x.strip().strip(chr(34)).strip(chr(39))
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*args,**kw):return None
opener=urllib.request.build_opener(NoRedirect)
for line in sys.stdin:
 try:
  params=json.loads(line)
  if params.get('resource')not in ['sync-status','account-links','catalog','content-activity','weekly-reviews','context','preauthorized-invitations']:raise ValueError('invalid_resource')
  q=urllib.request.Request('https://ip.lhbl.com.cn/api/ops/private-domain/v1/sync?'+urllib.parse.urlencode(params),headers={'Authorization':'Bearer '+v['HXC_PRIVATE_DOMAIN_API_TOKEN']})
  with opener.open(q,timeout=60)as r:
   raw=r.read(8*1024*1024+1)
   if len(raw)>8*1024*1024:raise ValueError('response_too_large')
   value=json.loads(raw)
  print(json.dumps({'data':value},ensure_ascii=False),flush=True)
 except urllib.error.HTTPError as e:print(json.dumps({'http_error':e.code}),flush=True)
 except Exception as e:print(json.dumps({'error':'response_too_large' if str(e)=='response_too_large' else type(e).__name__}),flush=True)
'''
budget=Budget(seconds=3*3600)
p = None

if os.environ.get('WORKBENCH_OPERATIONS_INDEPENDENT') == '1':
 token_file = Path(settings.get('hxcUnifiedOAuthFile') or settings['hxcUnifiedTokenFile'])
 if not token_file.is_absolute() or token_file.stat().st_mode & 0o077:
  raise ValueError('private_hxc_token_file_required')
 value = token_file.read_text().strip()
 if not value:
  raise ValueError('hxc_unified_token_missing')
 oauth = json.loads(value) if settings.get('hxcUnifiedOAuthFile') else None
 getter = direct_hxc_reader(value if oauth is None else '', budget, original.ResponseTooLarge, oauth=oauth)
else:
 ssh,ssh_env,target=connection(settings['contextSSH'])
 p=subprocess.Popen(ssh+[target,'python3 -B -c '+shlex.quote(proxy)],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,env=ssh_env)

 def getter(params):
  return read_proxy(p,params,budget,original.ResponseTooLarge)

try:
 status=getter({'resource':'sync-status'});heads={x['resource']:x for x in status.get('resources',[])}
 if status.get('ok')is not True:raise RuntimeError('sync_status_invalid')
 first={}
 pinned=out/'pinned-pages.json'
 if pinned.exists():first=json.loads(pinned.read_text())
 for resource in original.RESOURCES:
  head=heads.get(resource)or{}
  if head.get('status')!='published' or head.get('contract_version')!=original.resource_contract(resource):raise RuntimeError('source_not_published_'+resource)
  if resource in first:continue
  page,params=original._fetch_page(getter,original.resource_params(resource),budget);header=original._page_header(page,resource)
  if str(header['watermark'])!=str(head['watermark'])or header['data_as_of']!=head.get('data_as_of')or header['coverage']!=head.get('coverage'):raise RuntimeError('source_changed_'+resource)
  at=datetime.fromisoformat(header['data_as_of'].replace('Z','+00:00'));age=(datetime.now(timezone.utc)-at).total_seconds()
  if not -300<=age<=10800:raise RuntimeError('source_stale_'+resource)
  first[resource]=(page,params)
 write_json(pinned,first)
 reports={}
 for resource in original.RESOURCES:
  page,params=first[resource]
  saved=out/(resource+'-receipt.json')
  if saved.exists():
   reports[resource]=json.loads(saved.read_text())
   if hashlib.file_digest((out/(resource+'.jsonl')).open('rb'),'sha256').hexdigest()!=reports[resource]['sha256']:raise RuntimeError('frozen_source_changed')
   continue
  path=out/(resource+'.partial')
  with path.open('w')as stream:
   os.chmod(path,0o600)
   for batch,report in original._batches(getter,resource,params,page,budget):
    for row in batch:stream.write(json.dumps(row,ensure_ascii=False)+'\n')
   stream.flush();os.fsync(stream.fileno())
  path.rename(out/(resource+'.jsonl'));reports[resource]=report
  report['sha256']=hashlib.file_digest((out/(resource+'.jsonl')).open('rb'),'sha256').hexdigest()
  write_json(saved,report)
  print(json.dumps({'resource':resource,'pages':report['pages'],'changes':report['changes'],'watermark':report['watermark']}),flush=True)
 write_json(out/'read-receipt.json',{'complete':True,'resources':reports});print('original unified source read complete',flush=True)
finally:
 if p is not None:
  p.stdin.close();p.terminate()
  try:p.wait(timeout=5)
  except subprocess.TimeoutExpired:p.kill();p.wait()
