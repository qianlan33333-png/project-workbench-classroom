import importlib.util,json,os,sys,subprocess,socket,time,shlex,signal
from pathlib import Path
from source_common import load, write_json
from source_ssh import connection
collection,settings=load();root=collection/'original';config=settings['hxc'];out=collection/'raw';out.mkdir(parents=True,exist_ok=True,mode=0o700)
sys.path.insert(0,str(root/'ops'));sys.path.insert(0,str(Path(__file__).resolve().parent))
import raw_transport as transport
s=importlib.util.spec_from_file_location('original_hxc',root/'ops/hxc-sync.py');m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
m.REMOTE_SCRIPT=Path(transport.__file__).read_text()+m.REMOTE_SCRIPT[len(m.STREAM_HELPER):]
m.read_wire=transport.read_wire;m.write_json=transport.write_json
sock=socket.socket();sock.bind(('127.0.0.1',0));port=sock.getsockname()[1];sock.close()
db=config['db'];ssh=config['ssh']
cmd,ssh_env,target=connection(ssh)
cmd+=['-o','ExitOnForwardFailure=yes','-N','-L',f'127.0.0.1:{port}:{db["DB_HOST"]}:{db.get("DB_PORT","3306")}',target]
p=subprocess.Popen(cmd,stderr=subprocess.PIPE,env=ssh_env)
def stopped(*args):
 p.terminate();raise SystemExit(1)
signal.signal(signal.SIGTERM,stopped)
try:
 for i in range(100):
  if p.poll()is not None:raise RuntimeError('source_tunnel_failed')
  try:
   with socket.create_connection(('127.0.0.1',port),timeout=.1):break
  except OSError:time.sleep(.1)
 else:raise RuntimeError('source_tunnel_unavailable')
 env={**db,'HXC_DB_HOST':'127.0.0.1','HXC_DB_PORT':str(port),'HXC_DB_USER':db['DB_USER'],'HXC_DB_PASSWORD':db['DB_PASSWORD'],'HXC_DB_NAME':db['DB_NAME']}
 from argparse import Namespace
 payload=m.run_direct(env,Namespace(limit=0,message_limit=0,days=0,full=True,page_size=5000,message_days=0))
 if payload.get('ok')is not True:raise RuntimeError('source_incomplete')
 m.atomic_private_json(out/'hxc-latest.json',payload)
 ids=[str(u['id'])for u in payload['users']if not u.get('is_deleted')]
 write_json(out/'hxc-account-ids.json',ids)
 report={k:payload.get(k)for k in ['synced_at','coverage','mode']};report['counts']={k:len(v)for k,v in payload.items()if isinstance(v,(list,transport.DiskRows))}
 import hashlib
 report['sha256']=hashlib.file_digest((out/'hxc-latest.json').open('rb'),'sha256').hexdigest()
 write_json(out/'hxc-read-receipt.json',report);print(json.dumps({'source':'hxc','counts':report['counts']}))
finally:
 p.terminate()
 try:p.wait(timeout=5)
 except subprocess.TimeoutExpired:p.kill();p.wait()
