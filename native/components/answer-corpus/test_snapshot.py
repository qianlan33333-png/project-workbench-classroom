"""Exercise the reused index and record reader on a frozen MySQL export.
Embedding computation alone is stubbed; original ranking/record parsing runs.
"""
import datetime as dt
import json
from pathlib import Path
import sys
import tempfile
import unittest
import zipfile
sys.path.insert(0,'/service/scripts')
import qa_retrieve as q

class SnapshotInput(unittest.TestCase):
 def test_new_increment_is_indexed_readable_by_id_and_changes_fingerprint(self):
  import numpy as np
  class Encoder:
   def __init__(self,config):self.np=np
   def encode(self,texts,query=False):return np.ones((len(texts),512),dtype='float32')/np.sqrt(512)
  original=q.Encoder;q.Encoder=Encoder
  try:
   with tempfile.TemporaryDirectory() as tmp:
    root=Path(tmp);(root/'知识库').mkdir();(root/'运营配置').mkdir();(root/'运营数据').mkdir()
    cfg={'baseline_archive':'baseline.zip','state_database':'legacy-does-not-exist.sqlite','model_sha256':'synthetic','snapshot_records':'运营数据/records.jsonl','snapshot_links':'运营数据/links.jsonl'}
    (root/q.CONFIG).write_text(json.dumps(cfg));(root/'知识库/答疑证据冲突.json').write_text(json.dumps({'block_all':False,'blocked_record_ids':[]}));(root/'知识库/说明.md').write_text('合成知识文档')
    base={'id':100,'content_text':'合成旧消息','direction':'staff_to_customer','occurred_at':'2026-11-01T00:00:00Z'}
    with zipfile.ZipFile(root/'baseline.zip','w') as archive:archive.writestr('messages.jsonl',json.dumps(base)+'\n');archive.writestr('message_customer_links.jsonl',json.dumps({'customer_id':1,'record_id':100})+'\n')
    record={**base,'id':999,'content_text':'新增快照验收 🌊'};(root/cfg['snapshot_records']).write_text(json.dumps(record)+'\n');(root/cfg['snapshot_links']).write_text(json.dumps({'customer_id':1,'record_id':999})+'\n')
    built=q.build(root);self.assertEqual(built['source_records'],2);before=built['fingerprint']
    result=q.read_context(root,['999'],records=True);texts=[r['content_text'] for source in result['sources'] for c in source.get('contexts',[]) for r in c['records']];self.assertIn(record['content_text'],texts);self.assertFalse((root/cfg['state_database']).exists())
    record['content_text']='同一编号的新快照文本';(root/cfg['snapshot_records']).write_text(json.dumps(record)+'\n');after=q.build(root);self.assertNotEqual(before,after['fingerprint']);result=q.read_context(root,['999'],records=True);self.assertTrue(any(record['content_text'] in json.dumps(s,ensure_ascii=False) for s in result['sources']))
  finally:q.Encoder=original

if __name__=='__main__':unittest.main()
