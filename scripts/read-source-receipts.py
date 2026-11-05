#!/usr/bin/env python3
"""Forced-command, read-only receipt projection. No network, writes, imports or arbitrary paths.
Install outside the original app and restrict its SSH key to this command.
"""
import argparse,json,pathlib,re,sqlite3,sys

def read(root,db_path,q):
 run=q.get('run_id');digest=q.get('digest')
 if not isinstance(run,str) or not re.fullmatch(r'copybatch-[a-f0-9]{32}',run) or not isinstance(digest,str) or not re.fullmatch(r'[a-f0-9]{64}',digest):raise ValueError('invalid_binding')
 db=sqlite3.connect(db_path.resolve().as_uri()+'?mode=ro',uri=True,timeout=2)
 try:
  db.execute('PRAGMA query_only=ON');db.execute('BEGIN')
  r=db.execute('SELECT result_json FROM action_logs WHERE action_id=?',('copy_package_review:'+run,)).fetchone()
  review=json.loads(r[0]) if r else {};keys=review.get('selected_item_keys') or []
  if review.get('decision')!='approved' or review.get('digest')!=digest or not 1<=len(keys)<=2000 or len(set(keys))!=len(keys):raise ValueError('review_binding_mismatch')
  r=db.execute('SELECT body FROM copy_handoff_requests WHERE run_id=? AND digest=? ORDER BY created_at DESC LIMIT 1',(run,digest)).fetchone();job=json.loads(r[0]) if r else {}
  if set(job.get('selected_item_keys') or [])!=set(keys) or len(job.get('selected_item_keys') or [])!=len(keys):raise ValueError('handoff_binding_mismatch')
  plan=job.get('plan_id')
  if type(plan) is not int or plan<=0:return {'version':'workbench-item-receipts.v1','run_id':run,'digest':digest,'state':'pending','items':{}}
  path=root/'var/operator-delivery-receipts'/f'plan-{plan}.json'
  if not path.exists():return {'version':'workbench-item-receipts.v1','run_id':run,'digest':digest,'state':'pending','items':{}}
  if path.is_symlink() or path.stat().st_size>2000000:raise ValueError('receipt_file_invalid')
  v=json.loads(path.read_text())
  if v.get('version')!='verified-push-receipts.v1' or v.get('read_only') is not True or v.get('run_id')!=run or v.get('plan_id')!=plan or set(v.get('items') or {})!=set(keys):raise ValueError('receipt_scope_mismatch')
  items={}
  for key,value in v['items'].items():
   if not isinstance(value,dict):raise ValueError('receipt_item_invalid')
   sent=value.get('provider_delivery_status')==1 and value.get('query_errcode')==0 and bool(value.get('provider_sent_at')) and bool(value.get('msgid_sha256'))
   failed=value.get('local_execution_state')=='final_failed' and not value.get('provider_sent_at')
   items[key]={'state':'sent' if sent else 'failed' if failed else 'unknown','provider_sent_at':value.get('provider_sent_at'),'msgid_sha256':value.get('msgid_sha256'),'query_errcode':value.get('query_errcode'),'failure_reason':str(value.get('failure_reason') or '')[:1000]}
  return {'version':'workbench-item-receipts.v1','run_id':run,'digest':digest,'plan_id':plan,'checked_at':v.get('checked_at'),'state':'verified','items':items}
 finally:db.close()

def main():
 p=argparse.ArgumentParser();p.add_argument('--root',required=True);p.add_argument('--database');a=p.parse_args();root=pathlib.Path(a.root)
 try:
  raw=sys.stdin.buffer.read(2049)
  if len(raw)>2048:raise ValueError('request_too_large')
  q=json.loads(raw);value=read(root,pathlib.Path(a.database) if a.database else root/'var/daru.sqlite',q)
 except Exception:print(json.dumps({'error':'receipt_not_verified'}));return 1
 print(json.dumps(value,separators=(',',':')));return 0
if __name__=='__main__':raise SystemExit(main())
