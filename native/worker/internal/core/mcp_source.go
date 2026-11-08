package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This is a normal official MCP call, configured by the owner. Neither the
// endpoint, tool name nor credentials can be supplied by a model invocation.
type mcpSourceResult struct {
	Records   []Record `json:"records"`
	Watermark string   `json:"watermark"`
}

func validMCPEndpoint(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Scheme == "https" || (u.Scheme == "http" && u.Hostname() == "127.0.0.1"))
}

type sourceMCPAuth struct {
	token string
	r     Run
}

func (a sourceMCPAuth) RoundTrip(req *http.Request) (*http.Response, error) {
	q := req.Clone(req.Context())
	q.Header = req.Header.Clone()
	q.Header.Set("Authorization", "Bearer "+a.token)
	q.Header.Set("X-Workbench-Kind", a.r.Principal.Kind)
	q.Header.Set("X-Workbench-Project", a.r.Principal.ProjectID)
	q.Header.Set("X-Workbench-Session", a.r.Principal.SessionID)
	q.Header.Set("X-Workbench-Run", a.r.ID)
	return http.DefaultTransport.RoundTrip(q)
}
func readMCPSource(ctx context.Context, c Connection, r Run) (mcpSourceResult, error) {
	var out mcpSourceResult
	if !validMCPEndpoint(c.Location) || !ValidID(c.SourceTool) {
		return out, ErrInvalid
	}
	token := os.Getenv(c.SecretEnv)
	if len(token) < 32 {
		return out, errors.New("MCP source credential unavailable")
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "workbench-source", Version: "1"}, nil)
	session, e := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: c.Location, HTTPClient: &http.Client{Timeout: 3 * time.Hour, Transport: sourceMCPAuth{token, r}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil)
	if e != nil {
		return out, errors.New("MCP source unavailable")
	}
	defer session.Close()
	result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: c.SourceTool, Arguments: map[string]string{"operationKey": r.ID}})
	if e != nil || result.IsError {
		return out, errors.New("MCP source did not complete; retry uses the original operation key")
	}
	raw, e := json.Marshal(result.StructuredContent)
	if e != nil || len(raw) > 2*1024*1024 {
		return out, ErrInvalid
	}
	if e = json.Unmarshal(raw, &out); e != nil || out.Watermark == "" || len(out.Records) == 0 || len(out.Records) > 2000 {
		return out, ErrInvalid
	}
	return out, nil
}
