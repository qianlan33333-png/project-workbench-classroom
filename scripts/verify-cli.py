#!/usr/bin/env python3
"""Verify an installed CLI against a live workbench using disposable projects only."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import uuid

parser = argparse.ArgumentParser()
parser.add_argument('--cli', required=True)
parser.add_argument('--config')
parser.add_argument('--report', default='.var/cli-live-acceptance.json')
args = parser.parse_args()
base = [sys.executable, str(Path(args.cli).expanduser())]
if args.config:
    base += ['--config', args.config]
records = []
created = []
before = None

def cli(*words, content=None, error=None):
    result = subprocess.run(base + list(words), input=content, capture_output=True, timeout=150)
    body = json.loads(result.stderr if result.returncode else result.stdout)
    if error is None:
        assert result.returncode == 0, (words, body)
    else:
        assert result.returncode != 0 and body.get('status') == error, (words, body)
    records.append({'command': list(words[:3]), 'ok': True, 'expectedError': error})
    return body

ok = False
try:
    assert cli('schema')['version'] == '0.2.0'
    assert cli('status')['expiresAt'] is None
    before = {p['id'] for p in cli('projects', 'list')}
    p = cli('projects', 'create', '--name', 'CLI 临时验收 ' + uuid.uuid4().hex[:8], '--description', '只用于 CLI 验收', '--alias', 'CLI测试别名')
    pid = p['id']
    created.append(pid)
    update = cli('projects', 'update', pid, '--name', p['name'] + ' 更名')
    assert update['description'] == p['description'] and update['defaultSessionId'] == p['defaultSessionId']
    assert cli('projects', 'get', update['name'])['id'] == pid
    path = '中文目录/定位.md'
    original = '---\ntitle: CLI 验收\n---\n品牌定位与 [[选题]]\n'
    first = cli('files', 'write', pid, path, '--from', '-', '--base-hash', '', content=original.encode())
    read = cli('files', 'read', pid, path)
    assert read['content'] == original
    cli('files', 'write', pid, path, '--text', original + '更新稿\n', '--base-hash', read['hash'])
    cli('files', 'write', pid, path, '--text', '过期稿', '--base-hash', first['after'], error=409)
    assert cli('files', 'search', pid, '品牌')
    assert len(cli('files', 'history', pid, path)) == 2
    assert len(cli('files', 'list', pid)) > 0
    sessions = cli('api', 'GET', '/projects/' + pid + '/sessions')
    assert sessions[0]['id'] == p['defaultSessionId']
    cli('api', 'GET', '/sessions/' + p['defaultSessionId'] + '/history')
    cli('api', 'GET', '/schedules?projectId=' + pid)
    with tempfile.TemporaryDirectory(prefix='pwb-verify-') as temp:
        temp = Path(temp)
        source = temp / 'fixture.bin'
        source.write_bytes(b'\x00\xff\xfeCLI\n')
        cli('files', 'upload', pid, '附件/fixture.bin', str(source))
        download = temp / 'received.bin'
        cli('files', 'download', pid, '附件/fixture.bin', '--output', str(download))
        assert source.read_bytes() == download.read_bytes()
        archive = temp / 'project.zip'
        cli('projects', 'export', pid, '--output', str(archive))
        imported = cli('projects', 'create', '--name', 'CLI ZIP 验收 ' + uuid.uuid4().hex[:8])
        created.append(imported['id'])
        cli('projects', 'import', imported['id'], str(archive))
        cli('projects', 'import', imported['id'], str(archive), error=409)
        imported_file = cli('files', 'read', imported['id'], path)
        assert imported_file['content'] == original + '更新稿\n'
        cli('files', 'download', imported['id'], '附件/fixture.bin', '--output', str(download))
        assert source.read_bytes() == download.read_bytes()
    removed = cli('files', 'delete', pid, path)
    cli('files', 'read', pid, path, error=404)
    cli('files', 'undo', removed['id'])
    assert cli('files', 'read', pid, path)['content'] == original + '更新稿\n'
    assert cli('projects', 'archive', pid)['archived'] is True
    assert cli('projects', 'restore', pid)['archived'] is False
    ok = True
finally:
    cleanup_errors = []
    for pid in created:
        try:
            cli('projects', 'delete', pid)
            cli('projects', 'get', pid, error=404)
        except Exception as exc:
            cleanup_errors.append(str(exc))
    same_projects = before is not None and {p['id'] for p in cli('projects', 'list')} == before
    report = {'passed': ok and not cleanup_errors and same_projects, 'checkedAt': time.strftime('%Y-%m-%dT%H:%M:%S%z'), 'cliSha256': hashlib.sha256(Path(args.cli).expanduser().read_bytes()).hexdigest(), 'calls': records, 'temporaryProjectIds': created, 'existingProjectsPreserved': same_projects, 'cleanupErrors': cleanup_errors}
    target = Path(args.report)
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({'passed': report['passed'], 'calls': len(records), 'existingProjectsPreserved': same_projects, 'report': str(target)}, ensure_ascii=False))
    assert report['passed'], 'Live CLI acceptance failed; inspect report'
