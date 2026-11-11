#!/usr/bin/env python3
"""Fixed read-only source helpers use ordinary pinned SSH, without shell inputs."""
import os
import re
from pathlib import Path


def connection(config):
    target=config.get('target','')
    if not re.fullmatch(r'[a-zA-Z0-9_-]+@[a-zA-Z0-9.-]+',target):raise ValueError('source_ssh_target_invalid')
    known=Path(config['knownHosts'])
    if not known.is_absolute() or not known.is_file():raise ValueError('source_ssh_pin_required')
    args=['ssh','-C','-F','/dev/null','-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile='+str(known),'-o','ConnectTimeout=15','-o','ServerAliveInterval=15','-o','ServerAliveCountMax=3']
    env=os.environ.copy()
    if config.get('controlSocket'):
        args+=['-S',config['controlSocket'],'-o','BatchMode=yes']
    elif config.get('key'):
        key=Path(config['key'])
        if not key.is_absolute() or key.stat().st_mode&0o077:raise ValueError('source_ssh_private_key_required')
        args+=['-i',str(key),'-o','IdentitiesOnly=yes','-o','BatchMode=yes']
    else:
        name=config.get('passwordEnv','')
        if not re.fullmatch(r'WORKBENCH_SOURCE_[A-Z0-9_]+',name) or not env.get(name):raise ValueError('source_ssh_auth_not_configured')
        env.update(SSH_ASKPASS=str(Path(__file__).resolve()),SSH_ASKPASS_REQUIRE='force',DISPLAY='workbench-source',WORKBENCH_SOURCE_SSH_SECRET_ENV=name)
        args+=['-o','PubkeyAuthentication=no','-o','PreferredAuthentications=password','-o','NumberOfPasswordPrompts=1']
    if config.get('port'):args+=['-p',str(int(config['port']))]
    return args,env,target


if __name__=='__main__':
    name=os.environ.get('WORKBENCH_SOURCE_SSH_SECRET_ENV','')
    if not re.fullmatch(r'WORKBENCH_SOURCE_[A-Z0-9_]+',name):raise SystemExit(1)
    print(os.environ[name])
