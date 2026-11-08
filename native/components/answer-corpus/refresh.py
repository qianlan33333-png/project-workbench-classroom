"""MCP source operation: reuse the original collector, publish only a complete immutable view."""
import datetime as dt
import fcntl
import json
import os
from pathlib import Path
import re
import shutil
import sys
import urllib.request
sys.path.insert(0, '/service/scripts')
sys.path.insert(0, '/service/vendor')
import qa_sync


def native(config, path, body=None):
    request=urllib.request.Request(config['worker']+path, data=None if body is None else json.dumps(body).encode(), headers={'Authorization':'Bearer '+config['token'],'Content-Type':'application/json','X-Workbench-Kind':'owner','X-Workbench-Session':'corpus-maintainer'})
    with urllib.request.urlopen(request, timeout=30) as response: return json.load(response)


def run(key):
    if not re.fullmatch(r'[A-Za-z0-9_-]{1,160}',key):raise ValueError('invalid operation key')
    config=json.loads(Path('/service/native-access.json').read_text())
    destination=Path('/versions')/key
    destination.mkdir(mode=0o700,parents=True,exist_ok=True)
    with (Path('/versions')/'refresh.lock').open('a') as lock:
        fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
        manifest=destination/'published-source.json'
        if manifest.exists():
            end=qa_sync.stamp(json.loads((destination/'snapshot.json').read_text())['end'])
            db=qa_sync.Database()
            try:
                latest=max((qa_sync.stamp(row[0]) for row in db.execute('select last_end from checkpoints')),default=None)
                if latest and latest>end:raise RuntimeError('A newer collection has advanced; old operation cannot publish')
            finally:db.close()
            return json.loads(manifest.read_text())
        root=destination/'data'
        endfile=destination/'snapshot.json'
        if not endfile.exists():
            qa_sync.write_json(endfile,{'end':dt.datetime.now(dt.timezone.utc).isoformat()})
        end=qa_sync.stamp(json.loads(endfile.read_text())['end'])
        if not root.exists():
            resource=next(r for r in native(config,'/resources') if r['id']==config['resourceId'])
            current=resource['currentVersion']
            source=Path('/data') if current==config['baselineVersion'] else Path('/versions')/current/'data'
            if source!=Path('/data') and not (source.parent/'published-source.json').exists():raise ValueError('published source missing')
            staging=destination/'copying'
            if staging.exists():shutil.rmtree(staging)
            shutil.copytree(source,staging,copy_function=lambda src,dst: shutil.copy2(src,dst) if Path(src).suffix in {'.json','.jsonl','.sqlite'} else os.link(src,dst),ignore=shutil.ignore_patterns('__pycache__','*.lock','*.sqlite-wal','*.sqlite-shm'))
            staging.rename(root)
        settings=json.loads((root/'运营配置/CRM问答更新.json').read_text())
        settings['workers']=config.get('collectorWorkers',settings['workers'])
        if settings['workers'] not in range(1,9):raise ValueError('invalid collector concurrency')
        if settings['scope']!='private_only_no_customer_sends' or settings['schema_version']!=1:raise ValueError('unsupported collection scope')
        receipt=qa_sync.sync(root,settings,qa_sync.CRM(settings),snapshot=end)
        if receipt['state']!='complete' or receipt.get('retrieval_index_state')!='complete':raise RuntimeError('collection or indexing incomplete')
        index=json.loads((root/'知识库/检索索引状态.json').read_text())
        fields={k:index[k] for k in ['fingerprint','source_records','chunks','model','knowledge_files']}
        fields.update(description='原版答疑知识与增量私聊候选；仅按证据规则回答。',retrieval_tools=['qa_search','qa_read','qa_check'],updated_at=receipt['finished_at'],collection={'completed':receipt['completed_views'],'total':receipt['selected_views'],'newRecords':receipt['new_source_records']})
        result={'version':key,'watermark':index['fingerprint'],'records':[{'id':'corpus','fields':fields}]}
        qa_sync.write_json(manifest,result)
        return result

if __name__=='__main__':
    os.umask(0o077)
    print(json.dumps(run(sys.argv[1]),ensure_ascii=False))
