package core

import (
	"context"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMySQLReplyProfileLazyIdentityDeadlineAndScope(t *testing.T) {
	s, owner, route, message := replyFixture(t)
	ctx := t.Context()
	id := route.Principal.ProjectID
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	var calls atomic.Int32
	var slow atomic.Bool
	server := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		srv := mcp.NewServer(&mcp.Implementation{Name: "profile-fixture", Version: "1"}, nil)
		mcp.AddTool(srv, &mcp.Tool{Name: "profile_lookup"}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
			ExternalUserID string `json:"externalUserId"`
		}) (*mcp.CallToolResult, map[string]any, error) {
			calls.Add(1)
			if slow.Load() {
				select {
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				case <-time.After(time.Second):
					return nil, nil, errors.New("slow fixture")
				}
			}
			return nil, map[string]any{"externalUserId": in.ExternalUserID, "records": []Record{{ID: "person", Fields: map[string]any{"nickname": "Synthetic", "profile_fields": map[string]any{"need": "test"}, "unshared": "private"}}}}, nil
		})
		return srv
	}, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer server.Close()
	t.Setenv("WORKBENCH_SOURCE_PROFILE_TOKEN", strings.Repeat("t", 40))
	must(s.PutConnection(ctx, owner, Connection{ID: id, Name: "source", Kind: "mcp", Location: server.URL, SecretEnv: "WORKBENCH_SOURCE_PROFILE_TOKEN", SourceTool: "operations_refresh"}))
	library := id + "lib"
	must(s.PutProject(ctx, owner, Project{ID: library, Name: "library"}))
	resource := Resource{ID: id + "profiles", Name: "profiles", OwnerProject: library, ConnectionID: id, Sections: []Section{{ID: "profile", Name: "Profile", Fields: []string{"nickname", "profile_fields"}}}}
	must(s.PutResource(ctx, owner, resource))
	must(s.SetGrant(ctx, owner, Grant{ResourceID: resource.ID, ProjectID: id, Sections: []string{"profile"}}))
	must(s.BeginSnapshot(ctx, owner, Snapshot{ResourceID: resource.ID, Version: "v1"}))
	must(s.Stage(ctx, owner, resource.ID, "v1", []Record{{ID: "contract", Fields: map[string]any{"nickname": "contract"}}}))
	_, e := s.Publish(ctx, owner, resource.ID, "v1", 1)
	must(e)
	cfg, e := s.ReplySettingsForConnection(ctx, id)
	must(e)
	cfg.Subject = &SubjectBinding{ResourceID: resource.ID, Section: "profile", LookupTool: "profile_lookup"}
	cfg, e = s.PutReplySettings(ctx, owner, cfg)
	must(e)
	_, e = s.DB.ExecContext(ctx, `UPDATE wb_message_routes SET body=JSON_SET(body,'$.autoReply',FALSE,'$.revision',33) WHERE id=?`, route.ID)
	must(e)
	resolveCalls := 0
	resolve := func(_ context.Context, _ Transport, contact string) (string, error) {
		resolveCalls++
		if contact != route.ActorID {
			t.Fatal("wrong contact")
		}
		return "wm-test", nil
	}
	out, e := s.ResolveReplyProfile(ctx, route.Principal, message, resolve)
	must(e)
	if len(out) != 1 || out[0].Fields["unshared"] != nil {
		t.Fatal("missing or unscoped profile")
	}
	sources, e := s.SessionSources(ctx, route.Principal)
	must(e)
	if len(sources) != 1 || sources[0].ResourceID != resource.ID {
		t.Fatal("profile source not recorded")
	}
	restored, e := s.Route(ctx, route.ID)
	must(e)
	if restored.ExternalUserID != "wm-test" || restored.Principal != route.Principal || restored.AutoReply || restored.Revision != 33 {
		t.Fatal("binding changed session or takeover")
	}
	_, e = s.ResolveReplyProfile(ctx, route.Principal, message, resolve)
	must(e)
	if resolveCalls != 1 {
		t.Fatal("identity was not reused")
	}
	conflict := message
	conflict.ExternalUserID = "wm-other"
	before := calls.Load()
	_, e = s.ResolveReplyProfile(ctx, route.Principal, conflict, resolve)
	if !errors.Is(e, ErrConflict) || calls.Load() != before {
		t.Fatal("conflicting identity used")
	}
	_, e = s.ResolveReplyProfile(ctx, Principal{Kind: "project", ProjectID: id, SessionID: "other"}, message, resolve)
	if !errors.Is(e, ErrDenied) {
		t.Fatal("another session read profile")
	}
	off := false
	cfg.ReadProfile = &off
	cfg, e = s.PutReplySettings(ctx, owner, cfg)
	must(e)
	out, e = s.ResolveReplyProfile(ctx, route.Principal, message, resolve)
	must(e)
	if len(out) != 0 || calls.Load() != before {
		t.Fatal("disabled profile read source")
	}
	cfg.ReadProfile = nil
	cfg, e = s.PutReplySettings(ctx, owner, cfg)
	must(e)
	slow.Store(true)
	deadline, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, e = s.ResolveReplyProfile(deadline, route.Principal, message, resolve)
	if e == nil || time.Since(start) > time.Second {
		t.Fatal("profile ignored caller deadline")
	}
	slow.Store(false)
	must(s.SetGrant(ctx, owner, Grant{ResourceID: resource.ID, ProjectID: id, Sections: []string{}}))
	if !errors.Is(s.CheckSession(ctx, route.Principal), ErrDenied) {
		t.Fatal("revoked history source remains usable")
	}
	_, e = s.ResolveReplyProfile(ctx, route.Principal, message, resolve)
	if !errors.Is(e, ErrDenied) {
		t.Fatal("revoked source remains readable")
	}
}
