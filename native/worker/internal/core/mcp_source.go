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
	raw, e := callMCPSource(ctx, c, r, c.SourceTool, map[string]string{"operationKey": r.ID})
	if e != nil {
		return out, e
	}
	if e = json.Unmarshal(raw, &out); e != nil || out.Watermark == "" || len(out.Records) == 0 || len(out.Records) > 2000 {
		return out, ErrInvalid
	}
	return out, nil
}

// ReadOperationsDirectory is a fixed read tool on the source registered to the
// project-owned operations resource. Request data cannot choose a URL or tool.
func (s *Store) ReadOperationsDirectory(ctx context.Context, p Principal, resourceID, view string, params map[string]string) (json.RawMessage, error) {
	c, e := s.operationsSourceConnection(ctx, p, resourceID)
	if e != nil {
		return nil, e
	}
	return callMCPSource(ctx, c, Run{Principal: p}, "operations_read", map[string]any{"view": view, "params": params})
}

func (s *Store) OperationsPackageAction(ctx context.Context, run Run, view string, params map[string]string) (json.RawMessage, error) {
	operation := "operations_package"
	if view == "package_edit" {
		operation = "operations_package_edit"
	} else if view == "package_confirm" {
		operation = "operations_package_confirm"
	}
	if run.Operation != operation || !ValidID(run.ID) {
		return nil, ErrDenied
	}
	c, e := s.operationsSourceConnection(ctx, run.Principal, run.Destination)
	if e != nil {
		return nil, e
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	// Once dispatched, cancellation stops subsequent steps. Keep waiting for
	// this source mutation while the project writer still owns its slot.
	settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), 330*time.Second)
	defer cancel()
	return callMCPSource(settle, c, run, "operations_package_action", map[string]any{"view": view, "params": params})
}

func (s *Store) operationsSourceConnection(ctx context.Context, p Principal, resourceID string) (Connection, error) {
	resource, e := s.canWrite(ctx, p, resourceID)
	if e != nil {
		return Connection{}, e
	}
	c, e := s.sourceConnection(ctx, resource)
	if e != nil {
		return Connection{}, e
	}
	if c.Kind != "mcp" || c.SourceTool != "operations_refresh" {
		return Connection{}, ErrDenied
	}
	return c, nil
}

func callMCPSource(ctx context.Context, c Connection, r Run, tool string, args any) (json.RawMessage, error) {
	if !validMCPEndpoint(c.Location) || !ValidID(tool) {
		return nil, ErrInvalid
	}
	token := os.Getenv(c.SecretEnv)
	if len(token) < 32 {
		return nil, errors.New("MCP source credential unavailable")
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "workbench-source", Version: "1"}, nil)
	session, e := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: c.Location, HTTPClient: &http.Client{Timeout: 3 * time.Hour, Transport: sourceMCPAuth{token, r}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil)
	if e != nil {
		return nil, errors.New("MCP source unavailable")
	}
	defer session.Close()
	result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if e != nil || result.IsError {
		return nil, errors.New("MCP source did not complete")
	}
	raw, e := json.Marshal(result.StructuredContent)
	if e != nil || len(raw) > 2*1024*1024 {
		return nil, ErrInvalid
	}
	return raw, nil
}
