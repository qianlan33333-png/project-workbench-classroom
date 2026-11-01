package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"projectworkbench/pkg/wecomgateway"
)

var version = "0.1.0"

func main() {
	if e := run(os.Args[1:], os.Stdin, os.Stdout); e != nil {
		_ = json.NewEncoder(os.Stderr).Encode(map[string]any{"ok": false, "error": e.Error()})
		os.Exit(1)
	}
}
func run(args []string, in io.Reader, out io.Writer) error {
	f := flag.NewFlagSet("wecom-gw-cli", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	config := f.String("config", "gateway.json", "profile file")
	data := f.String("data", "-", "JSON file or stdin")
	id := f.String("id", "", "resource ID")
	key := f.String("idempotency-key", "", "stable send key")
	target := f.String("url", "", "webhook URL")
	showVersion := f.Bool("version", false, "show version")
	help := f.Bool("help", false, "show commands as JSON")
	cursor := f.String("cursor", "", "pagination cursor")
	limit := f.Int("limit", 50, "page size 1..200")
	// Flags may follow the two-word command, as in other service CLIs.
	var command []string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = append(command, args[0])
		args = args[1:]
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			command = append(command, args[0])
			args = args[1:]
		}
	}
	if e := f.Parse(args); e != nil {
		return errors.New("invalid arguments; use: wecom-gw-cli <resource> <action> --config FILE [--data -] [--id ID]")
	}
	if *showVersion {
		return json.NewEncoder(out).Encode(map[string]any{"ok": true, "version": version})
	}
	if *help {
		return json.NewEncoder(out).Encode(map[string]any{"ok": true, "version": version, "commands": []string{"config init/show", "accounts list/get", "messages send/get", "webhook show/apply", "events list/redeliver", "connection apply/list", "binding apply/list", "session list/pause/resume", "doctor check"}, "flags": []string{"--config FILE", "--data FILE|-", "--id ID", "--idempotency-key KEY", "--url CALLBACK", "--limit 1..200", "--cursor CURSOR"}})
	}
	if len(command) == 0 {
		command = f.Args()
	}
	if len(command) != 2 {
		return errors.New("commands: config init/show, accounts list/get, messages send/get, webhook show/apply, events list/redeliver, connection apply/list, binding apply, session list/pause/resume, doctor check")
	}
	cmd := strings.Join(command, " ")
	readData := func() (json.RawMessage, error) {
		var r io.Reader = in
		if *data != "-" {
			file, e := os.Open(*data)
			if e != nil {
				return nil, errors.New("input file unavailable")
			}
			defer file.Close()
			r = file
		}
		b, e := io.ReadAll(io.LimitReader(r, 1<<20+1))
		if e != nil || len(b) > 1<<20 || !json.Valid(b) {
			return nil, errors.New("valid JSON input up to 1MiB required")
		}
		return b, nil
	}
	if cmd == "config init" {
		b, e := readData()
		if e != nil {
			return e
		}
		var c wecomgateway.Config
		if e = json.Unmarshal(b, &c); e != nil {
			return errors.New("invalid profile")
		}
		if e = wecomgateway.ValidateURL(c.BaseURL); e != nil {
			return e
		}
		if _, e = os.Lstat(*config); !os.IsNotExist(e) {
			return errors.New("profile exists; refusing overwrite")
		}
		b, _ = json.MarshalIndent(c, "", "  ")
		if e = wecomgateway.WritePrivate(*config, b); e != nil {
			return errors.New("cannot save profile")
		}
		return json.NewEncoder(out).Encode(map[string]any{"ok": true, "data": c})
	}
	c, e := wecomgateway.ReadConfig(*config)
	if e != nil {
		return e
	}
	if cmd == "config show" {
		return json.NewEncoder(out).Encode(map[string]any{"ok": true, "data": c})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if strings.HasPrefix(cmd, "connection ") || strings.HasPrefix(cmd, "binding ") || strings.HasPrefix(cmd, "session ") {
		if e = wecomgateway.ValidateURL(c.WorkbenchURL); e != nil {
			return errors.New("valid workbenchUrl required")
		}
		password, e := wecomgateway.ReadSecret(c.PasswordEnv, c.PasswordFile)
		if e != nil {
			return e
		}
		method, path := "GET", ""
		var body any
		switch cmd {
		case "connection list":
			path = "/gateway/connections"
		case "connection apply":
			b, e := readData()
			if e != nil {
				return e
			}
			var v map[string]any
			if e = json.Unmarshal(b, &v); e != nil {
				return e
			}
			apiKey, e := wecomgateway.ReadSecret(c.APIKeyEnv, c.APIKeyFile)
			if e != nil {
				return e
			}
			secret, e := wecomgateway.ReadSecret("", c.WebhookSecretFile)
			if e != nil {
				return e
			}
			v["baseUrl"] = c.BaseURL
			v["apiKey"] = apiKey
			v["webhookSecret"] = secret
			body = v
			method = "POST"
			path = "/gateway/connections"
		case "binding apply":
			b, e := readData()
			if e != nil {
				return e
			}
			body = b
			method = "POST"
			path = "/gateway/bindings"
		case "binding list":
			path = "/gateway/bindings"
		case "session list":
			path = "/gateway/sessions"
		case "session pause", "session resume":
			if *id == "" {
				return errors.New("--id required")
			}
			method = "POST"
			path = "/gateway/sessions/" + url.PathEscape(*id) + "/" + strings.Split(cmd, " ")[1]
		default:
			return errors.New("unknown management command")
		}
		b, _ := json.Marshal(body)
		r, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.WorkbenchURL, "/")+"/api/v1"+path, bytes.NewReader(b))
		if e != nil {
			return errors.New("invalid workbench request")
		}
		r.SetBasicAuth("owner", password)
		r.Header.Set("Content-Type", "application/json")
		h := http.Client{Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		res, e := h.Do(r)
		if e != nil {
			return errors.New("workbench transport failed")
		}
		defer res.Body.Close()
		payload, e := io.ReadAll(io.LimitReader(res.Body, 4<<20))
		if e != nil || !json.Valid(payload) {
			return errors.New("workbench response unreadable")
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return fmt.Errorf("workbench rejected request (HTTP %d)", res.StatusCode)
		}
		return json.NewEncoder(out).Encode(map[string]any{"ok": true, "httpStatus": res.StatusCode, "data": json.RawMessage(payload)})
	}
	apiKey, e := wecomgateway.ReadSecret(c.APIKeyEnv, c.APIKeyFile)
	if e != nil {
		return e
	}
	client := wecomgateway.Client{BaseURL: c.BaseURL, APIKey: apiKey}
	if cmd == "webhook apply" {
		if c.WebhookSecretFile == "" {
			return errors.New("webhookSecretFile required")
		}
		_, se := wecomgateway.ReadSecret("", c.WebhookSecretFile)
		v, e := client.ApplyWebhook(ctx, *target, se == nil)
		if e != nil {
			return e
		}
		if v.Secret != "" {
			if e = wecomgateway.WritePrivate(c.WebhookSecretFile, []byte(v.Secret)); e != nil {
				return errors.New("webhook configured but secret could not be saved; operator recovery required")
			}
			v.Secret = ""
			v.HasSecret = true
		}
		return json.NewEncoder(out).Encode(map[string]any{"ok": true, "data": v})
	}
	method, path := "GET", ""
	var body any
	switch cmd {
	case "accounts list", "doctor check":
		path = "/accounts"
	case "accounts get":
		path = "/accounts/" + url.PathEscape(*id)
	case "messages get":
		path = "/messages/" + url.PathEscape(*id)
	case "messages send":
		b, e := readData()
		if e != nil {
			return e
		}
		var v wecomgateway.SendRequest
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if e = dec.Decode(&v); e != nil {
			return errors.New("invalid private text request")
		}
		res, e := client.SendResponse(ctx, v, *key)
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(map[string]any{"ok": true, "httpStatus": res.Status, "requestId": res.RequestID, "data": res.Data})
	case "webhook show":
		path = "/webhook"
	case "events list":
		path = "/events"
	case "events redeliver":
		method = "POST"
		path = "/events/" + url.PathEscape(*id) + "/redeliver"
	default:
		return errors.New("unknown command")
	}
	if (strings.HasSuffix(cmd, " get") || cmd == "events redeliver") && *id == "" {
		return errors.New("--id required")
	}
	if strings.HasSuffix(cmd, " list") {
		if *limit < 1 || *limit > 200 {
			return errors.New("limit must be 1..200")
		}
		q := url.Values{"limit": {fmt.Sprint(*limit)}}
		if *cursor != "" {
			q.Set("cursor", *cursor)
		}
		path += "?" + q.Encode()
	}
	res, e := client.Do(ctx, method, path, body, "")
	if e != nil {
		return e
	}
	if cmd == "webhook show" {
		var v wecomgateway.Webhook
		if e = json.Unmarshal(res.Data, &v); e != nil {
			return e
		}
		v.Secret = ""
		res.Data, _ = json.Marshal(v)
	}
	return json.NewEncoder(out).Encode(map[string]any{"ok": true, "httpStatus": res.Status, "requestId": res.RequestID, "data": res.Data})
}
