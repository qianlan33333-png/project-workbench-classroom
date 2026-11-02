#!/usr/bin/env python3
"""Render both private installation files from one deployment inventory.

Usage: render-auto-reply-install.py SPEC OUTPUT --keys-dir PRIVATE_DIR
       [--workbench-install-dir /absolute/path/on/workbench/host]
SPEC is a JSON array; see docs/WECOM_CRM_INSTALL.md. OUTPUT must be new.
"""
import argparse
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import stat
import tempfile
from urllib.parse import urlsplit

ID = re.compile(r"[a-zA-Z0-9_-]{1,80}")


def private_dir(path):
    path.mkdir(mode=0o700, exist_ok=True)
    if path.is_symlink() or not path.is_dir() or path.stat().st_mode & 0o077:
        raise ValueError("private directory required")


def signing_key(directory, key_id):
    path = directory / key_id
    # Publish a complete secret atomically even for simultaneous installations.
    with tempfile.NamedTemporaryFile(dir=directory, delete=False) as out:
        scratch = Path(out.name)
        out.write(secrets.token_urlsafe(48).encode())
    try:
        try:
            os.link(scratch, path)
        except FileExistsError:
            pass
        with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as f:
            mode = os.fstat(f.fileno()).st_mode
            if not stat.S_ISREG(mode) or mode & 0o077:
                raise ValueError("private regular key file required")
            key = f.read(257).decode().strip()
            if not 32 <= len(key.encode()) <= 256:
                raise ValueError("invalid installation key")
            return key
    finally:
        scratch.unlink()


def base_url(value):
    url = urlsplit(value)
    if (url.scheme != 'https' or not url.hostname or url.username is not None
            or url.password is not None or url.query or url.fragment
            or chr(92) in value or any(c.isspace() for c in value)):
        raise ValueError("HTTPS public base URL required")
    return value.rstrip('/')


def render(spec, dest, key_dir, wb_dir):
    if not spec or not isinstance(spec, list) or not wb_dir.is_absolute():
        raise ValueError("bindings and absolute workbench installation directory required")
    if os.path.lexists(dest):
        raise ValueError("output already exists; render to a new private staging directory")
    private_dir(key_dir)
    with tempfile.TemporaryDirectory(dir=dest.parent) as tmp:
        root = Path(tmp) / 'bundle'
        wb = root / 'workbench'
        crm = root / 'crm'
        wb.mkdir(parents=True)
        crm.mkdir()
        grants, installs, seen = [], [], set()
        for b in spec:
            key_id, binding = b['keyId'], b['bindingId']
            if not all(ID.fullmatch(x) for x in [key_id, binding, b['id'], b['connectionId']]):
                raise ValueError("invalid binding or key identifier")
            if (not all(isinstance(b[k], str) and b[k].strip() for k in ['corpId','staffUserId','label'])
                    or type(b['bindingVersion']) is not int or b['bindingVersion'] < 1):
                raise ValueError("missing binding scope")
            identities = {('key',key_id), ('binding',binding), ('id',b['id']), ('scope',b['corpId'],b['staffUserId'])}
            if identities & seen:
                raise ValueError("duplicate integration identity or staff scope")
            seen.update(identities)
            w = base_url(b['workbenchBase']) + '/api/v1/gateway/hooks/' + b['connectionId']
            c = base_url(b['crmBase']) + '/api/v1/integrations/auto-reply/events'
            key = signing_key(key_dir, key_id)
            key_name = key_id + '.secret'
            (wb / key_name).write_text(key)
            grant = dict(keyId=key_id,secretFile=str(wb_dir / key_name),bindingId=binding,
                         bindingVersion=b['bindingVersion'],staffUserId=b['staffUserId'],
                         callbackUrl=c,callbackOrigin='https://' + urlsplit(c).netloc)
            if b.get('contactMappings'):
                grant['contactMappings'] = b['contactMappings']
            grants.append(grant)
            installs.append(dict(id=b['id'],label=b['label'],corpId=b['corpId'],
                                 staffUserIds=[b['staffUserId']],keyId=key_id,secret=key,
                                 workbenchUrl=w,callbackUrl=c))
        (wb / 'grants.json').write_text(json.dumps(grants, ensure_ascii=False, indent=2))
        (crm / 'install.json').write_text(json.dumps(dict(bindings=installs), ensure_ascii=False, indent=2))
        root.rename(dest)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('spec', type=Path)
    parser.add_argument('output', type=Path)
    parser.add_argument('--keys-dir', type=Path, required=True)
    parser.add_argument('--workbench-install-dir', type=Path)
    args = parser.parse_args()
    os.umask(0o077)
    render(json.loads(args.spec.read_text()), args.output, args.keys_dir,
           args.workbench_install_dir or (args.output.absolute() / 'workbench'))
    print('Paired private installation bundle prepared')
