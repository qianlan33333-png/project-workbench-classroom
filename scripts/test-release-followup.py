#!/usr/bin/env python3
"""Rollback must fence durable followups before starting an older worker."""
import importlib.util
import json
from pathlib import Path
import re
import sqlite3
import subprocess
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('release_common', ROOT/'scripts/release-common.py')
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)

class Rollback(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory(); self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name).resolve()
        self.old, self.new = self.root/'releases/old', self.root/'releases/new'
        for p in (self.old, self.new):
            for name in ('bin/workbench','web/dist/index.html','runtime-image'):
                f=p/name; f.parent.mkdir(parents=True,exist_ok=True); f.touch()
        (self.new/'followup-protocol').write_text('1\n')
        self.stopped, self.removed, self.started = True, [], []
        for name, value in {'BASE':self.root,'DATA':self.root,'RESERVE':0,'prepare_gateway_worker':lambda p:None,
                            'set_runtime':lambda p:None,'health':lambda:None,'quiet':lambda:None,
                            'cleanup':lambda *a,**k:None,'runtime_image':lambda p:'image',
                            'run':self.system,'as_user':self.engine}.items():
            p=patch.object(release,name,value); p.start(); self.addCleanup(p.stop)
        self.db=sqlite3.connect(self.root/'workbench.sqlite',isolation_level=None); self.addCleanup(self.db.close)
        for file,name in [('store.go','schema'),('today_model.go','todaySchema'),('followups.go','followupSchema')]:
            raw=(ROOT/'internal/workbench'/file).read_text()
            self.db.executescript(re.search(r'const '+name+r' = `([^`]+)`',raw).group(1))
        for id,state,fid in [('busy','running','f'),('late','queued','f'),('ordinary','queued','')]:
            self.db.execute('INSERT INTO runs VALUES(?,?,?,?,?,1,1,?)',(id,'p','s',id,state,json.dumps({'followupId':fid,'state':state})))
            self.db.execute("INSERT INTO inbox VALUES(?,'automation','',?,'',?,'s',1,'{}')",(id,id,state))
        self.db.execute("INSERT INTO project_locks VALUES('p','busy',1),('other','manual',1)")
        self.db.execute("INSERT INTO schedules VALUES('timer','p',1,100,?)",(json.dumps({'followupId':'f','enabled':True}),))
        self.db.execute("INSERT INTO personal_decisions VALUES('d','followup:f:1',?)",(json.dumps({'followupId':'f','state':'answered','dispatched':False}),))
        self.db.execute("INSERT INTO personal_followups VALUES('f','s','k','scheduled',100,'timer',?)",(json.dumps({'state':'scheduled','version':2}),))
        for id,state in [('pending','pending'),('sending','sending'),('accepted','accepted')]:
            self.db.execute("INSERT INTO deliveries VALUES(?,?,'c',?,1,?)",(id,id,state,json.dumps({'runId':'busy','kind':'notification','receipt':id,'state':state})))
    def system(self,*args,**kwargs):
        if 'show' in args:return subprocess.CompletedProcess(args,0,stdout='inactive' if self.stopped else 'active')
        if args==('systemctl','start','project-workbench'):
            self.started.append(release.pointer('current'))
            self.assertEqual(self.db.execute("SELECT state FROM runs WHERE id='late'").fetchone()[0],'interrupted')
        return subprocess.CompletedProcess(args,0,stdout='')
    def engine(self,*args,**kwargs):
        if 'systemctl' in args:return self.system(*args,**kwargs)
        if 'ps' in args:return subprocess.CompletedProcess(args,0,stdout='one pw-busy\ntwo pw-ordinary\n')
        self.assertEqual(args,('podman','rm','--force','--ignore','one')); self.removed.append('one')
    def assert_fenced(self):
        self.assertEqual(self.removed,['one'])
        self.assertEqual(dict(self.db.execute('SELECT id,state FROM runs')),{'busy':'interrupted','late':'interrupted','ordinary':'queued'})
        self.assertEqual(self.db.execute('SELECT run_id FROM project_locks').fetchall(),[('manual',)])
        self.assertEqual(self.db.execute('SELECT enabled FROM schedules').fetchone()[0],0)
        self.assertEqual(self.db.execute("SELECT json_extract(data,'$.state') FROM personal_decisions").fetchone()[0],'stale')
        self.assertEqual(self.db.execute('SELECT state,next_at FROM personal_followups').fetchone(),('needs_review',0))
        self.assertEqual(dict(self.db.execute('SELECT id,state FROM deliveries')),{'pending':'cancelled','sending':'unknown','accepted':'accepted'})
        with self.assertRaisesRegex(sqlite3.IntegrityError,'followup worker version required'):
            self.db.execute("UPDATE runs SET state='queued',data='{}' WHERE id='late'")
        with self.assertRaisesRegex(sqlite3.IntegrityError,'followup worker version required'):
            self.db.execute("UPDATE schedules SET enabled=1,data='{}'")
        release.prepare_followup_worker(self.old)
        release.prepare_followup_worker(self.new)
        self.assertEqual(self.db.execute("SELECT count(*) FROM sqlite_master WHERE name LIKE 'rollback_guard_followup_%'").fetchone()[0],0)
        self.assertEqual(self.db.execute('SELECT enabled FROM schedules').fetchone()[0],0)
    def test_manual_rollback(self):
        release.point('current',self.new); release.point('previous',self.old)
        release.deploy(self.old); self.assert_fenced()
    def test_failed_deploy_recovery(self):
        release.point('current',self.new)
        (self.root/'.deploying').write_text(json.dumps({'old_current':str(self.old),'old_previous':None,'candidate':str(self.new)}))
        with patch.object(release,'remove',lambda *a:None):release.recover()
        self.assert_fenced()
    def test_worker_and_cleanup_failure_preserve_leases(self):
        self.stopped=False
        with self.assertRaisesRegex(RuntimeError,'not confirmed stopped'):release.start(self.old)
        self.stopped=True
        original=self.engine
        def fail(*a,**k):
            if 'rm' in a:raise subprocess.CalledProcessError(1,'podman rm')
            return original(*a,**k)
        with patch.object(release,'as_user',fail):
            with self.assertRaises(subprocess.CalledProcessError):release.start(self.old)
        self.assertEqual(self.started,[])
        self.assertEqual(self.db.execute('SELECT count(*) FROM project_locks').fetchone()[0],2)
        self.assertEqual(self.db.execute("SELECT state FROM runs WHERE id='late'").fetchone()[0],'queued')

if __name__=='__main__':unittest.main()
