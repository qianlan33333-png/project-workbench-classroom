"""Read current original business facts without taking ownership of its worker."""
import json,sys,shlex,subprocess
from pathlib import Path
from source_ssh import connection
path=Path(sys.argv[1])
if not path.is_absolute()or path.stat().st_mode&0o077:raise ValueError('private_source_configuration_required')
settings=json.loads(path.read_text());ssh,env,target=connection(settings['contextSSH'])
script=Path(__file__).with_name('source_common.py').read_text()+'\n'+Path(__file__).with_name('source_read_gate.py').read_text()
items=json.loads(Path(sys.argv[3]).read_text())
r=subprocess.run(ssh+[target,'python3 -B -c '+shlex.quote(script)],env=env,input=json.dumps(items),capture_output=True,text=True,timeout=240,check=True)
v=json.loads(r.stdout)
if type(v.get('ready'))is not bool:raise ValueError('original_business_facts_unverified')
if v['ready']:
    rights=subprocess.run([settings['python'],str(Path(__file__).with_name('source_rights.py')),str(path)],input=json.dumps(items),capture_output=True,text=True,timeout=40,check=True)
    v['ready']=json.loads(rights.stdout).get('ready')is True
print(json.dumps(v))
