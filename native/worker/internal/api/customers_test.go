package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workbench-native/internal/core"
)

type customerTestTransport struct{ token string }

func (c customerTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header = req.Header.Clone()
	r.Header.Set("Authorization", "Bearer "+c.token)
	// Deliberately forged Host and source headers must grant nothing.
	r.Header.Set("X-Workbench-Kind", "owner")
	r.Header.Set("X-Workbench-Session", "spoofed-owner")
	r.Header.Set("X-Workbench-Source", "foreign")
	return http.DefaultTransport.RoundTrip(r)
}
func customerSession(t *testing.T, endpoint, token string) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "customer-acceptance", Version: "1"}, nil)
	session, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: endpoint + "/mcp", HTTPClient: &http.Client{Transport: customerTestTransport{token: token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}
func customerTestClients(prefix string) []CustomerClient {
	return []CustomerClient{
		{Token: strings.Repeat("c", 40), Access: core.CustomerAccess{Source: prefix + "crm", Scopes: map[string]string{"union_id": prefix + "platform", "crm_one_id": prefix + "crm", "wecom_external_userid": prefix + "corp"}}},
		{Token: strings.Repeat("h", 40), Access: core.CustomerAccess{Source: prefix + "hxc", Scopes: map[string]string{"union_id": prefix + "platform", "hxc_user_id": prefix + "app"}}},
	}
}
func customerCall(t *testing.T, session *mcp.ClientSession, name string, args any) (*mcp.CallToolResult, map[string]json.RawMessage) {
	t.Helper()
	out, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(out.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]json.RawMessage
	if err = json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return out, v
}
func resultCID(t *testing.T, result map[string]json.RawMessage) string {
	t.Helper()
	var cid string
	if err := json.Unmarshal(result["cid"], &cid); err != nil || cid == "" {
		t.Fatalf("missing CID: %s %v", result, err)
	}
	return cid
}

func TestCustomerMCPToolsSchemasAndAuthentication(t *testing.T) {
	a := &Server{Token: strings.Repeat("o", 40), CustomerClients: customerTestClients("schema")}
	host := httptest.NewServer(a.Handler())
	defer host.Close()
	session := customerSession(t, host.URL, a.CustomerClients[0].Token)
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 2 {
		t.Fatalf("credential exposed %d tools", len(listed.Tools))
	}
	for _, tool := range listed.Tools {
		if (tool.Name != "customer_upsert" && tool.Name != "customer_get") || tool.InputSchema == nil || tool.OutputSchema == nil {
			t.Fatalf("unexpected tool contract: %+v", tool)
		}
	}
	for _, args := range []any{
		map[string]any{},
		map[string]any{"identifiers": []any{}},
		map[string]any{"identifiers": []any{map[string]any{"kind": "crm_one_id", "value": "C", "scope": "spoof"}}},
		map[string]any{"identifiers": []any{map[string]any{"kind": "crm_one_id", "value": "C"}}, "source": "foreign"},
		map[string]any{"identifiers": []any{map[string]any{"kind": "crm_one_id", "value": "C"}}, "data": []int{1}},
	} {
		out, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "customer_upsert", Arguments: args})
		if err == nil && (out == nil || !out.IsError) {
			t.Fatal("schema accepted invalid or forged input", args)
		}
	}
	// A source credential cannot invoke unrelated tools or Host REST routes.
	out, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "project_list", Arguments: map[string]any{}})
	if err == nil && (out == nil || !out.IsError) {
		t.Fatal("source credential invoked Host tool")
	}
	for _, path := range []string{"/projects", "/connections", "/resources"} {
		req, _ := http.NewRequest(http.MethodGet, host.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+a.CustomerClients[0].Token)
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
		if res.StatusCode != 403 {
			t.Fatal("Host route allowed", path, res.StatusCode)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, host.URL+"/mcp", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer invalid")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("invalid credential accepted")
	}
}

func TestCustomerClientConfiguration(t *testing.T) {
	t.Setenv("WORKBENCH_CUSTOMER_TEST_CRM", strings.Repeat("c", 40))
	t.Setenv("WORKBENCH_CUSTOMER_TEST_HXC", strings.Repeat("h", 40))
	clients := customerTestClients("config")
	clients[0].TokenEnv = "WORKBENCH_CUSTOMER_TEST_CRM"
	clients[1].TokenEnv = "WORKBENCH_CUSTOMER_TEST_HXC"
	raw, _ := json.Marshal(clients)
	got, err := LoadCustomerClients(string(raw), strings.Repeat("o", 40))
	if err != nil || len(got) != 2 || got[0].Token != clients[0].Token {
		t.Fatal("config failed", err)
	}
	disabled, err := LoadCustomerClients("", strings.Repeat("o", 40))
	if err != nil || len(disabled) != 0 {
		t.Fatal("default not disabled")
	}
	for _, bad := range []string{`[{"token":"not-supported"}]`, string(raw) + ` {}`, `[{"tokenEnv":"MISSING","access":{"source":"crm","scopes":{"crm_one_id":"corp"}}}]`} {
		if _, err := LoadCustomerClients(bad, strings.Repeat("o", 40)); err == nil {
			t.Fatal("bad config accepted")
		}
	}
	if _, err = LoadCustomerClients(string(raw), clients[0].Token); err == nil {
		t.Fatal("Host token reused")
	}
	clients[1].TokenEnv = clients[0].TokenEnv
	raw, _ = json.Marshal(clients)
	if _, err = LoadCustomerClients(string(raw), strings.Repeat("o", 40)); err == nil {
		t.Fatal("ambiguous credential accepted")
	}
}

func TestMySQLCustomerMCPRoundTrip(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated real MySQL not supplied")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx := context.Background()
	store, err := core.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	if err = store.MigrateCustomers(ctx); err != nil {
		t.Fatal(err)
	}
	prefix := "mcp" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	a := &Server{CustomerStore: store, Token: strings.Repeat("o", 40), CustomerClients: customerTestClients(prefix)}
	host := httptest.NewServer(a.Handler())
	defer host.Close()
	crm := customerSession(t, host.URL, a.CustomerClients[0].Token)
	hxc := customerSession(t, host.URL, a.CustomerClients[1].Token)
	input := json.RawMessage(`{"identifiers":[{"kind":"union_id","value":"U"},{"kind":"crm_one_id","value":"C"},{"kind":"wecom_external_userid","value":"W"}],"data":{"nickname":"CRM","large":9007199254740993}}`)
	out, value := customerCall(t, crm, "customer_upsert", input)
	if out.IsError || string(value["created"]) != "true" {
		t.Fatalf("write failed: %+v", out)
	}
	cid := resultCID(t, value)
	out, repeated := customerCall(t, crm, "customer_upsert", input)
	if out.IsError || resultCID(t, repeated) != cid || string(repeated["created"]) != "false" {
		t.Fatal("repeat not idempotent")
	}
	out, joined := customerCall(t, hxc, "customer_upsert", json.RawMessage(`{"identifiers":[{"kind":"union_id","value":"U"},{"kind":"hxc_user_id","value":"H"}],"data":{"nickname":"HXC"}}`))
	if out.IsError || resultCID(t, joined) != cid {
		t.Fatal("cross-source MCP write split identity")
	}
	out, _ = customerCall(t, crm, "customer_get", map[string]any{"cid": cid})
	if out.IsError || len(out.Content) != 1 {
		t.Fatal("read failed or lacks text fallback")
	}
	text, ok := out.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(text.Text, "9007199254740993") {
		t.Fatal("MCP rounded integer or lost text fallback", out.Content)
	}
	var read struct {
		Data        map[string]map[string]json.RawMessage `json:"data"`
		Identifiers []core.CustomerIdentifierView         `json:"identifiers"`
	}
	if err = json.Unmarshal([]byte(text.Text), &read); err != nil {
		t.Fatal(err)
	}
	if len(read.Data) != 1 || len(read.Identifiers) != 3 || string(read.Data[a.CustomerClients[0].Access.Source]["nickname"]) != `"CRM"` {
		t.Fatal("source read leaked or patch overwritten")
	}
	out, value = customerCall(t, crm, "customer_upsert", json.RawMessage(`{"identifiers":[{"kind":"union_id","value":"DIFFERENT"},{"kind":"crm_one_id","value":"C"}],"data":{"nickname":"bad"}}`))
	if !out.IsError || !strings.Contains(string(value["error"]), "IDENTITY_CONFLICT") || value["cid"] != nil {
		t.Fatalf("missing structured conflict: %+v", out)
	}
	out, value = customerCall(t, crm, "customer_get", map[string]any{"cid": "cid_" + uuid.NewString()})
	if !out.IsError || !strings.Contains(string(value["error"]), "NOT_FOUND") {
		t.Fatal("missing structured NOT_FOUND")
	}
	if err = store.CheckCustomerSchema(ctx); err != nil {
		t.Fatal(err)
	}
}
