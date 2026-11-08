import json, sys
from pathlib import Path
sys.path.insert(0, '/service/scripts')
import qa_retrieve, qa_gate

root = Path(sys.argv[2]).resolve()
if root != Path('/data') and not (Path('/versions') in root.parents and root.name == 'data' and (root.parent/'published-source.json').exists()):
    raise ValueError('unpublished source')
request = json.load(sys.stdin)
op = sys.argv[1]
if not isinstance(request, dict):
    raise ValueError('object required')
if op == 'search':
    result = qa_retrieve.search(root, request)
elif op == 'read':
    ids = request['ids']
    if not isinstance(ids, list) or len(ids) > 4 or not all(isinstance(x, str) and len(x) <= 160 for x in ids):
        raise ValueError('invalid source ids')
    result = qa_retrieve.read_context(root, ids, records=request.get('records', False))
elif op == 'check':
    value = request['request']
    data = value if isinstance(value, dict) else json.loads(value)
    if not isinstance(data, dict):
        raise ValueError('object required')
    result = qa_gate.decide(root, data)
    qa_gate.record_handoff(root, data, result)
else:
    raise ValueError('unknown operation')
print(json.dumps(result, ensure_ascii=False))
