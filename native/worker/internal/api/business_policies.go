package api

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workbench-native/internal/core"
)

func (a *Server) businessPolicyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /business-policies", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Store.ListBusinessPolicies(r.Context(), principal(r.Context()), r.URL.Query().Get("resourceId"))
		respond(w, v, err)
	})
	mux.HandleFunc("POST /business-policies", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ResourceID string              `json:"resourceId"`
			Policy     core.BusinessPolicy `json:"policy"`
		}
		if decode(w, r, &in) {
			v, err := a.Store.PutBusinessPolicy(r.Context(), principal(r.Context()), in.ResourceID, in.Policy)
			respond(w, v, err)
		}
	})
}

func (a *Server) businessPolicyTool(s *mcp.Server, bound core.Principal) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "business_lookup",
		Description: "Read current public business facts from a registered live table owned by this project. Use only when a missing current fact matters. Topic is the business category. Hint is an object name or alias; omit it for a short list within the selected category. Empty results mean there is no usable evidence. Never reads customer records. Dates, eligibility and links in answers must be preserved.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		MessageKey
		ResourceID string `json:"resourceId"`
		Topic      string `json:"topic"`
		Hint       string `json:"hint,omitempty"`
	}) (*mcp.CallToolResult, core.BusinessFacts, error) {
		out, err := a.Store.LookupBusinessFacts(ctx, bound, in.ConnectionID, in.EventID, in.ResourceID, in.Topic, in.Hint)
		return nil, out, err
	})
}
