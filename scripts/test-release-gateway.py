#!/usr/bin/env python3
"""Exercise both production rollback paths against the actual gateway store schema."""
import importlib.util
import json
import os
from pathlib import Path
import re
import sqlite3
import subprocess
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('release_common', ROOT / 'scripts/release-common.py')
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class Rollback(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name).resolve()
        self.legacy = self.root / 'releases/old'
        self.capable = self.root / 'releases/new'
        for path in (self.legacy, self.capable):
            for name in ('bin/workbench', 'web/dist/index.html', 'runtime-image'):
                file = path / name
                file.parent.mkdir(exist_ok=True, parents=True)
                file.touch()
        (self.capable / 'gateway-protocol-v1').write_text('1\n')
        self.patch('BASE', self.root)
        self.patch('DATA', self.root)
        self.patch('runtime_image', lambda path: 'image')
        self.patch('cleanup', lambda *args, **kwargs: None)
        self.patch('set_runtime', lambda path: None)
        self.patch('health', lambda: None)
        self.patch('quiet', lambda: None)  # Callback already arrived after a quiet check.
        self.patch('RESERVE', 0)
        self.stopped = True
        self.removed = []
        self.started = []
        self.patch('run', self.system)
        self.patch('as_user', self.service_user)
        env = patch.dict(os.environ)
        env.start()
        self.addCleanup(env.stop)
        for key in ('PW_EXPECTED_PREVIOUS', 'PW_ACCEPTANCE_SCRIPT', 'PW_PREVIEW_UNIT'):
            os.environ.pop(key, None)
        self.db = sqlite3.connect(self.root / 'workbench.sqlite', isolation_level=None)
        self.addCleanup(self.db.close)
        for file, schema in (('store.go', 'schema'), ('gateway.go', 'gatewaySchema'), ('gateway_crm.go', 'crmSchema')):
            source = (ROOT / 'internal/workbench' / file).read_text()
            self.db.executescript(re.search(r'const ' + schema + r' = `([^`]+)`', source).group(1))
        for session, mode in (('customer', 'auto'), ('already-paused', 'human')):
            self.db.execute('INSERT INTO gateway_sessions VALUES(?,?,?,?)',
                            (session, 'binding', session, json.dumps({'mode': mode, 'version': 5})))
        for id, source, session, state in (('ordinary','web','internal','queued'),
                                            ('busy','wecom-gateway','customer','running'),
                                            ('late','wecom-gateway','customer','queued')):
            self.db.execute('INSERT INTO runs VALUES(?,?,?,?,?,1,1,?)',
                            (id,id,session,id,state,json.dumps({'source':source,'state':state})))
            self.db.execute("INSERT INTO inbox VALUES(?,?,'gateway',?,'',?,?,1,?)",
                            (id,source,id,state,session,'{"gateway":{},"incoming":{"text":"saved"}}'))
        self.db.execute("INSERT INTO project_locks VALUES('busy','busy',1),('other','manual-lease',1)")
        self.db.execute("INSERT INTO inbox VALUES('extra','wecom-gateway','gateway','extra','','gateway_pending','customer',1,'{}')")
        for id, state, attempts in (('outgoing','pending',0), ('already-attempted','pending',1),
                                    ('submitting','sending',1), ('has-receipt','accepted',1)):
            self.db.execute("INSERT INTO deliveries VALUES(?,?,'gateway',?,1,?)",
                            (id, id, state, json.dumps({'kind':'gateway.answer','state':state,'receipt':id,
                                                       'gateway':{'attempts':attempts}})))

    def patch(self, key, value):
        p = patch.object(release, key, value)
        p.start()
        self.addCleanup(p.stop)

    def system(self, *args, **kwargs):
        if 'show' in args:
            return subprocess.CompletedProcess(args, 0, stdout='inactive' if self.stopped else 'active')
        if args == ('systemctl', 'start', 'project-workbench'):
            self.started.append(release.pointer('current'))
            self.assertFalse(self.db.execute("SELECT 1 FROM runs WHERE id='late' AND state='queued'").fetchone())
        return subprocess.CompletedProcess(args, 0, stdout='')

    def service_user(self, *args, **kwargs):
        if 'systemctl' in args:
            return self.system(*args, **kwargs)
        if 'ps' in args:
            self.assertTrue(any(arg.startswith('label=project-workbench-root=') for arg in args))
            return subprocess.CompletedProcess(args, 0, stdout='one pw-busy\ntwo pw-ordinary\n')
        self.assertEqual(args, ('podman', 'rm', '--force', '--ignore', 'one'))
        self.removed.append(args[-1])

    def assert_fenced(self):
        self.assertEqual(self.started, [self.legacy])
        self.assertEqual(self.removed, ['one'])
        self.assertEqual(dict(self.db.execute('SELECT id,state FROM runs')),
                         {'busy':'interrupted','late':'interrupted','ordinary':'queued'})
        self.assertEqual(self.db.execute('SELECT run_id FROM project_locks').fetchall(), [('manual-lease',)])
        self.assertEqual(dict(self.db.execute('SELECT id,state FROM deliveries')),
                         {'outgoing':'cancelled','already-attempted':'unknown','submitting':'unknown','has-receipt':'accepted'})
        self.assertEqual(self.db.execute("SELECT count(*) FROM deliveries WHERE json_extract(data,'$.receipt')=id AND json_extract(data,'$.gateway.attempts') IS NOT NULL").fetchone()[0], 4)
        self.assertEqual(self.db.execute("SELECT count(*) FROM gateway_history_index WHERE session_id='customer'").fetchone()[0], 3)
        self.assertEqual(self.db.execute("SELECT count(*) FROM inbox WHERE source='wecom-gateway' AND state='handled'").fetchone()[0], 3)
        self.assertEqual(dict(self.db.execute("SELECT id,json_extract(data,'$.version') FROM gateway_sessions")), {'customer':6,'already-paused':5})
        with self.assertRaisesRegex(sqlite3.IntegrityError, 'gateway worker version required'):
            self.db.execute("UPDATE runs SET state='queued' WHERE id='late'")
        with self.assertRaisesRegex(sqlite3.IntegrityError, 'gateway worker version required'):
            self.db.execute("INSERT INTO runs VALUES('web-compose','p','customer','new','queued',1,1,'{}')")
        release.prepare_gateway_worker(self.legacy)
        self.assertEqual(self.db.execute("SELECT json_extract(data,'$.version') FROM gateway_sessions WHERE id='customer'").fetchone()[0], 6)
        release.prepare_gateway_worker(self.capable)
        self.assertEqual(self.db.execute("SELECT count(*) FROM sqlite_master WHERE name LIKE 'rollback_guard_gateway_%'").fetchone()[0], 0)
        self.assertEqual(self.db.execute("SELECT json_extract(data,'$.mode') FROM gateway_sessions WHERE id='customer'").fetchone()[0], 'human')

    def test_manual_rollback_deploy(self):
        release.point('current', self.capable)
        release.point('previous', self.legacy)
        release.deploy(self.legacy)
        self.assert_fenced()

    def test_failed_acceptance_and_crash_recovery(self):
        release.point('current', self.capable)
        (self.root / '.deploying').write_text(json.dumps({'old_current':str(self.legacy),
                                                          'old_previous':None,'candidate':str(self.capable)}))
        self.patch('remove', lambda *args: None)
        release.recover()
        self.assert_fenced()

    def test_active_worker_or_failed_container_removal_cannot_start_old_worker(self):
        self.stopped = False
        with self.assertRaisesRegex(RuntimeError, 'not confirmed stopped'):
            release.start(self.legacy)
        self.stopped = True
        with patch.object(release, 'as_user', side_effect=lambda *a, **k: (
                self.service_user(*a, **k) if 'rm' not in a else self.fail_removal())):
            with self.assertRaises(subprocess.CalledProcessError):
                release.start(self.legacy)
        self.assertEqual(self.started, [])
        self.assertIsNone(release.pointer('current'))
        self.assertEqual(self.db.execute("SELECT state FROM runs WHERE id='late'").fetchone()[0], 'queued')
        self.assertEqual(self.db.execute("SELECT count(*) FROM project_locks").fetchone()[0], 2)

    @staticmethod
    def fail_removal():
        raise subprocess.CalledProcessError(1, 'podman rm')


if __name__ == '__main__':
    unittest.main()
