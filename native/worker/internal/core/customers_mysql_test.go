package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
)

func customerAcceptanceStore(t *testing.T) (*Store, context.Context, string) {
	t.Helper()
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("real MySQL settings not supplied")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || !strings.HasPrefix(cfg.DBName, "workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	if err = s.MigrateCustomers(ctx); err != nil {
		t.Fatal(err)
	}
	return s, ctx, "cidtest-" + uuid.NewString()
}

func customerFixture(prefix string) (CustomerAccess, CustomerAccess) {
	return CustomerAccess{Source: prefix + "crm", Scopes: map[string]string{"union_id": prefix + "platform", "crm_one_id": prefix + "crm", "wecom_external_userid": prefix + "corp"}},
		CustomerAccess{Source: prefix + "hxc", Scopes: map[string]string{"union_id": prefix + "platform", "hxc_user_id": prefix + "app"}}
}
func customerInput(ids ...string) CustomerUpsertInput {
	in := CustomerUpsertInput{}
	for i := 0; i < len(ids); i += 2 {
		in.Identifiers = append(in.Identifiers, CustomerIdentifier{Kind: ids[i], Value: ids[i+1]})
	}
	return in
}
func customerPatch(values map[string]any) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range values {
		out[k], _ = json.Marshal(v)
	}
	return out
}
func putCustomer(t *testing.T, s *Store, ctx context.Context, a CustomerAccess, in CustomerUpsertInput) CustomerUpsertResult {
	t.Helper()
	out, err := s.UpsertCustomer(ctx, a, in)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func getCustomer(t *testing.T, s *Store, ctx context.Context, a CustomerAccess, cid string) Customer {
	t.Helper()
	out, err := s.GetCustomer(ctx, a, cid)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMySQLCustomerCrossSourceAndRepeat(t *testing.T) {
	s, ctx, prefix := customerAcceptanceStore(t)
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint("hxc_first_", reverse), func(t *testing.T) {
			crm, hxc := customerFixture(prefix + fmt.Sprint(reverse))
			c := customerInput("union_id", "U1", "crm_one_id", "C1", "wecom_external_userid", "W1")
			c.Data = customerPatch(map[string]any{"nickname": "CRM name", "preserve": "original", "large": json.Number("9007199254740993")})
			h := customerInput("union_id", "U1", "hxc_user_id", "H1")
			h.Data = customerPatch(map[string]any{"nickname": "HXC name"})
			a, b, ai, bi := crm, hxc, c, h
			if reverse {
				a, b, ai, bi = hxc, crm, h, c
			}
			first := putCustomer(t, s, ctx, a, ai)
			second := putCustomer(t, s, ctx, b, bi)
			if !first.Created || second.Created || first.CID != second.CID {
				t.Fatalf("cross-source resolve: %+v %+v", first, second)
			}
			repeated := putCustomer(t, s, ctx, crm, c)
			if repeated.Created || repeated.CID != first.CID {
				t.Fatal("duplicate created another customer")
			}
			c.Data = customerPatch(map[string]any{"nickname": "updated", "nullable": nil, "nested": map[string]any{"a": 1}, "items": []int{1, 2}})
			putCustomer(t, s, ctx, crm, c)
			c.Data = customerPatch(map[string]any{"nested": map[string]any{"b": 2}, "items": []int{3}})
			putCustomer(t, s, ctx, crm, c)
			result := getCustomer(t, s, ctx, crm, first.CID)
			if len(result.Data) != 1 || len(result.Identifiers) != 3 {
				t.Fatalf("scope leak: %+v", result)
			}
			fields := result.Data[crm.Source]
			for key, want := range map[string]string{"nickname": `"updated"`, "preserve": `"original"`, "large": "9007199254740993", "nullable": "null", "items": "[3]"} {
				if string(fields[key]) != want {
					t.Errorf("%s=%s want %s", key, fields[key], want)
				}
			}
			var nested map[string]int
			_ = json.Unmarshal(fields["nested"], &nested)
			if len(nested) != 1 || nested["b"] != 2 {
				t.Fatal("nested patch did not replace field")
			}
			hread := getCustomer(t, s, ctx, hxc, first.CID)
			if len(hread.Data) != 1 || string(hread.Data[hxc.Source]["nickname"]) != `"HXC name"` || len(hread.Identifiers) != 2 {
				t.Fatal("other source overwritten or identifiers exposed")
			}
			crm.ReadSources = []string{hxc.Source}
			all := getCustomer(t, s, ctx, crm, first.CID)
			if len(all.Data) != 2 || len(all.Identifiers) != 3 {
				t.Fatal("explicit read sources not respected")
			}
		})
	}
}

func TestMySQLCustomerLateUnionAndConflict(t *testing.T) {
	s, ctx, prefix := customerAcceptanceStore(t)
	crm, hxc := customerFixture(prefix)
	first := putCustomer(t, s, ctx, crm, customerInput("crm_one_id", "C1"))
	late := putCustomer(t, s, ctx, crm, customerInput("union_id", "U1", "crm_one_id", "C1"))
	if first.CID != late.CID || late.Created {
		t.Fatal("late union created another customer")
	}
	joined := putCustomer(t, s, ctx, hxc, customerInput("union_id", "U1", "hxc_user_id", "H1"))
	if joined.CID != first.CID {
		t.Fatal("late union not shared")
	}
	second := putCustomer(t, s, ctx, crm, customerInput("union_id", "U2", "crm_one_id", "C2", "wecom_external_userid", "W2"))
	before := getCustomer(t, s, ctx, crm, first.CID)
	beforeJSON, _ := json.Marshal(before)
	for _, in := range []CustomerUpsertInput{
		customerInput("union_id", "U1", "crm_one_id", "C2", "wecom_external_userid", "never-insert"),
		customerInput("union_id", "NEW", "crm_one_id", "C1", "wecom_external_userid", "never-insert"),
		customerInput("crm_one_id", "C1", "wecom_external_userid", "W2"),
	} {
		in.Data = customerPatch(map[string]any{"must_not_write": true})
		if _, err := s.UpsertCustomer(ctx, crm, in); !errors.Is(err, ErrCustomerIdentityConflict) {
			t.Fatalf("expected conflict: %v", err)
		}
	}
	after := getCustomer(t, s, ctx, crm, first.CID)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("conflict changed customer")
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_customer_identifiers WHERE scope=? AND value=?`, crm.Scopes["wecom_external_userid"], "never-insert").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial identity write", count, err)
	}
	if second.CID == first.CID {
		t.Fatal("unrelated customers merged")
	}
}

func TestMySQLCustomerConcurrentSameIdentity(t *testing.T) {
	s, ctx, prefix := customerAcceptanceStore(t)
	crm, hxc := customerFixture(prefix)
	var before int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_customers`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	// Independent pools exercise database arbitration, not a process mutex.
	other, err := Open(ctx, os.Getenv("WORKBENCH_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.DB.Close()
	const n = 32
	type result struct {
		out CustomerUpsertResult
		err error
	}
	results := make(chan result, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			a, db, in := crm, s, customerInput("union_id", "shared", "crm_one_id", "C")
			if i%2 == 1 {
				a, db, in = hxc, other, customerInput("union_id", "shared", "hxc_user_id", "H")
			}
			in.Data = customerPatch(map[string]any{fmt.Sprint("field_", i): i})
			out, e := db.UpsertCustomer(ctx, a, in)
			results <- result{out, e}
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	created, cid := 0, ""
	for r := range results {
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.out.Created {
			created++
		}
		if cid != "" && cid != r.out.CID {
			t.Fatal("race created different CIDs")
		}
		cid = r.out.CID
	}
	if created != 1 {
		t.Fatalf("created=%d", created)
	}
	crm.ReadSources = []string{hxc.Source}
	out := getCustomer(t, s, ctx, crm, cid)
	if len(out.Data[crm.Source]) != n/2 || len(out.Data[hxc.Source]) != n/2 {
		t.Fatalf("lost concurrent patches: %d/%d", len(out.Data[crm.Source]), len(out.Data[hxc.Source]))
	}
	var count int
	if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_customers`).Scan(&count); err != nil || count-before != 1 {
		t.Fatal("orphan customer from rolled back race", count, err)
	}
}

func TestMySQLCustomerConcurrentUnionConflict(t *testing.T) {
	s, ctx, prefix := customerAcceptanceStore(t)
	crm, _ := customerFixture(prefix)
	first := putCustomer(t, s, ctx, crm, customerInput("crm_one_id", "shared"))
	const n = 12
	results := make(chan error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			in := customerInput("crm_one_id", "shared", "union_id", fmt.Sprint("U", i), "wecom_external_userid", fmt.Sprint("W", i))
			_, err := s.UpsertCustomer(ctx, crm, in)
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	ok, conflicts := 0, 0
	for err := range results {
		if err == nil {
			ok++
		} else if errors.Is(err, ErrCustomerIdentityConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflicts != n-1 {
		t.Fatalf("success=%d conflict=%d", ok, conflicts)
	}
	out := getCustomer(t, s, ctx, crm, first.CID)
	if len(out.Identifiers) != 3 {
		t.Fatal("conflict persisted extra identifiers", out.Identifiers)
	}
}

func TestMySQLCustomerScopesAndReadAccess(t *testing.T) {
	s, ctx, prefix := customerAcceptanceStore(t)
	crm, hxc := customerFixture(prefix)
	first := putCustomer(t, s, ctx, crm, customerInput("union_id", "Case", "crm_one_id", "A"))
	lower := putCustomer(t, s, ctx, crm, customerInput("union_id", "case", "crm_one_id", "a"))
	if first.CID == lower.CID {
		t.Fatal("case-folded identity")
	}
	hxc.Scopes["union_id"] = prefix + "other-platform"
	separate := putCustomer(t, s, ctx, hxc, customerInput("union_id", "Case", "hxc_user_id", "A"))
	if separate.CID == first.CID {
		t.Fatal("UnionID matched across platforms")
	}
	if _, err := s.GetCustomer(ctx, hxc, first.CID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("ungranted source visible", err)
	}
	hxc.ReadSources = []string{crm.Source}
	out := getCustomer(t, s, ctx, hxc, first.CID)
	if len(out.Identifiers) != 0 || len(out.Data) != 1 {
		t.Fatal("scope leak", out)
	}
	hxc.ReadOnly = true
	if _, err := s.UpsertCustomer(ctx, hxc, customerInput("hxc_user_id", "new")); !errors.Is(err, ErrDenied) {
		t.Fatal("readonly wrote", err)
	}
	for _, in := range []CustomerUpsertInput{customerInput("union_id", "only"), customerInput("hxc_user_id", "foreign"), customerInput("crm_one_id", "A", "crm_one_id", "B"), customerInput("crm_one_id", " A"), customerInput("crm_one_id", "bad\x00id")} {
		if _, err := s.UpsertCustomer(ctx, crm, in); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid input accepted", err)
		}
	}
}

func TestMySQLCustomerBudgetFailureRollsBack(t *testing.T) {
	s, ctx, prefix := customerAcceptanceStore(t)
	crm, _ := customerFixture(prefix)
	in := customerInput("crm_one_id", "C")
	in.Data = customerPatch(map[string]any{"first": strings.Repeat("a", 40000)})
	first := putCustomer(t, s, ctx, crm, in)
	in = customerInput("crm_one_id", "C", "wecom_external_userid", "rollback")
	in.Data = customerPatch(map[string]any{"second": strings.Repeat("b", 40000)})
	if _, err := s.UpsertCustomer(ctx, crm, in); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversized combined profile accepted", err)
	}
	out := getCustomer(t, s, ctx, crm, first.CID)
	if len(out.Identifiers) != 1 || len(out.Data[crm.Source]) != 1 {
		t.Fatal("late failure did not roll back entire transaction")
	}
}
