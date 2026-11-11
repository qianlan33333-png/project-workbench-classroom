package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

const OperationsReviewItem = "original-review"
const OperationsBatchResultItem = "original-batch-result"
const OperationsBatchCancelItem = "operations-batch-cancelled"
const operationsRowPrefix = "original-review-row-"

var operationsActionKey = regexp.MustCompile(`^[a-f0-9]{64}$`)

type OperationsReview struct {
	Exclusive                  bool `json:"exclusive,omitempty"`
	Package, Week, Run, Digest string
	IdentityVersion            string
	Version, Confirm, Edit     string
	Count                      int
}

type OperationsReviewRow struct {
	Key         string               `json:"key"`
	ID          string               `json:"id"`
	Nickname    string               `json:"nickname"`
	Reason      string               `json:"reason"`
	Message     string               `json:"message"`
	Progress    string               `json:"progress"`
	Revision    int                  `json:"revision"`
	Edited      bool                 `json:"edited"`
	Editable    bool                 `json:"editable"`
	Judgment    json.RawMessage      `json:"judgment"`
	Attachments json.RawMessage      `json:"attachments"`
	Customer    string               `json:"customer"`
	Slot        string               `json:"slot"`
	Materials   []OperationsMaterial `json:"materials"`
	ExpiresAt   *time.Time           `json:"expiresAt,omitempty"`
	Blocked     string               `json:"blocked,omitempty"`
}

type OperationsMaterial struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	AppID   string `json:"appId"`
	Page    string `json:"page"`
	Version int64  `json:"version,omitempty"`
	Cover   string `json:"cover,omitempty"`
}

type OperationsBatchItem struct {
	Key       string               `json:"key"`
	Customer  string               `json:"customer"`
	Message   string               `json:"message"`
	Materials []OperationsMaterial `json:"materials"`
	ExpiresAt *time.Time           `json:"expiresAt,omitempty"`
	Blocked   string               `json:"blocked,omitempty"`
}

type OperationsBatchStatus struct {
	Plan    string                            `json:"plan,omitempty"`
	Entries map[string]OperationsBatchReceipt `json:"entries"`
}

type OperationsBatchReceipt struct {
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	At    string `json:"at,omitempty"`
}

func OperationsReviewVersion(review OperationsReview, transport, staff, name string) string {
	return Digest([]string{review.Version, transport, staff, name})
}

func operationsItem[T any](ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, run, id, lock string) (T, error) {
	var v T
	var raw string
	err := q.QueryRowContext(ctx, `SELECT JSON_UNQUOTE(JSON_EXTRACT(body,'$.text')) FROM wb_items WHERE run_id=? AND item_id=?`+lock, run, id).Scan(&raw)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &v)
	}
	return v, err
}

func storeOperationsItem(ctx context.Context, tx *sql.Tx, run, id string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	body := ItemResult{ID: id, Text: string(raw), State: "succeeded", CreatedAt: time.Now().UTC()}
	_, err = tx.ExecContext(ctx, `INSERT INTO wb_items(run_id,item_id,state,body) VALUES(?,?,?,?)
		ON DUPLICATE KEY UPDATE state=VALUES(state),body=VALUES(body)`, run, id, body.State, jsonText(body))
	return err
}

func (s *Store) OperationsReview(ctx context.Context, run string) (OperationsReview, error) {
	return operationsItem[OperationsReview](ctx, s.DB, run, OperationsReviewItem, "")
}

func (s *Store) PublishOperationsReview(ctx context.Context, run string, next OperationsReview, rows []OperationsReviewRow) error {
	return s.publishOperationsReview(ctx, run, next, rows, nil)
}
func (s *Store) PublishLocalOperationsReview(ctx context.Context, run string, next OperationsReview, rows []OperationsReviewRow, roster ItemResult) error {
	return s.publishOperationsReview(ctx, run, next, rows, &roster)
}
func (s *Store) publishOperationsReview(ctx context.Context, run string, next OperationsReview, rows []OperationsReviewRow, roster *ItemResult) error {
	if !operationsActionKey.MatchString(next.Digest) || len(rows) > 5000 {
		return ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = guardWrite(ctx, tx); err != nil {
		return err
	}
	current, err := operationsItem[OperationsReview](ctx, tx, run, OperationsReviewItem, " FOR UPDATE")
	if err != nil && !IsMissing(err) {
		return err
	}
	if current.Confirm != "" || (current.Edit != "" && current.Edit != next.Edit) {
		return ErrConflict
	}
	var published []OperationsReviewRow
	seen := map[string]bool{}
	for _, row := range rows {
		if !ValidID(row.Key) || !ValidID(row.ID) || row.Message == "" || !operationsActionKey.MatchString(row.Slot) || seen[row.Key] {
			return ErrInvalid
		}
		seen[row.Key] = true
		var claimed int
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM wb_connector_state WHERE id=?)`, "op-action-"+row.Slot).Scan(&claimed); err != nil {
			return err
		}
		if claimed == 0 {
			published = append(published, row)
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM wb_items WHERE run_id=? AND item_id LIKE 'original-review-row-%'`, run); err != nil {
		return err
	}
	for _, row := range published {
		if err = storeOperationsItem(ctx, tx, run, operationsRowPrefix+row.Key, row); err != nil {
			return err
		}
	}
	next.Edit = ""
	next.Count = len(published)
	next.Version = Digest([]string{next.Digest, Digest(published)})
	if err = storeOperationsItem(ctx, tx, run, OperationsReviewItem, next); err != nil {
		return err
	}
	if roster != nil {
		if _, err = tx.ExecContext(ctx, `INSERT INTO wb_items(run_id,item_id,state,body) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE state=VALUES(state),body=VALUES(body)`, run, roster.ID, roster.State, jsonText(roster)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// Mark before crossing the source write boundary. A timed-out save leaves a
// readable draft but cannot be confirmed until the source is read again.
func (s *Store) BeginOperationsEdit(ctx context.Context, run, edit, digest, key string, revision int) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = guardWrite(ctx, tx); err != nil {
		return err
	}
	var cycle string
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(JSON_TYPE(JSON_EXTRACT(s.body,'$.cycle')),'NULL') FROM wb_settings s JOIN wb_runs r ON s.id=CONCAT('operations-',r.project_id) WHERE r.id=? FOR SHARE`, run).Scan(&cycle)
	if err != nil {
		return err
	}
	if cycle != "NULL" {
		return ErrDenied
	}
	head, err := operationsItem[OperationsReview](ctx, tx, run, OperationsReviewItem, " FOR UPDATE")
	if err != nil {
		return err
	}
	row, err := operationsItem[OperationsReviewRow](ctx, tx, run, operationsRowPrefix+key, "")
	if err != nil || !row.Editable || row.Revision != revision || head.Digest != digest || head.Confirm != "" || (head.Edit != "" && head.Edit != edit) {
		return ErrConflict
	}
	head.Edit = edit
	if err = storeOperationsItem(ctx, tx, run, OperationsReviewItem, head); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) OperationsReviewRow(ctx context.Context, run, key string) (OperationsReviewRow, error) {
	if !ValidID(key) {
		return OperationsReviewRow{}, ErrInvalid
	}
	return operationsItem[OperationsReviewRow](ctx, s.DB, run, operationsRowPrefix+key, "")
}

type operationsRowsQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func operationsReviewRows(ctx context.Context, q operationsRowsQuery, run, after string, limit int, summary bool) ([]OperationsReviewRow, error) {
	column := `JSON_UNQUOTE(JSON_EXTRACT(body,'$.text'))`
	if summary {
		column = `JSON_REMOVE(CAST(` + column + ` AS JSON),'$.message','$.judgment','$.attachments','$.materials','$.customer','$.slot')`
	}
	result, err := q.QueryContext(ctx, `SELECT `+column+` FROM wb_items
		WHERE run_id=? AND item_id LIKE 'original-review-row-%' AND item_id>? ORDER BY item_id LIMIT ?`, run, operationsRowPrefix+after, limit)
	if err != nil {
		return nil, err
	}
	defer result.Close()
	rows := []OperationsReviewRow{}
	for result.Next() {
		row, err := scanJSON[OperationsReviewRow](result)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, result.Err()
}

func (s *Store) OperationsReviewRows(ctx context.Context, run, after string, limit int) ([]OperationsReviewRow, error) {
	return operationsReviewRows(ctx, s.DB, run, after, limit, true)
}

// Claim original action keys and record confirmation in one transaction. A new
// day's draft or a changed sender cannot acquire the same original action.
func (s *Store) ConfirmOperationsReview(ctx context.Context, parent Run, confirm, version, staff, transport, name string, activeKeys []string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = guardWrite(ctx, tx); err != nil {
		return err
	}
	var cancelled string
	err = tx.QueryRowContext(ctx, `SELECT item_id FROM wb_items WHERE run_id=? AND item_id=? FOR SHARE`, confirm, OperationsBatchCancelItem).Scan(&cancelled)
	if err == nil {
		return ErrConflict
	}
	if err != sql.ErrNoRows {
		return err
	}
	head, err := operationsItem[OperationsReview](ctx, tx, parent.ID, OperationsReviewItem, " FOR UPDATE")
	if err != nil {
		return err
	}
	if head.Confirm == confirm {
		return nil
	}
	if head.Confirm != "" || head.Edit != "" || head.Count == 0 || version != OperationsReviewVersion(head, transport, staff, name) {
		return ErrConflict
	}
	var config struct {
		Staff      string `json:"staffId"`
		Transport  string
		ReadSource bool
		Cycle      json.RawMessage `json:"cycle"`
	}
	config, err = scanJSON[struct {
		Staff      string `json:"staffId"`
		Transport  string
		ReadSource bool
		Cycle      json.RawMessage `json:"cycle"`
	}](tx.QueryRowContext(ctx, `SELECT body FROM wb_settings WHERE id=? FOR SHARE`, "operations-"+parent.Principal.ProjectID))
	if err != nil || !config.ReadSource || config.Staff != staff || config.Transport != transport {
		return ErrConflict
	}
	if len(config.Cycle) != 0 && string(config.Cycle) != "null" && !IsLocalOperations(parent) {
		return ErrDenied
	}
	if IsLocalOperations(parent) {
		if err = AuthorizeAll(ctx, transactionGate{tx}, parent.Principal, UnionRefs(parent.Sources, parent.Lineage)); err != nil {
			return err
		}
		if parent.Operations == nil || parent.Operations.Review == nil {
			return ErrInvalid
		}
		pending := []Ref{parent.Operations.Review.Selection}
		seen := map[Ref]bool{}
		for len(pending) > 0 {
			ref := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			if seen[ref] {
				continue
			}
			seen[ref] = true
			if len(seen) > MaxSourceRefs {
				return ErrInvalid
			}
			resource, e := (transactionGate{tx}).Resource(ctx, ref.ResourceID)
			if e != nil {
				return e
			}
			if resource.CurrentVersion != ref.Version {
				return ErrConflict
			}
			snap, e := (transactionGate{tx}).Snapshot(ctx, ref.ResourceID, ref.Version)
			if e != nil {
				return e
			}
			pending = append(pending, snap.Dependencies...)
		}
	}
	rows, err := operationsReviewRows(ctx, tx, parent.ID, "", 5001, false)
	if err != nil {
		return err
	}
	if len(rows) != head.Count {
		return ErrConflict
	}
	active := make(map[string]bool, len(activeKeys))
	for _, key := range activeKeys {
		active[key] = true
	}
	for _, row := range rows {
		if row.Blocked != "" {
			continue
		}
		if !active[row.Key] || (row.ExpiresAt != nil && !time.Now().Before(*row.ExpiresAt)) {
			row.Blocked = "原服务动作已经失效。"
			if err = storeOperationsItem(ctx, tx, parent.ID, operationsRowPrefix+row.Key, row); err != nil {
				return err
			}
			continue
		}
		result, err := tx.ExecContext(ctx, `INSERT IGNORE INTO wb_connector_state(id,body) VALUES(?,JSON_OBJECT('confirmation',?))`, "op-action-"+row.Slot, confirm)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			row.Blocked = "同一服务动作已有确认记录。"
			if err = storeOperationsItem(ctx, tx, parent.ID, operationsRowPrefix+row.Key, row); err != nil {
				return err
			}
		}
	}
	head.Confirm = confirm
	if err = storeOperationsItem(ctx, tx, parent.ID, OperationsReviewItem, head); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) OperationsBatchStopped(ctx context.Context, id string) (bool, error) {
	_, err := s.Item(ctx, id, OperationsBatchCancelItem)
	if IsMissing(err) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) StopOperationsBatch(ctx context.Context, p Principal, id string) error {
	r, e := s.Run(ctx, p, id)
	if e != nil {
		return e
	}
	if r.Operation != "operations_batch_confirm" {
		return ErrInvalid
	}
	return s.RecordItem(ctx, id, ItemResult{ID: OperationsBatchCancelItem, State: "succeeded", InputHash: Digest(id), Text: "Cancelled before further submission; existing external results remain queryable."})
}

func (s *Store) OperationsBatchManifest(ctx context.Context, id string) (Run, []OperationsBatchItem, error) {
	run, err := s.ExecutionRun(ctx, id)
	if err != nil || run.Operation != "operations_batch_confirm" || run.ParentRunID == "" {
		return run, nil, ErrDenied
	}
	head, err := s.OperationsReview(ctx, run.ParentRunID)
	if err != nil || head.Confirm != id {
		return run, nil, ErrDenied
	}
	rows, err := operationsReviewRows(ctx, s.DB, run.ParentRunID, "", 5001, false)
	if err != nil || len(rows) != head.Count {
		return run, nil, ErrConflict
	}
	out := make([]OperationsBatchItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, OperationsBatchItem{Key: row.Key, Customer: row.Customer, Message: row.Message, Materials: row.Materials, ExpiresAt: row.ExpiresAt, Blocked: row.Blocked})
	}
	return run, out, nil
}

func (s *Store) OperationsBatchResult(ctx context.Context, run string) (OperationsBatchStatus, error) {
	return operationsItem[OperationsBatchStatus](ctx, s.DB, run, OperationsBatchResultItem, "")
}

func (s *Store) SaveOperationsBatchResult(ctx context.Context, run string, result OperationsBatchStatus) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	previous, err := operationsItem[OperationsBatchStatus](ctx, tx, run, OperationsBatchResultItem, " FOR UPDATE")
	if err != nil && !IsMissing(err) {
		return err
	}
	if previous.Plan != "" && result.Plan != "" && result.Plan != previous.Plan {
		return ErrConflict
	}
	if result.Plan == "" {
		result.Plan = previous.Plan
	}
	if result.Entries == nil {
		result.Entries = map[string]OperationsBatchReceipt{}
	}
	for key, old := range previous.Entries {
		next, found := result.Entries[key]
		if !found || old.State == "sent" || old.State == "failed" && next.State != "sent" ||
			old.State == "pending_confirmation" && next.State == "accepted" ||
			(old.State == "accepted" || old.State == "pending_confirmation") && (next.State == "unknown" || strings.TrimSpace(next.State) == "") {
			result.Entries[key] = old
		}
	}
	if err = storeOperationsItem(ctx, tx, run, OperationsBatchResultItem, result); err != nil {
		return err
	}
	return tx.Commit()
}

// Local edits share the review row/head transaction. The original source and
// frozen generated snapshot are never rewritten, and an old confirmation
// cannot approve the edited body.
func (s *Store) EditLocalOperationsReview(ctx context.Context, p Principal, run, key, digest, text string, revision int) (OperationsReview, OperationsReviewRow, error) {
	var head OperationsReview
	var row OperationsReviewRow
	parent, e := s.Run(ctx, p, run)
	if e != nil || !IsLocalOperations(parent) {
		return head, row, ErrDenied
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return head, row, e
	}
	defer tx.Rollback()
	head, e = operationsItem[OperationsReview](ctx, tx, run, OperationsReviewItem, " FOR UPDATE")
	if e != nil {
		return head, row, e
	}
	if head.Confirm != "" || head.Edit != "" || head.Digest != digest {
		return head, row, ErrConflict
	}
	if e = AuthorizeAll(ctx, transactionGate{tx}, p, UnionRefs(parent.Sources, parent.Lineage)); e != nil {
		return head, row, e
	}
	row, e = operationsItem[OperationsReviewRow](ctx, tx, run, operationsRowPrefix+key, " FOR UPDATE")
	if e != nil {
		return head, row, e
	}
	if row.Revision != revision || !row.Editable || (row.ExpiresAt != nil && !time.Now().Before(*row.ExpiresAt)) {
		return head, row, ErrConflict
	}
	if row.Message == text {
		return head, row, tx.Commit()
	}
	row.Message = text
	row.Revision++
	row.Edited = true
	if e = storeOperationsItem(ctx, tx, run, operationsRowPrefix+key, row); e != nil {
		return head, row, e
	}
	rows, e := operationsReviewRows(ctx, tx, run, "", 5001, false)
	if e != nil {
		return head, row, e
	}
	head.Digest = Digest(rows)
	head.Version = Digest([]string{head.Digest, Digest(rows)})
	if e = storeOperationsItem(ctx, tx, run, OperationsReviewItem, head); e != nil {
		return head, row, e
	}
	return head, row, tx.Commit()
}
