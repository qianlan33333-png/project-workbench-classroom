package core

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
)

//go:embed customers.sql
var customerSchema string

// CheckCustomerSchema keeps the customer tools off the shared workbench database.
func (s *Store) CheckCustomerSchema(ctx context.Context) error {
	var total, expected int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(CASE WHEN table_type='BASE TABLE' AND table_name IN ('wb_customers','wb_customer_identifiers') THEN 1 END) FROM information_schema.tables WHERE table_schema=DATABASE()`).Scan(&total, &expected)
	if err != nil || total != 2 || expected != 2 {
		return errors.New("customer database must contain exactly the two customer tables")
	}
	return nil
}

// MigrateCustomers initializes an empty or partially initialized dedicated database.
func (s *Store) MigrateCustomers(ctx context.Context) error {
	var unexpected int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND (table_type<>'BASE TABLE' OR table_name NOT IN ('wb_customers','wb_customer_identifiers'))`).Scan(&unexpected); err != nil {
		return err
	}
	if unexpected != 0 {
		return errors.New("customer database contains unrelated tables")
	}
	for _, statement := range strings.Split(customerSchema, ";") {
		if strings.TrimSpace(statement) != "" {
			if _, err := s.DB.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
	}
	return s.CheckCustomerSchema(ctx)
}

var ErrCustomerIdentityConflict = errors.New("customer identity conflict")

// CustomerAccess is trusted server configuration, never a tool argument or a
// session header. Scopes selects the identifier kinds this source may bind/read.
type CustomerAccess struct {
	Source      string            `json:"source"`
	Scopes      map[string]string `json:"scopes"`
	ReadSources []string          `json:"readSources,omitempty"`
	ReadOnly    bool              `json:"readOnly,omitempty"`
}

func (a CustomerAccess) Validate() error {
	if !ValidID(a.Source) || len(a.Scopes) == 0 || len(a.Scopes) > 4 || len(a.ReadSources) > 16 {
		return ErrDenied
	}
	nonUnion := false
	for kind, scope := range a.Scopes {
		if !customerIdentifierKind(kind) || !ValidID(scope) {
			return ErrDenied
		}
		nonUnion = nonUnion || kind != "union_id"
	}
	for _, source := range a.ReadSources {
		if !ValidID(source) {
			return ErrDenied
		}
	}
	if !nonUnion {
		return ErrDenied
	}
	return nil
}

func customerIdentifierKind(kind string) bool {
	return kind == "union_id" || kind == "crm_one_id" || kind == "wecom_external_userid" || kind == "hxc_user_id"
}

type CustomerIdentifier struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type CustomerUpsertInput struct {
	Identifiers []CustomerIdentifier       `json:"identifiers"`
	Data        map[string]json.RawMessage `json:"data,omitempty"`
}

type CustomerUpsertResult struct {
	CID     string `json:"cid"`
	Created bool   `json:"created"`
}

type CustomerIdentifierView struct {
	Kind  string `json:"kind"`
	Scope string `json:"scope"`
	Value string `json:"value"`
}

type Customer struct {
	CID         string                                `json:"cid"`
	Identifiers []CustomerIdentifierView              `json:"identifiers"`
	Data        map[string]map[string]json.RawMessage `json:"data"`
}

const customerDataLimit = 64 * 1024

func validateCustomerInput(a CustomerAccess, in CustomerUpsertInput) error {
	if err := a.Validate(); err != nil || a.ReadOnly {
		return ErrDenied
	}
	if len(in.Identifiers) == 0 || len(in.Identifiers) > 4 {
		return ErrInvalid
	}
	seen, nonUnion := map[string]bool{}, false
	for _, id := range in.Identifiers {
		if a.Scopes[id.Kind] == "" || seen[id.Kind] || id.Value == "" || !utf8.ValidString(id.Value) || utf8.RuneCountInString(id.Value) > 191 || strings.TrimSpace(id.Value) != id.Value || strings.ContainsFunc(id.Value, unicode.IsControl) {
			return ErrInvalid
		}
		seen[id.Kind] = true
		nonUnion = nonUnion || id.Kind != "union_id"
	}
	if !nonUnion {
		return ErrInvalid
	}
	b, err := json.Marshal(in.Data)
	if err != nil || len(b) > customerDataLimit {
		return ErrInvalid
	}
	return nil
}

// UpsertCustomer is synchronous: only a committed transaction returns a CID.
// Immutable mappings and a unique key arbitrate races across worker processes.
func (s *Store) UpsertCustomer(ctx context.Context, a CustomerAccess, in CustomerUpsertInput) (CustomerUpsertResult, error) {
	if err := validateCustomerInput(a, in); err != nil {
		return CustomerUpsertResult{}, err
	}
	ids := slices.Clone(in.Identifiers)
	slices.SortFunc(ids, func(x, y CustomerIdentifier) int { return strings.Compare(x.Kind, y.Kind) })
	for attempt := 0; ; attempt++ {
		out, err := s.upsertCustomer(ctx, a, ids, in.Data)
		var dbErr *mysql.MySQLError
		if attempt >= 7 || !errors.As(err, &dbErr) || (dbErr.Number != 1062 && dbErr.Number != 1213 && dbErr.Number != 1205) {
			return out, err
		}
		// Only retry definite transaction failures. Connection/commit ambiguity
		// is returned to the caller; a later call with the same IDs is safe.
		timer := time.NewTimer(time.Duration(attempt+1) * 5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return CustomerUpsertResult{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *Store) upsertCustomer(ctx context.Context, a CustomerAccess, ids []CustomerIdentifier, patch map[string]json.RawMessage) (CustomerUpsertResult, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return CustomerUpsertResult{}, err
	}
	defer tx.Rollback()
	cid := ""
	missing := []CustomerIdentifier{}
	for _, id := range ids {
		var found string
		err = tx.QueryRowContext(ctx, `SELECT cid FROM wb_customer_identifiers WHERE kind=? AND scope=? AND value=?`, id.Kind, a.Scopes[id.Kind], id.Value).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			missing = append(missing, id)
			continue
		}
		if err != nil {
			return CustomerUpsertResult{}, err
		}
		if cid != "" && cid != found {
			return CustomerUpsertResult{}, ErrCustomerIdentityConflict
		}
		cid = found
	}
	created := cid == ""
	if created {
		cid = "cid_" + uuid.NewString()
		if _, err = tx.ExecContext(ctx, `INSERT INTO wb_customers(cid,data) VALUES(?,JSON_OBJECT())`, cid); err != nil {
			return CustomerUpsertResult{}, err
		}
	}
	// Serialize both profile patches and adding a UnionID to an existing CID.
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM wb_customers WHERE cid=? FOR UPDATE`, cid).Scan(&raw); err != nil {
		return CustomerUpsertResult{}, err
	}
	for _, id := range ids {
		if id.Kind != "union_id" {
			continue
		}
		var different int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_customer_identifiers WHERE cid=? AND kind='union_id' AND scope=? AND value<>?`, cid, a.Scopes[id.Kind], id.Value).Scan(&different); err != nil {
			return CustomerUpsertResult{}, err
		}
		if different != 0 {
			return CustomerUpsertResult{}, ErrCustomerIdentityConflict
		}
	}
	for _, id := range missing {
		if _, err = tx.ExecContext(ctx, `INSERT INTO wb_customer_identifiers(kind,scope,value,cid) VALUES(?,?,?,?)`, id.Kind, a.Scopes[id.Kind], id.Value, cid); err != nil {
			return CustomerUpsertResult{}, err
		}
	}
	data := map[string]map[string]json.RawMessage{}
	if err = json.Unmarshal(raw, &data); err != nil {
		return CustomerUpsertResult{}, err
	}
	if data[a.Source] == nil {
		data[a.Source] = map[string]json.RawMessage{}
	}
	for field, value := range patch {
		data[a.Source][field] = value
	}
	next, err := json.Marshal(data)
	if err != nil || len(next) > customerDataLimit {
		return CustomerUpsertResult{}, ErrInvalid
	}
	if _, err = tx.ExecContext(ctx, `UPDATE wb_customers SET data=? WHERE cid=?`, next, cid); err != nil {
		return CustomerUpsertResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return CustomerUpsertResult{}, err
	}
	return CustomerUpsertResult{CID: cid, Created: created}, nil
}

func (s *Store) GetCustomer(ctx context.Context, a CustomerAccess, cid string) (Customer, error) {
	if err := a.Validate(); err != nil {
		return Customer{}, err
	}
	if !strings.HasPrefix(cid, "cid_") || len(cid) != 40 {
		return Customer{}, ErrInvalid
	}
	if _, err := uuid.Parse(cid[4:]); err != nil {
		return Customer{}, ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return Customer{}, err
	}
	defer tx.Rollback()
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM wb_customers WHERE cid=?`, cid).Scan(&raw); err != nil {
		return Customer{}, err
	}
	data := map[string]map[string]json.RawMessage{}
	if err = json.Unmarshal(raw, &data); err != nil {
		return Customer{}, err
	}
	out := Customer{CID: cid, Data: map[string]map[string]json.RawMessage{}, Identifiers: []CustomerIdentifierView{}}
	for source, fields := range data {
		if source == a.Source || slices.Contains(a.ReadSources, source) {
			out.Data[source] = fields
		}
	}
	if len(out.Data) == 0 {
		return Customer{}, sql.ErrNoRows // Do not reveal another source's customer existence.
	}
	rows, err := tx.QueryContext(ctx, `SELECT kind,scope,value FROM wb_customer_identifiers WHERE cid=? ORDER BY kind,scope,value`, cid)
	if err != nil {
		return Customer{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id CustomerIdentifierView
		if err = rows.Scan(&id.Kind, &id.Scope, &id.Value); err != nil {
			return Customer{}, err
		}
		if a.Scopes[id.Kind] == id.Scope {
			out.Identifiers = append(out.Identifiers, id)
		}
	}
	if err = rows.Err(); err != nil {
		return Customer{}, err
	}
	if err = rows.Close(); err != nil {
		return Customer{}, err
	}
	if err = tx.Commit(); err != nil {
		return Customer{}, err
	}
	return out, nil
}
