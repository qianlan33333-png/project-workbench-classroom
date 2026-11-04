package workbench

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// Opt-in disposable UI fixture: real Store/API/worker, deterministic executor.
// No credentials, provider, production data or channel transport are loaded.
type followupPreviewExecutor struct{ store *Store }

func (e *followupPreviewExecutor) Execute(_ context.Context, in RuntimeInput, _ RuntimeHooks) (RuntimeResult, error) {
	if in.Run.FollowupID != "" {
		f, err := e.store.Followup(in.Run.FollowupID)
		if err != nil {
			return RuntimeResult{}, err
		}
		_, err = e.store.SaveFollowup(in, FollowupChange{ID: f.ID, Version: f.Version, Action: "complete", Evidence: "隔离演示：已核对测试资料，完成条件得到确认。"})
		return RuntimeResult{Text: "已核对测试资料，并记录完成依据。"}, err
	}
	return RuntimeResult{Text: "已收到。这是隔离演示，不会执行真实业务。"}, nil
}
func (*followupPreviewExecutor) Cleanup(context.Context, string) error { return nil }
func TestFollowupPreview(t *testing.T) {
	dir := os.Getenv("PW_FOLLOWUP_PREVIEW")
	if dir == "" {
		t.Skip("opt-in local UI fixture")
	}
	dir, _ = filepath.Abs(dir)
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	s := testStore(t)
	p := testProject(t, s, "产品反馈")
	_ = p
	for i, action := range []string{"ask", "review", "save", "complete"} {
		in := followupInput(t, s)
		q := followupRequest()
		q.Key = string(rune('a' + i))
		q.Title = []string{"选择反馈验证顺序", "下午核对资料更新", "核实导出功能的实际结果", "确认测试资料已补齐"}[i]
		q.Action = action
		q.At = now() + 60*60000
		q.Evidence = "隔离演示：只读核对了测试资料，原问题已经解决。"
		q.Decision = followupDecision()
		if _, e := s.SaveFollowup(in, q); e != nil {
			t.Fatal(e)
		}
		in.Run.State = "completed"
		in.Run.Result = "已保存本次跟进，后续按约定继续。"
		if e := s.saveRun(in.Run); e != nil {
			t.Fatal(e)
		}
	}
	_, _ = s.DB.Exec("INSERT INTO daily_contents VALUES(?,?)", shanghaiDay(), encode(DailyContent{Day: shanghaiDay(), Version: 1, State: "ready", Text: "今天先确认反馈中的真实变化，再决定下一步。", UpdatedAt: now()}))
	hash, e := PasswordHash("isolated-local-preview")
	if e != nil {
		t.Fatal(e)
	}
	w := NewWorker(s, &followupPreviewExecutor{store: s}, nil)
	urls := map[string]string{}
	for _, which := range []string{"baseline", "current"} {
		web := filepath.Join(dir, "baseline-dist")
		if which == "current" {
			web, _ = filepath.Abs("../../web/dist")
		}
		g := gin.New()
		(&API{Store: s, Worker: w, Config: Config{DataDir: s.Root, WebDir: web, PasswordHash: hash}}).Mount(g)
		ln, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		server := &http.Server{Handler: http.HandlerFunc(func(out http.ResponseWriter, req *http.Request) {
			req.SetBasicAuth("owner", "isolated-local-preview")
			g.ServeHTTP(out, req)
		})}
		t.Cleanup(func() { server.Close() })
		go server.Serve(ln)
		urls[which] = "http://" + ln.Addr().String() + "/#page=today"
	}
	b, _ := json.Marshal(urls)
	if e = os.WriteFile(filepath.Join(dir, "preview.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, e = os.Stat(filepath.Join(dir, "stop")); e == nil {
				return
			}
			_ = s.FireSchedules(time.Now())
			s.dispatchDecisions()
			if messages, e := s.Inbox(); e == nil {
				for _, m := range messages {
					if m.State == "unrouted" {
						if handled, e := w.handleControl(m); e == nil && !handled {
							_, _ = s.Route(ctx, m.ID, nil)
						}
					}
				}
			}
			if r, e := w.claim(); e == nil {
				w.execute(ctx, r)
			}
		}
	}
}
