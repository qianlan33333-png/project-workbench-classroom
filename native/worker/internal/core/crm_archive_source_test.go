package core

import (
	"crypto/sha256"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const crmFixtureRows = `[{"id":9007199254740993,"name":"  CRM 原名  "}]`

func archiveFixture(rows string) string {
	return fmt.Sprintf(`{"data":{"contract":"crm-production-read-facts.v1","complete":true,"read_only":true,
		"source_as_of":"2026-11-09T00:00:00Z","source_snapshot_id":"crm-production-fixture",
		"manifest":{"version":"crm-read-facts-manifest.v1","complete":true,"hash_algorithm":"sha256",
		"canonicalization":"go-json-number-preserving-v1","source_as_of":"2026-11-09T00:00:00Z",
		"source_snapshot_id":"crm-production-fixture","collections":{"customers":{"row_count":1,"sha256":"%x"}}},
		"messages":[%s],"customers":%s},"error":null}`, sha256.Sum256([]byte(crmFixtureRows)), strings.Repeat(`{"text":"`+strings.Repeat("x", 60000)+`"},`, 10)+`{}`, rows)
}

func TestCRMArchiveStreamsExactIdentityAndChecksManifest(t *testing.T) {
	spec := JSONSource{ArrayField: "customers", KeyPointer: "/id", Fields: map[string]string{"name": "/name"}}
	var records []Record
	accept := func(r Record) error { records = append(records, r); return nil }
	if err := streamCRMArchive(strings.NewReader(archiveFixture(crmFixtureRows)), spec, accept); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != "9007199254740993" || records[0].Fields["name"] != "  CRM 原名  " {
		t.Fatal("wrong CRM record", records)
	}
	for _, body := range []string{
		archiveFixture(strings.Replace(crmFixtureRows, "原名", "被改", 1)),
		archiveFixture(`[]`),
		strings.Replace(archiveFixture(crmFixtureRows), `"read_only":true`, `"read_only":false`, 1),
		archiveFixture(crmFixtureRows) + ` {}`,
	} {
		if err := streamCRMArchive(strings.NewReader(body), spec, func(Record) error { return nil }); err == nil {
			t.Fatal("invalid archive passed source verification")
		}
	}
}

func TestMySQLCRMArchiveSourceUsesInstalledCLI(t *testing.T) {
	cli := os.Getenv("WORKBENCH_TEST_CRM_CLI")
	if cli == "" {
		t.Skip("installed CRM CLI not configured")
	}
	store, ctx, id := acceptanceStore(t)
	var tampered atomic.Bool
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/open/v1/archive-facts" || r.Header.Get("Authorization") != "Bearer archive-fixture" || r.Header.Get("Idempotency-Key") != "" {
			t.Error("unexpected CRM source request")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var in struct {
			IDs []string `json:"hxc_account_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.IDs) != 1 || in.IDs[0] != "00000000-0000-4000-8000-000000000001" {
			t.Error("wrong account scope")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests.Add(1)
		rows := crmFixtureRows
		if tampered.Load() {
			rows = strings.Replace(rows, "原名", "被改", 1)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, archiveFixture(rows))
	}))
	defer server.Close()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	cert := filepath.Join(dir, "fixture.pem")
	check(os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600))
	t.Setenv("SSL_CERT_FILE", cert)
	check(os.WriteFile(filepath.Join(dir, "profiles.json"), []byte(fmt.Sprintf(`{"current":"fixture","profiles":{"fixture":{"origin":%q}}}`, server.URL)), 0600))
	request := filepath.Join(dir, "request.json")
	check(os.WriteFile(request, []byte(`{"hxc_account_ids":["00000000-0000-4000-8000-000000000001"]}`), 0600))
	t.Setenv("WORKBENCH_SOURCE_CRM_FIXTURE_TOKEN", "archive-fixture")
	config, err := json.Marshal(crmArchiveConfig{CLI: cli, ConfigDir: dir, Origin: server.URL, TokenEnv: "WORKBENCH_SOURCE_CRM_FIXTURE_TOKEN", RequestFile: request})
	check(err)
	t.Setenv("WORKBENCH_SOURCE_CRM_FIXTURE", string(config))
	owner := Principal{Kind: "owner", SessionID: id + "owner"}
	conn := Connection{ID: id + "crm", Name: "CRM CLI fixture", Kind: CRMArchiveSource, SecretEnv: "WORKBENCH_SOURCE_CRM_FIXTURE"}
	check(store.PutConnection(ctx, owner, conn))
	resource := Resource{ID: id + "res", Name: "CRM facts", ConnectionID: conn.ID, MinRecords: 1,
		JSONSource: &JSONSource{ArrayField: "customers", KeyPointer: "/id", Fields: map[string]string{"name": "/name"}},
		TrimFields: []string{"name"}, Sections: []Section{{ID: "main", Fields: []string{"name"}}}}
	check(store.PutResource(ctx, owner, resource))
	run := Run{ID: id + "good", Principal: owner, Destination: resource.ID}
	snap, err := store.SyncSource(ctx, run, func() {})
	check(err)
	ref := Ref{ResourceID: resource.ID, Version: snap.Version, Section: "main"}
	page, err := store.Read(ctx, owner, ref, "", 5)
	check(err)
	if snap.Count != 1 || len(page.Records) != 1 || page.Records[0].ID != "9007199254740993" || page.Records[0].Fields["name"] != "CRM 原名" {
		t.Fatal("CRM record was not published and cleaned", snap.Count)
	}
	_, err = store.SyncSource(ctx, run, func() {})
	check(err)
	if requests.Load() != 1 {
		t.Fatal("completed source was read again")
	}
	tampered.Store(true)
	run.ID = id + "bad"
	if _, err = store.SyncSource(ctx, run, func() {}); err == nil {
		t.Fatal("tampered source was published")
	}
	current, err := store.Resource(ctx, resource.ID)
	check(err)
	if current.CurrentVersion != snap.Version || requests.Load() != 2 {
		t.Fatal("invalid source changed the published version")
	}
}
