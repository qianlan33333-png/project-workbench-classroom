# Executed from stdin on the original host; no files, CLI cache or DB writes.
import json,sys,urllib.request,urllib.parse,pathlib,urllib.error
ids=json.load(sys.stdin)
import re
if not ids or len(ids)!=len(set(ids)) or any(not re.fullmatch(r'[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}',x)for x in ids):raise RuntimeError('invalid_account_scope')
a=json.loads(pathlib.Path('/etc/daru/archive-readonly.json').read_text())
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*args,**kw):return None
opener=urllib.request.build_opener(NoRedirect)
form=urllib.parse.urlencode(dict(grant_type='client_credentials',client_id=a['client_id'],client_secret=a['client_secret'],audience=a['audience'],scope='read')).encode()
with opener.open(urllib.request.Request(a['origin']+'/oauth/token',data=form,headers={'Content-Type':'application/x-www-form-urlencoded'}),timeout=30)as response:token=json.load(response)['access_token']
request=urllib.request.Request(a['origin']+'/open/v1/archive-facts',data=json.dumps({'hxc_account_ids':ids}).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
with opener.open(request,timeout=90)as response:
 while True:
  data=response.read(1024*1024)
  if not data:break
  sys.stdout.buffer.write(data)
 sys.stdout.buffer.flush()
