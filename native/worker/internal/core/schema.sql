CREATE TABLE IF NOT EXISTS wb_projects (
 id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 body JSON NOT NULL
);
CREATE TABLE IF NOT EXISTS wb_settings (id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,body JSON NOT NULL);
CREATE TABLE IF NOT EXISTS wb_write_fences(project_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,epoch BIGINT NOT NULL,execution VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,active BOOLEAN NOT NULL,sync_attempt INT NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS wb_write_attempts(execution VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,project_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,epoch BIGINT NOT NULL DEFAULT 0,closed BOOLEAN NOT NULL DEFAULT FALSE);
CREATE TABLE IF NOT EXISTS wb_session_sources (
 session_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 resource_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 version VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 section_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 PRIMARY KEY(session_id,resource_id,version,section_id)
);
CREATE TABLE IF NOT EXISTS wb_sessions (
 id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 project_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 kind VARCHAR(16) NOT NULL,
 expert_ref VARCHAR(180) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
);
CREATE TABLE IF NOT EXISTS wb_run_sources (
 run_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 resource_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 version VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 section_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 PRIMARY KEY(run_id,resource_id,version,section_id)
);
CREATE TABLE IF NOT EXISTS wb_signal_outbox (
 id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 body JSON NOT NULL
);
CREATE TABLE IF NOT EXISTS wb_connections (
 id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 body JSON NOT NULL
);
CREATE TABLE IF NOT EXISTS wb_resources (
 id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 owner_project VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
 current_version VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
 revision BIGINT NOT NULL DEFAULT 1,
 body JSON NOT NULL
);
CREATE TABLE IF NOT EXISTS wb_grants (
 resource_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 project_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 section_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 PRIMARY KEY(resource_id,project_id,section_id),
 FOREIGN KEY(resource_id) REFERENCES wb_resources(id),
 FOREIGN KEY(project_id) REFERENCES wb_projects(id)
);
CREATE TABLE IF NOT EXISTS wb_snapshots (
 resource_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 version VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 previous_version VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 state VARCHAR(16) NOT NULL,
 body JSON NOT NULL,
 record_count BIGINT NOT NULL DEFAULT 0,
 published_at DATETIME(6) NULL,
 PRIMARY KEY(resource_id,version),
 FOREIGN KEY(resource_id) REFERENCES wb_resources(id)
);
CREATE TABLE IF NOT EXISTS wb_records (
 resource_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 version VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 item_id VARCHAR(191) COLLATE utf8mb4_0900_bin NOT NULL,
 digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 body JSON NOT NULL,
 PRIMARY KEY(resource_id,version,item_id),
 FOREIGN KEY(resource_id,version) REFERENCES wb_snapshots(resource_id,version)
);
CREATE TABLE IF NOT EXISTS wb_runs (
 id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 project_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 session_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 input_digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 body JSON NOT NULL,
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 INDEX wb_runs_session(session_id,created_at)
);
CREATE TABLE IF NOT EXISTS wb_items (
 run_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 item_id VARCHAR(191) COLLATE utf8mb4_0900_bin NOT NULL,
 state VARCHAR(24) NOT NULL,
 body JSON NOT NULL,
 PRIMARY KEY(run_id,item_id),
 FOREIGN KEY(run_id) REFERENCES wb_runs(id)
);
CREATE TABLE IF NOT EXISTS wb_prepared (
 run_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 item_id VARCHAR(191) COLLATE utf8mb4_0900_bin NOT NULL,
 body JSON NOT NULL,
 PRIMARY KEY(run_id,item_id),
 FOREIGN KEY(run_id) REFERENCES wb_runs(id)
);
CREATE TABLE IF NOT EXISTS wb_decisions (
 id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 run_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 revision BIGINT NOT NULL,
 content_digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 state VARCHAR(16) NOT NULL,
 body JSON NOT NULL
);
CREATE TABLE IF NOT EXISTS wb_memory (
 id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 revision BIGINT NOT NULL,
 forgotten BOOLEAN NOT NULL DEFAULT FALSE,
 body JSON NOT NULL
);
CREATE TABLE IF NOT EXISTS wb_events (
 sequence BIGINT UNSIGNED NOT NULL AUTO_INCREMENT UNIQUE,
 id VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 session_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 run_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 delivered BOOLEAN NOT NULL DEFAULT FALSE,
 body JSON NOT NULL,
 INDEX wb_events_delivery(delivered,created_at)
);
CREATE TABLE IF NOT EXISTS wb_messages (
 connection_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 event_id VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 body JSON NOT NULL,
 PRIMARY KEY(connection_id,event_id)
);
CREATE TABLE IF NOT EXISTS wb_transports(id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, body JSON NOT NULL);
CREATE TABLE IF NOT EXISTS wb_message_routes(
 id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 connection_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 conversation_id VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 actor_id VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 body JSON NOT NULL,
 UNIQUE(connection_id,conversation_id,actor_id)
);
CREATE TABLE IF NOT EXISTS wb_connector_state(id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, body JSON NOT NULL);
CREATE TABLE IF NOT EXISTS wb_deliveries (
 id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 run_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 item_id VARCHAR(191) COLLATE utf8mb4_0900_bin NOT NULL,
 state VARCHAR(24) NOT NULL,
 body JSON NOT NULL,
 UNIQUE(run_id,item_id)
);
CREATE TABLE IF NOT EXISTS wb_delivery_transports(id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, body JSON NOT NULL);

CREATE TABLE IF NOT EXISTS wb_business_policies (
 resource_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 topic VARCHAR(48) NOT NULL,
 name VARCHAR(160) NOT NULL,
 aliases VARCHAR(512) NOT NULL DEFAULT '',
 answer TEXT NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 valid_from DATETIME(6) NULL,
 valid_until DATETIME(6) NULL,
 PRIMARY KEY(resource_id,id),
 KEY business_lookup(resource_id,topic,enabled,valid_until),
 FOREIGN KEY(resource_id) REFERENCES wb_resources(id)
);

CREATE TABLE IF NOT EXISTS wb_activation_codes (
  digest BINARY(32) PRIMARY KEY,
  expires_at DATETIME NOT NULL,
  installation CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NULL,
  claimed_at DATETIME NULL
);
