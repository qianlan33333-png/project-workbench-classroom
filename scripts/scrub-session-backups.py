#!/usr/bin/env python3
"""Remove only purged conversation originals from managed workbench snapshots.

Installed root-owned on the cloud host. Paths are fixed; callers cannot select
other applications or arbitrary files. The independent ledger survives restores.
"""
import json,os,pathlib,shutil,sqlite3
DATA=pathlib.Path('/var/lib/project-workbench')
BASE=pathlib.Path('/opt/project-workbench')
LEDGER=pathlib.Path('/var/lib/project-workbench-purge-ledger')

def scrub(db_path, marker):
    sid=marker['id']; hid=marker['harnessId']; project=marker['projectId']
    c=sqlite3.connect(db_path)
    tables={r[0] for r in c.execute("SELECT name FROM sqlite_master WHERE type='table'")}
    if 'sessions' not in tables: c.close(); return
    messages=list(c.execute("SELECT id,event_id FROM inbox WHERE session_id=? OR json_extract(data,'$.incoming.sessionId')=?",(sid,sid)))
    run_ids=[r[0] for r in c.execute('SELECT id FROM runs WHERE session_id=?',(sid,))]
    root=db_path.parent
    # A previous scrubber may already have deleted the DB rows while leaving
    # the routed native session behind. Recover its run ID from the session tree.
    home=root/'runtime'/'router-home'
    if (root/'runtime').is_symlink() or home.is_symlink(): raise ValueError('invalid scratch root')
    for session in home.glob('*/sessions/*/*'):
        if session.name==hid: run_ids.append(session.relative_to(home).parts[0])
    for rid in set(run_ids):
        if not isinstance(rid,str) or pathlib.Path(rid).name!=rid or rid in ('','.','..'): raise ValueError('invalid run ID')
        for name in ('router','router-home'):
            scratch=root/'runtime'/name
            if scratch.is_symlink() or (root/'runtime').is_symlink(): raise ValueError('invalid scratch root')
            dest=scratch/rid
            if dest.is_symlink(): dest.unlink()
            elif dest.is_dir(): shutil.rmtree(dest)
    c.execute('PRAGMA foreign_keys=OFF')
    with c:
        if 'legacy_consumed' in tables:
            for h in marker.get('messageHashes',[]):c.execute('INSERT OR IGNORE INTO legacy_consumed(hash) VALUES(?)',(h,))
        for mid,event in messages:
            c.execute("DELETE FROM events WHERE json_extract(data,'$.id')=? OR json_extract(data,'$.messageId')=? OR json_extract(data,'$.eventId')=?",(mid,mid,event))
        for sql in [
            "DELETE FROM deliveries WHERE json_extract(data,'$.sessionId')=?",
            "DELETE FROM interactions WHERE run_id IN (SELECT id FROM runs WHERE session_id=?)",
            "DELETE FROM baselines WHERE run_id IN (SELECT id FROM runs WHERE session_id=?)",
            "UPDATE revisions SET run_id='' WHERE run_id IN (SELECT id FROM runs WHERE session_id=?)",
            "DELETE FROM entry_focus WHERE session_id=?",
            "DELETE FROM events WHERE session_id=?",
            "DELETE FROM harness_events WHERE session_id=?",
            "DELETE FROM runtime_sessions WHERE session_id=?",
            "DELETE FROM runs WHERE session_id=?",
            "DELETE FROM inbox WHERE session_id=? OR json_extract(data,'$.incoming.sessionId')=?",
            "DELETE FROM sessions WHERE id=?",
        ]:
            table=sql.split()[2] if sql.startswith('DELETE') else 'revisions'
            if table in tables: c.execute(sql,(sid,)*sql.count('?'))
    c.execute('PRAGMA wal_checkpoint(TRUNCATE)');c.execute('VACUUM');c.close()
    root=db_path.parent
    native=root/'runtime'/'projects'/project
    session_root=native/'sessions'
    if session_root.exists():
        for p in session_root.glob('*/'+hid):
            if p.is_dir() and not p.is_symlink():shutil.rmtree(p)
    for rid in run_ids:
        (native/('invocation-'+rid+'.yml')).unlink(missing_ok=True)
    shutil.rmtree(root/'trash'/sid,ignore_errors=False) if (root/'trash'/sid).exists() else None

def main():
    LEDGER.mkdir(mode=0o755,exist_ok=True)
    markers=DATA/'purged-sessions';markers.mkdir(mode=0o700,exist_ok=True)
    owner=DATA.stat()
    if os.geteuid()==0:os.chown(markers,owner.st_uid,owner.st_gid)
    for p in markers.glob('*.json'):
        v=json.loads(p.read_text())
        if any(not isinstance(v.get(k),str) or '/' in v[k] or '\\' in v[k] or v[k] in ('','.','..') for k in ['id','projectId','harnessId']):raise ValueError('invalid purge marker')
        dst=LEDGER/p.name
        if not dst.exists():dst.write_text(json.dumps(v));dst.chmod(0o644)
    for p in LEDGER.glob('*.json'):
        marker=json.loads(p.read_text())
        if not (markers/p.name).exists():shutil.copyfile(p,markers/p.name)
        if os.geteuid()==0:os.chown(markers/p.name,owner.st_uid,owner.st_gid)
        for db in BASE.glob('rollback-*/data/workbench.sqlite'):
            if db.is_file() and not db.is_symlink():scrub(db,marker)
    print('Managed conversation snapshots checked')
if __name__=='__main__':main()
