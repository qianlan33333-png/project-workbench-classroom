#!/usr/bin/env python3
"""Exec one standard service from the immutable native release."""
import json, os, sys, time, urllib.request
from pathlib import Path

release = Path(__file__).resolve().parents[1]
config_file = Path(os.environ.get('WORKBENCH_SERVICE_CONFIG', '/etc/project-workbench/native.json'))
config = json.loads(config_file.read_text())
role = sys.argv[1]
if role not in ('host', 'worker', 'channels', 'temporal'):
    raise SystemExit('Unknown service')
settings = config[role]
env = dict(os.environ, **config.get('common', {}), **settings.get('env', {}))
env['PATH'] = str(release / 'node/bin') + ':/usr/local/bin:/usr/bin:/bin'
node = str(release / 'node/bin/node')
env['DSH_BUNDLED_SKILL_DIR'] = str(release / 'skills')
if role == 'channels':
    marker=Path('/opt/project-workbench/.deploying')
    state=json.loads(marker.read_text()) if marker.exists() else {}
    if state.get('kind')=='native' and not state.get('committed') and Path(state.get('candidate','')).resolve()==release:
        raise SystemExit('External receivers stay stopped before cutover commit')
if role in ('host','channels'):
    url=(env.get('WORKBENCH_WORKER_URL','http://127.0.0.1:7381') if role=='host' else env.get('HARNESS_BRIDGE_URL','http://127.0.0.1:7382')).rstrip('/')+'/health'
    headers={} if role=='host' else {'Authorization':'Bearer '+env['WORKBENCH_HOST_TOKEN']}
    for attempt in range(60):
        try:
            with urllib.request.urlopen(urllib.request.Request(url,headers=headers),timeout=2) as response:
                if json.load(response).get('ready'):break
        except (OSError,ValueError):pass
        time.sleep(1)
    else:raise SystemExit('Upstream service is not ready')

if role == 'host':
    command = [node, str(release / 'runtime/node_modules/@deepseek-ai/dsh/lib/bin.js'), 'web',
               '--patch', str(release / 'runtime/harness/cordis.patch.yml'), '--no-open',
               '--host', '127.0.0.1', '--port', str(settings.get('port', 3082))]
elif role == 'worker':
    # The installed controller owns this path; private standalone acceptance
    # may inject a fixture into the binary without touching a live deployment.
    env['WORKBENCH_DEPLOYMENT_MARKER'] = '/opt/project-workbench/.deploying'
    command = [str(release / 'bin/workbench-worker'), *sys.argv[2:]]
elif role == 'channels':
    command = [node, str(release / 'runtime/connectors/server.mjs')]
else:
    command = [str(release / 'temporal/temporal-server'), '--config-file', settings['configFile'], 'start']
os.chdir(settings.get('cwd',str(release)))
os.execvpe(command[0], command, env)
