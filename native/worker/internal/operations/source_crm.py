"""Read all CRM arrays once using the installed workbench CRM CLI adapter."""
import sys,json,subprocess,os,hashlib
from pathlib import Path
from source_common import load, write_json
root,settings=load();raw=root/'raw';sys.path.insert(0,str(root/'original/ops'))
from crm_read_facts_contract import loads,validate,canonical
ids=json.loads((raw/'hxc-account-ids.json').read_text())
request=raw/'crm-request.json';write_json(request,{'hxc_account_ids':ids})
part=raw/'crm-response.partial'
if part.exists():part.unlink()
worker=Path(settings['worker'])
if not worker.is_absolute():raise ValueError('installed_worker_required')
r=subprocess.run([str(worker),'operations-export-crm',str(request),str(part)],stderr=subprocess.PIPE,timeout=180)
if r.returncode:
    (raw/'crm-error-private.log').write_bytes(r.stderr);raise RuntimeError('crm_cli_export_failed')
facts=loads(part.read_text())
while isinstance(facts,dict)and 'contract'not in facts and isinstance(facts.get('data'),dict):facts=facts['data']
receipt=validate(facts,ids)
final=raw/'crm-production-source.json'
with final.open('w')as out:os.chmod(final,0o600);out.write(canonical(facts))
with final.open('rb')as stream:receipt['sha256']=hashlib.file_digest(stream,'sha256').hexdigest()
part.unlink();receipt['transport']='crm_cli';write_json(raw/'crm-read-receipt.json',receipt)
