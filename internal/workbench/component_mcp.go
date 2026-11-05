package workbench

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

func componentMCP(parent context.Context, p Plugin, action string, args map[string]any) (json.RawMessage, error) {
	client := &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return componentMCPClient(parent, p, action, args, client)
}

func componentFrame(body []byte, id any) (json.RawMessage, bool, error) {
	var f struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &f) != nil {
		return nil, false, nil
	}
	if f.Method != "" || string(f.ID) != encode(id) {
		return nil, false, nil
	}
	if len(f.Error) > 0 && string(f.Error) != "null" {
		return nil, true, errors.New("组件协议错误")
	}
	if len(f.Result) == 0 {
		return nil, true, errors.New("组件协议缺少结果")
	}
	return f.Result, true, nil
}
func componentRPCResult(r io.Reader, sse bool, id any, handlers ...func(json.RawMessage) error) (json.RawMessage, error) {
	if !sse {
		body, e := io.ReadAll(io.LimitReader(r, (1<<20)+1))
		if e != nil {
			return nil, e
		}
		if len(body) > 1<<20 {
			return nil, errors.New("组件响应过大")
		}
		v, ok, e := componentFrame(body, id)
		if e != nil {
			return nil, e
		}
		if !ok {
			return nil, errors.New("组件响应编号不匹配")
		}
		return v, nil
	}
	scan := bufio.NewScanner(io.LimitReader(r, (1<<20)+1))
	scan.Buffer(make([]byte, 4096), 1<<20)
	var data []string
	total := 0
	for scan.Scan() {
		line := scan.Text()
		total += len(line) + 1
		if total > 1<<20 {
			return nil, errors.New("组件响应过大")
		}
		if line == "" {
			if len(data) > 0 {
				candidate := json.RawMessage(strings.Join(data, "\n"))
				for _, handler := range handlers {
					if err := handler(candidate); err != nil {
						return nil, err
					}
				}
				v, ok, e := componentFrame(candidate, id)
				if ok {
					return v, e
				}
				data = nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if len(data) > 0 {
		candidate := json.RawMessage(strings.Join(data, "\n"))
		for _, handler := range handlers {
			if err := handler(candidate); err != nil {
				return nil, err
			}
		}
		v, ok, e := componentFrame(candidate, id)
		if ok {
			return v, e
		}
	}
	return nil, errors.New("组件流中没有匹配的结果")
}

// Credentials and destination come only from owner settings. Redirects are forbidden.
func componentMCPClient(parent context.Context, p Plugin, action string, args map[string]any, client *http.Client) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	session := ""
	version := "2025-03-26"
	rpc := func(method string, params any, id any) (json.RawMessage, error) {
		frame := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
		if id != nil {
			frame["id"] = id
		}
		req, e := http.NewRequestWithContext(ctx, "POST", p.URL, bytes.NewBufferString(encode(frame)))
		if e != nil {
			return nil, e
		}
		for k, v := range p.Headers {
			req.Header.Set(k, v)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", version)
		if session != "" {
			req.Header.Set("Mcp-Session-Id", session)
		}
		res, e := client.Do(req)
		if e != nil {
			return nil, errors.New("组件连接失败")
		}
		defer res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return nil, errors.New("组件响应失败")
		}
		if x := res.Header.Get("Mcp-Session-Id"); x != "" {
			session = x
		}
		if id == nil {
			return nil, nil
		}
		return componentRPCResult(res.Body, strings.Contains(res.Header.Get("Content-Type"), "text/event-stream"), id, func(raw json.RawMessage) error {
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if json.Unmarshal(raw, &request) != nil || request.Method == "" || len(request.ID) == 0 {
				return nil
			}
			frame := map[string]any{"jsonrpc": "2.0", "id": request.ID}
			if request.Method == "ping" {
				frame["result"] = map[string]any{}
			} else {
				frame["error"] = map[string]any{"code": -32601, "message": "Unsupported server request"}
			}
			reply, er := http.NewRequestWithContext(ctx, "POST", p.URL, strings.NewReader(encode(frame)))
			if er != nil {
				return er
			}
			for k, values := range req.Header {
				reply.Header[k] = values
			}
			response, er := client.Do(reply)
			if er != nil {
				return errors.New("组件服务端请求响应失败")
			}
			response.Body.Close()
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				return errors.New("组件服务端请求响应失败")
			}
			return nil
		})
	}
	init, e := rpc("initialize", map[string]any{"protocolVersion": version, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "workbench-components", "version": "1"}}, 1)
	if e != nil {
		return nil, e
	}
	var negotiated struct {
		Version string `json:"protocolVersion"`
	}
	if json.Unmarshal(init, &negotiated) != nil {
		return nil, errors.New("组件初始化无效")
	}
	switch negotiated.Version {
	case "2025-03-26", "2025-06-18", "2025-11-25":
		version = negotiated.Version
	default:
		return nil, errors.New("组件MCP协议版本不兼容")
	}
	if _, e = rpc("notifications/initialized", map[string]any{}, nil); e != nil {
		return nil, e
	}
	raw, e := rpc("tools/call", map[string]any{"name": action, "arguments": args}, 2)
	if e != nil {
		return nil, e
	}
	var out struct {
		IsError    bool            `json:"isError"`
		Structured json.RawMessage `json:"structuredContent"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &out) != nil || out.IsError {
		return nil, errors.New("组件工具执行失败，请核查任务")
	}
	if len(out.Structured) > 0 && string(out.Structured) != "null" {
		return out.Structured, nil
	}
	for _, c := range out.Content {
		if c.Type == "text" && json.Valid([]byte(c.Text)) {
			return json.RawMessage(c.Text), nil
		}
	}
	return nil, errors.New("组件未返回结构化结果")
}
