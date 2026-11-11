"""One fixed original read/clean adapter behind data_refresh, not a workflow DSL."""
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
from source_common import write_json

config_path=Path(sys.argv[1]);run_id=sys.argv[2]
independent = sys.argv[3:] == ['independent']
if not config_path.is_absolute() or config_path.stat().st_mode&0o077 or not re.fullmatch(r'[a-zA-Z0-9_-]{1,96}',run_id):raise ValueError('private_source_configuration_required')
config=json.loads(config_path.read_text());base=Path(config['outputRoot']);code=Path(config['codeRoot'])
if not base.is_absolute()or not code.is_absolute():raise ValueError('absolute_source_roots_required')
root=base/run_id;root.mkdir(parents=True,exist_ok=True,mode=0o700)
if (root/'consumed.json').exists():raise ValueError('source_already_published_use_original_mysql_snapshots')
lock=(root/'.collection.lock').open('a');os.chmod(lock.name,0o600);fcntl.flock(lock,fcntl.LOCK_EX)
result=root/'native-result.json'
if result.exists():
    value=json.loads(result.read_text())
    for name,entry in value['files'].items():
        with (root/(name+'.jsonl')).open('rb')as stream:
            if hashlib.file_digest(stream,'sha256').hexdigest()!=entry['sha256']:raise ValueError('completed_source_artifact_changed')
    print(json.dumps(value));raise SystemExit(0)
original=root/'original'
if not original.exists():
    staging=root/'original.partial'
    if staging.exists():shutil.rmtree(staging)
    staging.mkdir(mode=0o700)
    # Only code is bootstrapped. Never seed a finished canonical archive.
    for name in ['ops','src','docs/product-operation-packs']:
        if (code/name).exists():shutil.copytree(code/name,staging/name,symlinks=False)
    shutil.copyfile(code/'package.json',staging/'package.json')
    staging.replace(original)
raw=root/'raw';raw.mkdir(mode=0o700,exist_ok=True)
python=config['python'];here=Path(__file__).resolve().parent
env={**os.environ,'PYTHONDONTWRITEBYTECODE':'1',
     'WORKBENCH_OPERATIONS_INDEPENDENT':'1' if independent else '0'}

def step(name,args=None):
    with (raw/(name+'.log')).open('wb')as log:
        os.chmod(log.name,0o600)
        command=[python,str(here/('source_'+name+'.py')),str(root),str(config_path)] if args is None else args
        process=subprocess.run(command,env=env,cwd=root,stdout=log,stderr=subprocess.STDOUT)
    if process.returncode:
        shutil.copyfile(raw/(name+'.log'),raw/(name+'.failed.log'))
        os.chmod(raw/(name+'.failed.log'),0o600)
        raise RuntimeError('original_source_step_failed_'+name)

def digest(path):
    with path.open('rb')as stream:return hashlib.file_digest(stream,'sha256').hexdigest()

def phase(name,paths,args=None):
    checkpoint=raw/(name+'-complete.json')
    if checkpoint.exists():
        receipt=json.loads(checkpoint.read_text())
        if any(not path.is_file()or receipt.get(str(path.relative_to(root)))!=digest(path)for path in paths):
            raise RuntimeError('completed_cleaning_artifact_changed_'+name)
        return
    step(name,args)
    write_json(checkpoint,{str(path.relative_to(root)):digest(path)for path in paths})

def read(name):
    from source_ssh import connection
    import shlex
    ssh,ssh_env,target=connection(config['contextSSH'])
    target_path=raw/('original-'+name+'.json')
    if target_path.exists():return
    partial=target_path.with_suffix('.partial')
    with partial.open('wb')as output:
        os.chmod(partial,0o600)
        source=(here/('source_read_'+name+'.py')).read_text()
        process=subprocess.run(ssh+[target,'python3 -B -c '+shlex.quote(source)],stdout=output,stderr=subprocess.PIPE,env=ssh_env,timeout=240)
    if process.returncode:raise RuntimeError('original_source_read_failed_'+name)
    # Parse complete JSON before the atomic checkpoint; partial responses never publish.
    json.loads(partial.read_text());partial.replace(target_path)

if independent:
    frozen = raw / 'workbench-inputs.json'
    if not frozen.exists():
        path = Path(config['independentInputs'])
        if not path.is_absolute() or path.stat().st_mode & 0o077:
            raise ValueError('private_workbench_inputs_required')
        write_json(frozen, json.loads(path.read_text()))
    local = json.loads(frozen.read_text())
    # Policies and methods are owned by this workbench. Past execution facts
    # may be included as historical inputs, alongside live CRM activity.
    history = dict(local['history'])
    history.update(complete=True, read_only=True)
    history.setdefault('source_as_of', '')
    for key in ('refs', 'actions', 'fact_reviews', 'copy_index', 'renewal_slots', 'previous_cycle_ends'):
        history.setdefault(key, {})
    history.setdefault('legacy_successful_cycles', [])
    history['asset_reads'] = {key: 'present' if key in history['assets'] else 'absent_in_original' for key in (
        'var/service-packs/weekly-review-pack-latest.json', 'var/service-packs/content-service-pack-latest.json')}
    for name, value in {'history': history, 'profiles': {}, 'generation': {},
                        'methods': local['methods'], 'writing': local['writing'],
                        'handoffs': {'read_only': True, 'complete': True, 'handoffs': []}}.items():
        write_json(raw / ('original-' + name + '.json'), value)

if not (raw/'hxc-read-receipt.json').exists():step('hxc')
if not (raw/'crm-read-receipt.json').exists():step('crm')
if not (raw/'unified/read-receipt.json').exists():step('unified')
for name,filename in [('hxc','hxc-latest.json'),('crm','crm-production-source.json')]:
    receipt=json.loads((raw/(name+'-read-receipt.json')).read_text())
    if receipt.get('sha256')!=digest(raw/filename):raise RuntimeError('completed_raw_source_changed_'+name)
unified=json.loads((raw/'unified/read-receipt.json').read_text())
if unified.get('complete')is not True:raise RuntimeError('unified_source_incomplete')
for name,receipt in unified['resources'].items():
    if receipt.get('sha256')!=digest(raw/'unified'/(name+'.jsonl')):raise RuntimeError('completed_unified_source_changed')
if not independent:
    for name in ['history','profiles','generation','methods','handoffs','writing']:
        read(name)
inputs=json.loads((raw/'original-history.json').read_text())
if inputs.get('complete')is not True or inputs.get('read_only')is not True:raise RuntimeError('original_history_incomplete')
for relative,value in inputs['assets'].items():
    path=original/relative
    if not path.resolve().is_relative_to(original.resolve()):raise RuntimeError('source_asset_path_invalid')
    write_json(path,value)
if independent:
    # The existing builder consumes a source binding. Bind it to this run's
    # completed upstream reads instead of carrying the old server's gate.
    resources = unified['resources']
    write_json(original / 'var/hxc-source-gate.json', {
        'state': 'ready', 'contract_version': 'relationship.v1',
        'required_resources': sorted(resources),
        'safety_fingerprint': hashlib.sha256(json.dumps(resources, sort_keys=True).encode()).hexdigest(),
    })
private=original/'var/private-data';private.mkdir(parents=True,exist_ok=True,mode=0o700)
warehouse=private/'warehouse-canonical.json'
phase('warehouse',[warehouse],[python,str(original/'ops/export-warehouse-workbench.py'),'--source-facts',str(raw/'crm-production-source.json'),'--out',str(warehouse)])
phase('project',[private/'questionnaire-assessment.json',private/'conversation-profile-candidates.json',raw/'projected-account-links.json'])
phase('build',[private/'canonical_users_live.json',original/'public/data/canonical_users_live.json',original/'public/data/workbench_live.json',private/'user-source-detail-ledger.json',private/'user-source-detail-records.ndjson',private/'strategy-audience-users.json'])
evidence=original/'public/data/activation_evidence_live.json'
phase('evidence',[evidence])
step('prepare')
value=json.loads(result.read_text());print(json.dumps(value))
