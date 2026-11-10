import json
import sqlite3
import tempfile
import types
import unittest
from pathlib import Path
from unittest.mock import patch
from directory import profile_lookup

class ProfileLookupTest(unittest.TestCase):
    def test_exact_identity_and_fail_closed_source(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp); folder=root/'var/canonical-read-index';folder.mkdir(parents=True)
            path=folder/'key.sqlite';signature=['snapshot']
            module=types.SimpleNamespace(signature=lambda _:signature,cache_key=lambda _:'key',
                connect=lambda p:sqlite3.connect('file:'+str(p)+'?mode=ro&immutable=1',uri=True))
            with sqlite3.connect(path) as db:
                db.execute('CREATE TABLE metadata(key,value)');db.execute('CREATE TABLE users(workbench_ref,body)')
                db.executemany('INSERT INTO metadata VALUES(?,?)',[('__read_index',json.dumps({'source_signature':signature})),('snapshot_id','"v1"')])
                db.execute('INSERT INTO users VALUES(?,?)',('usr_one',json.dumps({'external_userid':'wm_one'})))
            doc={'state':'ready','id':'usr_one','snapshot':'v1','nickname':'Synthetic','groups':[]}
            with patch('directory.ORIGINAL',root),patch.dict('sys.modules',{'canonical_archive_index':module}),patch('directory.profile',return_value=doc) as read:
                self.assertEqual(profile_lookup(None,{'externalUserId':'wm_one'})['records'][0]['id'],'usr_one')
                self.assertEqual(read.call_args.args[1],{'id':'usr_one','snapshot_id':'v1'})
                read.reset_mock()
                self.assertEqual(profile_lookup(None,{'externalUserId':'unknown'})['records'],[]);read.assert_not_called()
                with sqlite3.connect(path) as db:db.execute('INSERT INTO users VALUES(?,?)',('usr_two',json.dumps({'external_userid':'wm_one'})))
                with self.assertRaises(ValueError):profile_lookup(None,{'externalUserId':'wm_one'})
                read.assert_not_called()
                with sqlite3.connect(path) as db:db.execute("DELETE FROM users WHERE workbench_ref='usr_two'")
                read.side_effect=lambda *_:(setattr(module,'signature',lambda _:['changed']) or doc)
                with self.assertRaises(ValueError):profile_lookup(None,{'externalUserId':'wm_one'})
                module.signature=lambda _:signature
                path.unlink()
                with self.assertRaises(sqlite3.OperationalError):profile_lookup(None,{'externalUserId':'wm_one'})
                self.assertFalse(path.exists())
                with self.assertRaises(ValueError):profile_lookup(None,{'externalUserId':"nickname OR 1=1"})
if __name__=='__main__':unittest.main()
