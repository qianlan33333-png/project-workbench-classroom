package workbench

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

const crmHistoryPath = ".gateway-history.jsonl"
const historyQuery = `SELECT h.seq,i.created_at,json_extract(i.data,'$.gateway.historyRole'),coalesce(json_extract(i.data,'$.incoming.text'),'') FROM gateway_history_index h JOIN inbox i ON i.id=h.message_id WHERE h.session_id=? AND h.seq>? AND h.seq<=? ORDER BY i.created_at,h.seq`

type historyRow struct {
	Role string `json:"source"`
	Time string `json:"time"`
	Text string `json:"text"`
}
type historyQ interface {
	Query(string, ...any) (*sql.Rows, error)
}

func walkHistory(q historyQ, session string, lo, hi int64, f func(string) error) error {
	rows, e := q.Query(historyQuery, session, lo, hi)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var pos, at int64
		var r historyRow
		if e = rows.Scan(&pos, &at, &r.Role, &r.Text); e != nil {
			return e
		}
		if r.Text == "" {
			if r.Role == "人工客服" {
				r.Text = "人工已回复，正文不可用"
			} else {
				r.Text = "客户消息正文不可用"
			}
		}
		r.Time = time.UnixMilli(at).UTC().Format(time.RFC3339Nano)
		if e = f(encode(r) + "\n"); e != nil {
			return e
		}
	}
	return rows.Err()
}

// Freeze the interval only after the project's existing writer lock serializes runs.
// Receipt order controls the watermark, message time controls presentation of late entries.
func (s *Store) claimGatewayTx(tx *sql.Tx, r *Run) error {
	g, e := txGatewaySession(tx, r.SessionID)
	if e != nil {
		return e
	}
	b, e := gatewayBindingTx(tx, g.BindingID)
	if e != nil {
		return e
	}
	var active int
	if e = tx.QueryRow("SELECT count(*) FROM connections c JOIN projects p ON p.id=? WHERE c.id=? AND json_extract(c.data,'$.enabled')=1 AND p.archived=0", b.ProjectID, b.ConnectionID).Scan(&active); e != nil {
		return e
	}
	m := r.Gateway
	if active != 1 || !b.Enabled || g.Mode != "auto" || b.Version != m.BindingVersion || g.BindingVersion != m.BindingVersion || g.Version != m.Version || m.Deadline <= now() {
		if m.Deadline <= now() && active == 1 && b.Enabled && g.Version == m.Version && g.BindingVersion == b.Version && g.Mode == "auto" {
			if e = s.gatewayHandoffTx(tx, &g, b, "等待应答超时", r.Text); e != nil {
				return e
			}
		}
		r.State = "cancelled"
		r.FinishedAt = now()
		if _, e = tx.Exec("UPDATE runs SET state=?,data=? WHERE id=?", r.State, encode(r), r.ID); e != nil {
			return e
		}
		_, e = tx.Exec("UPDATE inbox SET state='handled',data=json_set(data,'$.state','handled','$.gateway.historyRole','客户') WHERE id=?", r.MessageID)
		return e
	}
	m.HistoryFrom = g.HistoryCursor
	if e = tx.QueryRow("SELECT coalesce(max(seq),0) FROM gateway_history_index WHERE session_id=? AND seq>?", g.ID, g.HistoryCursor).Scan(&m.HistoryThrough); e != nil {
		return e
	}
	m.ManualReply = "" // New-format runs consume historical human replies only once.
	if m.HistoryThrough == 0 {
		return nil
	}
	e = walkHistory(tx, g.ID, m.HistoryFrom, m.HistoryThrough, func(line string) error {
		if len(m.HistoryPreview)+len(line) <= 16000 && !m.HistoryFile {
			m.HistoryPreview += line
		} else {
			m.HistoryFile = true
			return io.EOF
		}
		return nil
	})
	if e == io.EOF {
		return nil
	}
	return e
}
func (s *Store) acceptGatewayHistory(ctx context.Context, r Run, id string) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec("UPDATE runtime_sessions SET state='active',last_message_id=? WHERE session_id=?", id, r.SessionID)
		if e != nil {
			return e
		}
		if r.Gateway != nil && r.Gateway.HistoryThrough > 0 {
			_, e = tx.Exec("UPDATE gateway_sessions SET data=json_set(data,'$.historyCursor',max(coalesce(json_extract(data,'$.historyCursor'),0),?)) WHERE id=?", r.Gateway.HistoryThrough, r.SessionID)
		}
		return e
	})
}
func (s *Store) writeGatewayHistory(root string, r Run) error {
	if r.Gateway == nil || !r.Gateway.HistoryFile {
		return nil
	}
	f, e := os.OpenFile(filepath.Join(root, crmHistoryPath), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	e = walkHistory(s.DB, r.SessionID, r.Gateway.HistoryFrom, r.Gateway.HistoryThrough, func(line string) error { _, e := io.WriteString(f, line); return e })
	return errors.Join(e, f.Close())
}
