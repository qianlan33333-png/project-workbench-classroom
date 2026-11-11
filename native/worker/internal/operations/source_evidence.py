"""Call the original evidence builder with only its consumed source fields."""
import importlib.util
import sys
from datetime import datetime
from source_common import load, write_json

root, settings = load()
original = root / 'original'
sys.path.insert(0, str(original / 'ops'))
from routine_read_model import select_source

def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, original / 'ops' / filename)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value

builder = module('original_activation_evidence', 'build-activation-evidence.py')
warehouse = module('original_warehouse_source_facts', 'warehouse-source-facts.py')
facts = warehouse.fold_source_facts(warehouse.load_source_facts(root / 'raw/crm-production-source.json'))
if not isinstance(facts.get('activation_receipts'), list):
    raise ValueError('production_activation_receipts_missing')
surveys = [row for row in facts['surveys'] if row.get('customer_id') is not None]
receipts = facts['activation_receipts']
for row in surveys:
    row['title_snapshot'] = row.get('title')
    row['submitted_at'] = datetime.fromisoformat(str(row['submitted_at']).replace('Z', '+00:00')) if row.get('submitted_at') else None
for row in receipts:
    row['occurred_at'] = datetime.fromisoformat(str(row['occurred_at']).replace('Z', '+00:00')) if row.get('occurred_at') else None
source_as_of, source_snapshot_id = facts['source_as_of'], facts['source_snapshot_id']
del facts
users, memberships = [], []
fields = ('customer_id', '黄小璨用户ID', 'workbench_user_ref', 'operational_excluded', '_orderHistory')
canonical = select_source(original / 'var/private-data/canonical_users_live.json',
    {'users': lambda row: users.append({key: row[key] for key in fields if key in row})}, ('snapshot_id',))
hxc = select_source(root / 'raw/hxc-latest.json', {'memberships': memberships.append}, ('synced_at',))
data = builder.build(dict(canonical, users=users), dict(hxc, memberships=memberships), surveys, receipts,
    source_as_of=source_as_of, source_snapshot_id=source_snapshot_id)
write_json(original / 'public/data/activation_evidence_live.json', data)
print({'users': len(data['users']), 'surveys': len(surveys), 'receipts': len(receipts)})
