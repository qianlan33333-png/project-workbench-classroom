#!/usr/bin/env python3
"""Verify old workers cannot execute retained agent messages after rollback."""
import importlib.util,json,sqlite3,subprocess,tempfile,unittest
from pathlib import Path
from unittest.mock import patch
spec=importlib.util.spec_from_file_location('release',Path(__file__).with_name('release-common.py'))
release=importlib.util.module_from_spec(spec);spec.loader.exec_module(release)
class CommunicationRollback(unittest.TestCase):
 def test_freeze_and_refuse_legacy_requeue(self):
  with tempfile.TemporaryDirectory() as tmp:
   p=Path(tmp);old=p/'old';new=p/'new';old.mkdir();new.mkdir();(new/'communication-protocol').write_text('1\n')
   db=sqlite3.connect(p/'workbench.sqlite');db.executescript('CREATE TABLE communication_roots(id TEXT PRIMARY KEY,count INTEGER,cancelled INTEGER);CREATE TABLE runs(id TEXT PRIMARY KEY,state TEXT,data TEXT);CREATE TABLE inbox(source TEXT,state TEXT,data TEXT);');db.execute("INSERT INTO communication_roots VALUES('root',1,0)");db.execute("INSERT INTO runs VALUES('agent','queued',?)",(json.dumps({'source':'agent','agent':{'fromSessionId':'s'},'state':'queued'}),));db.execute("INSERT INTO runs VALUES('human','queued',?)",(json.dumps({'source':'web','state':'queued'}),));db.execute("INSERT INTO inbox VALUES('agent','context','{}')");db.commit()
   stopped=lambda *a,**k:subprocess.CompletedProcess(a,0,stdout='inactive')
   with patch.object(release,'DATA',p),patch.object(release,'run',stopped),patch.object(release,'as_user',stopped):
    release.prepare_communication_worker(old)
    self.assertEqual(db.execute("SELECT state FROM runs WHERE id='agent'").fetchone()[0],'communication_frozen');self.assertEqual(db.execute("SELECT state FROM runs WHERE id='human'").fetchone()[0],'queued')
    with self.assertRaises(sqlite3.IntegrityError):db.execute("UPDATE runs SET state='queued',data='{}' WHERE id='agent'")
    db.rollback();release.prepare_communication_worker(new)
    self.assertEqual(db.execute("SELECT count(*) FROM sqlite_master WHERE name LIKE 'rollback_guard_communication_%'").fetchone()[0],0)
    self.assertEqual(db.execute("SELECT state FROM inbox").fetchone()[0],'communication_frozen')
   db.close()
if __name__=='__main__':unittest.main()
