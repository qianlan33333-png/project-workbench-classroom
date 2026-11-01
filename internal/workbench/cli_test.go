package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestPasswordCLIProjectFilesAndNoExpiry(t *testing.T) {
	if _, e := exec.LookPath("python3"); e != nil {
		t.Fatal("CLI acceptance requires Python 3.9+")
	}
	s := testStore(t)
	hash, _ := PasswordHash("cli-test-password")
	start := func() *httptest.Server {
		gin.SetMode(gin.ReleaseMode)
		router := gin.New()
		(&API{Store: s, Config: Config{PasswordHash: hash}}).Mount(router)
		return httptest.NewServer(router)
	}
	srv := start()
	defer func() { srv.Close() }()
	temp := t.TempDir()
	config := filepath.Join(temp, "client.json")
	script, _ := filepath.Abs("../../cli/pwb.py")
	cli := func(input string, wantOK bool, args ...string) map[string]any {
		t.Helper()
		ctx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		cmd := exec.CommandContext(ctx, "python3", append([]string{script, "--config", config, "--url", srv.URL}, args...)...)
		for _, v := range os.Environ() {
			if !strings.HasPrefix(v, "PWB_") {
				cmd.Env = append(cmd.Env, v)
			}
		}
		cmd.Stdin = strings.NewReader(input)
		var out, stderr bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &stderr
		err := cmd.Run()
		if (err == nil) != wantOK {
			t.Fatalf("CLI %v: %v stdout=%s stderr=%s", args, err, &out, &stderr)
		}
		body := out.Bytes()
		if !wantOK {
			body = stderr.Bytes()
		}
		var v map[string]any
		if e := json.Unmarshal(body, &v); e != nil {
			t.Fatalf("invalid CLI JSON: %s / %s", &out, &stderr)
		}
		return v
	}
	cli("cli-test-password\n", true, "login", "--password-stdin")
	st, _ := os.Stat(config)
	if st.Mode().Perm() != 0600 {
		t.Fatal("config permissions", st.Mode())
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM logins").Scan(&count)
	if count != 0 {
		t.Fatal("CLI created expiring browser login")
	}
	// A new API instance and old browser sessions have no effect on password auth.
	s.DB.Exec("INSERT INTO logins VALUES('expired',1)")
	srv.Close()
	srv = start()
	status := cli("", true, "status")
	if status["authenticated"] != true || status["expiresAt"] != nil {
		t.Fatal(status)
	}
	p := cli("", true, "projects", "create", "--name", "CLI 验收", "--description", "保留说明", "--alias", "原别名")
	id := p["id"].(string)
	session := p["defaultSessionId"]
	p = cli("", true, "projects", "update", id, "--name", "CLI 更名")
	if p["description"] != "保留说明" || p["defaultSessionId"] != session {
		t.Fatal("partial update erased project fields", p)
	}
	cli("", false, "api", "PATCH", "/projects/"+id, "--data", `{"id":"replace-id"}`)
	first := cli("初稿\n", true, "files", "write", id, "中文/说明.md", "--from", "-", "--base-hash", "")
	read := cli("", true, "files", "read", "CLI 更名", "中文/说明.md")
	if read["content"] != "初稿\n" {
		t.Fatal(read)
	}
	cli("", true, "files", "write", id, "中文/说明.md", "--text", "修改稿", "--base-hash", read["hash"].(string))
	stale := cli("", false, "files", "write", id, "中文/说明.md", "--text", "过期稿", "--base-hash", first["after"].(string))
	if stale["status"] != float64(409) {
		t.Fatal(stale)
	}
	attachment := filepath.Join(temp, "test.bin")
	os.WriteFile(attachment, []byte{0, 255, 1, 2}, 0600)
	cli("", true, "files", "upload", id, "附件/test.bin", attachment)
	dest := filepath.Join(temp, "download.bin")
	cli("", true, "files", "download", id, "附件/test.bin", "--output", dest)
	b, _ := os.ReadFile(dest)
	if !bytes.Equal(b, []byte{0, 255, 1, 2}) {
		t.Fatal("attachment bytes changed")
	}
	zip := filepath.Join(temp, "project.zip")
	cli("", true, "projects", "export", id, "--output", zip)
	p2 := cli("", true, "projects", "create", "--name", "导入验收")
	id2 := p2["id"].(string)
	cli("", true, "projects", "import", id2, zip)
	duplicate := cli("", false, "projects", "import", id2, zip)
	if duplicate["status"] != float64(409) {
		t.Fatal(duplicate)
	}
	deleted := cli("", true, "files", "delete", id, "中文/说明.md")
	cli("", true, "files", "undo", deleted["id"].(string))
	cli("", true, "projects", "delete", id)
	cli("", false, "projects", "get", id)
	cli("", true, "projects", "delete", id2)
	if rows, _ := s.Projects(); len(rows) != 0 {
		t.Fatal(rows)
	}
	// Password changes revoke only the old password, without issuing or refreshing tokens.
	newer, _ := PasswordHash("new-cli-password")
	s.SetSetting("password_hash", newer)
	bad := cli("", false, "status")
	if bad["status"] != float64(401) {
		t.Fatal(bad)
	}
	cli("new-cli-password\n", true, "login", "--password-stdin")
	cli("", true, "logout")
	cli("", false, "status")
}

func TestDeleteProjectRemovesScopeAndPreservesDedup(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "删除甲")
	other := testProject(t, s, "保留乙")
	c := channel(t, s, "wechat")
	in := incoming(c, "删除甲：记住原文")
	// Restore a pre-upgrade project turn with its original channel delivery identity.
	m := submitRoute(t, s, Incoming{Source: "web", SessionID: p.DefaultSessionID, EventID: in.EventID, Text: in.Text})
	m.Incoming = in
	if _, err := s.DB.Exec("UPDATE inbox SET source=?,connection_id=?,entry_key=?,data=? WHERE id=?", in.Source, c.ID, entryKey(in), encode(m), m.ID); err != nil {
		t.Fatal(err)
	}

	w := NewWorker(s, &fakeExecutor{store: s}, nil)
	run, e := w.claim()
	if e != nil {
		t.Fatal(e)
	}
	if e = s.DeleteProject(p.ID); !errors.Is(e, ErrLocked) {
		t.Fatal("deleted active run", e)
	}
	w.execute(context.Background(), run)
	delivery, e := s.ClaimDelivery(c.ID)
	if e != nil || delivery == nil {
		t.Fatal(e)
	}
	if e = s.DeleteProject(p.ID); !errors.Is(e, ErrLocked) {
		t.Fatal("deleted in-flight send", e)
	}
	if e = s.FinishDelivery(delivery.ID, "sent", "receipt", ""); e != nil {
		t.Fatal(e)
	}
	s.SaveSchedule(Schedule{ProjectID: p.ID, Name: "暂停任务", Instruction: "测试", Cron: "0 9 * * *"})
	root, _ := s.ProjectRoot(p.ID)
	runtime := filepath.Join(s.Root, "runtime", "projects", p.ID)
	os.MkdirAll(runtime, 0700)
	os.WriteFile(filepath.Join(runtime, "history"), []byte("old history"), 0600)
	if e = s.DeleteProject(p.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Project(p.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	for _, dir := range []string{root, runtime} {
		if _, e = os.Stat(dir); !os.IsNotExist(e) {
			t.Fatal("project directory remains", dir, e)
		}
	}
	if _, e = s.Project(other.ID); e != nil {
		t.Fatal("other project removed", e)
	}
	if _, e = s.Session(p.DefaultSessionID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, e = s.Run(run.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	replay, e := s.Submit(in)
	if e != nil || replay.ID != m.ID || replay.State != "deleted" || replay.Incoming.Text != "" {
		t.Fatal("deleted message replayed", replay, e)
	}
	in.Text = "不同内容"
	if _, e = s.Submit(in); e == nil {
		t.Fatal("event identity conflict lost")
	}
	s.QueueReply(m, run.ID, p.DefaultSessionID, "answer", "不能重新发送")
	if d, e := s.ClaimDelivery(c.ID); d != nil || e != nil {
		t.Fatal("late completion sent after delete", d, e)
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM entry_focus").Scan(&n)
	if n != 0 {
		t.Fatal("deleted project focus remains")
	}
	for _, table := range []string{"runs", "sessions", "schedules", "revisions", "documents"} {
		s.DB.QueryRow("SELECT count(*) FROM "+table+" WHERE project_id=?", p.ID).Scan(&n)
		if n != 0 {
			t.Fatal("project rows remain", table)
		}
	}
	rows, e := s.DB.Query("PRAGMA foreign_key_check")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("broken foreign keys")
	}
}

func TestBasicPasswordAuthDoesNotFallBackToCookie(t *testing.T) {
	s := testStore(t)
	hash, _ := PasswordHash("basic-password")
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	(&API{Store: s, Config: Config{PasswordHash: hash}}).Mount(r)
	s.DB.Exec("INSERT INTO logins VALUES(?,?)", digest([]byte("cookie")), time.Now().Add(time.Hour).UnixMilli())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	req.AddCookie(&http.Cookie{Name: "pw_session", Value: "cookie"})
	req.SetBasicAuth("owner", "wrong")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != 401 {
		t.Fatal(res.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	req.SetBasicAuth("owner", "basic-password")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatal(res.Code)
	}
}

func TestProjectDeletionRecovery(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "保留项目")
	// A crash after the DB commit must not leave old workspace/history forever.
	id := newID()
	for _, path := range []string{filepath.Join(s.Root, "projects", id), filepath.Join(s.Root, "runtime", "projects", id)} {
		if e := os.MkdirAll(path, 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(path, "leftover"), []byte("delete me"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.SetSetting("project_delete:"+id, id); e != nil {
		t.Fatal(e)
	}
	w := NewWorker(s, &fakeExecutor{store: s}, nil)
	if e := w.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{filepath.Join(s.Root, "projects", id), filepath.Join(s.Root, "runtime", "projects", id)} {
		if _, e := os.Stat(path); !os.IsNotExist(e) {
			t.Fatal("cleanup not resumed", path, e)
		}
	}
	if marker, e := s.Setting("project_delete:" + id); e != nil || marker != "" {
		t.Fatal("cleanup intent remains", marker, e)
	}
	if _, e := s.Project(p.ID); e != nil {
		t.Fatal("live project affected", e)
	}
}

func TestCLIMultipartExpertChanges(t *testing.T) {
	s := testStore(t)
	ex, e := s.CreateExpert("CLI 专家", "独立 multipart 验收", "方法")
	if e != nil {
		t.Fatal(e)
	}
	hash, _ := PasswordHash("cli-multipart-test-password")
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	(&API{Store: s, Config: Config{PasswordHash: hash}}).Mount(router)
	server := httptest.NewServer(router)
	defer server.Close()
	temp := t.TempDir()
	binary := filepath.Join(temp, "fixture.bin")
	os.WriteFile(binary, []byte{0, 255, 17}, 0600)
	script, _ := filepath.Abs("../../cli/pwb.py")
	change := ExpertChanges{BaseDigest: ex.Digest, Files: []ExpertFileChange{{Op: "write", Path: "参考/fixture.bin", Upload: "file0"}}}
	cmd := exec.Command("python3", script, "--url", server.URL, "--config", filepath.Join(temp, "cli.json"), "api", "POST", "/experts/"+ex.ID+"/changes", "--form", "changes="+encode(change), "--file", "file0="+binary)
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "PWB_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "PWB_PASSWORD=cli-multipart-test-password")
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var updated Expert
	if e = json.Unmarshal(out, &updated); e != nil {
		t.Fatal(e, string(out))
	}
	b, e := s.ReadExpertFile(ex.ID, updated.Digest, "参考/fixture.bin")
	if e != nil || !bytes.Equal(b, []byte{0, 255, 17}) {
		t.Fatal(b, e)
	}
}
