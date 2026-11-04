package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPersonalAttachmentScopeAndIdempotentMaterialization(t *testing.T) {
	s := testStore(t)
	p, _ := s.PersonalSpace()
	other := testProject(t, s, "业务正文")
	ref, e := s.StagePersonalUpload(p.DefaultSessionID, "../本地记录.txt", []byte("附件校验 ONLY-ATTACHMENT"))
	if e != nil {
		t.Fatal(e)
	}
	in := Incoming{Source: "web", ConnectionID: "web", ActorID: "owner", ConversationID: "web", SessionID: p.DefaultSessionID, EventID: newID(), Attachments: []string{ref}}
	m := submitRoute(t, s, in)
	again, e := s.Submit(in)
	if e != nil || again.ID != m.ID {
		t.Fatal(again, e)
	}
	r, _ := s.Run(m.RunID)
	for range 2 {
		if e = s.MaterializeAttachments(&r); e != nil {
			t.Fatal(e)
		}
	}
	if len(r.Attachments) != 1 {
		t.Fatal(r.Attachments)
	}
	content, _, e := s.ReadFile(p.ID, r.Attachments[0])
	if e != nil || string(content) != "附件校验 ONLY-ATTACHMENT" {
		t.Fatal(e, string(content))
	}
	files, e := s.Files(other.ID)
	if e != nil || len(files) != 0 {
		t.Fatal("business files changed", e)
	}
	root, _ := s.ProjectRoot(p.ID)
	if e = os.WriteFile(filepath.Join(root, r.Attachments[0]), []byte("tampered"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = s.MaterializeAttachments(&r); e == nil {
		t.Fatal("changed materialized file accepted")
	}
}
func TestPersonalAttachmentCannotCrossSourceOrSession(t *testing.T) {
	s := testStore(t)
	p, _ := s.PersonalSpace()
	other := testProject(t, s, "另一个项目")
	if _, e := s.StagePersonalUpload(other.DefaultSessionID, "x.txt", []byte("not allowed")); e == nil {
		t.Fatal("non-personal upload")
	}
	ref, e := s.StagePersonalUpload(p.DefaultSessionID, "x.txt", []byte("owned"))
	if e != nil {
		t.Fatal(e)
	}
	for _, in := range []Incoming{
		{Source: "web", ConnectionID: "web", ActorID: "owner", SessionID: other.DefaultSessionID, Attachments: []string{ref}},
		{Source: "web", ConnectionID: "web", ActorID: "owner", SessionID: p.DefaultSessionID, Attachments: []string{"项目正文.md"}},
		{Source: "web", ConnectionID: "web", ActorID: "someone", SessionID: p.DefaultSessionID, Attachments: []string{ref}},
	} {
		in.EventID = newID()
		in.Text = "读取附件"
		m, e := s.Submit(in)
		if e != nil {
			continue
		}
		if _, e = s.ResolveMessage(m.ID, in.SessionID); e == nil {
			t.Fatal("invalid upload grant admitted", in)
		}
	}
	wx := channel(t, s, "wechat")
	in := incoming(wx, "附件")
	in.Attachments = []string{ref}
	m, e := s.Submit(in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Route(context.Background(), m.ID, nil); e == nil {
		t.Fatal("channel consumed web upload")
	}
	_, e = s.StagePersonalUpload(p.DefaultSessionID, strings.Repeat("a", 1024), []byte("x"))
	if e == nil {
		t.Fatal("unbounded filename")
	}
}

func TestPersonalAttachmentReaderUsesCurrentRunAndRealText(t *testing.T) {
	s := testStore(t)
	p, _ := s.PersonalSpace()
	ref, e := s.StagePersonalUpload(p.DefaultSessionID, "记录.txt", []byte(strings.Repeat("正文", 3002)+"校验7319"))
	if e != nil {
		t.Fatal(e)
	}
	m := submitRoute(t, s, Incoming{Source: "web", ConnectionID: "web", ActorID: "owner", SessionID: p.DefaultSessionID, EventID: newID(), Attachments: []string{ref}})
	r, _ := s.Run(m.RunID)
	if e = s.MaterializeAttachments(&r); e != nil {
		t.Fatal(e)
	}
	ss, _ := s.Session(r.SessionID)
	runtime, _ := filepath.Abs("../../runtime")
	h := Harness{Store: s, Config: Config{DataDir: s.Root, RuntimeDir: runtime, RuntimeMode: "native"}}
	in := RuntimeInput{Run: r, Session: ss, Project: p}
	v, e := h.readMessageAttachment(context.Background(), in, []byte(`{"index":0,"offset":6000}`))
	if e != nil || !strings.Contains(encode(v), "校验7319") {
		t.Fatal(v, e)
	}
	for _, q := range []string{`{"index":1}`, `{"index":-1}`, `{"index":0,"offset":-1}`} {
		if _, e = h.readMessageAttachment(context.Background(), in, []byte(q)); e == nil {
			t.Fatal("out of scope read", q)
		}
	}
	in.Session.ID = "other-session"
	if _, e = h.readMessageAttachment(context.Background(), in, []byte(`{"index":0}`)); e == nil {
		t.Fatal("other session read")
	}
}

func TestPersonalAttachmentOwnerHTTP(t *testing.T) {
	s := testStore(t)
	p, _ := s.PersonalSpace()
	hash, _ := PasswordHash("attachment-http-only")
	g := gin.New()
	(&API{Store: s, Config: Config{PasswordHash: hash}}).Mount(g)
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	_ = form.WriteField("sessionId", p.DefaultSessionID)
	file, _ := form.CreateFormFile("file", "本地.md")
	_, _ = file.Write([]byte("HTTP-ATTACHMENT"))
	_ = form.Close()
	req := httptest.NewRequest("POST", "/api/v1/personal/attachments", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", form.FormDataContentType())
	out := httptest.NewRecorder()
	g.ServeHTTP(out, req)
	if out.Code != 401 {
		t.Fatal("unauthenticated upload", out.Code)
	}
	req = httptest.NewRequest("POST", "/api/v1/personal/attachments", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.SetBasicAuth("owner", "attachment-http-only")
	out = httptest.NewRecorder()
	g.ServeHTTP(out, req)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	var uploaded struct {
		Path, Name string
		Size       int
	}
	if e := json.Unmarshal(out.Body.Bytes(), &uploaded); e != nil || uploaded.Name != "本地.md" || uploaded.Size != 15 {
		t.Fatal(uploaded, e)
	}
	req = httptest.NewRequest("POST", "/api/v1/messages", strings.NewReader(encode(Incoming{SessionID: p.DefaultSessionID, EventID: newID(), Attachments: []string{uploaded.Path}})))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("owner", "attachment-http-only")
	out = httptest.NewRecorder()
	g.ServeHTTP(out, req)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	var message Message
	_ = json.Unmarshal(out.Body.Bytes(), &message)
	if _, e := s.Route(context.Background(), message.ID, nil); e != nil {
		t.Fatal(e)
	}
}

func TestPersonalAttachmentCancellationReapsHelperChildren(t *testing.T) {
	s := testStore(t)
	p, _ := s.PersonalSpace()
	ref, e := s.StagePersonalUpload(p.DefaultSessionID, "x.txt", []byte("safe"))
	if e != nil {
		t.Fatal(e)
	}
	m := submitRoute(t, s, Incoming{Source: "web", ConnectionID: "web", ActorID: "owner", SessionID: p.DefaultSessionID, EventID: newID(), Attachments: []string{ref}})
	r, _ := s.Run(m.RunID)
	if e = s.MaterializeAttachments(&r); e != nil {
		t.Fatal(e)
	}
	ss, _ := s.Session(r.SessionID)
	runtime := t.TempDir()
	marker := filepath.Join(t.TempDir(), "child-started")
	leak := marker + "-leaked"
	code := "import subprocess,sys,time\np=subprocess.Popen([sys.executable,'-c'," + strconv.Quote("import time;from pathlib import Path;Path("+strconv.Quote(marker)+").touch();time.sleep(.5);Path("+strconv.Quote(leak)+").touch()") + "])\ntime.sleep(60)\n"
	if e = os.WriteFile(filepath.Join(runtime, "attachment-reader.py"), []byte(code), 0600); e != nil {
		t.Fatal(e)
	}
	h := Harness{Store: s, Config: Config{DataDir: s.Root, RuntimeDir: runtime}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, e := h.readMessageAttachment(ctx, RuntimeInput{Run: r, Session: ss, Project: p}, []byte(`{"index":0}`))
		done <- e
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, e = os.Stat(marker); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case e = <-done:
		if e == nil {
			t.Fatal("cancelled read succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("helper did not stop")
	}
	time.Sleep(650 * time.Millisecond)
	if _, e = os.Stat(leak); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("helper child survived cancellation", e)
	}
	if e = h.Cleanup(context.Background(), r.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(s.Root, "runtime", "attachment-scratch", r.ID)); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("scratch survived cleanup", e)
	}
}
