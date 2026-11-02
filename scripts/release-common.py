#!/usr/bin/env python3
"""Shared implementation for the existing release/rollback/cleanup entrypoints."""
import argparse
from contextlib import closing
import fcntl
import hashlib
import json
import os
from pathlib import Path
import pwd
import re
import shutil
import sqlite3
import subprocess
import sys
import tarfile
import time
import urllib.request

BASE = Path('/opt/project-workbench')
DATA = Path('/var/lib/project-workbench')
CONFIG = Path('/etc/project-workbench/config.json')
USER = 'projectwb'
RESERVE = 5 * 1024**3


def run(*args, check=True, capture=False):
    return subprocess.run([str(x) for x in args], check=check, cwd='/', text=True,
                          stdout=subprocess.PIPE if capture else None,
                          stderr=subprocess.PIPE if capture else None)


def as_user(*args, **kwargs):
    uid = pwd.getpwnam(USER).pw_uid
    return run('sudo', '-H', '-u', USER, 'env', f'XDG_RUNTIME_DIR=/run/user/{uid}',
               f'DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/{uid}/bus', *args, **kwargs)


def sync_dir(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def write(path, text, mode=0o600):
    tmp = path.with_name(path.name + '.next')
    tmp.unlink(missing_ok=True)
    with tmp.open('x') as stream:
        os.chmod(tmp, mode)
        stream.write(text)
        stream.flush()
        os.fsync(stream.fileno())
    if path.exists():
        owner = path.stat()
        os.chown(tmp, owner.st_uid, owner.st_gid)
        os.chmod(tmp, owner.st_mode & 0o777)
    os.replace(tmp, path)
    sync_dir(path.parent)


def release(path, exists=True):
    path = Path(path)
    if not path.is_absolute():
        path = BASE / 'releases' / path
    if (path.parent != BASE / 'releases' or path.name in ('.', '..') or path.is_symlink()
            or not re.fullmatch(r'[a-zA-Z0-9._-]+', path.name)):
        raise RuntimeError(f'Not a direct release directory: {path}')
    if exists and not (path.is_dir() and (path / 'bin/workbench').is_file()
                       and (path / 'web/dist/index.html').is_file()):
        raise RuntimeError(f'Incomplete release: {path}')
    return path


def pointer(name):
    p = BASE / name
    if not p.is_symlink():
        if p.exists():
            raise RuntimeError(f'{name} must be a symlink')
        return None
    return release(p.resolve(strict=True))


def point(name, target):
    p = BASE / name
    if target is None:
        p.unlink(missing_ok=True)
    else:
        tmp = BASE / (name + '.next')
        tmp.unlink(missing_ok=True)
        tmp.symlink_to(release(target))
        os.replace(tmp, p)
    sync_dir(BASE)


def image_id(value):
    return as_user('podman', 'image', 'inspect', value, '--format', '{{.Id}}', capture=True).stdout.strip()


def runtime_image(path):
    value = (path / 'runtime-image').read_text().strip()
    actual = image_id(value)
    if value.removeprefix('sha256:') != actual.removeprefix('sha256:'):
        raise RuntimeError(f'Runtime image must be a complete immutable ID: {path}')
    return actual.removeprefix('sha256:')


def set_runtime(path):
    cfg = json.loads(CONFIG.read_text())
    value = runtime_image(path)
    if cfg.get('RuntimeImage', '').removeprefix('sha256:') != value:
        cfg['RuntimeImage'] = value
        write(CONFIG, json.dumps(cfg, ensure_ascii=False, indent=2) + '\n')


def quiet():
    with sqlite3.connect(f'file:{DATA}/workbench.sqlite?mode=ro', uri=True) as db:
        for sql in ["SELECT count(*) FROM runs WHERE state IN ('queued','running','waiting','stopping')",
                    "SELECT count(*) FROM project_locks WHERE run_id NOT LIKE 'maintenance:%'",
                    "SELECT count(*) FROM tool_jobs WHERE state IN ('queued','running')"]:
            if db.execute(sql).fetchone()[0]:
                raise RuntimeError('Wait for active business/tool operations; nothing was interrupted')


def health():
    for _ in range(150):
        try:
            with urllib.request.urlopen('http://127.0.0.1:3082/api/v1/health', timeout=2) as response:
                if response.status == 200:
                    return
        except OSError:
            pass
        time.sleep(1)
    raise RuntimeError('Workbench did not become healthy')


def prepare_gateway_worker(path):
    marker = path / 'gateway-protocol-v2'
    supported = marker.is_file() and marker.read_text().strip() == '2'
    db_path = DATA / 'workbench.sqlite'
    if not db_path.exists():
        return
    with closing(sqlite3.connect(f'{db_path.as_uri()}?mode=rw', uri=True, timeout=30)) as db, db:
        db.execute('BEGIN IMMEDIATE')
        if not db.execute("SELECT 1 FROM sqlite_master WHERE type='table' AND name='gateway_sessions'").fetchone():
            return
        if supported:
            db.execute('DROP TRIGGER IF EXISTS rollback_guard_gateway_insert')
            db.execute('DROP TRIGGER IF EXISTS rollback_guard_gateway_update')
            return

        # start() is shared by deploy, manual rollback and interrupted-deploy
        # recovery. Their earlier quiet check cannot exclude arrivals during stop.
        for execute, unit, name in ((run, ('systemctl',), 'project-workbench'),
                                    (as_user, ('systemctl', '--user'), 'project-workbench-runtime')):
            status = execute(*unit, 'show', name, '-p', 'ActiveState', '--value', capture=True, check=False)
            if status.returncode not in (0, 4) or status.stdout.strip() != 'inactive':
                raise RuntimeError(f'Cannot fence gateway work while {name} is not confirmed stopped')

        active = "state IN ('queued','running','waiting','stopping')"
        gateway = "(session_id IN (SELECT id FROM gateway_sessions) OR json_extract(data,'$.source')='wecom-gateway')"
        ids = db.execute(f'SELECT id FROM runs WHERE {active} AND {gateway}').fetchall()
        names = {'pw-' + row[0] for row in ids}
        if names:
            label = hashlib.sha256(str(DATA).encode()).hexdigest()[:16]
            containers = as_user('podman', 'ps', '--all', '--no-trunc', '--filter',
                                 'label=project-workbench-root=' + label,
                                 '--format', '{{.ID}} {{.Names}}', capture=True)
            for line in containers.stdout.splitlines():
                container, name = line.split()
                if name in names:
                    as_user('podman', 'rm', '--force', '--ignore', container)
        # Release the lease only after the worker and its identified executor
        # have stopped. An engine failure rolls the entire SQL transaction back.
        db.execute(f'DELETE FROM project_locks WHERE run_id IN (SELECT id FROM runs WHERE {active} AND {gateway})')
        reason = '回滚至旧版企微执行环境，恢复安全版本后请人工核对'
        # v1 still accepts callbacks but exposes credentials to external runs.
        # Keep binding identities/grants intact and fence legacy workers before boot.
        db.execute("""UPDATE gateway_bindings SET data=json_set(data,'$.enabled',json('false'))
                      WHERE json_extract(data,'$.enabled')=1""")
        db.execute(f"""UPDATE runs SET state='interrupted',
                       data=json_set(data,'$.state','interrupted','$.error',?,'$.finishedAt',?)
                       WHERE {active} AND {gateway}""", (reason, time.time_ns() // 1_000_000))
        db.execute("""UPDATE inbox SET state='handled',data=json_set(data,'$.state','handled',
                      '$.gateway.historyRole',coalesce(json_extract(data,'$.gateway.historyRole'),'客户'))
                      WHERE source='wecom-gateway' AND state IN ('gateway_pending','queued','running','waiting','stopping')""")
        db.execute("""UPDATE gateway_sessions SET data=json_set(data,'$.mode','human','$.reason',?,
                      '$.version',coalesce(json_extract(data,'$.version'),0)+1)
                      WHERE json_extract(data,'$.mode')='auto'""", (reason,))
        # Older RecoverDeliveries decodes and re-encodes a narrower struct for
        # "sending", losing gateway metadata. Preserve bodies, keys and receipts.
        db.execute("""UPDATE deliveries SET state='unknown',data=json_set(data,'$.state','unknown','$.error',?)
                      WHERE (state='sending' AND json_extract(data,'$.kind') IN ('gateway.answer','gateway.handoff','gateway.crm'))
                      OR (state='pending' AND json_extract(data,'$.kind')='gateway.answer'
                          AND coalesce(json_extract(data,'$.gateway.attempts'),0)>0)""", (reason,))
        db.execute("""UPDATE deliveries SET state='cancelled',data=json_set(data,'$.state','cancelled','$.error',?)
                      WHERE state='pending' AND json_extract(data,'$.kind')='gateway.answer'""", (reason,))
        # A bookmarked conversation or generic retry in an older UI must not
        # create ordinary unrestricted work in one of these external sessions.
        for suffix, operation in (('insert', 'INSERT'), ('update', 'UPDATE')):
            db.execute(f"""CREATE TRIGGER IF NOT EXISTS rollback_guard_gateway_{suffix} BEFORE {operation} ON runs
                           WHEN NEW.state IN ('queued','running','waiting','stopping') AND
                           (NEW.session_id IN (SELECT id FROM gateway_sessions) OR json_extract(NEW.data,'$.source')='wecom-gateway')
                           BEGIN SELECT RAISE(ABORT,'gateway worker version required'); END""")


def start(path, channels=True):
    prepare_gateway_worker(path)
    point('current', path)
    set_runtime(path)
    run('systemctl', 'start', 'project-workbench')
    health()
    if channels:
        run('systemctl', 'start', 'project-workbench-channels')


def remove(path, keep):
    """Only called for explicit project-owned roots, never arbitrary CLI paths."""
    if not path.exists() and not path.is_symlink():
        return
    protected = [DATA, CONFIG, CONFIG.parent, Path('/var/lib/project-workbench-purge-ledger'),
                 Path('/srv/qianlan-company'), *keep]
    cfg = json.loads(CONFIG.read_text())
    protected += [Path(v).resolve() for v in cfg.values() if isinstance(v, str) and v.startswith('/')]
    if any(path == p or path in p.parents for p in protected):
        raise RuntimeError(f'Refusing to delete a current data/config/dependency root: {path}')
    if path.is_symlink():
        path.unlink()
        return
    for proc in Path('/proc').iterdir():
        if proc.name.isdigit():
            for field in ('exe', 'cwd'):
                try:
                    live = (proc / field).resolve(strict=True)
                except OSError:
                    continue
                if live == path or path in live.parents:
                    raise RuntimeError(f'Still in use by process {proc.name}: {path}')
    for line in run('findmnt', '-rn', '-o', 'TARGET', capture=True).stdout.splitlines():
        mounted = Path(line)
        if mounted == path or path in mounted.parents:
            raise RuntimeError(f'Still mounted: {path}')
    print(f'Removing {path}', flush=True)
    if path.is_dir():
        shutil.rmtree(path)
    else:
        path.unlink()


def cleanup(extra=(), clear_incoming=True):
    original = (pointer('current'), pointer('previous'))
    keep = {p for p in (*original, *extra) if p is not None}
    if not original[0]:
        raise RuntimeError('Cannot clean without a current release')
    retained_images = {runtime_image(p) for p in keep}
    # The runtime store belongs to projectwb, exclusively used by this project.
    # Engine checks protect shared parent images; never force or prune by unused.
    for root in (BASE / 'releases').iterdir():
        if (pointer('current'), pointer('previous')) != original:
            raise RuntimeError('Release pointers changed; stopped before deletion')
        if root not in keep:
            release(root, exists=False)
            remove(root, keep)
    for p in BASE.iterdir():
        if p.name.startswith('rollback-') and p.is_dir() and not p.is_symlink():
            remove(p, keep)
    for name in ('frontend-releases', 'frontend-rollbacks', 'artifacts'):
        remove(BASE / name, keep)
    if clear_incoming:
        remove(BASE / 'incoming', keep)
    for name in ('frontend-active.json', 'caddy-before-preview', 'cutover.json'):
        remove(BASE / name, keep)
    for p in BASE.glob('current.*'):
        if p.is_symlink():
            p.unlink()
    # Only delete image IDs outside the two retained versions. Successful image
    # removal also reclaims its unused storage layers through Podman's own API.
    ids = set(as_user('podman', 'images', '--all', '--quiet', '--no-trunc', capture=True).stdout.split())
    ids = {x.removeprefix('sha256:') for x in ids}
    pending = ids - retained_images
    while pending:
        removed = set()
        for ident in sorted(pending):
            result = as_user('podman', 'rmi', '--no-prune', ident, check=False, capture=True)
            if result.returncode == 0:
                print('Removed image ' + ident, flush=True)
                removed.add(ident)
        if not removed:
            # Parent images of retained images are legitimate dependencies.
            parents = set()
            todo = list(retained_images)
            while todo:
                parent = as_user('podman', 'image', 'inspect', todo.pop(), '--format', '{{.Parent}}',
                                 capture=True).stdout.strip().removeprefix('sha256:')
                if parent and parent not in parents:
                    parents.add(parent)
                    todo.append(parent)
            unknown = pending - parents
            if unknown:
                raise RuntimeError('Images still in use or needing inspection: ' + ', '.join(sorted(unknown)))
            break
        pending -= removed


def recover():
    intent = BASE / '.deploying'
    if not intent.exists():
        return
    state = json.loads(intent.read_text())
    if set(state) != {'old_current', 'old_previous', 'candidate'}:
        raise RuntimeError('Invalid unfinished deployment state')
    old = release(state['old_current']) if state['old_current'] else None
    prev = release(state['old_previous'], exists=False) if state['old_previous'] else None
    candidate = release(state['candidate'], exists=False)
    current, previous = pointer('current'), pointer('previous')
    if current == candidate and previous == old:
        health()
        cleanup()
    else:
        if current not in (old, candidate) or previous not in (prev, old):
            raise RuntimeError('Unexpected pointer changes; no recovery deletion performed')
        if old:
            active = run('systemctl', 'is-active', '--quiet', 'project-workbench', check=False).returncode == 0
            if current != old or not active:
                run('systemctl', 'stop', 'project-workbench-channels', 'project-workbench')
                start(old)
            else:
                run('systemctl', 'start', 'project-workbench-channels')
            point('previous', prev)
            if candidate not in (old, prev):
                remove(candidate, {old, prev} - {None})
            cleanup()
        else:
            run('systemctl', 'stop', 'project-workbench-channels', 'project-workbench', check=False)
            point('current', None)
            point('previous', None)
            remove(candidate, set())
    intent.unlink()
    sync_dir(BASE)


def deploy(candidate):
    old, prev = pointer('current'), pointer('previous')
    if candidate == old:
        raise RuntimeError('Candidate is already current')
    expected = os.environ.get('PW_EXPECTED_PREVIOUS')
    if expected and str(old) != expected:
        raise RuntimeError('Current release differs from the caller expected baseline')
    if not (candidate / 'runtime-image').exists():
        if not old:
            raise RuntimeError('Candidate has no pinned runtime-image')
        write(candidate / 'runtime-image', runtime_image(old) + '\n', 0o644)
    runtime_image(candidate)
    write(BASE / '.deploying', json.dumps({'old_current': str(old) if old else None,
          'old_previous': str(prev) if prev else None, 'candidate': str(candidate)}) + '\n')
    committed = False
    try:
        if old:
            cleanup((candidate,), clear_incoming=False)
        if shutil.disk_usage(BASE).free < RESERVE:
            raise RuntimeError('Less than 5 GiB free after preparation; retaining original versions')
        if old:
            quiet()
        preview = os.environ.get('PW_PREVIEW_UNIT')
        if preview:
            if not re.fullmatch(r'pwb-[a-z0-9-]+-preview(?:\.service)?', preview):
                raise RuntimeError('Invalid preview unit')
            as_user('systemctl', '--user', 'stop', preview)
        run('systemctl', 'stop', 'project-workbench-channels')
        if old:
            quiet()
        run('systemctl', 'stop', 'project-workbench')
        if old:
            start(candidate)
        else:
            # Preserve first-install isolation: channel cutover remains explicit.
            start(candidate, channels=False)
        acceptance = os.environ.get('PW_ACCEPTANCE_SCRIPT')
        if acceptance:
            run(str(Path(acceptance).resolve(strict=True)))
        point('previous', old)
        committed = True  # Cleanup errors must never enter the release rollback path.
        cleanup()
        (BASE / '.deploying').unlink()
        sync_dir(BASE)
        print(f'Current: {candidate.name}; previous: {old.name if old else "none"}')
    except BaseException:
        if not committed:
            recover()
        raise


def install(archive, name):
    archive = Path(archive).resolve(strict=True)
    candidate = release(name, exists=False)
    if candidate.exists():
        raise RuntimeError('Use a fresh release name')
    expected = Path(str(archive) + '.sha256').read_text().split()[0]
    with archive.open('rb') as source:
        actual = hashlib.file_digest(source, 'sha256').hexdigest()
    if actual != expected:
        raise RuntimeError('Archive checksum mismatch')
    if pointer('current'):
        cleanup(clear_incoming=False)
    with tarfile.open(archive) as source:
        size = sum(member.size for member in source.getmembers())
        if shutil.disk_usage(BASE).free < size * 2 + RESERVE:
            raise RuntimeError('Not enough space for extraction, image build and 5 GiB reserve')
        write(BASE / '.deploying', json.dumps({
            'old_current': str(pointer('current')) if pointer('current') else None,
            'old_previous': str(pointer('previous')) if pointer('previous') else None,
            'candidate': str(candidate)}) + '\n')
        candidate.mkdir()
        try:
            source.extractall(candidate, filter='data')
            run('chown', '-R', USER + ':' + USER, candidate)
            run(candidate / 'node/bin/node', '--version')
            if not (candidate / 'node/include/node/node_api.h').is_file():
                raise RuntimeError('Complete Node headers are required')
            if run(candidate / 'node/bin/node', '--version', capture=True).stdout.strip() != 'v24.22.0':
                raise RuntimeError('Expected the pinned Linux Node 24.22.0 distribution')
            tag = 'localhost/project-workbench-runtime:' + candidate.name
            as_user('podman', 'build', '--build-arg',
                    'APT_MIRROR=' + os.environ.get('APT_MIRROR', 'deb.debian.org'), '-t', tag, candidate)
            write(candidate / 'runtime-image', image_id(tag) + '\n', 0o644)
            downloads = DATA / 'tools/downloads'
            downloads.mkdir(parents=True, exist_ok=True)
            for item in (candidate / 'tool-artifacts').glob('*.tar.gz'):
                if not (downloads / item.name).exists():
                    shutil.copy2(item, downloads / item.name)
            run('chown', '-R', USER + ':' + USER, downloads)
            helper = candidate / 'scripts/scrub-session-backups.py'
            if helper.exists():
                run('install', '-m', '755', helper, '/usr/local/libexec/pwb-purge-backups.py')
                sudoers = Path('/etc/sudoers.d/project-workbench-purge')
                write(sudoers, 'projectwb ALL=(root) NOPASSWD: /usr/bin/python3 /usr/local/libexec/pwb-purge-backups.py\n', 0o440)
                run('visudo', '-cf', sudoers)
            for unit in ('project-workbench.service', 'project-workbench-channels.service'):
                run('install', '-m', '644', candidate / 'deploy' / unit, Path('/etc/systemd/system') / unit)
            run('systemctl', 'daemon-reload')
            deploy(candidate)
        except BaseException:
            if (BASE / '.deploying').exists():
                recover()
            if pointer('current') != candidate and not (BASE / '.deploying').exists():
                remove(candidate, {p for p in (pointer('current'), pointer('previous')) if p})
            raise
    archive.unlink(missing_ok=True)
    Path(str(archive) + '.sha256').unlink(missing_ok=True)


def legacy():
    quiet()
    health()
    keep = {pointer('current'), pointer('previous')} - {None}
    units = ('yihaowei-workbench', 'deepseek-harness', 'workbench-wechat',
             'workbench-feishu', 'workbench-development')
    for unit in units:
        run('systemctl', 'disable', '--now', unit + '.service', check=False, capture=True)
        path = Path('/etc/systemd/system') / (unit + '.service')
        if path.exists():
            path.unlink()
        remove(Path('/etc/systemd/system') / (unit + '.service.d'), keep)
    previews = as_user('systemctl', '--user', 'list-unit-files', '--no-legend', capture=True).stdout
    for line in previews.splitlines():
        name = line.split()[0]
        if re.fullmatch(r'pwb-[a-z0-9-]+-preview\.service', name):
            as_user('systemctl', '--user', 'disable', '--now', name, check=False, capture=True)
            for root in (Path('/home/projectwb/.config/systemd/user'), Path('/etc/systemd/user')):
                (root / name).unlink(missing_ok=True)
    as_user('systemctl', '--user', 'daemon-reload')
    run('systemctl', 'daemon-reload')
    paths = [Path(p) for p in (
        '/opt/yihaowei-workbench', '/opt/deepseek-harness', '/opt/workbench-development',
        '/srv/deepseek-workspace', '/etc/yihaowei-workbench',
        '/var/lib/yihaowei-workbench', '/var/lib/deepseek-harness',
        '/var/lib/workbench-development', '/var/lib/workbench-execution',
        '/var/cache/yihaowei-workbench', '/var/cache/deepseek-harness', '/var/cache/yihaowei-v2-build',
        '/var/lib/project-workbench-preview-lifecycle')]
    paths += list(Path('/var/lib').glob('pwb-*-preview'))
    paths += list(Path('/var/backups').glob('yihaowei-workbench-*'))
    paths += list(CONFIG.parent.glob('*-preview.json'))
    paths += list(CONFIG.parent.glob('preview-*.json'))
    for path in paths:
        remove(path, keep)
    # This user's only external build container is an abandoned Image Builder
    # scratch container. Do not remove arbitrary external containers by wildcard.
    external = as_user('podman', 'ps', '-a', '--external', '--format', '{{.ID}} {{.Names}} {{.Status}}',
                       capture=True).stdout
    for line in external.splitlines():
        parts = line.split()
        if len(parts) == 3 and parts[1:] == ['debian-working-container', 'Storage']:
            as_user('buildah', 'rm', parts[0])
    cleanup()


def initialize_install():
    try:
        account = pwd.getpwnam(USER)
    except KeyError:
        run('useradd', '--create-home', '--shell', '/bin/bash', USER)
        account = pwd.getpwnam(USER)
    run('loginctl', 'enable-linger', USER)
    for directory in (BASE, DATA, CONFIG.parent, Path('/usr/local/libexec')):
        directory.mkdir(parents=True, exist_ok=True)
    if not CONFIG.is_file():
        raise RuntimeError('Create /etc/project-workbench/config.json before first installation')
    os.chown(DATA, account.pw_uid, account.pw_gid)
    os.chown(CONFIG.parent, account.pw_uid, account.pw_gid)
    os.chown(CONFIG, account.pw_uid, account.pw_gid)
    os.chmod(CONFIG, 0o600)
    if not (CONFIG.parent / 'service.env').exists():
        write(CONFIG.parent / 'service.env', f'XDG_RUNTIME_DIR=/run/user/{account.pw_uid}\n'
              f'DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/{account.pw_uid}/bus\n')
        os.chown(CONFIG.parent / 'service.env', account.pw_uid, account.pw_gid)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    sub.add_parser('deploy').add_argument('release')
    sub.add_parser('rollback')
    sub.add_parser('cleanup').add_argument('--legacy', action='store_true')
    p = sub.add_parser('install'); p.add_argument('archive'); p.add_argument('name')
    args = parser.parse_args()
    if os.geteuid() != 0:
        parser.error('Run through sudo as root')
    BASE.mkdir(exist_ok=True)
    (BASE / 'releases').mkdir(exist_ok=True)
    # An outer preparation shell can hold this same inherited descriptor.
    inherited = os.environ.get('PW_RELEASE_LOCK_FD')
    with (BASE / '.release.lock').open('a') as lock:
        fd = int(inherited) if inherited else lock.fileno()
        own = os.fstat(lock.fileno())
        if os.fstat(fd).st_ino != own.st_ino or os.fstat(fd).st_dev != own.st_dev:
            raise RuntimeError('Inherited descriptor does not refer to the release lock')
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if args.command == 'install':
            initialize_install()
        recover()
        if args.command == 'deploy':
            deploy(release(args.release))
        elif args.command == 'rollback':
            previous = pointer('previous')
            if previous is None:
                raise RuntimeError('No previous successful version')
            if not (previous / 'planner-protocol').exists():
                quiet()
                with sqlite3.connect(DATA / 'workbench.sqlite', timeout=30) as db:
                    db.execute("UPDATE schedules SET enabled=0,data=json_set(data,'$.enabled',json('false'),'$.version',coalesce(json_extract(data,'$.version'),0)+1,'$.pausedReason','回滚兼容保护，请核对规则后手动开启') WHERE enabled=1 AND (coalesce(json_extract(data,'$.onceAt'),0)>0 OR coalesce(json_extract(data,'$.sessionId'),'')<>'' OR coalesce(json_array_length(data,'$.changes'),0)>0 OR coalesce(json_array_length(data,'$.exceptions'),0)>0 OR coalesce(json_extract(data,'$.conversationOutput'),0)=1)")
            deploy(previous)
        elif args.command == 'install':
            install(args.archive, args.name)
        else:
            if args.legacy:
                legacy()
            else:
                cleanup()


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, RuntimeError, subprocess.CalledProcessError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
