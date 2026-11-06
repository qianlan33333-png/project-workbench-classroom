#!/usr/bin/env python3
"""Downgrading cannot turn workflow continuations into ordinary tool or model work."""
import importlib.util
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('release', Path(__file__).with_name('release-common.py'))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class WorkflowRollback(unittest.TestCase):
    def test_freeze_and_resume_at_the_original_boundary(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            old, new = root / 'old', root / 'new'
            old.mkdir()
            new.mkdir()
            (new / 'workflow-protocol').write_text('1\n')
            db = sqlite3.connect(root / 'workbench.sqlite')
            db.executescript("""
                CREATE TABLE runs(id TEXT,state TEXT,data TEXT);
                CREATE TABLE inbox(id TEXT,state TEXT,data TEXT);
                CREATE TABLE schedules(id TEXT,enabled INTEGER,data TEXT);
                CREATE TABLE component_plugins(id TEXT,data TEXT);
                CREATE TABLE component_calls(id TEXT,plugin_id TEXT,state TEXT,next_poll INTEGER,data TEXT);
                CREATE TABLE workflow_runs(active_key TEXT);
                INSERT INTO workflow_runs VALUES('project:sync_contexts');
            """)
            db.execute("INSERT INTO runs VALUES('summary','queued',?)", (json.dumps({'workflowId': 'work'}),))
            db.execute("INSERT INTO runs VALUES('chat','queued','{}')")
            db.execute("INSERT INTO inbox VALUES('summary','queued',?)", (json.dumps({'runId': 'summary'}),))
            db.execute("INSERT INTO component_plugins VALUES('plugin',?)", (json.dumps({'component': {'key': 'context.operations'}}),))
            db.execute("INSERT INTO schedules VALUES('sync',1,?)", (json.dumps({'component': {'mode': 'program', 'pluginId': 'plugin', 'action': 'sync_contexts'}}),))
            original = {'state': 'ready', 'workflowId': 'work', 'binding': 'original'}
            db.execute("INSERT INTO component_calls VALUES('call','plugin','ready',123,?)", (json.dumps(original),))
            db.commit()
            with patch.object(release, 'DATA', root):
                release.prepare_workflow_worker(old)
                release.prepare_workflow_worker(old)
                self.assertEqual(dict(db.execute("SELECT id,state FROM runs")), {'summary': 'workflow_frozen', 'chat': 'queued'})
                self.assertEqual(db.execute("SELECT state FROM inbox").fetchone()[0], 'workflow_frozen')
                self.assertEqual(db.execute("SELECT state,json_extract(data,'$.binding') FROM component_calls").fetchone(), ('workflow_frozen', ''))
                self.assertEqual(db.execute("SELECT enabled FROM schedules").fetchone()[0], 0)
                with self.assertRaises(sqlite3.IntegrityError):
                    db.execute("UPDATE schedules SET enabled=1")
                db.rollback()
                with self.assertRaises(sqlite3.IntegrityError):
                    db.execute("INSERT INTO component_calls VALUES('duplicate','plugin','ready',0,?)", (json.dumps({'projectId': 'project', 'action': 'sync_contexts'}),))
                db.rollback()
                release.prepare_workflow_worker(new)
                self.assertEqual(db.execute("SELECT state FROM inbox").fetchone()[0], 'queued')
                self.assertEqual(db.execute("SELECT state,next_poll FROM component_calls").fetchone(), ('ready', 0))
                self.assertEqual(json.loads(db.execute("SELECT data FROM component_calls").fetchone()[0]), original)
                self.assertEqual(db.execute("SELECT enabled FROM schedules").fetchone()[0], 1)
                self.assertEqual(db.execute("SELECT count(*) FROM sqlite_master WHERE name LIKE 'rollback_guard_workflow_%'").fetchone()[0], 0)
            db.close()


if __name__ == '__main__':
    unittest.main()
