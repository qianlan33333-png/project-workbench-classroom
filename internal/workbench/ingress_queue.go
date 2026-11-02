package workbench

// Partial indexes contain pending work only. SQLite appends rowid to each
// ordinary index, preserving arrival order when created_at has ties.
const ingressQueueSchema = `
CREATE INDEX IF NOT EXISTS inbox_gateway_pending ON inbox(created_at) WHERE state='gateway_pending';
CREATE INDEX IF NOT EXISTS inbox_route_ready ON inbox(coalesce(json_extract(data,'$.routeAfter'),created_at)) WHERE state='unrouted';
CREATE INDEX IF NOT EXISTS inbox_route_entry ON inbox(entry_key,created_at) WHERE state='unrouted';
`

const gatewayInboxSQL = `SELECT data FROM inbox WHERE state='gateway_pending' ORDER BY created_at,rowid LIMIT 20`

// Route each entry in arrival order, including explicit destinations: queue()
// updates its focus too. Independent entries can proceed while one is deferred.
const routeInboxSQL = `SELECT i.data FROM inbox i WHERE i.state='unrouted'
 AND coalesce(json_extract(i.data,'$.routeAfter'),i.created_at)<=?
 AND NOT EXISTS (
   SELECT 1 FROM inbox older WHERE older.state='unrouted' AND older.entry_key=i.entry_key
   AND (older.created_at,older.rowid)<(i.created_at,i.rowid))
 ORDER BY coalesce(json_extract(i.data,'$.routeAfter'),i.created_at),i.rowid LIMIT 20`
