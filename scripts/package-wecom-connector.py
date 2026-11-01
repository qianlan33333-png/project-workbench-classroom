#!/usr/bin/env python3
"""Build a standalone connector release; no network, accounts, or deployments."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile

root = Path(__file__).resolve().parents[1]
p = argparse.ArgumentParser()
p.add_argument('--version', default='0.1.0')
p.add_argument('--output', default='.var/connector-release')
args = p.parse_args()
if not args.version or any(c not in '0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ.-' for c in args.version):
    p.error('invalid version')
out = root / args.output
out.mkdir(parents=True, exist_ok=True)
source = hashlib.sha256()
for folder in ['pkg/wecomgateway', 'cmd/wecom-gw-cli']:
    for path in sorted((root / folder).glob('*.go')):
        source.update(str(path.relative_to(root)).encode() + b'\0' + path.read_bytes())
manifest = {'version': args.version, 'sourceSha256': source.hexdigest(), 'artifacts': []}
for system, arch in [('linux', 'amd64'), ('darwin', 'arm64')]:
    name = f'wecom-gw-cli-{args.version}-{system}-{arch}.tar.gz'
    with tempfile.TemporaryDirectory(prefix='wecom-connector-') as tmp:
        binary = Path(tmp) / 'wecom-gw-cli'
        subprocess.run([os.environ.get('GO', 'go'), 'build', '-trimpath', '-ldflags',
                        f'-s -w -X main.version={args.version}', '-o', str(binary), './cmd/wecom-gw-cli'],
                       cwd=root, env={**os.environ, 'GOOS': system, 'GOARCH': arch, 'CGO_ENABLED': '0'}, check=True)
        with tarfile.open(out / name, 'w:gz') as archive:
            for path, target in [(binary, 'wecom-gw-cli'),
                                 (root / 'docs/WECOM_CONNECTOR.md', 'README.md'),
                                 (root / 'examples/wecom-gateway', 'examples')]:
                archive.add(path, arcname=target)
    artifact = out / name
    digest = hashlib.sha256(artifact.read_bytes()).hexdigest()
    artifact.with_suffix(artifact.suffix + '.sha256').write_text(f'{digest}  {name}\n')
    manifest['artifacts'].append({'file': name, 'sha256': digest, 'bytes': artifact.stat().st_size})
(out / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
print(json.dumps(manifest))
