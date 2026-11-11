"""Replay the original collector wire format using private JSONL, without SQLite."""
import atexit
import heapq
import json
import os
import signal
import shutil
import sys
import tempfile
from pathlib import Path


class _Descending:
    def __init__(self, value): self.value = value
    def __eq__(self, other): return self.value == other.value
    def __lt__(self, other): return self.value > other.value


def _value(value):
    # SQLite's original ordering: NULL, numeric, then text.
    if value is None: return (0, 0)
    if isinstance(value, (int, float)): return (1, value)
    return (2, str(value))


def _spool_root():
    root=os.getenv('WORKBENCH_SOURCE_SPOOL_DIR') or ('/var/tmp' if sys.platform.startswith('linux') else tempfile.gettempdir())
    if not os.path.isabs(root):raise RuntimeError('source_spool_requires_absolute_directory')
    root=str(Path(root).resolve())
    if sys.platform.startswith('linux'):
        mounts=[]
        with open('/proc/self/mountinfo') as stream:
            for line in stream:
                left,right=line.split(' - ',1);mount=left.split()[4]
                if root==mount or root.startswith(mount.rstrip('/')+'/'):mounts.append((len(mount),right.split()[0]))
        if not mounts or max(mounts)[1] in ('tmpfs','ramfs'):
            raise RuntimeError('source_spool_requires_disk_filesystem')
    return root


class DiskRows:
    """Bounded JSONL merge sort; stable ties retain original collection order."""
    def __init__(self, order_field=None, descending=False):
        self.directory = tempfile.TemporaryDirectory(prefix='operations-rows-', dir=_spool_root())
        self.path = Path(self.directory.name) / 'rows.jsonl'
        self.path.touch(mode=0o600)
        self.count, self.order_field, self.descending = 0, order_field, descending
        self.time_order, self.closed, self.sorted_path = False, False, None
        atexit.register(self.close)

    def extend(self, rows):
        if shutil.disk_usage(self.directory.name).free < 1024*1024*1024:raise RuntimeError('source_spool_disk_headroom_insufficient')
        with self.path.open('a') as output:
            for row in rows:
                output.write(json.dumps(row, ensure_ascii=False, default=str) + '\n')
                self.count += 1
        self.sorted_path = None

    def __len__(self): return self.count

    def _key(self, item):
        n, row = item
        if self.time_order:
            return (str(row.get('created_at') or ''), str(row.get('id') or ''), n)
        fields = self.order_field if isinstance(self.order_field, tuple) else (self.order_field,)
        value = next((row.get(k) for k in fields if row.get(k) is not None), None)
        value = _value(value)
        return (_Descending(value) if self.descending else value, n)

    def __iter__(self):
        if not self.time_order and not self.order_field:
            with self.path.open() as stream:
                for line in stream: yield json.loads(line)
            return
        if self.sorted_path is None:
            chunks, batch = [], []
            def flush():
                path = Path(self.directory.name) / ('chunk-' + str(len(chunks)))
                with path.open('w') as out:
                    os.chmod(path, 0o600)
                    for item in sorted(batch, key=self._key): out.write(json.dumps(item, ensure_ascii=False) + '\n')
                chunks.append(path)
                batch.clear()
            with self.path.open() as stream:
                for n, line in enumerate(stream):
                    batch.append((n, json.loads(line)))
                    if len(batch) == 500: flush()
            if batch: flush()
            # Merge at most 32 open files per pass, including a full 200k-row source.
            while len(chunks) > 1:
                merged = []
                for start in range(0, len(chunks), 32):
                    group = chunks[start:start+32]
                    path = Path(self.directory.name) / ('merge-' + str(start) + '-' + str(len(chunks)))
                    streams = [p.open() for p in group]
                    try:
                        with path.open('w') as out:
                            os.chmod(path, 0o600)
                            inputs = [map(json.loads, stream) for stream in streams]
                            for item in heapq.merge(*inputs, key=self._key): out.write(json.dumps(item, ensure_ascii=False) + '\n')
                    finally:
                        for stream in streams: stream.close()
                    for p in group: p.unlink()
                    merged.append(path)
                chunks = merged
            self.sorted_path = chunks[0] if chunks else self.path
        with self.sorted_path.open() as stream:
            for line in stream:
                value = json.loads(line)
                yield value[1] if self.count else value

    def order_by_created_id(self):
        self.time_order, self.sorted_path = True, None

    def close(self):
        if not self.closed:
            self.closed = True
            self.directory.cleanup()


def acquire_source_lease():
    # Native Temporal already serializes this project's collector. No legacy lock
    # or sync_runs database is created; a deadline still bounds a broken source.
    signal.signal(signal.SIGALRM, lambda *_: (_ for _ in ()).throw(TimeoutError('source_read_deadline')))
    signal.alarm(540)
    return None

import hashlib

def write_json(value, output):
    """Emit existing JSON format without constructing a full serialized copy."""
    if isinstance(value, dict):
        output.write('{')
        for i, (key, item) in enumerate(value.items()):
            if i:
                output.write(',')
            output.write(json.dumps(key, ensure_ascii=False) + ':')
            write_json(item, output)
        output.write('}')
    elif isinstance(value, (list, tuple, DiskRows)):
        output.write('[')
        for i, item in enumerate(value):
            if i:
                output.write(',')
            write_json(item, output)
        output.write(']')
    else:
        output.write(json.dumps(value, ensure_ascii=False, default=str))


def write_wire(payload, output):
    digest = hashlib.sha256()
    def emit(record):
        line = json.dumps(record, ensure_ascii=False, default=str) + '\n'
        digest.update(line.encode('utf-8'))
        output.write(line)
    emit({'hxc_stream': 1})
    for key, value in payload.items():
        if isinstance(value, (list, DiskRows)):
            emit({'kind': 'array', 'key': key})
            count = 0
            for row in value:
                emit({'kind': 'row', 'key': key, 'value': row})
                count += 1
            emit({'kind': 'end', 'key': key, 'count': count})
        else:
            emit({'kind': 'value', 'key': key, 'value': value})
    output.write(json.dumps({'kind': 'complete', 'keys': list(payload),
                             'sha256': digest.hexdigest()}) + '\n')
    output.flush()


def read_wire(stream):
    """Reject incomplete/changed streams before publishing any source file."""
    digest = hashlib.sha256()
    first = stream.readline()
    if json.loads(first) != {'hxc_stream': 1}:
        raise RuntimeError('hxc_stream_header_invalid')
    digest.update(first)
    payload = {}
    active = None
    count = 0
    pending = []
    spools = []
    try:
        for line in stream:
            record = json.loads(line)
            kind, key = record.get('kind'), record.get('key')
            if kind == 'complete':
                if active is not None or record.get('keys') != list(payload) or record.get('sha256') != digest.hexdigest() or stream.read(1):
                    raise RuntimeError('hxc_stream_incomplete_or_digest_mismatch')
                return payload
            digest.update(line)
            if kind in ('array', 'value'):
                if active is not None or not isinstance(key, str) or key in payload:
                    raise RuntimeError('hxc_stream_key_invalid')
                if kind == 'value':
                    payload[key] = record['value']
                else:
                    # Users are small and identity recovery intentionally edits them.
                    value = [] if key == 'users' else DiskRows()
                    if isinstance(value, DiskRows):
                        spools.append(value)
                    payload[key] = value
                    active, count = key, 0
            elif kind == 'row' and key == active and active is not None:
                value = record['value']
                if not isinstance(value, dict):
                    raise RuntimeError('hxc_stream_row_invalid')
                pending.append(value)
                if len(pending) >= 500:
                    payload[key].extend(pending)
                    pending.clear()
                count += 1
            elif kind == 'end' and key == active and active is not None:
                if record.get('count') != count:
                    raise RuntimeError('hxc_stream_count_mismatch')
                payload[key].extend(pending)
                pending.clear()
                active = None
            else:
                raise RuntimeError('hxc_stream_record_invalid')
        raise RuntimeError('hxc_stream_missing_completion')
    except BaseException:
        for value in spools:
            value.close()
        raise
