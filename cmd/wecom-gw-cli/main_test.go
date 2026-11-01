package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gw "projectworkbench/pkg/wecomgateway"
)

func TestProfilesReuseSameCLIAndKeepSecretsOutOfOutput(t *testing.T) {
	for _, name := range []string{"business-A", "business-B"} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+name {
					t.Error("profile crossed credential scope")
				}
				fmt.Fprintf(w, `{"items":[{"accountId":%q}]}`, name)
			}))
			defer srv.Close()
			dir := t.TempDir()
			key := filepath.Join(dir, "key")
			if e := gw.WritePrivate(key, []byte(name)); e != nil {
				t.Fatal(e)
			}
			profile := filepath.Join(dir, "profile.json")
			data, _ := json.Marshal(gw.Config{BaseURL: srv.URL, APIKeyFile: key, WebhookSecretFile: filepath.Join(dir, "secret")})
			var out bytes.Buffer
			if e := run([]string{"config", "init", "--config", profile}, bytes.NewReader(data), &out); e != nil {
				t.Fatal(e)
			}
			st, _ := os.Stat(profile)
			if st.Mode().Perm() != 0600 {
				t.Fatal("profile is not private")
			}
			out.Reset()
			if e := run([]string{"accounts", "list", "--config", profile}, nil, &out); e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(out.String(), name) {
				t.Fatal("wrong profile", out.String())
			}
		})
	}
}
func TestWebhookApplyCapturesSecretWithoutPrintingIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			fmt.Fprint(w, `{"url":"","hasSecret":false,"events":[]}`)
		} else {
			fmt.Fprint(w, `{"url":"https://receiver.example/hook","hasSecret":true,"events":[],"secret":"once-only-private"}`)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	_ = gw.WritePrivate(key, []byte("test"))
	secret := filepath.Join(dir, "webhook")
	config := filepath.Join(dir, "profile.json")
	raw, _ := json.Marshal(gw.Config{BaseURL: srv.URL, APIKeyFile: key, WebhookSecretFile: secret})
	_ = gw.WritePrivate(config, raw)
	var out bytes.Buffer
	if e := run([]string{"webhook", "apply", "--config", config, "--url", "https://receiver.example/hook"}, nil, &out); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(out.String(), "once-only-private") {
		t.Fatal("secret printed")
	}
	b, e := os.ReadFile(secret)
	if e != nil || string(b) != "once-only-private" {
		t.Fatal("one-time secret not persisted")
	}
}
