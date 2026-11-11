"""Original polished profile projection, through its existing read-only query."""
import sys,json,os
sys.path.insert(0,'/opt/daru-v3-warehouse-candidate/ops')
from crm_warehouse import env_file_values
os.environ['PROMPT_DATABASE_URL']=env_file_values('/etc/daru-ops-console.env')['PROMPT_DATABASE_URL']
from copy_profile_context import profile_index
from pathlib import Path
import psycopg
from salutation_policy import suggest_salutation
from salutation_projection import load
rows={ref:{'source_hash':value[0],'result':value[1]}for ref,value in profile_index().items()}
for ref,item in (load(Path('/opt/daru-v3-warehouse-candidate')).get('items')or{}).items():
    rows.setdefault(ref,{})['salutation']=item.get('decision')or{}
with psycopg.connect(os.environ['PROMPT_DATABASE_URL'],options='-c default_transaction_read_only=on')as db:
    with db.cursor()as cur:
        cur.execute("SELECT safe_user_ref,value FROM workbench_profile_overrides WHERE field='preferred_salutation'")
        for ref,value in cur:
            decision=suggest_salutation(manual=str(value or ''))
            if decision.get('state')=='operator_confirmed':rows.setdefault(ref,{})['salutation']=decision
json.dump(rows,sys.stdout,ensure_ascii=False,default=str)
