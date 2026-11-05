#!/usr/bin/env python3
"""Rollback must not turn retained expert read-only intents into writable runs."""
import importlib.util,json,sqlite3,subprocess,tempfile,unittest
from pathlib import Path
from unittest.mock import patch
spec=importlib.util.spec_from_file_location('release',Path(__file__).with_name('release-common.py'));release=importlib.util.module_from_spec(spec);spec.loader.exec_module(release)
class ComponentRollback(unittest.TestCase):
 def test_old_worker_freezes_and_cannot_requeue(self):
  with tempfile.TemporaryDirectory() as tmp:
   root=Path(tmp);old=root/'old';new=root/'new';old.mkdir();new.mkdir();(new/'component-protocol').write_text('1\n')
   db=sqlite3.connect(root/'workbench.sqlite');db.executescript('CREATE TABLE component_session_configs(data TEXT);CREATE TABLE runs(id TEXT,state TEXT,data TEXT);CREATE TABLE inbox(id TEXT,state TEXT,data TEXT);CREATE TABLE schedules(id TEXT,enabled INTEGER,data TEXT);')
   for table in ('runs','inbox'):
    for id in ('component','ordinary'):
     db.execute('INSERT INTO '+table+' VALUES(?,?,?)',(id,'queued',json.dumps({'state':'queued',**({'componentConfig':{'mode':'expert'}} if id=='component' else {})})))
   db.execute("INSERT INTO schedules VALUES('component',1,?)",(json.dumps({'component':{'mode':'expert'},'enabled':True}),));db.commit()
   stopped=lambda *a,**k:subprocess.CompletedProcess(a,0,stdout='inactive')
   with patch.object(release,'DATA',root),patch.object(release,'run',stopped),patch.object(release,'as_user',stopped):
    release.prepare_component_worker(old)
    for table in ('runs','inbox'):
     self.assertEqual(dict(db.execute('SELECT id,state FROM '+table)),{'component':'component_frozen','ordinary':'queued'})
     with self.assertRaises(sqlite3.IntegrityError):db.execute("UPDATE "+table+" SET state='queued',data='{}' WHERE id='component'")
     db.rollback()
    self.assertEqual(db.execute('SELECT enabled FROM schedules').fetchone()[0],0)
    with self.assertRaises(sqlite3.IntegrityError):db.execute("UPDATE schedules SET enabled=1,data='{}'")
    db.rollback();release.prepare_component_worker(new)
    self.assertEqual(db.execute("SELECT count(*) FROM sqlite_master WHERE name LIKE 'rollback_guard_component_%'").fetchone()[0],0)
    self.assertEqual(db.execute("SELECT state FROM runs WHERE id='component'").fetchone()[0],'component_frozen')
   db.close()
if __name__=='__main__':unittest.main()
