package workbench

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRealCloudBrowserAndAppTools(t *testing.T) {
	path := os.Getenv("PW_REAL_CONFIG")
	if path == "" {
		t.Skip("credentialed cloud config not set")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var cfg Config
	if e = json.Unmarshal(raw, &cfg); e != nil {
		t.Fatal(e)
	}
	if cfg.RuntimeMode != "podman" {
		t.Skip("cloud container acceptance")
	}
	s := testStore(t)
	cfg.DataDir = s.Root
	p := testProject(t, s, "云端完整验收")
	if _, e = s.SaveFile(p.ID, "基准资料.md", []byte("本项目的基准数字是 37 和 41。"), ""); e != nil {
		t.Fatal(e)
	}
	prompt := "完成一次完整任务：1.读取当前项目基准资料.md；2.必须使用 browser 的 browser_open 工具打开 https://dsh.qianlan333.cloud/company/ 读取网页标题；3.运行 Python 计算资料中两个数字的乘积，写 result.txt；4.用 browser_screenshot 将截图保存到 browser.png；5.使用 workbench 的 save_schedule 创建一个暂停的每日 09:00 任务，名称为 验收定时，指令为 读资料并总结，enabled=false；6.将网页标题和计算结果写验收报告.md；7.最后调用 ask_user_question 问用户接管检查是否通过，收到通过后结束。不要对外发送消息。"
	m := submitRoute(t, s, Incoming{Source: "web", SessionID: p.DefaultSessionID, EventID: newID(), Text: prompt})
	r, _ := s.Run(m.RunID)
	r.State = "running"
	s.saveRun(r)
	ss, _ := s.Session(r.SessionID)
	hash, _ := PasswordHash("cloud-test-password-only")
	apiConfig := cfg
	apiConfig.BasePath = ""
	apiConfig.PublicURL = ""
	apiConfig.PasswordHash = hash
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	(&API{Store: s, Config: apiConfig}).Mount(router)
	api := httptest.NewServer(router)
	defer api.Close()
	h := &Harness{Config: cfg, Store: s}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	asked := 0
	result, e := h.Execute(ctx, RuntimeInput{Run: r, Session: ss, Project: p, Prompt: prompt}, RuntimeHooks{Interaction: func(ctx context.Context, kind string, payload json.RawMessage) (any, error) {
		if kind == "approval" {
			return map[string]string{"outcome": "allowed-once"}, nil
		}
		asked++
		login, e := http.Post(api.URL+"/api/v1/login", "application/json", strings.NewReader(`{"password":"cloud-test-password-only"}`))
		if e != nil {
			return nil, e
		}
		login.Body.Close()
		req, _ := http.NewRequest("GET", api.URL+"/api/v1/browser/vnc.html", nil)
		for _, c := range login.Cookies() {
			req.AddCookie(c)
		}
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			return nil, e
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || !strings.Contains(string(body), "noVNC") {
			t.Errorf("authenticated visual takeover unavailable: %d", res.StatusCode)
		}
		req, _ = http.NewRequest("GET", api.URL+"/api/v1/browser/websockify", nil)
		for _, c := range login.Cookies() {
			req.AddCookie(c)
		}
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		req.Header.Set("Sec-WebSocket-Protocol", "binary")
		res, e = http.DefaultClient.Do(req)
		if e != nil {
			return nil, e
		}
		if res.StatusCode != 101 {
			t.Errorf("VNC websocket did not upgrade: %d", res.StatusCode)
		}
		res.Body.Close()
		var q struct {
			Questions []struct {
				ID string `json:"id"`
			} `json:"questions"`
		}
		json.Unmarshal(payload, &q)
		answers := []map[string]any{}
		for _, item := range q.Questions {
			answers = append(answers, map[string]any{"id": item.ID, "selected": []string{}, "custom": "通过"})
		}
		return map[string]any{"answers": answers}, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range []string{"result.txt", "browser.png", "验收报告.md"} {
		b, _, e := s.ReadFile(p.ID, f)
		if e != nil || len(b) == 0 {
			t.Fatal("missing artifact", f, e)
		}
		if f == "result.txt" && strings.TrimSpace(string(b)) != "1517" {
			t.Fatal("wrong computation")
		}
	}
	schedules, e := s.Schedules(p.ID)
	if e != nil || len(schedules) != 1 || schedules[0].Enabled {
		t.Fatal("native app tools schedule failed", e)
	}
	if asked == 0 || result.Text == "" {
		t.Fatal("no user interaction")
	}
	t.Log("rootless container: read source, browser, Python, artifacts, scoped MCP, authenticated VNC HTTP + WebSocket all passed")
}
