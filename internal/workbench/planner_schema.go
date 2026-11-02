package workbench

// Failure receipts survive clearing the inbox or deleting a rule. Storing both
// in the same transaction prevents a crash from dropping or doubling a notice.
const plannerSchema = `
CREATE TABLE IF NOT EXISTS calendar_events(id TEXT PRIMARY KEY,operation TEXT UNIQUE NOT NULL,data TEXT NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS calendar_remote_id ON calendar_events(json_extract(data,'$.remoteId')) WHERE coalesce(json_extract(data,'$.remoteId'),'')<>'';
CREATE TABLE IF NOT EXISTS user_notices(id TEXT PRIMARY KEY DEFAULT(lower(hex(randomblob(16)))),kind TEXT NOT NULL,event_key TEXT NOT NULL,title TEXT NOT NULL,summary TEXT NOT NULL,link TEXT NOT NULL,created INTEGER NOT NULL DEFAULT(CAST(unixepoch('subsec')*1000 AS INTEGER)),seen INTEGER NOT NULL DEFAULT 0,cleared INTEGER NOT NULL DEFAULT 0,UNIQUE(kind,event_key));
CREATE TABLE IF NOT EXISTS schedule_failures(id TEXT PRIMARY KEY DEFAULT(lower(hex(randomblob(16)))),schedule TEXT NOT NULL,operation TEXT NOT NULL,run TEXT NOT NULL,detail TEXT NOT NULL,at INTEGER NOT NULL,name TEXT NOT NULL,UNIQUE(schedule,operation));
CREATE TRIGGER IF NOT EXISTS notify_schedule_failure AFTER INSERT ON schedule_failures BEGIN
 INSERT OR IGNORE INTO user_notices(id,kind,event_key,title,summary,link,created) VALUES(NEW.id,'schedule.failed',NEW.id,NEW.name||' · 执行失败',NEW.detail,'#page=notices&failure='||NEW.id,NEW.at);
END;
CREATE TRIGGER IF NOT EXISTS failed_schedule_check AFTER INSERT ON schedule_checks WHEN NEW.outcome='failed' BEGIN
 INSERT OR IGNORE INTO schedule_failures(schedule,operation,run,detail,at,name) SELECT NEW.schedule_id,NEW.operation,NEW.run_id,NEW.detail,NEW.checked_at,json_extract(data,'$.name') FROM schedules WHERE id=NEW.schedule_id;
END;
CREATE TRIGGER IF NOT EXISTS failed_schedule_run AFTER UPDATE ON schedule_checks WHEN NEW.outcome='failed' AND OLD.outcome<>'failed' BEGIN
 INSERT OR IGNORE INTO schedule_failures(schedule,operation,run,detail,at,name) SELECT NEW.schedule_id,NEW.operation,NEW.run_id,NEW.detail,coalesce((SELECT json_extract(data,'$.finishedAt') FROM runs WHERE id=NEW.run_id),NEW.checked_at),json_extract(data,'$.name') FROM schedules WHERE id=NEW.schedule_id;
END;
`
