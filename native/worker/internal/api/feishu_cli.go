package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workbench-native/internal/core"
)

func (a *Server) feishuCLITool(s *mcp.Server, p core.Principal) {
	type input struct {
		Operation string            `json:"operation" jsonschema:"document, search_documents, drive_files, wiki_node, base_records, spreadsheet, presentation, agenda, tasks, meeting, meeting_note, minutes, chats, chat_messages, whiteboard, markdown, or mindnote"`
		Params    map[string]string `json:"params,omitempty" jsonschema:"Named CLI parameters for the selected read, such as doc, query, node-token, base-token, table-id, spreadsheet-token, range, presentation, meeting-ids, minute-tokens, chat-id, or file-token"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "feishu_cli_read",
		Description: "Read using the owner's linked Feishu CLI user identity. Uses fixed read-only commands for documents, Base, sheets, calendars, tasks, Wiki, meetings, Minutes and conversations.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		OutputSchema: &jsonschema.Schema{
			Type: "object", Required: []string{"data"},
			Properties: map[string]*jsonschema.Schema{"data": {}},
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in input) (*mcp.CallToolResult, any, error) {
		config, err := a.personalConfig(ctx)
		if err != nil || p.Kind != "personal" || config.SessionID != p.SessionID {
			return nil, nil, core.ErrDenied
		}
		raw, err := json.Marshal(in)
		if err != nil || len(raw) > 8192 {
			return nil, nil, core.ErrInvalid
		}
		token := os.Getenv("WORKBENCH_CONNECTOR_TOKEN")
		if len(token) < 32 {
			return nil, nil, errors.New("Feishu CLI connector is unavailable")
		}
		port := os.Getenv("WORKBENCH_CONNECTOR_PORT")
		if port == "" {
			port = "7383"
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, nil, core.ErrInvalid
		}
		ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(n)+"/links/cli/read", bytes.NewReader(raw))
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, err := client.Do(req)
		if err != nil {
			return nil, nil, errors.New("Feishu CLI connector is unavailable")
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, 128*1024+1))
		if err != nil || len(data) > 128*1024 || !json.Valid(data) {
			return nil, nil, errors.New("Feishu CLI response unavailable or too large")
		}
		if response.StatusCode != http.StatusOK {
			return nil, nil, errors.New("Feishu read failed. Verify the CLI authorization, input and data permissions")
		}
		if err := a.Store.RememberFeishuCLIRead(ctx, p); err != nil {
			return nil, nil, errors.New("Feishu read provenance could not be saved")
		}
		// The SDK's typed output re-marshals through float64. Preserve IDs and
		// other large numbers by returning the validated JSON value verbatim.
		payload := append(append(json.RawMessage(`{"data":`), data...), '}')
		return &mcp.CallToolResult{
			StructuredContent: payload,
			Content:           []mcp.Content{&mcp.TextContent{Text: string(payload)}},
		}, nil, nil
	})
}
