"""Drop temporary source copies only after the parent published every input."""
import json,re,shutil,sys
from pathlib import Path
from source_common import write_json
config=Path(sys.argv[1]);run=sys.argv[2]
if not config.is_absolute()or config.stat().st_mode&0o077 or not re.fullmatch(r'[a-zA-Z0-9_-]{1,96}',run):
    raise ValueError('private_source_configuration_required')
settings=json.loads(config.read_text());base=Path(settings['outputRoot']);root=base/run
if not base.is_absolute()or root.is_symlink():raise ValueError('invalid_collection_root')
if not (root/'consumed.json').exists():
    result=json.loads((root/'native-result.json').read_text())
    write_json(root/'consumed.json',{'report':result['report'],'files':result['files'],'published_in_mysql':True})
for name in ('original','raw'):
    path=root/name
    if path.exists():shutil.rmtree(path)
for name in ('profiles','history','strategies','methods'):
    (root/(name+'.jsonl')).unlink(missing_ok=True)
(root/'native-result.json').unlink(missing_ok=True)
print('{}')
