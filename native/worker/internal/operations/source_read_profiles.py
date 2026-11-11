import sys,json
from pathlib import Path
sys.path.insert(0,'/opt/daru-v3-warehouse-candidate/ops')
from crm_warehouse import env_file_values
import psycopg
cfg=env_file_values('/etc/daru-ops-console.env')
with psycopg.connect(cfg['PROMPT_DATABASE_URL'],options='-c default_transaction_read_only=on')as db:
 with db.cursor()as cur:
  cur.execute('BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY')
  cur.execute('SELECT safe_user_ref,result,updated_at FROM workbench_conversation_profiles_v1')
  first=True;sys.stdout.write('{')
  for ref,result,updated in cur:
   if not first:sys.stdout.write(',')
   first=False;sys.stdout.write(json.dumps(ref)+':'+json.dumps({'result':result,'updated_at':updated.isoformat()if updated else ''},ensure_ascii=False))
  sys.stdout.write('}')
