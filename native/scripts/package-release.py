#!/usr/bin/env python3
"""Build an immutable Linux release using the pinned official npm lockfile.
Run on Linux; inputs are verified Node/Temporal distributions and a reviewed Go binary.
Never reads application data, credentials, channel state or local acceptance fixtures.
"""
import argparse, hashlib, json, os, platform, shutil, subprocess, tarfile, tempfile
from pathlib import Path

p=argparse.ArgumentParser(description=__doc__)
for name in ('worker','node','temporal','output'):
    p.add_argument('--'+name,required=True,type=Path)
a=p.parse_args()
if platform.system()!='Linux' or platform.machine() not in ('x86_64','amd64'):
    raise SystemExit('Build on Linux amd64, without copying macOS dependencies')
root=Path(__file__).resolve().parents[1]
def linux_amd64(file):
    with file.open('rb') as stream:header=stream.read(20)
    if len(header)!=20 or header[:6]!=b'\x7fELF\x02\x01' or int.from_bytes(header[18:20],'little')!=62:
        raise SystemExit('Expected a Linux amd64 ELF executable: '+file.name)
    if not os.access(file,os.X_OK):raise SystemExit('Input is not executable: '+file.name)
for executable in (a.worker,a.temporal,a.node/'bin/node'):linux_amd64(executable)
temporal_version=subprocess.check_output([str(a.temporal.resolve()),'--version'],text=True).strip().split()[-1]
if temporal_version!='1.32.2':raise SystemExit('Pinned official Temporal 1.32.2 is required')
node=a.node.resolve()/ 'bin/node'
if subprocess.check_output([str(node),'--version'],text=True).strip()!='v24.22.0':
    raise SystemExit('Pinned official Node v24.22.0 is required')
a.output=a.output.resolve();a.output.parent.mkdir(parents=True,exist_ok=True)
with tempfile.TemporaryDirectory(prefix='workbench-native-build-',dir=a.output.parent) as tmp:
    stage=Path(tmp)/'release';stage.mkdir()
    shutil.copytree(a.node,stage/'node',symlinks=True)
    (stage/'bin').mkdir();shutil.copy2(a.worker,stage/'bin/workbench-worker')
    (stage/'temporal').mkdir();shutil.copy2(a.temporal,stage/'temporal/temporal-server')
    shutil.copytree(root/'deploy',stage/'deploy',ignore=shutil.ignore_patterns('__pycache__'))
    shutil.copytree(root/'skills',stage/'skills')
    shutil.copytree(root/'docs',stage/'docs')
    shutil.copytree(root/'components',stage/'components',ignore=shutil.ignore_patterns('__pycache__','*.test.mjs','test_*.py'))
    # The native preset intentionally has no arbitrary filesystem reader. Ship
    # the developer Skill self-contained, generated from the one contract source.
    skill=stage/'skills/workbench-tool-development/SKILL.md'
    skill.write_text(skill.read_text()+'\n\n'+(root/'docs/EXTENSIONS.md').read_text())
    runtime=stage/'runtime';runtime.mkdir()
    for name in ('package.json','package-lock.json'):
        shutil.copy2(root/name,runtime/name)
    for name in ('harness','connectors'):
        shutil.copytree(root/name,runtime/name,ignore=shutil.ignore_patterns('node_modules','*.test.js','*.test.mjs'))
    env=dict(os.environ,PATH=str(stage/'node/bin')+':'+os.environ.get('PATH',''),NODE_OPTIONS='--max-old-space-size=512')
    subprocess.run([str(stage/'node/bin/npm'),'ci','--omit=dev','--no-audit','--no-fund'],cwd=runtime,env=env,check=True)
    files={str(f.relative_to(stage)):hashlib.file_digest(f.open('rb'),'sha256').hexdigest() for f in stage.rglob('*') if f.is_file() and not f.is_symlink()}
    manifest={'format':'personal-workbench-native-v1','harness':'0.2.2-rc.1','node':'24.22.0','temporal':temporal_version,'workerStartupGate':1,'files':files}
    (stage/'native-release.json').write_text(json.dumps(manifest,sort_keys=True))
    with tarfile.open(a.output,'w:gz',compresslevel=3) as tar:
        for f in sorted(stage.iterdir()):tar.add(f,arcname=f.name)
with a.output.open('rb') as f:digest=hashlib.file_digest(f,'sha256').hexdigest()
Path(str(a.output)+'.sha256').write_text(digest+'  '+a.output.name+'\n')
print('Prepared native release',a.output.name,digest)
