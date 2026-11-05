#!/usr/bin/env python3
import importlib.util,json,pathlib,sqlite3,tempfile,unittest
spec=importlib.util.spec_from_file_location('reader',pathlib.Path(__file__).with_name('read-source-receipts.py'));m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
class ReceiptReader(unittest.TestCase):
 def test_exact_scope_and_real_sent_proof(self):
  with tempfile.TemporaryDirectory() as tmp:
   root=pathlib.Path(tmp);(root/'var/operator-delivery-receipts').mkdir(parents=True);dbpath=root/'var/daru.sqlite';db=sqlite3.connect(dbpath);db.executescript('CREATE TABLE action_logs(action_id TEXT,result_json TEXT);CREATE TABLE copy_handoff_requests(run_id TEXT,digest TEXT,body TEXT,created_at TEXT);')
   run='copybatch-'+'a'*32;digest='b'*64;keys=['usr_a','usr_b','usr_c'];db.execute('INSERT INTO action_logs VALUES(?,?)',('copy_package_review:'+run,json.dumps({'decision':'approved','digest':digest,'selected_item_keys':keys})));db.execute('INSERT INTO copy_handoff_requests VALUES(?,?,?,?)',(run,digest,json.dumps({'plan_id':1,'selected_item_keys':keys}),'1'));db.commit()
   path=root/'var/operator-delivery-receipts/plan-1.json';value={'version':'verified-push-receipts.v1','read_only':True,'run_id':run,'plan_id':1,'items':{'usr_a':{'provider_delivery_status':1,'query_errcode':0,'provider_sent_at':'2026-11-05T08:00:00Z','msgid_sha256':'f'*64},'usr_b':{'local_execution_state':'provider_accepted','provider_delivery_status':0},'usr_c':{'local_execution_state':'final_failed'}}};path.write_text(json.dumps(value))
   before=dbpath.read_bytes();v=m.read(root,dbpath,{'run_id':run,'digest':digest});self.assertEqual(before,dbpath.read_bytes());self.assertEqual([v['items'][k]['state'] for k in keys],['sent','unknown','failed']);self.assertNotIn('recipient_id',v['items']['usr_a'])
   with self.assertRaises(ValueError):m.read(root,dbpath,{'run_id':run,'digest':'c'*64})
   value['items'].pop('usr_b');path.write_text(json.dumps(value))
   with self.assertRaises(ValueError):m.read(root,dbpath,{'run_id':run,'digest':digest})
   path.unlink();path.symlink_to(dbpath)
   with self.assertRaises(ValueError):m.read(root,dbpath,{'run_id':run,'digest':digest})
   with self.assertRaises(ValueError):m.read(root,dbpath,{'run_id':'../../etc/passwd','digest':digest})
if __name__=='__main__':unittest.main()
