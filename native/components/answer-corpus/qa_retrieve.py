#!/usr/bin/env python3
"""Project-local hybrid retrieval. Similarity retrieves evidence, never approves a fact."""
import argparse
import collections
import datetime as dt
import fcntl
import hashlib
import io
import json
import os
from pathlib import Path
import re
import sqlite3
import sys
import tempfile
import uuid
import zipfile

INDEX = '运营数据/全量语义索引.sqlite'
CONFIG = '运营配置/语义检索.json'
MODEL_REVISION = '75c43b069aac4d136ba6bc1122f995fedcfd2781'

def dump(x):
    return json.dumps(x, ensure_ascii=False, sort_keys=True)

def sha(x):
    return hashlib.sha256(x.encode() if isinstance(x, str) else x).hexdigest()

def inside(root, rel):
    p = (root / rel).resolve()
    if root != p and root not in p.parents:
        raise ValueError('Path outside current project')
    return p

PARTS = '运营数据/语义索引分片'

def index_path(root):
    legacy=inside(root,INDEX)
    if legacy.exists():return legacy
    manifest=inside(root,PARTS+'/manifest.json')
    target=Path(tempfile.gettempdir())/('hxc-index-'+sha(str(root))[:16]+'.sqlite')
    if not manifest.exists():return target
    with Path(str(target)+'.load.lock').open('a') as lock:
        fcntl.flock(lock,fcntl.LOCK_EX)
        return materialize_index(root,manifest,target)

def materialize_index(root,manifest,target):
    spec=json.loads(manifest.read_text())
    if target.exists():
        try:
            with sqlite3.connect(target) as db:
                if db.execute("select value from meta where key='fingerprint'").fetchone()[0]==spec['fingerprint']:return target
        except sqlite3.Error:pass
    staged=Path(str(target)+'.loading')
    with staged.open('wb') as out:
        for part in spec['parts']:
            payload=inside(root,PARTS+'/'+part['name']).read_bytes()
            if sha(payload)!=part['sha256']:raise ValueError('Index shard checksum differs')
            out.write(payload)
    os.replace(staged,target)
    return target

def publish_index(root,target,receipt):
    folder=inside(root,PARTS);folder.mkdir(parents=True,exist_ok=True);parts=[]
    with target.open('rb') as source:
        number=0
        while True:
            data=source.read(96*1024*1024)
            if not data:break
            name='index.'+str(number).zfill(3);tmp=folder/(name+'.new')
            tmp.write_bytes(data);os.replace(tmp,folder/name)
            parts.append({'name':name,'sha256':sha(data),'bytes':len(data)});number+=1
    spec={'schema_version':1,'fingerprint':receipt['fingerprint'],'parts':parts,'source_records':receipt['source_records'],'chunks':receipt['chunks']}
    tmp=folder/'manifest.json.new';tmp.write_text(dump(spec)+'\n');os.replace(tmp,folder/'manifest.json')
    keep={p['name'] for p in parts}
    for old in folder.glob('index.*'):
        if old.name not in keep:old.unlink()
    return spec


def config(root):
    return json.loads(inside(root, CONFIG).read_text())

def business_files(root):
    for p in sorted((root / '知识库').rglob('*')):
        if not p.is_file() or p.suffix not in {'.md', '.json', '.jsonl', '.txt'}:
            continue
        if p.name in {'答疑证据冲突.json','检索索引状态.json','问答库更新索引.md','可答范围与转真人.md'}:
            continue
        yield p

def fingerprint(root, cfg):
    # Content-based signature travels with a prebuilt index; mtimes and SQLite WAL layout do not.
    rows=[(str(p.relative_to(root)),sha(p.read_bytes())) for p in [inside(root,cfg['baseline_archive']),*business_files(root)]]
    state=[]
    # Published connector exports are immutable inputs; the index remains a
    # derived cache. Existing baselines without exports retain their old reader.
    if cfg.get('snapshot_records'):
        for key in ['snapshot_records','snapshot_links']:
            p=inside(root,cfg[key]);rows.append((str(p.relative_to(root)),sha(p.read_bytes())))
        return sha(dump({'schema':3,'files':rows,'model':cfg['model_sha256']}))
    cache=inside(root,cfg['state_database'])
    if cache.exists():
        with sqlite3.connect('file:'+str(cache)+'?mode=ro',uri=True) as db:
            state=[(rid,sha(dump(json.loads(raw)))) for rid,raw in db.execute('select id,data from records order by id')]
            links=list(db.execute('select customer_id,record_id from links order by customer_id,record_id'))
            state.append(('links',sha(dump(links))))
    return sha(dump({'schema':2,'files':rows,'cache':state,'model':cfg['model_sha256']}))


def tokens(text):
    runs = re.findall(r'[\u3400-\u9fff]+|[a-z0-9_]+', text.lower())
    out=[]
    for run in runs:
        if '\u3400' <= run[0] <= '\u9fff':
            out += [run[i:i+2] for i in range(len(run)-1)] if len(run)>1 else [run]
        else: out.append(run)
    return ' '.join(out)

def pieces(text, size=450, overlap=70):
    for start in range(0,len(text),size-overlap):
        value=text[start:start+size].strip()
        if value: yield start,value
        if start+size>=len(text): break

class Encoder:
    def __init__(self, cfg):
        extra=cfg.get('runtime_path')
        if extra and Path(extra).exists(): sys.path.insert(0,extra)
        import numpy as np
        import onnxruntime as ort
        from tokenizers import Tokenizer
        self.np=np
        model=Path(cfg['model_path'])
        if sha(model.read_bytes())!=cfg['model_sha256']:
            raise ValueError('Embedding model checksum differs')
        tok=Path(cfg['tokenizer_path'])
        if sha(tok.read_bytes())!=cfg['tokenizer_sha256']:
            raise ValueError('Tokenizer checksum differs')
        self.tokenizer=Tokenizer.from_file(str(tok))
        self.tokenizer.enable_truncation(max_length=512)
        self.tokenizer.enable_padding()
        opts=ort.SessionOptions();opts.intra_op_num_threads=2;opts.inter_op_num_threads=1
        self.session=ort.InferenceSession(str(model),sess_options=opts,providers=['CPUExecutionProvider'])
        self.names={x.name for x in self.session.get_inputs()}
    def encode(self,texts,query=False):
        if query: texts=['为这个句子生成表示以用于检索相关文章：'+x for x in texts]
        encoded=self.tokenizer.encode_batch(texts)
        inputs={'input_ids':self.np.array([x.ids for x in encoded],dtype='int64'),
                'attention_mask':self.np.array([x.attention_mask for x in encoded],dtype='int64'),
                'token_type_ids':self.np.array([x.type_ids for x in encoded],dtype='int64')}
        output=self.session.run(None,{k:v for k,v in inputs.items() if k in self.names})[0]
        if output.ndim==3: output=output[:,0,:]
        return (output/self.np.maximum(self.np.linalg.norm(output,axis=1,keepdims=True),1e-12)).astype('float32')

def add_chunk(db,kind,locator,body,meta):
    ident=kind[0].upper()+sha(locator+'\n'+body)[:23]
    db.execute('insert or ignore into chunks(id,kind,locator,body,meta,sha) values(?,?,?,?,?,?)',
               (ident,kind,locator,body,dump(meta),sha(body)))

def build(root, cfg=None, force=False):
    cfg=cfg or config(root);target=index_path(root);target.parent.mkdir(parents=True,exist_ok=True)
    with (target.parent/'语义索引.lock').open('a') as lock:
        fcntl.flock(lock,fcntl.LOCK_EX)
        fp=fingerprint(root,cfg)
        if target.exists() and not force:
            with sqlite3.connect(target) as db:
                row=db.execute("select value from meta where key='fingerprint'").fetchone()
                if row and row[0]==fp:return json.loads(db.execute("select value from meta where key='receipt'").fetchone()[0])
        reusable={}
        if target.exists():
            with sqlite3.connect(target) as previous:
                old=json.loads(previous.execute("select value from meta where key='receipt'").fetchone()[0])
                if old.get('model_sha256')==cfg['model_sha256']:
                    reusable={digest:vector for digest,vector in previous.execute('select sha,vector from chunks') if vector}
        tmp=Path(str(target)+'.building');tmp.unlink(missing_ok=True)
        db=sqlite3.connect(tmp)
        db.executescript('''create table meta(key text primary key,value text);
        create table records(id integer primary key,data text,sha text);
        create table links(customer_id integer,record_id integer,primary key(customer_id,record_id));
        create index links_record on links(record_id);
        create table chunks(rowid integer primary key,id text unique,kind text,locator text,body text,meta text,sha text,vector blob);
        create virtual table terms using fts5(content,tokenize='unicode61');''')
        archive=inside(root,cfg['baseline_archive'])
        with zipfile.ZipFile(archive) as z:
            with z.open('messages.jsonl') as f:
                for line in io.TextIOWrapper(f):
                    r=json.loads(line);db.execute('insert into records values(?,?,?)',(r['id'],dump(r),sha(dump(r))))
            with z.open('message_customer_links.jsonl') as f:
                for line in io.TextIOWrapper(f):
                    r=json.loads(line);db.execute('insert or ignore into links values(?,?)',(r['customer_id'],r['record_id']))
        cache=inside(root,cfg['state_database'])
        if cfg.get('snapshot_records'):
            with inside(root,cfg['snapshot_records']).open() as source:
                for line in source:
                    r=json.loads(line);db.execute('insert or replace into records values(?,?,?)',(r['id'],dump(r),sha(dump(r))))
            with inside(root,cfg['snapshot_links']).open() as source:
                for line in source:
                    r=json.loads(line);db.execute('insert or ignore into links values(?,?)',(r['customer_id'],r['record_id']))
        elif cache.exists():
            with sqlite3.connect('file:'+str(cache)+'?mode=ro',uri=True) as other:
                for rid,raw in other.execute('select id,data from records'):
                    r=json.loads(raw);db.execute('insert or replace into records values(?,?,?)',(rid,dump(r),sha(dump(r))))
                for cid,rid in other.execute('select customer_id,record_id from links'):
                    db.execute('insert or ignore into links values(?,?)',(cid,rid))
        grouped=collections.defaultdict(list)
        for rid,raw in db.execute('select id,data from records order by id'):
            r=json.loads(raw);body=r.get('content_text','').strip()
            if body: grouped[body].append(rid)
        for body,ids in grouped.items():
            for offset,part in pieces(body):
                add_chunk(db,'record',str(ids[0])+':'+str(offset),part,{'record_ids':ids,'offset':offset})
        file_manifest={}
        for p in business_files(root):
            raw=p.read_text();rel=str(p.relative_to(root));file_manifest[rel]=sha(raw)
            for offset,part in pieces(raw):
                add_chunk(db,'knowledge',rel+':'+str(offset),part,{'path':rel,'offset':offset,'file_sha256':sha(raw)})
        db.commit();encoder=Encoder(cfg);count=db.execute('select count(*) from chunks').fetchone()[0]
        embedded=0
        for start in range(0,count,24):
            rows=db.execute('select rowid,body,sha from chunks order by rowid limit 24 offset ?',(start,)).fetchall()
            missing=[row for row in rows if row[2] not in reusable]
            vectors=encoder.encode([row[1] for row in missing]) if missing else []
            new={row[0]:v.tobytes() for row,v in zip(missing,vectors)};embedded+=len(missing)
            for rowid,body,digest in rows:
                db.execute('update chunks set vector=? where rowid=?',(reusable[digest] if digest in reusable else new[rowid],rowid))
                db.execute('insert into terms(rowid,content) values(?,?)',(rowid,tokens(body)))
            if start%2400==0:print(dump({'indexing':start,'total':count,'new_embeddings':embedded}),file=sys.stderr,flush=True)
        receipt={'schema_version':2,'state':'complete','fingerprint':fp,'source_records':db.execute('select count(*) from records').fetchone()[0],
                 'text_records':sum(len(v) for v in grouped.values()),'unique_texts':len(grouped),'chunks':count,'knowledge_files':file_manifest,
                 'dimension':512,'new_embeddings':embedded,'model_sha256':cfg['model_sha256'],'model':'BAAI/bge-small-zh-v1.5','onnx_revision':MODEL_REVISION,
                 'built_at':dt.datetime.now(dt.timezone.utc).isoformat()}
        if fingerprint(root,cfg)!=fp:db.close();tmp.unlink();raise ValueError('Source changed while indexing; rebuild required')
        for k,v in [('fingerprint',fp),('receipt',dump(receipt))]:db.execute('insert into meta values(?,?)',(k,v))
        db.commit();db.close();os.replace(tmp,target)
        if not inside(root,INDEX).exists():publish_index(root,target,receipt)
        inside(root,'知识库/检索索引状态.json').write_text(dump(receipt)+'\n')
        return receipt

def current_record(root, cfg, rid, indexed):
    if cfg.get('snapshot_records'):return indexed
    # Incremental changes override the baseline. Missing/changed text invalidates stored citations.
    cache=inside(root,cfg['state_database'])
    if cache.exists():
        with sqlite3.connect('file:'+str(cache)+'?mode=ro',uri=True) as db:
            row=db.execute('select data from records where id=?',(rid,)).fetchone()
            if row:return json.loads(row[0])
    return indexed

def evidence(root, ident, db=None):
    own=db is None;db=db or sqlite3.connect(index_path(root));cfg=config(root)
    try:
        row=db.execute('select kind,locator,body,meta,sha from chunks where id=?',(ident,)).fetchone()
        if not row:raise ValueError('Unknown evidence ID')
        kind,locator,body,raw,expected=row;meta=json.loads(raw)
        if kind=='knowledge':
            p=inside(root,meta['path']);content=p.read_text()
            if sha(content)!=meta['file_sha256'] or body not in content:raise ValueError('Knowledge source changed')
            return {'id':ident,'kind':kind,'locator':locator,'text':body,'sha256':expected,'metadata':meta}
        blocked=json.loads(inside(root,'知识库/答疑证据冲突.json').read_text())
        if blocked.get('block_all'):raise ValueError('All evidence blocked')
        blocked_ids=set(map(str,blocked.get('blocked_record_ids',[])))
        candidates=[i for i in meta['record_ids'] if str(i) not in blocked_ids]
        if not candidates:raise ValueError('Source conflict')
        records=[]
        for rid in candidates[:3]:
            indexed=json.loads(db.execute('select data from records where id=?',(rid,)).fetchone()[0]);r=current_record(root,cfg,rid,indexed)
            if dump(r)!=dump(indexed) or body not in r.get('content_text',''):raise ValueError('Record changed')
            records.append(r)
        return {'id':ident,'kind':kind,'locator':locator,'text':body,'sha256':expected,'records':records,
                'duplicate_occurrences':len(meta['record_ids']),'not_independent_verification':True}
    finally:
        if own:db.close()

def search(root,request):
    cfg=config(root);receipt=build(root,cfg);encoder=Encoder(cfg);np=encoder.np
    queries=request.get('queries')
    if not isinstance(queries,list) or not 1<=len(queries)<=6 or not all(isinstance(q,str) and q.strip() for q in queries):raise ValueError('Use 1-6 semantic queries')
    db=sqlite3.connect(index_path(root));rows=db.execute('select rowid,id,vector from chunks order by rowid').fetchall()
    matrix=np.stack([np.frombuffer(row[2],dtype='float32') for row in rows]);scores=collections.defaultdict(float);dense={};lexical={}
    for query,vector in zip(queries,encoder.encode(queries,query=True)):
        sims=np.einsum('ij,j->i',matrix,vector,optimize=False)
        if not np.isfinite(sims).all():raise ValueError('Non-finite semantic similarity')
        order=np.argsort(sims)[-70:][::-1]
        for rank,i in enumerate(order):
            ident=rows[int(i)][1];scores[ident]+=1/(60+rank);dense[ident]=max(dense.get(ident,-1),float(sims[i]))
        terms=list(dict.fromkeys(tokens(query).split()))[:80]
        if terms:
            expr=' OR '.join('"'+term+'"' for term in terms)
            for rank,(ident,score) in enumerate(db.execute('select c.id,bm25(terms) from terms join chunks c on c.rowid=terms.rowid where terms match ? order by bm25(terms) limit 70',(expr,))):
                scores[ident]+=1/(60+rank);lexical[ident]=float(score)
    result=[]
    for ident in sorted(scores,key=scores.get,reverse=True):
        try:hit=evidence(root,ident,db)
        except (ValueError,OSError):continue
        if hit['kind']=='record':
            hit['records']=[{k:v for k,v in r.items() if k!='content_text'} for r in hit['records']]
        hit.update(dense_score=dense.get(ident),lexical_ranked=ident in lexical);result.append(hit)
        if len(result)>=min(max(int(request.get('limit',18)),1),40):break
    db.close();ident=uuid.uuid4().hex
    proof={'id':ident,'question':request.get('question',''),'queries':queries,'fingerprint':receipt['fingerprint'],
           'hits':{r['id']:r['sha256'] for r in result},'created_at':dt.datetime.now(dt.timezone.utc).isoformat()}
    p=inside(root,'运营数据/检索记录/'+ident+'.json');p.parent.mkdir(parents=True,exist_ok=True);p.write_text(dump(proof)+'\n')
    return {'action':'retrieved','search_receipt':ident,'corpus':{k:receipt[k] for k in ['source_records','chunks','model','built_at']},
            'instruction':'相似度不是依据充分。核对原始上下文、方向、时间和适用范围；客户提问、旧推送、个案状态不能当现行政策。无命中先换语义检索，确无答案再转接。', 'hits':result}

def read_context(root,ids,records=False):
    build(root);db=sqlite3.connect(index_path(root));result=[]
    if records:
        expanded=[]
        for rid in ids[:12]:
            expanded += [r[0] for r in db.execute("select distinct c.id from chunks c,json_each(c.meta,'$.record_ids') j where c.kind='record' and j.value=?",(int(rid),))]
        ids=list(dict.fromkeys(expanded))
        if not ids:raise ValueError('No parsed text for source record IDs')
    for ident in ids[:12]:
        hit=evidence(root,ident,db)
        if hit['kind']=='record':
            rid=hit['records'][0]['id'];contexts=[]
            cids=[r[0] for r in db.execute('select customer_id from links where record_id=? limit 2',(rid,))]
            for cid in cids:
                before=db.execute('select r.data from records r join links l on r.id=l.record_id where l.customer_id=? and r.id<=? order by r.id desc limit 6',(cid,rid)).fetchall()
                after=db.execute('select r.data from records r join links l on r.id=l.record_id where l.customer_id=? and r.id>? order by r.id limit 6',(cid,rid)).fetchall()
                contexts.append({'customer_ref':'C'+str(cid),'records':[json.loads(r[0]) for r in reversed(before)]+[json.loads(r[0]) for r in after]})
            hit['contexts']=contexts
        else:
            content=inside(root,hit['metadata']['path']).read_text();offset=hit['metadata']['offset']
            hit['knowledge_context']=content[max(0,offset-600):offset+1800]
        result.append(hit)
    db.close()
    for hit in result:
        for context in hit.get('contexts',[]):
            for r in context['records']:
                text=r.get('content_text','')
                if len(text)>1600:r['content_text']=text[:1600];r['excerpt_truncated']=True
        if hit['kind']=='record':hit['records']=[{k:v for k,v in r.items() if k!='content_text'} for r in hit['records']]
    ident=uuid.uuid4().hex
    fp=fingerprint(root,config(root))
    proof={'id':ident,'fingerprint':fp,'hits':{r['id']:r['sha256'] for r in result},'created_at':dt.datetime.now(dt.timezone.utc).isoformat()}
    p=inside(root,'运营数据/上下文读取/'+ident+'.json');p.parent.mkdir(parents=True,exist_ok=True);p.write_text(dump(proof)+'\n')
    return {'action':'evidence_context','context_receipt':ident,'sources':result}

def main():
    p=argparse.ArgumentParser();p.add_argument('--project-root',type=Path,required=True)
    modes=p.add_mutually_exclusive_group(required=True);modes.add_argument('--build',action='store_true');modes.add_argument('--request-file',type=Path);modes.add_argument('--read',nargs='+');modes.add_argument('--records',nargs='+')
    args=p.parse_args();root=args.project_root.resolve()
    try:
        if args.build:r=build(root)
        elif args.read:r=read_context(root,args.read)
        elif args.records:r=read_context(root,args.records,records=True)
        else:r=search(root,json.loads(inside(root,str(args.request_file)).read_text()))
    except Exception as e:
        print(dump({'action':'retrieval_error','error':str(e),'instruction':'检索故障不是知识缺失；检查配置重试，不编造事实。'}));raise SystemExit(1)
    print(dump(r))
if __name__=='__main__':main()
