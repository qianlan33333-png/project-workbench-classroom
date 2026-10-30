#!/usr/bin/env python3
"""Package already verified builds; never fetch or change dependency versions."""
import argparse, hashlib, json, os, pathlib, subprocess, tarfile

root = pathlib.Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser()
parser.add_argument('--harness', default='.var/harness-deploy-offline')
parser.add_argument('--output', default='.var/project-workbench-release.tar.gz')
parser.add_argument('--node', default='.toolchain/node-v24.22.0-linux-x64')
args = parser.parse_args()
harness = root / args.harness
node = root / args.node
assert (node/'bin/node').is_file() and (node/'include/node/node_api.h').is_file(), 'Provide complete Node 24.22.0 Linux x64 distribution with --node'
assert (root / 'bin/workbench-linux-amd64').is_file(), 'Build the Linux binary first'
assert (harness / 'lib/bin.js').is_file(), 'Deploy the built fixed Harness first'
for link in harness.rglob('*'):
    if link.is_symlink():
        assert link.resolve().is_relative_to(harness.resolve()) and link.exists(), f'Non-portable dependency: {link}; run portable-harness.py'
assert (root / 'web/dist/index.html').is_file(), 'Build the web app first'
subprocess.run(['sh', str(root/'scripts/stage-cli.sh')], check=True)

def include(info):
    if '/__pycache__/' in info.name or info.name.endswith('.pyc') or '__MACOSX' in info.name or '/.git/' in info.name or info.name.endswith('.DS_Store'):
        return None
    info.uid = info.gid = 0
    info.uname = info.gname = ''
    return info

out = root / args.output
out.parent.mkdir(parents=True, exist_ok=True)
with tarfile.open(out, 'w:gz', compresslevel=3) as tar:
    entries = [(root/'bin/workbench-linux-amd64','bin/workbench'),
               (root/'web/dist','web/dist'), (root/'runtime','runtime'),
               (harness,'harness'), (root/'deploy','deploy'),
               (root/'deploy/Containerfile','Containerfile'),
               (root/'runtime/entrypoint.sh','entrypoint.sh'),
               (root/'vendor-src/deepseek-harness/native/system/packages/linux-x64','native-linux'),
               (root/'vendor-src/deepseek-harness/native/system/packages/entry/src','native-src'),
               (root/'docs','docs'), (root/'scripts','scripts'), (root/'cli','cli'),
               (node,'node'), (root/'README.md','README.md')]
    for source, destination in entries:
        tar.add(source, arcname=destination, filter=include)
sha = hashlib.sha256(out.read_bytes()).hexdigest()
out.with_suffix(out.suffix+'.sha256').write_text(sha+'  '+out.name+'\n')
print(json.dumps({'artifact':str(out),'sha256':sha,'bytes':out.stat().st_size}))
