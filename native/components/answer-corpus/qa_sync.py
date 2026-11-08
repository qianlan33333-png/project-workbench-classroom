#!/usr/bin/env python3
"""Read scoped CRM private chats into a resumable project Q&A candidate library."""
import argparse
import collections
import concurrent.futures
import datetime as dt
import fcntl
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import pymysql
import subprocess
import threading
import time
import sys
import zipfile

from qa_gate import canonical, stamp

ALLOWED = {'admin.listCustomers', 'admin.listCustomerMessageArchive'}
QUESTION = re.compile(r'[?？吗呢]|请问|怎么|如何|为什么|是否|有没有|多少|哪里|什么时候|申请|退款|发票|打不开|没收到|回放|咨询')
CAMPAIGN = re.compile(r'这是一条老黄亲自发出的信息|很认真的讲.{0,6}这是一条|直播已经开始|欢迎报名.{0,10}公开课|问卷提醒就到今天|老黄这几天的公开课已经讲完')


def path_in(root, name):
    root = root.resolve()
    path = (root / name).resolve()
    if root not in path.parents:
        raise ValueError('All state and knowledge files must remain inside this project')
    return path


def write_text(path, text):
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name + '.tmp')
    temporary.write_text(text)
    os.chmod(temporary, 0o600)
    temporary.replace(path)


def write_json(path, value):
    write_text(path, json.dumps(value, ensure_ascii=False, indent=2) + '\n')


def read_lines(path):
    if not path.exists():
        return []
    return [json.loads(line) for line in path.read_text().splitlines() if line.strip()]


class CRM:
    def __init__(self, config):
        self.config = config
        self.command = shutil.which('aicrm-cli')
        if not self.command:
            raise RuntimeError('Official aicrm-cli is not available on PATH')
        self.lock = threading.Lock()
        self.last = 0
        self.auth_failed = threading.Event()

    def invoke(self, operation, params=None, query=None):
        if operation not in ALLOWED:
            raise ValueError('Business operation is not in the read-only allowlist')
        if self.auth_failed.is_set():
            raise PermissionError('CRM authorization is unavailable')
        args = [self.command, '--timeout', str(self.config['cli_timeout_seconds']) + 's', 'invoke', operation]
        for key, value in (params or {}).items():
            args += ['--param', str(key) + '=' + str(value)]
        for key, value in (query or {}).items():
            if value is not None:
                args += ['--query', str(key) + '=' + str(value)]
        for attempt in range(2):
            with self.lock:
                time.sleep(max(0, self.config['call_interval_seconds'] - (time.monotonic() - self.last)))
                self.last = time.monotonic()
            if self.auth_failed.is_set():
                raise PermissionError('CRM authorization is unavailable')
            env = os.environ.copy()
            env.update(GOMAXPROCS='1', GOGC='500')
            result = subprocess.run(args, capture_output=True, text=True, env=env,
                                    timeout=self.config['cli_timeout_seconds'] + 10)
            try:
                value = json.loads(result.stdout)
            except ValueError:
                value = {}
            status = value.get('http_status') or value.get('error', {}).get('http_status')
            if result.returncode == 0 and status == 200:
                return value['data']
            if status in [401, 403]:
                self.auth_failed.set()
                raise PermissionError('CRM authorization failed: HTTP ' + str(status))
            if attempt == 0 and (status in [429, 500, 502, 503, 504] or not status):
                time.sleep(2)
                continue
            # Do not include transport bodies or credentials in project logs.
            raise RuntimeError('Read-only CRM operation failed: ' + operation + ', status=' + str(status))


class Database:
    # Thin DB-API facade to retain the audited collector's tuple/transaction code.
    def __init__(self):
        config = json.loads(Path(os.environ['QA_MYSQL_CONFIG']).read_text())
        self.connection = pymysql.connect(**config, charset='utf8mb4', autocommit=False)
    def execute(self, sql, args=()):
        cursor = self.connection.cursor()
        cursor.execute(sql, args)
        rows = cursor.fetchall() if cursor.description else ()
        cursor.close()
        return Rows(rows)
    def executemany(self, sql, rows):
        with self.connection.cursor() as cursor:
            cursor.executemany(sql, list(rows))
    def __enter__(self): return self
    def __exit__(self, typ, value, traceback):
        if typ: self.connection.rollback()
        else: self.connection.commit()
    def close(self): self.connection.close()
class Rows(list):
    def fetchone(self): return self[0] if self else None

def database(root, config):
    db = Database()
    schemas = [
      'catalog(customer_id BIGINT PRIMARY KEY)',
      'checkpoints(customer_id BIGINT PRIMARY KEY,last_end VARCHAR(40) NOT NULL)',
      'seen(id BIGINT PRIMARY KEY,sha256 CHAR(64) NOT NULL)',
      'records(id BIGINT PRIMARY KEY,data LONGTEXT NOT NULL,sha256 CHAR(64) NOT NULL)',
      'links(customer_id BIGINT,record_id BIGINT,PRIMARY KEY(customer_id,record_id))',
      'variants(id BIGINT,sha256 CHAR(64),data LONGTEXT NOT NULL,PRIMARY KEY(id,sha256))',
      'meta(`key` VARCHAR(100) PRIMARY KEY,value TEXT NOT NULL)']
    for schema in schemas: db.execute('CREATE TABLE IF NOT EXISTS '+schema)
    if not db.execute("select 1 from meta where `key`='baseline_imported'").fetchone():
        with zipfile.ZipFile(path_in(root, config['baseline_archive'])) as archive, db:
            manifest = json.loads(archive.read('manifest.json'))
            if int(manifest['source']['staff_user_id']) != int(config['staff_user_id']):
                raise ValueError('Baseline employee does not match project configuration')
            with archive.open('record_digests.jsonl') as source:
                db.executemany('insert ignore into seen values(%s,%s)',
                               ((row['id'], row['sha256']) for row in map(json.loads, io.TextIOWrapper(source))))
            count = 0
            with archive.open('read_checkpoints.jsonl') as source:
                for row in map(json.loads, io.TextIOWrapper(source)):
                    if not row.get('complete'):
                        raise ValueError('Baseline includes unfinished customer views')
                    cid = int(row['customer_id'])
                    db.execute('insert ignore into catalog values(%s)', (cid,))
                    db.execute('insert ignore into checkpoints values(%s,%s)',
                               (cid, row.get('as_of') or manifest['statistics']['recent_end']))
                    count += 1
            if count != manifest['coverage']['customer_directory_objects']:
                raise ValueError('Baseline directory count is inconsistent')
            db.execute('insert into meta values(%s,%s)', ('baseline_imported', dt.datetime.now(dt.timezone.utc).isoformat()))
    return db


def directory(crm):
    ids, signatures = set(), set()
    query = {'limit': 200}
    while True:
        page = crm.invoke('admin.listCustomers', query=query)
        ids.update(int(row['customer_id']) for row in page.get('items', []))
        cursor = page.get('next_cursor')
        if not cursor:
            return sorted(ids)
        if cursor in signatures:
            raise RuntimeError('Repeated customer-directory cursor')
        signatures.add(cursor)
        query['cursor'] = cursor


def customer(crm, config, cid, start, end):
    query = {'staff_user_id': config['staff_user_id'], 'chat_type': 'private', 'limit': 100,
             'start_at': start.isoformat(), 'end_at': end.isoformat()}
    rows, cursors, pages = {}, set(), 0
    while True:
        page = crm.invoke('admin.listCustomerMessageArchive', {'customer_id': cid}, query)
        if int(page['customer_id']) != cid:
            raise RuntimeError('Returned archive belongs to a different customer view')
        pages += 1
        for row in page.get('items', []):
            if row['chat_type'] != 'private' or row.get('staff_names') != [config['staff_display_name']]:
                raise RuntimeError('Returned record is outside the configured private-chat employee scope')
            if not start <= stamp(row['occurred_at']) <= end:
                raise RuntimeError('Returned record is outside the requested time range')
            old = rows.get(row['id'])
            if old is not None and canonical(old) != canonical(row):
                raise RuntimeError('Record changed while paging one customer view')
            rows[row['id']] = row
        nxt = page.get('next')
        if not nxt:
            return list(rows.values()), pages
        signature = canonical(nxt)
        if signature in cursors:
            raise RuntimeError('Repeated private-chat archive cursor')
        cursors.add(signature)
        query.update(nxt)


def body(text):
    text = text.strip()
    if text.startswith('这是一条引用/回复消息：') and '\n------\n' in text:
        text = text.split('\n------\n', 1)[1].strip()
    return re.split(r'\n(?:-\s*){3,}\n', text)[-1].strip()


def redact(text):
    text = re.sub(r'(?<!\d)1[3-9]\d{9}(?!\d)', '[手机号已隐去]', text)
    return re.sub(r'[\w.+-]+@[\w.-]+\.[A-Za-z]{2,}', '[邮箱已隐去]', text)


def candidates(db, config, end):
    since = end - dt.timedelta(days=30)
    source = {rid: json.loads(raw) for rid, raw in db.execute('select id,data from records')}
    links = collections.defaultdict(list)
    for cid, rid in db.execute('select customer_id,record_id from links'):
        links[cid].append(source[rid])
    result = {}
    for cid, rows in links.items():
        rows.sort(key=lambda row: (stamp(row['occurred_at']), row['id']))
        turns = []
        for row in rows:
            if not row.get('content_text', '').strip():
                continue
            at = stamp(row['occurred_at'])
            if turns and turns[-1]['direction'] == row['direction'] and (at-turns[-1]['end']).total_seconds() <= 600:
                turns[-1]['rows'].append(row)
                turns[-1]['end'] = at
            else:
                turns.append({'at': at, 'end': at, 'direction': row['direction'], 'rows': [row]})
        for i, turn in enumerate(turns):
            if turn['direction'] != 'customer_to_staff' or not since <= turn['at'] <= end:
                continue
            question = '\n'.join(body(row['content_text']) for row in turn['rows'])
            if not QUESTION.search(question):
                continue
            answers = []
            if i + 1 < len(turns):
                nxt = turns[i+1]
                if nxt['direction'] == 'staff_to_customer' and (nxt['at']-turn['end']).total_seconds() <= 7200:
                    answers = [row for row in nxt['rows'] if not CAMPAIGN.search(body(row['content_text']))]
            qids, aids = [row['id'] for row in turn['rows']], [row['id'] for row in answers]
            ident = 'QA-' + str(qids[0])
            row = {'id': ident, 'customer_ref': f'C{cid:05d}', 'customer_refs': [f'C{cid:05d}'],
                   'asked_at': turn['at'].isoformat(), 'question': redact(question),
                   'answer_original': redact('\n'.join(body(row['content_text']) for row in answers)),
                   'question_record_ids': qids, 'answer_record_ids': aids,
                   'state': '待复核-相邻文本不等于有效答复' if answers else '待真人处理-缺少配对文本',
                   'source_path': f'来源/增量问答证据/{ident}.md', 'source_staff_user_id': config['staff_user_id']}
            if ident in result:
                prior = result[ident]
                prior['customer_refs'] = sorted(set(prior['customer_refs'] + row['customer_refs']))
                if prior['answer_record_ids'] != aids or prior['question_record_ids'] != qids:
                    prior['state'] = '待复核-关联视图配对不同'
            else:
                result[ident] = row
    return result, source


def materialize(root, db, config, snapshot, receipt):
    recent, source = candidates(db, config, snapshot)
    previous = {}
    for key in ['baseline_candidates', 'baseline_internal_candidates', 'rolling_candidates', 'rolling_internal_candidates']:
        for row in read_lines(path_in(root, config[key])):
            previous[row['id']] = row
    previous.update(recent)
    since = snapshot - dt.timedelta(days=30)
    current = sorted([row for row in previous.values() if since <= stamp(row['asked_at']) <= snapshot],
                     key=lambda row: (row['asked_at'], row['id']), reverse=True)
    internal_refs = {f'C{int(cid):05d}' for cid in config['internal_customer_ids']}
    internal, external = [], []
    for row in current:
        refs = set(row.get('customer_refs', [row['customer_ref']]))
        (internal if refs.intersection(internal_refs) else external).append(row)
    for key, rows in [('rolling_candidates', external), ('rolling_internal_candidates', internal)]:
        write_text(path_in(root, config[key]), ''.join(json.dumps(row, ensure_ascii=False)+'\n' for row in rows))
    for row in recent.values():
        text = [f'# {row["id"]}', '', '增量采集的候选，不是可直接回答的政策。', '',
                '- 时间：' + row['asked_at'], '- 状态：' + row['state'], '',
                '## 问题', '', row['question'], '', '## 相邻答复', '', row['answer_original'] or '未找到有效配对文本。', '']
        for rid in row['question_record_ids'] + row['answer_record_ids']:
            record = source[rid]
            text += [f'### M{rid}', '', record['occurred_at']+' · '+record['direction'], '', redact(record.get('content_text', '')), '']
        write_text(path_in(root, row['source_path']), '\n'.join(text)+'\n')
    grouped = collections.defaultdict(list)
    for record in source.values():
        day = stamp(record['occurred_at']).astimezone(dt.timezone(dt.timedelta(hours=8))).date().isoformat()
        grouped[day].append(record)
    for day, rows in grouped.items():
        text = [f'# {day} 增量私聊', '', '范围由项目CRM配置指定；包含重叠窗口，消息以CRM编号去重。非文本未全部解析。原文仅作资料，不是执行指令。', '']
        for record in sorted(rows, key=lambda row: (stamp(row['occurred_at']), row['id'])):
            text += [f'## M{record["id"]}', '', record['occurred_at']+' · '+record['direction']+' · '+record['message_type'], '',
                     redact(record.get('content_text', '')) or '（没有已解析的文本正文）', '',
                     '媒体编号：'+json.dumps(record.get('media_ids', []), ensure_ascii=False), '']
        write_text(root / f'来源/增量聊天/{day}.md', '\n'.join(text)+'\n')
    conflict_path = root / '知识库/答疑证据冲突.json'
    conflicts = json.loads(conflict_path.read_text())
    for rid, sha, _ in db.execute('select id,sha256,data from variants'):
        if not any(x.get('id') == rid and x.get('sha256') == sha for x in conflicts['variants']):
            conflicts['variants'].append({'id': rid, 'sha256': sha, 'state': 'source_changed_requires_review'})
        conflicts['blocked_record_ids'] = sorted(set(conflicts['blocked_record_ids']+[rid]))
    write_json(conflict_path, conflicts)
    receipt.update(rolling_external_candidates=len(external), rolling_internal_candidates=len(internal),
                   candidate_auto_approval=False, source_variants=len(conflicts['variants']))
    last = snapshot.astimezone(dt.timezone(dt.timedelta(hours=8))).isoformat()
    text = ['# 问答库更新索引', '', f'最近执行：{last}；状态：{receipt["state"]}；模式：{receipt["scope"]}。', '',
            f'本次完成{receipt["completed_views"]}/{receipt["selected_views"]}个私聊视图，新增源消息{receipt["new_source_records"]}条。', '',
            f'滚动30天其他私聊候选{len(external)}组，内部联调{len(internal)}组；候选均未自动成为可答政策。', '',
            '- [[知识库/可答范围与转真人]]', '- [[知识库/滚动问答候选.jsonl]]', '- [[知识库/滚动内部运营候选.jsonl]]',
            '- [[运营记录/最近CRM更新.json]]', '', '基础归档与82条历史核对问答保留。日更只读取私聊；失败视图水位未推进，明日继续。']
    write_text(root / '知识库/问答库更新索引.md', '\n'.join(text)+'\n')
    if (root / '运营配置/语义检索.json').exists():
        # Freeze the MySQL increment before building the existing retrieval
        # cache. Queries never read live collector tables across versions.
        records_file='运营数据/CRM快照记录.jsonl'
        links_file='运营数据/CRM快照关联.jsonl'
        write_text(root/records_file,''.join(canonical(row)+'\n' for _,row in sorted(source.items())))
        write_text(root/links_file,''.join(canonical({'customer_id':cid,'record_id':rid})+'\n' for cid,rid in db.execute('select customer_id,record_id from links order by customer_id,record_id')))
        retrieval=json.loads((root/'运营配置/语义检索.json').read_text())
        retrieval.update(snapshot_records=records_file,snapshot_links=links_file)
        write_json(root/'运营配置/语义检索.json',retrieval)
        try:
            import qa_retrieve
            indexed=qa_retrieve.build(root)
            receipt.update(retrieval_index_state='complete',retrieval_index_fingerprint=indexed['fingerprint'])
        except Exception as error:
            receipt.update(retrieval_index_state='failed',retrieval_index_error=type(error).__name__)
            raise RuntimeError('Retrieval index build failed; publication blocked') from error



def sync(root, config, crm, selected=None, snapshot=None):
    snapshot = snapshot or dt.datetime.now(dt.timezone.utc)
    db = database(root, config)
    # A resumed older operation must not roll back a newer collector watermark
    # or incorporate future records in an older pinned resource snapshot.
    latest = max((stamp(row[0]) for row in db.execute('select last_end from checkpoints')), default=None)
    if latest and latest > snapshot:
        db.close()
        raise RuntimeError('A newer collection has advanced; start a new operation')
    receipt = {'schema_version': 1, 'snapshot': snapshot.isoformat(), 'scope': 'selected_verification' if selected else 'all_directory_private_incremental',
               'state': 'running', 'staff_user_id': config['staff_user_id'], 'crm_business_writes': False,
               'completed_views': 0, 'selected_views': 0, 'new_source_records': 0, 'pages': 0, 'failures': []}
    try:
        ids = sorted(set(selected)) if selected else directory(crm)
        receipt['selected_views'] = len(ids)
        stats_key='refresh-'+snapshot.isoformat()
        saved=db.execute('select value from meta where `key`=%s',(stats_key,)).fetchone()
        stats=json.loads(saved[0]) if saved else {'new_source_records':0,'pages':0}
        # Upgrade an interrupted first collection from the original adapter:
        # baseline seen IDs are imported atomically and are never deleted.
        if not saved and not selected and not db.execute("select 1 from meta where `key`='last_complete_refresh'").fetchone():
            with zipfile.ZipFile(path_in(root, config['baseline_archive'])) as archive:
                with archive.open('record_digests.jsonl') as source:
                    baseline_count=len({json.loads(line)['id'] for line in source if line.strip()})
            stats['new_source_records']=max(0,db.execute('select count(*) from seen').fetchone()[0]-baseline_count)
        receipt.update(stats)
        receipt['resumed_views']=0
        if not ids:
            raise RuntimeError('Empty customer directory is not accepted as a successful refresh')
        with db:
            db.executemany('insert ignore into catalog values(%s)', ((cid,) for cid in ids))
        work = []
        for cid in ids:
            old = db.execute('select last_end from checkpoints where customer_id=%s', (cid,)).fetchone()
            if old and stamp(old[0]) == snapshot:
                receipt['completed_views']+=1
                receipt['resumed_views']+=1
                continue
            rolling_start = snapshot - dt.timedelta(days=config['new_customer_lookback_days'])
            start = stamp(old[0])-dt.timedelta(hours=config['overlap_hours']) if old else rolling_start
            work.append((cid, start))
        with concurrent.futures.ThreadPoolExecutor(max_workers=config['workers']) as pool:
            # Submit bounded work so an authorization failure does not create a large request backlog.
            iterator = iter(work)
            pending = {}
            def fill():
                while len(pending) < config['workers'] and not getattr(crm, 'auth_failed', threading.Event()).is_set():
                    item = next(iterator, None)
                    if item is None:
                        break
                    cid, start = item
                    pending[pool.submit(customer, crm, config, cid, start, snapshot)] = cid
            fill()
            while pending:
                done, _ = concurrent.futures.wait(pending, return_when=concurrent.futures.FIRST_COMPLETED)
                for future in done:
                    cid = pending.pop(future)
                    try:
                        rows, pages = future.result()
                        added = 0
                        with db:
                            for row in rows:
                                raw = canonical(row)
                                sha = hashlib.sha256(raw.encode()).hexdigest()
                                prior = db.execute('select sha256 from seen where id=%s', (row['id'],)).fetchone()
                                if prior and prior[0] != sha:
                                    db.execute('insert ignore into variants values(%s,%s,%s)', (row['id'], sha, raw))
                                if not prior:
                                    db.execute('insert into seen values(%s,%s)', (row['id'], sha))
                                    added += 1
                                db.execute('replace into records values(%s,%s,%s)', (row['id'], raw, sha))
                                db.execute('insert ignore into links values(%s,%s)', (cid, row['id']))
                            db.execute('replace into checkpoints values(%s,%s)', (cid, snapshot.isoformat()))
                            next_stats={'new_source_records':stats['new_source_records']+added,'pages':stats['pages']+pages}
                            db.execute('replace into meta values(%s,%s)',(stats_key,json.dumps(next_stats)))
                        stats=next_stats
                        receipt['new_source_records'] = stats['new_source_records']
                        receipt['completed_views'] += 1
                        receipt['pages'] = stats['pages']
                    except Exception as error:
                        receipt['failures'].append({'customer_id': cid, 'error_type': type(error).__name__, 'detail': str(error)[:300]})
                if receipt['completed_views'] and receipt['completed_views'] % 500 == 0:
                    print(json.dumps({'completed_views': receipt['completed_views'], 'selected_views': len(ids), 'new_records': receipt['new_source_records']}), file=sys.stderr, flush=True)
                fill()
        receipt['state'] = 'complete' if receipt['completed_views'] == len(ids) and not receipt['failures'] else 'partial_or_authorization_failed'
    except Exception as error:
        receipt['state'] = 'failed'
        receipt['failures'].append({'error_type': type(error).__name__, 'detail': str(error)[:300]})
    try:
        # Retain enough private context for the rolling month and overlap; old source pages stay archived.
        with db:
            cutoff = snapshot - dt.timedelta(days=30, hours=config['overlap_hours'])
            old_ids=[(rid,) for rid,raw in db.execute('select id,data from records') if stamp(json.loads(raw)['occurred_at']) < cutoff]
            for offset in range(0,len(old_ids),200):
                db.executemany('delete from records where id=%s',old_ids[offset:offset+200])
            db.execute('delete from links where record_id not in(select id from records)')
        if receipt['state'] != 'complete':
            raise RuntimeError('Incomplete private archive views; publication blocked')
        materialize(root, db, config, snapshot, receipt)
        if receipt['state'] == 'complete' and not selected:
            with db:
                db.execute('replace into meta values(%s,%s)', ('last_complete_refresh', snapshot.isoformat()))
    except Exception as error:
        receipt['state'] = 'materialization_failed'
        receipt['failures'].append({'error_type': type(error).__name__, 'detail': str(error)[:300]})
    receipt['finished_at'] = dt.datetime.now(dt.timezone.utc).isoformat()
    write_json(root / '运营记录/最近CRM更新.json', receipt)
    name = '运营记录/CRM更新/' + snapshot.strftime('%Y%m%dT%H%M%S%f') + '.json'
    write_json(root / name, receipt)
    db.close()
    return receipt


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--project-root', type=Path, required=True)
    parser.add_argument('--customer-id', type=int, action='append')
    args = parser.parse_args()
    os.umask(0o077)
    root = args.project_root.resolve()
    config = json.loads((root / '运营配置/CRM问答更新.json').read_text())
    if config.get('schema_version') != 1 or config.get('scope') != 'private_only_no_customer_sends':
        raise SystemExit('Unsupported project update configuration')
    if config['workers'] not in range(1, 9) or config['staff_user_id'] <= 0:
        raise SystemExit('Invalid scope or concurrency')
    lock_path = path_in(root, '运营数据/CRM同步.lock')
    lock_path.parent.mkdir(parents=True, exist_ok=True)
    with lock_path.open('a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise SystemExit('This project already has a CRM refresh in progress')
        receipt = sync(root, config, CRM(config), args.customer_id)
    print(json.dumps(receipt, ensure_ascii=False), flush=True)
    raise SystemExit(0 if receipt['state'] == 'complete' else 1)


if __name__ == '__main__':
    main()
