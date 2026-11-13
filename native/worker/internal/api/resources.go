package api

import (
	"context"
	"encoding/json"
	"github.com/go-openapi/jsonpointer"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"strings"
	"unicode/utf8"
	"workbench-native/internal/core"
)

func (a *Server) resourceRoutes(mux *http.ServeMux) {
	a.resourceSyncRoutes(mux)
	mux.HandleFunc("POST /resource-connection", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ResourceID   string `json:"resourceId"`
			ConnectionID string `json:"connectionId"`
			Revision     int64  `json:"revision"`
		}
		if decode(w, r, &in) {
			v, err := a.Store.RebindMySQLResource(r.Context(), principal(r.Context()), in.ResourceID, in.ConnectionID, in.Revision)
			respond(w, v, err)
		}
	})
	mux.HandleFunc("GET /resource-page", func(w http.ResponseWriter, r *http.Request) {
		ref := core.Ref{ResourceID: r.URL.Query().Get("resourceId"), Version: r.URL.Query().Get("version"), Section: r.URL.Query().Get("section")}
		page, e := a.Store.Read(r.Context(), principal(r.Context()), ref, r.URL.Query().Get("after"), 10)
		respond(w, page, e)
	})
	mux.HandleFunc("GET /resource-export", func(w http.ResponseWriter, r *http.Request) {
		p := principal(r.Context())
		ref := core.Ref{ResourceID: r.URL.Query().Get("resourceId"), Version: r.URL.Query().Get("version"), Section: r.URL.Query().Get("section")}
		page, e := a.Store.Read(r.Context(), p, ref, "", 200)
		if e != nil {
			respond(w, nil, e)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+ref.ResourceID+`-`+ref.Section+`.jsonl"`)
		encoder := json.NewEncoder(w)
		for {
			for _, v := range page.Records {
				if e = encoder.Encode(v); e != nil {
					return
				}
			}
			if page.Next == "" {
				return
			}
			page, e = a.Store.Read(r.Context(), p, ref, page.Next, 200)
			if e != nil {
				panic(http.ErrAbortHandler)
			}
		}
	})
}

type SearchInput struct {
	Ref   core.Ref `json:"ref"`
	Query string   `json:"query"`
	After string   `json:"after,omitempty"`
}
type SearchHit struct {
	ID      string `json:"id"`
	Preview string `json:"preview"`
}
type SearchResult struct {
	Ref     core.Ref    `json:"ref"`
	Hits    []SearchHit `json:"hits"`
	Next    string      `json:"next,omitempty"`
	Scanned int         `json:"scanned"`
}
type ExcerptInput struct {
	Ref     core.Ref `json:"ref"`
	ID      string   `json:"id"`
	Pointer string   `json:"pointer"`
	Offset  int      `json:"offset,omitempty"`
	Limit   int      `json:"limit,omitempty"`
}
type ExcerptResult struct {
	Ref     core.Ref `json:"ref"`
	ID      string   `json:"id"`
	Pointer string   `json:"pointer"`
	Text    string   `json:"text"`
	Total   int      `json:"total"`
	Next    *int     `json:"next,omitempty"`
}

func (a *Server) resourceTools(server *mcp.Server, p core.Principal) {
	mcp.AddTool(server, &mcp.Tool{Name: "resource_excerpt", Description: "Read a bounded Unicode text fragment of one field in an allowed fixed resource record. Use JSON Pointer, record id and returned next offset for long knowledge documents; default 2000, maximum 4000 characters. Never reads hidden sections."}, func(ctx context.Context, _ *mcp.CallToolRequest, in ExcerptInput) (*mcp.CallToolResult, ExcerptResult, error) {
		out := ExcerptResult{Ref: in.Ref, ID: in.ID, Pointer: in.Pointer}
		if e := a.Store.CheckMessageKnowledge(ctx, p, in.Ref); e != nil {
			return nil, out, e
		}
		if in.Offset < 0 || in.Limit < 0 || in.Limit > 4000 {
			return nil, out, core.ErrInvalid
		}
		if in.Limit == 0 {
			in.Limit = 2000
		}
		pointer, e := jsonpointer.New(in.Pointer)
		if e != nil {
			return nil, out, core.ErrInvalid
		}
		records, e := a.Store.ReadIDs(ctx, p, in.Ref, []string{in.ID})
		if e != nil {
			return nil, out, e
		}
		if len(records) != 1 {
			return nil, out, core.ErrInvalid
		}
		if e = a.Store.CheckMessageKnowledge(ctx, p, in.Ref); e != nil {
			return nil, out, e
		}
		value, _, e := pointer.Get(records[0].Fields)
		if e != nil {
			return nil, out, core.ErrInvalid
		}
		text, ok := value.(string)
		if !ok {
			raw, err := json.Marshal(value)
			if err != nil {
				return nil, out, err
			}
			text = string(raw)
		}
		runes := []rune(text)
		out.Total = len(runes)
		if in.Offset > len(runes) {
			return nil, out, core.ErrInvalid
		}
		end := min(in.Offset+in.Limit, len(runes))
		out.Text = string(runes[in.Offset:end])
		if end < len(runes) {
			out.Next = &end
		}
		return nil, out, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "resource_search", Description: "Bounded literal search within one allowed snapshot section. Returns small previews and a continuation cursor; does not search hidden fields or load a whole knowledge base."}, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, SearchResult, error) {
		out := SearchResult{Ref: in.Ref, Hits: []SearchHit{}}
		if e := a.Store.CheckMessageKnowledge(ctx, p, in.Ref); e != nil {
			return nil, out, e
		}
		query := strings.ToLower(strings.TrimSpace(in.Query))
		if query == "" || len(query) > 128 {
			return nil, out, core.ErrInvalid
		}
		after := in.After
		for pages := 0; pages < 5; pages++ {
			page, e := a.Store.Read(ctx, p, in.Ref, after, 200)
			if e != nil {
				return nil, out, e
			}
			if e = a.Store.CheckMessageKnowledge(ctx, p, in.Ref); e != nil {
				return nil, out, e
			}
			for _, record := range page.Records {
				out.Scanned++
				raw, _ := json.Marshal(record.Fields)
				text := string(raw)
				if i := strings.Index(strings.ToLower(text), query); i >= 0 {
					runes := []rune(text)
					start := max(0, utf8.RuneCountInString(strings.ToLower(text)[:i])-100)
					out.Hits = append(out.Hits, SearchHit{record.ID, string(runes[start:min(start+240, len(runes))])})
				}
				after = record.ID
				if len(out.Hits) >= 10 {
					out.Next = after
					return nil, out, nil
				}
			}
			if page.Next == "" {
				return nil, out, nil
			}
			after = page.Next
		}
		out.Next = after
		return nil, out, nil
	})
}
