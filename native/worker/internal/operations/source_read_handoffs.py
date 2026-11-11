"""Read current original copy-review takeovers; no schema initialization."""
import json,sys,sqlite3
from pathlib import Path
root=Path('/opt/daru-v3-warehouse-candidate')
sys.path.insert(0,str(root/'ops'))
from agent_user_scheduler import safe_ref
db=sqlite3.connect((root/'var/daru.sqlite').resolve().as_uri()+'?mode=ro',uri=True)
db.row_factory=sqlite3.Row;db.execute('PRAGMA query_only=ON');db.execute('BEGIN')
# Same latest-per-original-batch/candidate query and key as the current
# application.tasks.current_copy_handoffs; retain its original action_id too.
rows=db.execute("SELECT action_id,canonical_user_id,todo_type,status,created_at,result_json FROM action_logs WHERE action_type='copy_review' ORDER BY created_at DESC,action_id DESC").fetchall()
latest={}
for row in rows:
    body=json.loads(row['result_json']or'{}')
    key=(row['canonical_user_id'],body.get('batch_id')or'',body.get('candidate_id')or row['todo_type'])
    latest.setdefault(key,row)
holds=[{'workbench_user_ref':safe_ref(row['canonical_user_id']),'source_ref':row['action_id'],
        'source_at':row['created_at'],'handoff_context':{'handoff_scope':'global'},
        'status':'manual_handoff','product_service_key':'manual'}
       for row in latest.values()if row['status']=='manual_handoff']
json.dump({'read_only':True,'complete':True,'handoffs':holds},sys.stdout,ensure_ascii=False)
db.close()
