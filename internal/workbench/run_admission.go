package workbench

import (
	"strings"
)

// Six is the total run ceiling, including the personal assistant. Only verified
// read-only customer sessions can use capacity beyond the ordinary writer budget.
const maxConcurrentRuns = 6
const maxProjectRuns = 2

type GatewayReadMCPGrant struct {
	ProjectID string `json:"projectId"`
	PluginID  string `json:"pluginId"`
	URL       string `json:"url"`
}

func (g GatewayReadMCPGrant) allows(p Plugin, project string) bool {
	return p.Transport == "http" && p.ProjectID == project && project != "" && g.ProjectID == project &&
		g.PluginID == p.ID && strings.HasPrefix(p.URL, "https://") && g.URL == p.URL
}

// Keep one authoritative lease for writers/maintenance in the legacy table.
// The committed active run row itself is a shared reader lease. No transient
// state or duplicate reader records survive Recover() or a binary rollback.
const runLeaseSchema = `
CREATE VIEW IF NOT EXISTS workbench_run_leases AS
 SELECT l.project_id, l.run_id, r.session_id, 0 AS shared FROM project_locks l LEFT JOIN runs r ON r.id=l.run_id
 UNION ALL
 SELECT r.project_id, r.id, r.session_id, COALESCE(json_extract(r.data,'$.gateway.sharedRead'),0)
 FROM runs r WHERE r.state IN ('running','waiting','stopping')
 AND NOT EXISTS (SELECT 1 FROM project_locks l WHERE l.run_id=r.id);
`

// The exact destination and owning project are pinned by host configuration.
// The binding may select zero tools, or only explicitly approved HTTP MCPs;
// a CLI, stdio MCP, missing plugin or changed endpoint takes an exclusive slot.
const claimRunSQL = `
WITH active AS MATERIALIZED (SELECT * FROM workbench_run_leases),
queued AS MATERIALIZED (
 SELECT r.id,r.project_id,r.session_id,r.priority,r.created_at,r.rowid AS seq,
 CASE WHEN json_extract(r.data,'$.source')='wecom-gateway' AND json_type(r.data,'$.gateway')='object'
   AND EXISTS(SELECT 1 FROM gateway_sessions gs WHERE gs.id=r.session_id)
   AND COALESCE(json_array_length(r.data,'$.gateway.toolIds'),0)=0
   AND NOT EXISTS(SELECT 1 FROM json_each(r.data,'$.gateway.pluginIds') sel WHERE NOT EXISTS (
     SELECT 1 FROM project_plugins p JOIN json_each(?) grant
       ON json_extract(grant.value,'$.pluginId')=p.id AND json_extract(grant.value,'$.projectId')=p.project_id
       AND json_extract(grant.value,'$.url')=json_extract(p.data,'$.url')
     WHERE p.id=sel.value AND p.project_id=r.project_id AND json_extract(p.data,'$.transport')='http'
       AND json_extract(p.data,'$.url') GLOB 'https://*' AND COALESCE(json_extract(p.data,'$.enabled'),1)=1))
 THEN 1 ELSE 0 END AS shared
 FROM runs r WHERE r.state='queued'
)
SELECT r.data,q.shared FROM queued q JOIN runs r ON r.id=q.id
WHERE (SELECT COUNT(*) FROM active)<?
  AND NOT EXISTS(SELECT 1 FROM active WHERE run_id LIKE 'maintenance:%')
  AND NOT EXISTS(SELECT 1 FROM settings WHERE key='tool_install_running')
  AND NOT EXISTS(SELECT 1 FROM active a WHERE a.project_id=q.project_id
      AND (a.shared=0 OR q.shared=0 OR a.session_id=q.session_id))
  AND (q.shared=1 OR q.project_id=? OR (SELECT COUNT(*) FROM active WHERE shared=0 AND project_id<>?)<?)
  AND NOT EXISTS(SELECT 1 FROM queued earlier WHERE earlier.project_id=q.project_id
     AND (earlier.created_at,earlier.seq)<(q.created_at,q.seq)
     AND (earlier.session_id=q.session_id OR (q.shared=1 AND earlier.shared=0)))
ORDER BY (q.project_id=?) DESC,q.priority,q.created_at,q.seq LIMIT 1
`

func (s *Store) finishRunSnapshot(r Run) error {
	if r.Gateway != nil && r.Gateway.SharedRead {
		return nil
	}
	return s.FinishSnapshot(r)
}
