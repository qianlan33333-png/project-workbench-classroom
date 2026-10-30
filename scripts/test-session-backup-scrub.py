#!/usr/bin/env python3
import importlib.util,json,pathlib,sqlite3,tempfile,hashlib
spec=importlib.util.spec_from_file_location('scrub',pathlib.Path(__file__).with_name('scrub-session-backups.py'));m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
with tempfile.TemporaryDirectory() as tmp:
 root=pathlib.Path(tmp);m.DATA=root/'live';m.DATA.mkdir();m.BASE=root/'deploy';m.LEDGER=root/'ledger';(m.DATA/'purged-sessions').mkdir()
 marker={'id':'session-a','projectId':'project-a','harnessId':'pw-session-a','purgedAt':1,'messageHashes':[hashlib.sha256(b'web:event-a').hexdigest()]}
 (m.DATA/'purged-sessions/session-a.json').write_text(json.dumps(marker))
 backup=m.BASE/'rollback-test/data';backup.mkdir(parents=True);db=backup/'workbench.sqlite';c=sqlite3.connect(db)
 c.executescript('''CREATE TABLE sessions(id TEXT,data TEXT);CREATE TABLE inbox(id TEXT,session_id TEXT,event_id TEXT,data TEXT);CREATE TABLE runs(id TEXT,session_id TEXT);CREATE TABLE events(session_id TEXT,data TEXT);CREATE TABLE legacy_consumed(hash TEXT PRIMARY KEY);CREATE TABLE revisions(run_id TEXT);
 INSERT INTO sessions VALUES('session-a','secret-original');INSERT INTO sessions VALUES('session-b','other-project');INSERT INTO inbox VALUES('message-a','session-a','event-a','{"incoming":{"sessionId":"session-a"}}');INSERT INTO runs VALUES('run-a','session-a');INSERT INTO events VALUES('session-a','{"text":"secret-original"}');INSERT INTO revisions VALUES('run-a');''');c.commit();c.close()
 native=backup/'runtime/projects/project-a/sessions/--workspace--/pw-session-a';native.mkdir(parents=True);(native/'session.v4.jsonl').write_text('secret-original')
 files=backup/'projects/project-a/files';files.mkdir(parents=True);(files/'成果.md').write_text('keep-business-file')
 m.main();m.main()
 c=sqlite3.connect(db);assert c.execute('SELECT id FROM sessions').fetchall()==[('session-b',)];assert c.execute('SELECT count(*) FROM inbox').fetchone()[0]==0;assert c.execute('SELECT count(*) FROM legacy_consumed').fetchone()[0]==1;c.close()
 assert not native.exists();assert (files/'成果.md').read_text()=='keep-business-file';assert b'secret-original' not in db.read_bytes()
 (m.DATA/'purged-sessions/session-a.json').unlink();m.main();assert (m.DATA/'purged-sessions/session-a.json').exists()
 print('Backup originals scrubbed, business files retained, retry idempotent, independent ledger survives restore')
