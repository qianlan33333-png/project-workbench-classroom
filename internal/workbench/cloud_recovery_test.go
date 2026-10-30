package workbench

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRealCloudStopAndOrphanRecovery(t *testing.T) {
	path := os.Getenv("PW_REAL_CONFIG")
	if path == "" {
		t.Skip("credentialed cloud config not set")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var cfg Config
	json.Unmarshal(raw, &cfg)
	if cfg.RuntimeMode != "podman" {
		t.Skip("requires production container")
	}
	s := testStore(t)
	cfg.DataDir = s.Root
	p := testProject(t, s, "云端取消恢复")
	h := &Harness{Config: cfg, Store: s}
	w := NewWorker(s, h, nil)
	m := submitRoute(t, s, Incoming{Source: "web", SessionID: p.DefaultSessionID, EventID: newID(), Text: "使用命令工具启动后台进程：每 0.2 秒往当前目录的 tick.log 追加一个点；请用 Python subprocess.Popen 独立启动，不等待进程退出。随后调用 ask_user_question 询问我是否继续；询问期间保留后台进程。不要做其他事情。"})
	r, e := w.claim()
	if e != nil || r.ID != m.RunID {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); w.execute(ctx, r) }()
	for {
		items, _ := s.PendingInteractions()
		if len(items) > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("runtime never asked a question")
		case <-done:
			t.Fatal("run finished without pending question")
		case <-time.After(100 * time.Millisecond):
		}
	}
	if e = w.Stop(r.ID); e != nil {
		t.Fatal(e)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("cancel did not reap process group")
	}
	stopped, _ := s.Run(r.ID)
	if stopped.State != "cancelled" {
		t.Fatal(stopped.State, stopped.Error)
	}
	root, _ := s.ProjectRoot(p.ID)
	file := filepath.Join(root, "tick.log")
	before, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(600 * time.Millisecond)
	after, _ := os.ReadFile(file)
	if string(before) != string(after) {
		t.Fatal("background process survived cancellation")
	}
	if e = s.CheckUnlocked(p.ID); e != nil {
		t.Fatal(e)
	}
	var revisions int
	s.DB.QueryRow("SELECT count(*) FROM revisions WHERE run_id=? AND path='tick.log'", r.ID).Scan(&revisions)
	if revisions != 1 {
		t.Fatal("cancel lost file changes")
	}
	// Reproduce a crashed service: persistent run+baseline+lock and a real surviving container.
	submitRoute(t, s, Incoming{Source: "web", SessionID: p.DefaultSessionID, EventID: newID(), Text: "恢复验收"})
	orphan, e := w.claim()
	if e != nil {
		t.Fatal(e)
	}
	baseline, e := s.Snapshot(p.ID)
	if e != nil {
		t.Fatal(e)
	}
	s.DB.Exec("INSERT INTO baselines VALUES(?,?)", orphan.ID, encode(baseline))
	args := []string{"run", "-d", "--name", "pw-" + orphan.ID, "--label", "project-workbench=true", "--userns=keep-id", "--volume", root + ":/workspace:rw", cfg.RuntimeImage, "sh", "-c", "while true; do echo x >> /workspace/orphan.log; sleep 0.2; done"}
	if b, e := exec.CommandContext(ctx, cfg.Podman, args...).CombinedOutput(); e != nil {
		t.Fatal(e, string(b))
	}
	t.Cleanup(func() { h.Cleanup(context.Background(), orphan.ID) })
	for i := 0; i < 30; i++ {
		if _, e = os.Stat(filepath.Join(root, "orphan.log")); e == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if e = w.Recover(ctx); e != nil {
		t.Fatal(e)
	}
	recovered, _ := s.Run(orphan.ID)
	if recovered.State != "interrupted" {
		t.Fatal(recovered.State)
	}
	before, e = os.ReadFile(filepath.Join(root, "orphan.log"))
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(600 * time.Millisecond)
	after, _ = os.ReadFile(filepath.Join(root, "orphan.log"))
	if string(before) != string(after) {
		t.Fatal("orphan survived recovery")
	}
	if e = s.CheckUnlocked(p.ID); e != nil {
		t.Fatal(e)
	}
	t.Log("real cancellation and crashed-service recovery: containers reaped, background writes stopped, revisions preserved, locks released")
}
