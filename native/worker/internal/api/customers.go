package api

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workbench-native/internal/core"
)

// CustomerClient only grants the two customer tools. It never grants a Host
// principal or accepts caller-supplied project/source/scope headers.
type CustomerClient struct {
	TokenEnv string              `json:"tokenEnv"`
	Access   core.CustomerAccess `json:"access"`
	Token    string              `json:"-"`
}

func LoadCustomerClients(raw, hostToken string) ([]CustomerClient, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil // Disabled until sources are explicitly configured.
	}
	var clients []CustomerClient
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&clients); err != nil {
		return nil, errors.New("invalid WORKBENCH_CUSTOMER_CLIENTS JSON")
	}
	if err := d.Decode(new(any)); err != io.EOF || len(clients) > 16 {
		return nil, errors.New("invalid WORKBENCH_CUSTOMER_CLIENTS")
	}
	sources, tokens := map[string]bool{}, map[string]bool{hostToken: true}
	for i := range clients {
		c := &clients[i]
		c.Token = os.Getenv(c.TokenEnv)
		if c.Access.Validate() != nil || c.TokenEnv == "" || len(c.Token) < 32 || tokens[c.Token] || sources[c.Access.Source] {
			return nil, fmt.Errorf("invalid customer client configuration at index %d", i)
		}
		sources[c.Access.Source], tokens[c.Token] = true, true
	}
	return clients, nil
}

type customerAccessKey struct{}

func (a *Server) customerAccess(token string) (core.CustomerAccess, bool) {
	for _, c := range a.CustomerClients {
		if len(c.Token) >= 32 && subtle.ConstantTimeCompare([]byte(token), []byte(c.Token)) == 1 {
			return c.Access, true
		}
	}
	return core.CustomerAccess{}, false
}

type customerToolError struct {
	Code string `json:"code"`
}

// A single success-or-error object keeps structuredContent useful even for
// business refusals; raw SQL errors and customer identifiers are not exposed.
type customerWriteOutput struct {
	CID     string             `json:"cid,omitempty"`
	Created *bool              `json:"created,omitempty"`
	Error   *customerToolError `json:"error,omitempty"`
}

type customerReadOutput struct {
	*core.Customer
	Error *customerToolError `json:"error,omitempty"`
}

func customerError(err error) *customerToolError {
	code := "INTERNAL_ERROR"
	switch {
	case errors.Is(err, core.ErrCustomerIdentityConflict):
		code = "IDENTITY_CONFLICT"
	case errors.Is(err, core.ErrDenied):
		code = "ACCESS_DENIED"
	case errors.Is(err, core.ErrInvalid):
		code = "INVALID_INPUT"
	case errors.Is(err, sql.ErrNoRows):
		code = "NOT_FOUND"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		code = "REQUEST_INTERRUPTED"
	}
	return &customerToolError{Code: code}
}

func customerObject(properties map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func customerOutputSchema(success map[string]any) map[string]any {
	failure := customerObject(map[string]any{"error": customerObject(map[string]any{"code": map[string]any{"type": "string"}}, "code")}, "error")
	return map[string]any{"type": "object", "oneOf": []any{success, failure}}
}

// Use the SDK's raw tool handler here: v1.8's typed AddTool applies defaults by
// round-tripping arbitrary JSON through float64, which changes large integers.
// These two tools have no defaults; validate without rewriting the document.
func addCustomerTool[In, Out any](s *mcp.Server, tool *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) {
	resolve := func(schema any) *jsonschema.Resolved {
		b, err := json.Marshal(schema)
		if err != nil {
			panic(err)
		}
		var definition jsonschema.Schema
		if err = json.Unmarshal(b, &definition); err != nil {
			panic(err)
		}
		resolved, err := definition.Resolve(nil)
		if err != nil {
			panic(err)
		}
		return resolved
	}
	input, output := resolve(tool.InputSchema), resolve(tool.OutputSchema)
	validate := func(raw []byte, schema *jsonschema.Resolved) error {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var value any
		if err := dec.Decode(&value); err != nil {
			return err
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			return core.ErrInvalid
		}
		return schema.Validate(value)
	}
	s.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in In
		var result *mcp.CallToolResult
		var value any
		if validate(req.Params.Arguments, input) != nil || json.Unmarshal(req.Params.Arguments, &in) != nil {
			result = &mcp.CallToolResult{IsError: true}
			value = customerWriteOutput{Error: customerError(core.ErrInvalid)}
		} else {
			var out Out
			var err error
			result, out, err = handler(ctx, req, in)
			if err != nil {
				return nil, err
			}
			value = out
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		if err = validate(raw, output); err != nil {
			return nil, errors.New("invalid customer tool output")
		}
		if result == nil {
			result = &mcp.CallToolResult{}
		}
		result.StructuredContent = json.RawMessage(raw)
		result.Content = []mcp.Content{&mcp.TextContent{Text: string(raw)}}
		return result, nil
	})
}

func (a *Server) customerMCP(access core.CustomerAccess) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "workbench-customer-tools", Version: "0.1.0"}, nil)
	kind := map[string]any{"type": "string", "enum": []string{"union_id", "crm_one_id", "wecom_external_userid", "hxc_user_id"}}
	str := map[string]any{"type": "string", "minLength": 1, "maxLength": 191}
	cid := map[string]any{"type": "string", "pattern": `^cid_[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`}
	object := map[string]any{"type": "object"}
	input := customerObject(map[string]any{
		"identifiers": map[string]any{"type": "array", "minItems": 1, "maxItems": 4, "items": customerObject(map[string]any{"kind": kind, "value": str}, "kind", "value")},
		"data":        object,
	}, "identifiers")
	output := customerOutputSchema(customerObject(map[string]any{"cid": cid, "created": map[string]any{"type": "boolean"}}, "cid", "created"))
	addCustomerTool(s, &mcp.Tool{Name: "customer_upsert", Description: "Resolve exact, scoped customer identifiers, atomically bind/update or create a CID. Conflicting identities reject the whole request. Source and scopes are fixed by credentials; data patches only this source's fields.", InputSchema: input, OutputSchema: output}, func(ctx context.Context, _ *mcp.CallToolRequest, in core.CustomerUpsertInput) (*mcp.CallToolResult, customerWriteOutput, error) {
		out, err := a.CustomerStore.UpsertCustomer(ctx, access, in)
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, customerWriteOutput{Error: customerError(err)}, nil
		}
		return nil, customerWriteOutput{CID: out.CID, Created: &out.Created}, nil
	})
	readOutput := customerOutputSchema(customerObject(map[string]any{
		"cid": cid, "data": map[string]any{"type": "object", "additionalProperties": object},
		"identifiers": map[string]any{"type": "array", "items": customerObject(map[string]any{"kind": kind, "scope": str, "value": str}, "kind", "scope", "value")},
	}, "cid", "data", "identifiers"))
	addCustomerTool(s, &mcp.Tool{Name: "customer_get", Description: "Read a CID's current customer data within credential-bound source and identifier scopes. No writes or automatic merging.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}, InputSchema: customerObject(map[string]any{"cid": cid}, "cid"), OutputSchema: readOutput}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		CID string `json:"cid"`
	}) (*mcp.CallToolResult, customerReadOutput, error) {
		out, err := a.CustomerStore.GetCustomer(ctx, access, in.CID)
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, customerReadOutput{Error: customerError(err)}, nil
		}
		return nil, customerReadOutput{Customer: &out}, nil
	})
	return s
}
