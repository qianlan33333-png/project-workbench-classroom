"""Real isolated MySQL, fake read-only CRM: recovery must reuse checkpoints."""
import datetime as dt
import importlib.util
import io
import json
import os
from pathlib import Path
import sys
import tempfile
import threading
import unittest
import zipfile

# The private fixture supplies the original audited gate/retrieve modules.
sys.path.insert(0,os.environ.get('QA_ORIGINAL_SCRIPTS','/service/scripts'))
import qa_sync

class Recovery(unittest.TestCase):
 def test_partial_resume_skips_completed_views_and_old_operation_cannot_regress(self):
  # Never run against a production collector database.
  config=json.loads(Path(os.environ['QA_MYSQL_CONFIG']).read_text())
  self.assertIn('acceptance',config['database'])
  db=qa_sync.Database()
  for table in ['catalog','checkpoints','seen','records','links','variants','meta']:db.execute('DROP TABLE IF EXISTS '+table)
  db.connection.commit();db.close()
  with tempfile.TemporaryDirectory() as tmp:
   root=Path(tmp);end=dt.datetime(2026,11,8,10,tzinfo=dt.timezone.utc)
   settings={'staff_user_id':10,'staff_display_name':'黄小璨','baseline_archive':'baseline.zip','workers':1,'overlap_hours':72,'new_customer_lookback_days':30}
   with zipfile.ZipFile(root/'baseline.zip','w') as archive:
    archive.writestr('manifest.json',json.dumps({'source':{'staff_user_id':10},'statistics':{'recent_end':(end-dt.timedelta(days=1)).isoformat()},'coverage':{'customer_directory_objects':3}}));archive.writestr('record_digests.jsonl','');archive.writestr('read_checkpoints.jsonl',''.join(json.dumps({'customer_id':i,'complete':True,'as_of':(end-dt.timedelta(days=1)).isoformat()})+'\n' for i in [1,2,3]))
   class CRM:
    def __init__(self,fail=False):self.calls=[];self.fail=fail;self.auth_failed=threading.Event()
    def invoke(self,operation,params=None,query=None):
     if operation=='admin.listCustomers':return{'items':[{'customer_id':i} for i in [1,2,3]]}
     cid=params['customer_id'];self.calls.append(cid)
     if self.fail and cid==2:self.auth_failed.set();raise PermissionError('synthetic revoked credential')
     return{'customer_id':cid,'items':[{'id':cid*100,'chat_type':'private','staff_names':['黄小璨'],'occurred_at':(end-dt.timedelta(hours=1)).isoformat(),'content_text':'synthetic','direction':'customer_to_staff'}]}
   original=qa_sync.materialize
   qa_sync.materialize=lambda root,db,config,snapshot,receipt:receipt.update(retrieval_index_state='complete')
   try:
    first=CRM(True);r=qa_sync.sync(root,settings,first,snapshot=end);self.assertNotEqual(r['state'],'complete');self.assertEqual(first.calls,[1,2])
    second=CRM();r=qa_sync.sync(root,settings,second,snapshot=end);self.assertEqual(r['state'],'complete');self.assertEqual(second.calls,[2,3]);self.assertEqual(r['completed_views'],3);self.assertEqual(r['resumed_views'],1);self.assertEqual(r['new_source_records'],3)
    third=CRM();r=qa_sync.sync(root,settings,third,snapshot=end);self.assertEqual(third.calls,[]);self.assertEqual(r['new_source_records'],3)
    with self.assertRaises(RuntimeError):qa_sync.sync(root,settings,CRM(),snapshot=end-dt.timedelta(minutes=1))
    # A rolled-back per-view statistics write must not leak into the next view.
    db=qa_sync.Database()
    with db:
     for table in ['checkpoints','seen','records','links','variants','meta']:db.execute('delete from '+table)
    db.close()
    original_database=qa_sync.Database;injected={'done':False}
    class FailingDatabase(original_database):
     def execute(self,sql,args=()):
      if sql.startswith('replace into meta') and args[0].startswith('refresh-') and not injected['done']:
       injected['done']=True;raise RuntimeError('synthetic stats transaction rollback')
      return super().execute(sql,args)
    qa_sync.Database=FailingDatabase
    failed=qa_sync.sync(root,settings,CRM(),snapshot=end)
    self.assertNotEqual(failed['state'],'complete')
    qa_sync.Database=original_database
    fixed=qa_sync.sync(root,settings,CRM(),snapshot=end)
    self.assertEqual(fixed['new_source_records'],3);self.assertEqual(fixed['pages'],3)
   finally:qa_sync.materialize=original

if __name__=='__main__':unittest.main()
