#!/usr/bin/env python3
"""Package already verified builds; never fetch or change dependency versions."""
import argparse, hashlib, json, os, pathlib, tarfile

root = pathlib.Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser()
parser.add_argument('--harness', default='.var/harness-deploy-offline')
parser.add_argument('--output', default='.var/project-workbench-release.tar.gz')
args = parser.parse_args()
harness = root / args.harness
assert (root / 'bin/workbench-linux-amd64').is_file(), 'Build the Linux binary first'
assert (harness / 'lib/bin.js').is_file(), 'Deploy the built fixed Harness first'
assert (root / 'web/dist/index.html').is_file(), 'Build the web app first'

def include(info):
    if '__MACOSX' in info.name or '/.git/' in info.name or info.name.endswith('.DS_Store'):
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
               (root/'docs','docs'), (root/'scripts','scripts')]
    for source, destination in entries:
        tar.add(source, arcname=destination, filter=include)
sha = hashlib.sha256(out.read_bytes()).hexdigest()
out.with_suffix(out.suffix+'.sha256').write_text(sha+'  '+out.name+'\n')
print(json.dumps({'artifact':str(out),'sha256':sha,'bytes':out.stat().st_size}))
