#!/usr/bin/env python3
"""Native format support for the existing workbench release lock/controller.
No CLI and no independent lock: release-common.py owns the transaction.
"""
import hashlib, json, os, pwd, re, shutil, sqlite3, subprocess, tarfile, time, urllib.error, urllib.request
from pathlib import Path

UNITS=('project-workbench-channels','project-workbench','project-workbench-worker','project-workbench-temporal')

def write_state(c,state):
    # Only release paths, booleans and phase data; no credentials. Service users
    # must read the gate across restarts while only root may write it.
    path=c.BASE/'.deploying'
    c.write(path,json.dumps(state),mode=0o644)
    path.chmod(0o644)  # c.write deliberately preserves an existing file's mode.

def is_native(path):
    return path is not None and (path/'native-release.json').is_file()

def verify(path):
    manifest=json.loads((path/'native-release.json').read_text())
    for executable in (path/'bin/workbench-worker',path/'node/bin/node',path/'temporal/temporal-server'):
        with executable.open('rb') as stream:header=stream.read(20)
        if len(header)!=20 or header[:6]!=b'\x7fELF\x02\x01' or int.from_bytes(header[18:20],'little')!=62:raise RuntimeError('Native executable architecture mismatch')
    if manifest.get('format')!='personal-workbench-native-v1':raise RuntimeError('Unsupported native format')
    if (manifest.get('harness'),manifest.get('node'),manifest.get('temporal'))!=('0.2.2-rc.1','24.22.0','1.32.2'):
        raise RuntimeError('Native dependency pins changed without review')
    for name,digest in manifest['files'].items():
        file=path/name
        if Path(name).is_absolute() or '..' in Path(name).parts or not file.is_file() or file.is_symlink():
            raise RuntimeError('Invalid native manifest member')
        with file.open('rb') as source:actual=hashlib.file_digest(source,'sha256').hexdigest()
        if actual!=digest:raise RuntimeError('Native artifact checksum mismatch: '+name)
    for required in ('bin/workbench-worker','node/bin/node','temporal/temporal-server','runtime/harness/index.js','deploy/start.py'):
        if required not in manifest['files']:raise RuntimeError('Incomplete native artifact')
    for file in path.rglob('*'):
        if file.is_symlink() and not file.resolve().is_relative_to(path.resolve()):
            raise RuntimeError('Native dependency symlink escapes release')

def configure_units(c,path):
    folder=path/'deploy/units' if is_native(path) else path/'deploy'
    names=UNITS if is_native(path) else UNITS[:2]
    for name in names:c.run('install','-m','644',folder/(name+'.service'),Path('/etc/systemd/system')/(name+'.service'))
    c.run('systemctl','daemon-reload')

def health(c):
    # The official Host can serve its UI even if a plugin failed; all three
    # ready signals are required, not merely an HTTP 200 from the web process.
    cfg=json.loads(Path('/etc/project-workbench/native.json').read_text())
    token=cfg['common']['WORKBENCH_HOST_TOKEN']
    for attempt in range(120):
        try:
            with urllib.request.urlopen('http://127.0.0.1:7381/health',timeout=2) as res:assert json.load(res)['ready']
            request=urllib.request.Request('http://127.0.0.1:7382/health',headers={'Authorization':'Bearer '+token})
            with urllib.request.urlopen(request,timeout=2) as res:assert json.load(res)['ready']
            try:
                urllib.request.urlopen('http://127.0.0.1:3082/',timeout=2).close()
            except urllib.error.HTTPError as error:
                # Native Harness protects its index even on loopback. A bare
                # probe must receive 401; the mandatory acceptance hook checks
                # the authenticated page and catalog before committing.
                if error.code!=401:raise
                error.close()
            if c.run('systemctl','is-active','--quiet','project-workbench-worker','project-workbench-temporal',check=False).returncode:raise OSError('service inactive')
            return
        except (OSError,ValueError,KeyError,AssertionError):time.sleep(1)
    raise RuntimeError('Native Host/Worker/Temporal did not become ready')

def stop(c):
    c.run('systemctl','stop',*UNITS,check=False)
    if c.run('systemctl','is-active','--quiet',*UNITS,check=False).returncode==0:raise RuntimeError('A workbench service did not stop')

def start(c,path,channels=False):
    configure_units(c,path)
    c.point('current',path)
    if not is_native(path):
        c.start(path,channels=channels);return
    c.run('systemctl','start','project-workbench-temporal')
    c.run('systemctl','start','project-workbench-worker')
    for _ in range(60):
        try:urllib.request.urlopen('http://127.0.0.1:7381/health',timeout=2).close();break
        except OSError:time.sleep(1)
    else:raise RuntimeError('Native Worker not ready')
    c.run('systemctl','start','project-workbench')
    health(c)
    if channels:c.run('systemctl','start','project-workbench-channels')

def cleanup(c):
    current,previous=c.pointer('current'),c.pointer('previous')
    if not is_native(current) or (previous and not is_native(previous)):raise RuntimeError('Native cleanup requires native-only retained releases')
    keep={x for x in (current,previous) if x}
    removing=[path for path in (c.BASE/'releases').iterdir() if path not in keep]
    check_retirement_dependencies(json.loads(Path('/etc/project-workbench/native.json').read_text()),[*removing,c.BASE/'incoming'])
    for path in removing:c.remove(path,keep)
    # Container images and old data are retired separately after the local backup
    # and the new generation's production acceptance. Never global-prune here.
    c.remove(c.BASE/'incoming',keep)

def restore_channels(c,state):
    c.run('systemctl','enable','project-workbench')
    if not state.get('channels'):return
    c.run('systemctl','enable','project-workbench-channels')
    c.run('systemctl','start','project-workbench-channels')
    cfg=json.loads(Path('/etc/project-workbench/native.json').read_text())
    token=cfg.get('common',{}).get('WORKBENCH_CONNECTOR_TOKEN') or cfg['channels']['env']['WORKBENCH_CONNECTOR_TOKEN']
    for attempt in range(60):
        try:
            request=urllib.request.Request('http://127.0.0.1:7383/health',headers={'Authorization':'Bearer '+token})
            with urllib.request.urlopen(request,timeout=2) as res:
                if json.load(res).get('ready'):return
        except (OSError,ValueError):pass
        time.sleep(1)
    raise RuntimeError('Committed native connector is not ready; old consumer must remain stopped')

def recover(c,state):
    old=c.release(state['old_current'],exists=not state.get('committed')) if state.get('old_current') else None
    prev=c.release(state['old_previous'],exists=not state.get('committed')) if state.get('old_previous') else None
    candidate=c.release(state['candidate'],exists=False)
    if state.get('preparing'):
        if c.pointer('current')!=old or c.pointer('previous')!=prev or candidate in (old,prev):raise RuntimeError('Prepared candidate baseline changed')
        c.remove(candidate,{x for x in (old,prev) if x})
        (c.BASE/'.deploying').unlink(missing_ok=True);c.sync_dir(c.BASE)
        return
    if state.get('committed'):
        if c.pointer('current')!=candidate:raise RuntimeError('Unexpected committed native pointer')
        health(c)
        restore_channels(c,state)
        if old and not is_native(old):
            # Old code is kept only until explicit incompatible-generation retirement.
            return
        cleanup(c)
    else:
        stop(c)
        if old:
            if state.get('old_plugin_active'):c.run('systemctl','start','workbench-context-plugin')
            start(c,old,channels=state.get('channels',False))
        else:c.point('current',None)
        c.point('previous',prev)
        if candidate not in (old,prev):c.remove(candidate,{x for x in (old,prev) if x})
    (c.BASE/'.deploying').unlink(missing_ok=True);c.sync_dir(c.BASE)

def legacy_quiet(c):
    c.quiet()
    with sqlite3.connect(f'file:{c.DATA}/workbench.sqlite?mode=ro',uri=True) as db:
        checks=(
          ("component_calls","SELECT COUNT(*) FROM component_calls WHERE state NOT IN ('succeeded','failed','cancelled')"),
          ("workflow_runs","SELECT COUNT(*) FROM workflow_runs WHERE json_extract(data,'$.state') IN ('running','needs_check','waiting','unknown')"),
        )
        for table,query in checks:
            if db.execute("SELECT 1 FROM sqlite_master WHERE type='table' AND name=?",(table,)).fetchone() and db.execute(query).fetchone()[0]:
                raise RuntimeError('Resolve old asynchronous '+table+' before cutover; unknown effects cannot be abandoned')
    config=Path('/var/lib/workbench-context-plugin/config.json')
    if config.exists():
        dsn=json.loads(config.read_text())['mysqlDSN']
        match=re.fullmatch(r'([^:]+):(.+)@tcp\(([^:]+):(\d+)\)/([^?]+)(?:\?.*)?',dsn)
        if not match:raise RuntimeError('Unsupported old plugin DSN; inspect before cutover')
        user,password,host,port,database=match.groups()
        query="SELECT COUNT(*) FROM plugin_jobs WHERE state NOT IN ('succeeded','failed','cancelled'); SELECT COUNT(*) FROM plugin_items WHERE state IN ('import_unknown','unknown');"
        result=subprocess.run(['mysql','--batch','--skip-column-names','--protocol=TCP','-h',host,'-P',port,'-u',user,database],input=query,text=True,env=dict(os.environ,MYSQL_PWD=password),capture_output=True)
        if result.returncode or any(int(n) for n in result.stdout.split()):
            raise RuntimeError('Old context plugin has unresolved work or cannot be verified; no cutover')

def deploy(c,candidate):
    verify(candidate)
    old,previous=c.pointer('current'),c.pointer('previous')
    guard={}
    if candidate==old:raise RuntimeError('Candidate is already current')
    if os.environ.get('PW_EXPECTED_PREVIOUS') and str(old)!=os.environ['PW_EXPECTED_PREVIOUS']:raise RuntimeError('Current baseline changed')
    if old and not is_native(old):
        # Local backup/restore verification must precede this incompatible cutover.
        marker=Path('/etc/project-workbench/native-cutover.json')
        guard=json.loads(marker.read_text())
        if guard.get('oldCurrent')!=str(old) or not guard.get('localBackupVerified') or not guard.get('localBackupSHA256'):raise RuntimeError('Local backup boundary not verified')
        legacy_quiet(c)
    if shutil.disk_usage(c.BASE).free<c.RESERVE:raise RuntimeError('Less than 5 GiB free')
    channels=c.run('systemctl','is-active','--quiet','project-workbench-channels',check=False).returncode==0
    # A consistent incompatible-generation backup is taken with the old intake
    # stopped under this same lock. Preserve its previously active services so
    # success enables the new receiver and pre-commit recovery restores the old.
    if guard.get('legacyQuiesced'):channels=channels or guard.get('legacyChannelsActive',False)
    state={'kind':'native','old_current':str(old) if old else None,'old_previous':str(previous) if previous else None,'candidate':str(candidate),'channels':channels,'committed':False,'old_plugin_active':old is not None and not is_native(old) and c.run('systemctl','is-active','--quiet','workbench-context-plugin',check=False).returncode==0}
    if guard.get('legacyQuiesced'):state['old_plugin_active']=state['old_plugin_active'] or guard.get('legacyPluginActive',False)
    write_state(c,state)
    try:
        stop(c)
        if old and not is_native(old):
            c.run('systemctl','stop','workbench-context-plugin')
            legacy_quiet(c)
        start(c,candidate,channels=False)
        acceptance=os.environ.get('PW_ACCEPTANCE_SCRIPT')
        if not acceptance:raise RuntimeError('Native cutover requires the reviewed acceptance hook')
        c.run(str(Path(acceptance).resolve(strict=True)))
        # First native generation has no compatible previous. Old program and data
        # remain recoverable until the final local-only retirement step.
        c.point('previous',old if is_native(old) else None)
        # Commit before enabling an external consumer. After this write, failure
        # must not restore old cursors or hide newly accepted business events.
        state['committed']=True;write_state(c,state)
        restore_channels(c,state)
    except BaseException:
        saved=json.loads((c.BASE/'.deploying').read_text())
        recover(c,saved);raise
    if old and not is_native(old):
        print('Native entry accepted; incompatible old generation awaits verified retirement')
    else:recover(c,state)

def install(c,archive,name):
    archive=Path(archive).resolve(strict=True);candidate=c.release(name,exists=False)
    if candidate.exists():raise RuntimeError('Use a fresh native release name')
    expected=Path(str(archive)+'.sha256').read_text().split()[0]
    with archive.open('rb') as file:actual=hashlib.file_digest(file,'sha256').hexdigest()
    if actual!=expected:raise RuntimeError('Archive checksum mismatch')
    with tarfile.open(archive) as source:
        size=sum(x.size for x in source.getmembers())
        if shutil.disk_usage(c.BASE).free<size+c.RESERVE:raise RuntimeError('Not enough candidate space')
        write_state(c,{'kind':'native','preparing':True,'candidate':str(candidate),'old_current':str(c.pointer('current')) if c.pointer('current') else None,'old_previous':str(c.pointer('previous')) if c.pointer('previous') else None})
        candidate.mkdir()
        try:
            source.extractall(candidate,filter='data');verify(candidate)
            c.run('chown','-R',c.USER+':'+c.USER,candidate)
        except BaseException:
            c.remove(candidate,{x for x in (c.pointer('current'),c.pointer('previous')) if x});raise
    deploy(c,candidate)
    archive.unlink(missing_ok=True);Path(str(archive)+'.sha256').unlink(missing_ok=True)

def mysql_query(dsn,query):
    match=re.fullmatch(r'([^:]+):(.+)@tcp\(([^:]+):(\d+)\)/([^?]+)(?:\?.*)?',dsn)
    if not match:raise RuntimeError('Database connection cannot be verified before retirement')
    user,password,host,port,database=match.groups()
    result=subprocess.run(['mysql','--batch','--skip-column-names','--protocol=TCP','-h',host,'-P',port,'-u',user],input=query,text=True,env=dict(os.environ,MYSQL_PWD=password),capture_output=True,timeout=30)
    if result.returncode:raise RuntimeError('Database metadata query failed')
    return result.stdout.strip().splitlines()

def check_retirement_dependencies(config,roots,old_uuid=None):
    def references(value):
        if not isinstance(value,str) or not value.startswith('/'):return False
        path=Path(value).resolve()
        return any(path==root.resolve() or root.resolve() in path.parents for root in roots)
    def walk(v):
        if isinstance(v,str):
            if references(v):raise RuntimeError('Native configuration or workspace still points to retiring data')
        elif isinstance(v,dict):
            for x in v.values():walk(x)
        elif isinstance(v,list):
            for x in v:walk(x)
    walk(config)
    token=config['common']['WORKBENCH_HOST_TOKEN']
    def get(path):
        request=urllib.request.Request('http://127.0.0.1:7381'+path,headers={'Authorization':'Bearer '+token,'X-Workbench-Kind':'owner','X-Workbench-Session':'release-dependency-check'})
        with urllib.request.urlopen(request,timeout=10) as response:return json.load(response)
    connections=get('/connections')
    resources=get('/resources')
    environments={**config.get('common',{}),**config['worker'].get('env',{})}
    def root_query(query):
        # Local root must be proven to observe the SAME server, not an unrelated
        # socket whose empty metadata could incorrectly authorize deleting data.
        result=subprocess.run(['mysql','--batch','--skip-column-names','--protocol=SOCKET','--user=root'],input='SELECT @@server_uuid;'+query,text=True,capture_output=True,timeout=30)
        lines=result.stdout.strip().splitlines()
        if result.returncode or not lines or lines[0]!=old_uuid:raise RuntimeError('Cannot verify retiring-instance catalog with local administrator')
        return lines[1:]
    for connection in connections:
        if references(connection.get('location','')):raise RuntimeError('Registered resource connection still uses retiring files')
        if connection.get('kind')!='mysql' or old_uuid is None:continue
        dsn=environments.get(connection.get('secretEnv',''),'')
        identity=mysql_query(dsn,'SELECT @@server_uuid, DATABASE();')
        if len(identity)!=1:raise RuntimeError('Source database identity unavailable')
        # Compare real server identity even for public aliases or forwarded ports.
        if identity[0].split('\t')[0]!=old_uuid:continue
        database=re.fullmatch(r'([^:]+):(.+)@tcp\(([^:]+):(\d+)\)/([^?]+)(?:\?.*)?',dsn).group(5)
        if database=='workbench_context':raise RuntimeError('Registered resource still reads retiring MySQL data')
        pending=[(database,r.get('relation','')) for r in resources if r.get('connectionId')==connection['id']]
        seen=set()
        while pending:
            schema,table=pending.pop()
            if (schema,table) in seen:continue
            seen.add((schema,table))
            if len(seen)>256:raise RuntimeError('Source view dependency graph requires inspection')
            if schema=='workbench_context':raise RuntimeError('Source view depends on retiring MySQL data')
            def text(v):return "CONVERT(X'"+v.encode().hex()+"' USING utf8mb4)"
            found=root_query('SELECT TABLE_TYPE FROM information_schema.TABLES WHERE TABLE_SCHEMA='+text(schema)+' AND TABLE_NAME='+text(table)+';')
            if found==['BASE TABLE']:continue
            if found!=['VIEW']:raise RuntimeError('Configured source relation was not found in verified catalog')
            predicate='VIEW_SCHEMA='+text(schema)+' AND VIEW_NAME='+text(table)
            lines=root_query('SELECT TABLE_SCHEMA,TABLE_NAME FROM information_schema.VIEW_TABLE_USAGE WHERE '+predicate+'; SELECT COUNT(*) FROM information_schema.VIEW_ROUTINE_USAGE WHERE TABLE_SCHEMA='+text(schema)+' AND TABLE_NAME='+text(table)+';')
            if not lines or lines[-1]!='0':raise RuntimeError('Source view calls routines; resolve dependencies before retirement')
            pending.extend(tuple(line.split('\t',1)) for line in lines[:-1])
    registry=Path(config['host']['env']['DSH_HOME'])/'storages/workspace.json'
    if not registry.is_file():raise RuntimeError('Native workspace registry must be checked before retirement')
    walk(json.loads(registry.read_text()))

def check_container_dependencies(c,roots,definition_roots=None):
    # Host findmnt cannot see rootless containers' private mount namespaces.
    # Stopped containers also matter: a later start must not use deleted files.
    roots=[p.resolve() for p in roots]
    ids=c.as_user('podman','ps','--all','--quiet','--no-trunc',capture=True).stdout.split()
    if ids:
        containers=json.loads(c.as_user('podman','inspect',*ids,capture=True).stdout)
        for container in containers:
            for mount in container.get('Mounts',[]):
                source=Path(mount['Source']).resolve()
                if any(source==root or root in source.parents for root in roots):
                    raise RuntimeError('Container still depends on retiring files: '+container['Name'])
    if definition_roots is None:
        home=Path(pwd.getpwnam(c.USER).pw_dir)
        definition_roots=[Path('/etc/systemd/system'),Path('/etc/systemd/user'),Path('/etc/containers/systemd'),Path('/usr/share/containers/systemd'),home/'.config/systemd/user',home/'.config/containers/systemd']
    for directory in definition_roots:
        if not directory.exists():continue
        for file in directory.rglob('*'):
            if not file.is_file():continue
            # This exact stopped unit is removed by retire itself.
            if file.resolve()==Path('/etc/systemd/system/workbench-context-plugin.service'):continue
            text=file.read_text(errors='replace')
            if any(re.search(re.escape(str(root))+r'''(?=/|[\s:'"=]|$)''',text) for root in roots):
                raise RuntimeError('Startup definition still references retiring files: '+str(file))

def retire(c,state):
    """Finish the explicitly authorized incompatible generation, under the same lock.
    This uses only the existing temporary deployment record; no cleanup ledger.
    """
    if state.get('kind')!='native' or not state.get('committed'):
        raise RuntimeError('No committed incompatible native cutover')
    candidate=c.release(state['candidate']);old=c.release(state['old_current'],exists=False)
    if c.pointer('current')!=candidate or is_native(old) or c.pointer('previous') is not None:
        raise RuntimeError('Unexpected retirement boundary')
    guard_path=Path('/etc/project-workbench/native-cutover.json')
    guard=json.loads(guard_path.read_text())
    if (guard.get('oldCurrent')!=str(old) or not guard.get('localBackupVerified')
        or not re.fullmatch('[a-f0-9]{64}',guard.get('localBackupSHA256',''))
        or guard.get('readbackRelease')!=candidate.name
        or set(guard.get('backupContains',[]))!={'files','workbench_context'}):
        raise RuntimeError('Local file/database restore and this release readback must be verified')
    health(c);restore_channels(c,state)
    if c.run('systemctl','is-active','--quiet','workbench-context-plugin',check=False).returncode==0:
        raise RuntimeError('Old plugin is still active')
    roots=[c.DATA,Path('/var/lib/workbench-context-plugin'),Path('/opt/workbench-context-plugin')]
    config=json.loads(Path('/etc/project-workbench/native.json').read_text())
    keep={x for x in (c.pointer('current'),c.pointer('previous')) if x}
    retiring_releases=[path for path in (c.BASE/'releases').iterdir() if path not in keep]
    old_uuid=state.get('oldPluginInstance',{}).get('uuid')
    if not old_uuid:
        old_dsn=json.loads(Path('/var/lib/workbench-context-plugin/config.json').read_text())['mysqlDSN']
        identity=mysql_query(old_dsn,'SELECT @@server_uuid;')
        if len(identity)!=1 or not re.fullmatch('[a-fA-F0-9-]{36}',identity[0]):raise RuntimeError('Retiring MySQL identity unavailable')
        old_uuid=identity[0]
    if guard.get('backupServerUUID')!=old_uuid:
        raise RuntimeError('Local backup belongs to a different MySQL instance')
    check_retirement_dependencies(config,[*roots,*retiring_releases,c.BASE/'incoming'],old_uuid)
    if not state.get('retirementStarted'):
        config_path=Path('/var/lib/workbench-context-plugin/config.json')
        database=re.fullmatch(r'([^:]+):(.+)@tcp\(([^:]+):(\d+)\)/([^?]+)(?:\?.*)?',json.loads(config_path.read_text())['mysqlDSN'])
        if not database or database.group(3) not in ('127.0.0.1','localhost') or database.group(4)!='3306' or database.group(5)!='workbench_context':raise RuntimeError('Old database identity must be inspected')
        images=[]
        for path in (old,c.release(state['old_previous'],exists=False) if state.get('old_previous') else None):
            if path and (path/'runtime-image').is_file():
                image=(path/'runtime-image').read_text().strip().removeprefix('sha256:')
                if not re.fullmatch('[a-f0-9]{64}',image):raise RuntimeError('Invalid old runtime image identity')
                images.append(image)
        state.update(retirementStarted=True,oldImages=sorted(set(images)),oldPluginInstance={'host':database.group(3),'port':database.group(4),'user':database.group(1),'database':database.group(5),'uuid':old_uuid});write_state(c,state)
    # Reject mounts before DROP/rmtree. Never traverse a shared bind mount or
    # unmount an unrelated consumer as a side effect of retirement.
    deleting=[*roots,*retiring_releases,c.BASE/'incoming']
    check_container_dependencies(c,deleting)
    mount_result=subprocess.run(['findmnt','--json','--output','TARGET'],capture_output=True,text=True)
    if mount_result.returncode:raise RuntimeError('Cannot inspect mount boundaries')
    def mounts(nodes):
        for item in nodes:
            yield Path(item['target']).resolve()
            yield from mounts(item.get('children',[]))
    for target in mounts(json.loads(mount_result.stdout)['filesystems']):
        if any(target==root.resolve() or root.resolve() in target.parents for root in deleting):raise RuntimeError('Retiring path contains a mount; inspect the consumer before cleanup')
    for proc in Path('/proc').iterdir():
        if not proc.name.isdigit():continue
        for field in ('cwd','exe'):
            try:live=(proc/field).resolve(strict=True)
            except OSError:continue
            if any(live==root.resolve() or root.resolve() in live.parents for root in deleting):raise RuntimeError('Retiring path is still used by a process')
    c.run('systemctl','disable','workbench-context-plugin')
    for image in state.get('oldImages',[]):
        exists=c.as_user('podman','image','exists',image,check=False)
        if exists.returncode==0:c.as_user('podman','rmi',image)
    # The database name is deliberately fixed to the verified retiring component.
    # No other database/user/service in this MySQL instance is removed.
    if not state.get('oldDatabaseDropped'):
        # Credentials are read only while the old config still exists; the
        # resumable state retains instance identity, never its password.
        match=re.fullmatch(r'([^:]+):(.+)@tcp\(([^:]+):(\d+)\)/([^?]+)(?:\?.*)?',json.loads(Path('/var/lib/workbench-context-plugin/config.json').read_text())['mysqlDSN'])
        if not match:raise RuntimeError('Old database credentials are unavailable')
        user,password,host,port,database=match.groups()
        if state['oldPluginInstance']!={'host':host,'port':port,'user':user,'database':database,'uuid':old_uuid} or mysql_query(match.group(0),'SELECT @@server_uuid;')!=[old_uuid]:raise RuntimeError('Old database instance changed')
        result=subprocess.run(['mysql','--protocol=TCP','-h',host,'-P',port,'-u',user,'--execute','DROP DATABASE IF EXISTS `workbench_context`'],env=dict(os.environ,MYSQL_PWD=password),capture_output=True,text=True)
        if result.returncode:raise RuntimeError('Retiring database was not dropped; keep the recovery marker')
        state['oldDatabaseDropped']=True;write_state(c,state)
    for root in roots:
        if root.is_symlink():raise RuntimeError('Old data root became a symlink')
        if not root.exists():continue
        for proc in Path('/proc').iterdir():
            if not proc.name.isdigit():continue
            for field in ('cwd','exe'):
                try:live=(proc/field).resolve(strict=True)
                except OSError:continue
                if live==root or root in live.parents:raise RuntimeError('Old data still used by a process')
        shutil.rmtree(root)
    for file in (c.CONFIG,c.CONFIG.parent/'service.env',Path('/etc/systemd/system/workbench-context-plugin.service')):
        file.unlink(missing_ok=True)
    c.run('systemctl','daemon-reload')
    cleanup(c)
    guard_path.unlink(missing_ok=True)
    (c.BASE/'.deploying').unlink(missing_ok=True);c.sync_dir(c.BASE)
