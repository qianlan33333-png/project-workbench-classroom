package api

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workbench-native/internal/core"
)

type libraryCommand struct {
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
	Text      string `json:"text"`
	received  time.Time
}

type grantRequest struct {
	ResourceID string `json:"resourceId"`
	ProjectID  string `json:"projectId"`
	Mode       string `json:"mode"`
}
type describeRequest struct {
	ResourceID  string `json:"resourceId"`
	Description string `json:"description"`
}

var grantOwner = regexp.MustCompile(`^(?:请)?(?:帮我)?(?:给|为)(.+?)(?:开通|授予|设置)(.+?)的(只读|管理)权限$`)
var revokeOwner = regexp.MustCompile(`^(?:请)?(?:帮我)?(?:撤销|取消|收回)(.+?)对(.+?)的(?:只读|管理)?权限$`)
var createOwner = regexp.MustCompile(`^(?:请)?(?:帮我)?(?:新建|创建)(?:一个|一张)?(数据库|语料库)[：: ]*(.+?)$`)
var quoted = strings.NewReplacer("「", "", "」", "", "“", "", "”", "", "\"", "", "《", "", "》", "")

func commandText(s string) string {
	return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(s), "。！!"))
}
func label(s string) string { return strings.TrimSpace(quoted.Replace(s)) }
func grantMatches(text, project, resource, mode string) bool {
	text = commandText(text)
	if strings.ContainsAny(text, "\n\r") {
		return false
	}
	if m := grantOwner.FindStringSubmatch(text); m != nil {
		return label(m[1]) == project && label(m[2]) == resource && map[string]string{"只读": "read", "管理": "manage"}[m[3]] == mode
	}
	if m := revokeOwner.FindStringSubmatch(text); m != nil {
		return label(m[1]) == project && label(m[2]) == resource && mode == "none"
	}
	return false
}
func createMatches(text string, in core.LibraryInput) bool {
	if strings.ContainsAny(text, "\n\r") {
		return false
	}
	parts := strings.FieldsFunc(commandText(text), func(r rune) bool { return r == '；' || r == ';' })
	if len(parts) == 0 {
		return false
	}
	m := createOwner.FindStringSubmatch(strings.TrimSpace(parts[0]))
	if m == nil || label(m[2]) != in.Name || map[string]string{"数据库": "database", "语料库": "corpus"}[m[1]] != in.Kind {
		return false
	}
	desc := ""
	var cols []string
	seen := map[string]bool{}
	for _, p := range parts[1:] {
		p = strings.TrimSpace(p)
		i := strings.IndexAny(p, "：:")
		if i < 0 {
			return false
		}
		key, value := p[:i], strings.TrimSpace(strings.TrimLeft(p[i:], "：:"))
		if seen[key] || value == "" {
			return false
		}
		seen[key] = true
		switch key {
		case "用途":
			desc = value
		case "字段", "列":
			if cols != nil {
				return false
			}
			cols = strings.FieldsFunc(value, func(r rune) bool { return r == '、' || r == '，' || r == ',' })
		default:
			return false
		}
	}
	if desc != in.Description || len(cols) != len(in.Columns) {
		return false
	}
	for i := range cols {
		if label(cols[i]) != in.Columns[i] {
			return false
		}
	}
	return true
}

func (a *Server) libraryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /resource-metadata", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ResourceID  string `json:"resourceId"`
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if !decode(w, r, &in) {
			return
		}
		v, err := a.Store.UpdateLibraryMetadata(r.Context(), principal(r.Context()), in.ResourceID, in.Name, in.Description)
		respond(w, v, err)
	})
	mux.HandleFunc("GET /resource-permissions", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.Store.ResourcePermissions(r.Context(), principal(r.Context()), r.URL.Query().Get("resourceId"))
		respond(w, struct {
			core.ResourcePermissions
			Locked bool `json:"locked,omitempty"`
		}{v, a.LibraryPermissionsLocked}, e)
	})
	mux.HandleFunc("POST /resource-permissions", func(w http.ResponseWriter, r *http.Request) {
		if a.LibraryPermissionsLocked {
			respond(w, nil, core.ErrDenied)
			return
		}
		var in core.ResourcePermissions
		if !decode(w, r, &in) {
			return
		}
		v, e := a.Store.PutResourcePermissions(r.Context(), principal(r.Context()), in)
		respond(w, v, e)
	})
	// Only the loopback Host can send trusted native message provenance; this
	// route is deliberately absent from the browser API and all MCP schemas.
	mux.HandleFunc("POST /resource-owner-command", func(w http.ResponseWriter, r *http.Request) {
		if !principal(r.Context()).IsOwner() {
			respond(w, nil, core.ErrDenied)
			return
		}
		var in libraryCommand
		if !decode(w, r, &in) {
			return
		}
		if err := a.Store.CheckLibraryPersonal(r.Context(), core.Principal{Kind: "personal", SessionID: in.SessionID}); err != nil {
			respond(w, nil, err)
			return
		}
		if len(in.RequestID) > 250 || len(in.Text) > 8000 || (in.RequestID == "" && in.Text != "") {
			respond(w, nil, core.ErrInvalid)
			return
		}
		in.received = time.Now()
		a.libraryMu.Lock()
		a.libraryEvidence = in
		a.libraryMu.Unlock()
		write(w, map[string]bool{"received": true})
	})
}

func (a *Server) applyLibraryCommand(p core.Principal, match func(string) bool, apply func(string) error) error {
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	ev := a.libraryEvidence
	if p.Kind != "personal" || ev.SessionID != p.SessionID || ev.RequestID == "" || time.Since(ev.received) > 2*time.Minute || !match(ev.Text) {
		return core.ErrDenied
	}
	if err := apply(ev.RequestID); err != nil {
		return err
	}
	a.libraryEvidence = libraryCommand{}
	return nil
}

type documentHost interface {
	LibraryDocuments(context.Context, core.Principal, string, string) (core.LibraryDocuments, error)
}
type libraryDocumentInput struct {
	ResourceID string `json:"resourceId"`
	URI        string `json:"uri,omitempty"`
}

func (a *Server) libraryTools(s *mcp.Server, p core.Principal) {
	if host, ok := a.Host.(documentHost); ok {
		mcp.AddTool(s, &mcp.Tool{Name: "resource_document", Description: "List external corpus documents, or read a URI returned by the same corpus. Respects current resource access; does not call external write tools."},
			func(ctx context.Context, _ *mcp.CallToolRequest, in libraryDocumentInput) (*mcp.CallToolResult, core.LibraryDocuments, error) {
				if !core.ValidID(in.ResourceID) || len(in.URI) > 2048 {
					return nil, core.LibraryDocuments{}, core.ErrInvalid
				}
				v, e := host.LibraryDocuments(ctx, p, in.ResourceID, in.URI)
				return nil, v, e
			})
	}

	mcp.AddTool(s, &mcp.Tool{Name: "resource_item_write", Description: "Create, update or delete one item on a manageable writable library. Use action create/update/delete, itemId on update/delete, fields as a patch, and a unique operationKey. On live business policies delete disables the row. A corpus uses title and markdown. This tool never deletes a library."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in core.LibraryChange) (*mcp.CallToolResult, core.LibraryReceipt, error) {
			v, e := a.Store.ChangeLibraryItem(ctx, p, in)
			return nil, v, e
		})
	mcp.AddTool(s, &mcp.Tool{Name: "resource_policy_list", Description: "Search current rows in a live business policy table; returns at most eight. Only managers can set includeInactive for maintenance, never to answer current policy questions. Internal use only."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in core.PolicyQuery) (*mcp.CallToolResult, core.PolicyPage, error) {
			v, e := a.Store.LibraryPolicies(ctx, p, in)
			return nil, v, e
		})
	if p.Kind != "personal" || a.Store.CheckLibraryPersonal(context.Background(), p) != nil {
		return
	}
	mcp.AddTool(s, &mcp.Tool{Name: "resource_describe", Description: "Update a library's shared purpose description. This text is directory metadata visible to all internal projects; don't include secrets or business records."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in describeRequest) (*mcp.CallToolResult, core.ResourceDiscovery, error) {
			v, e := a.Store.DescribeLibrary(ctx, p, in.ResourceID, in.Description)
			return nil, v, e
		})
	mcp.AddTool(s, &mcp.Tool{Name: "resource_create", Description: "Create an empty workbench database or Markdown corpus. Requires the current owner message explicitly naming the type and name; optional description and columns must match. Example: 新建数据库「运营素材」；用途：记录活动；字段：名称、内容。"},
		func(ctx context.Context, _ *mcp.CallToolRequest, in core.LibraryInput) (*mcp.CallToolResult, core.ResourceDiscovery, error) {
			var v core.ResourceDiscovery
			e := a.applyLibraryCommand(p, func(t string) bool { return createMatches(t, in) }, func(request string) error {
				var err error
				v, err = a.Store.CreateLibrary(ctx, p, request, in)
				return err
			})
			return nil, v, e
		})
	if a.LibraryPermissionsLocked {
		return
	}
	mcp.AddTool(s, &mcp.Tool{Name: "resource_grant", Description: "Set or revoke a project's access for all current sections of a library. Only the owner's explicit current direct instruction can authorize this. mode read, manage, or none. Examples: 给「自动化运营」开通「黄小璨业务口径」的只读权限；撤销「自动化运营」对「黄小璨业务口径」的权限。 Use resource/project list to resolve unique names."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in grantRequest) (*mcp.CallToolResult, core.ResourcePermissions, error) {
			var v core.ResourcePermissions
			if e := a.Store.CheckLibraryPersonal(ctx, p); e != nil {
				return nil, v, e
			}
			r, e := a.Store.Resource(ctx, in.ResourceID)
			if e != nil {
				return nil, v, e
			}
			projects, e := a.Store.Projects(ctx)
			if e != nil {
				return nil, v, e
			}
			resources, e := a.Store.ResourceDirectory(ctx, p)
			if e != nil {
				return nil, v, e
			}
			name := ""
			for _, x := range projects {
				if x.ID == in.ProjectID {
					name = x.Name
					break
				}
			}
			matches := 0
			for _, x := range projects {
				if x.Name == name {
					matches++
				}
			}
			if name == "" || matches != 1 {
				return nil, v, core.ErrInvalid
			}
			matches = 0
			visible := false
			for _, x := range resources {
				if x.Name == r.Name {
					matches++
				}
				if x.ID == r.ID {
					visible = true
				}
			}
			if matches != 1 || !visible {
				return nil, v, core.ErrInvalid
			}
			e = a.applyLibraryCommand(p, func(t string) bool { return grantMatches(t, name, r.Name, in.Mode) }, func(_ string) error {
				grant := core.ResourceAccess{ProjectID: in.ProjectID, Mode: in.Mode}
				if in.Mode != "none" {
					for _, sec := range r.Sections {
						grant.Sections = append(grant.Sections, sec.ID)
					}
				}
				var err error
				v, err = a.Store.SetResourceGrant(ctx, core.Principal{Kind: "owner", SessionID: "library-command"}, in.ResourceID, grant)
				return err
			})
			return nil, v, e
		})
}
