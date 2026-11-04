package workbench

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in, disposable Store + actual credentialed Harness; never channel delivery.
func TestRealTodayAttachmentContents(t *testing.T) {
	cfgPath, fixture := os.Getenv("PW_REAL_ATTACHMENT_CONFIG"), os.Getenv("PW_ATTACHMENT_FIXTURES")
	if cfgPath == "" || fixture == "" {
		t.Skip("set isolated credentialed config and PDF/image fixtures")
	}
	raw, e := os.ReadFile(cfgPath)
	if e != nil {
		t.Fatal(e)
	}
	var cfg Config
	if e = json.Unmarshal(raw, &cfg); e != nil {
		t.Fatal(e)
	}
	s := testStore(t)
	cfg.DataDir = s.Root
	if cfg.RuntimeDir == "" {
		cfg.RuntimeDir, _ = filepath.Abs("../../runtime")
	}
	p, e := s.PersonalSpace()
	if e != nil {
		t.Fatal(e)
	}
	business := testProject(t, s, "附件验收只读项目")
	original := []byte("业务正文不能因个人附件改变")
	if _, e = s.SaveFile(business.ID, "事实.md", original, ""); e != nil {
		t.Fatal(e)
	}
	h := &Harness{Store: s, Config: cfg}
	w := NewWorker(s, h, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	zipped := func(parts map[string]string) []byte {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		for n, v := range parts {
			f, er := z.Create(n)
			if er != nil {
				t.Fatal(er)
			}
			if _, er = f.Write([]byte(v)); er != nil {
				t.Fatal(er)
			}
		}
		if er := z.Close(); er != nil {
			t.Fatal(er)
		}
		return b.Bytes()
	}
	token := strings.ReplaceAll(newID(), "-", "")
	files := []struct {
		name string
		data []byte
		want string
	}{
		{"记录.txt", []byte("校验短语 TXT-" + token), "TXT-" + token},
		{"文档.docx", zipped(map[string]string{"word/document.xml": "<document><p><t>DOC-" + token + "</t></p></document>"}), "DOC-" + token},
		{"表格.xlsx", zipped(map[string]string{"xl/_rels/workbook.xml.rels": "<Relationships><Relationship Id=\"r1\" Target=\"worksheets/sheet1.xml\"/></Relationships>", "xl/workbook.xml": "<workbook xmlns:r=\"r\"><sheet name=\"验收\" r:id=\"r1\"/></workbook>", "xl/worksheets/sheet1.xml": "<worksheet><row><c r=\"A1\" t=\"inlineStr\"><is><t>XLS-" + token + "</t></is></c></row></worksheet>"}), "XLS-" + token},
		{"演示.pptx", zipped(map[string]string{"ppt/slides/slide1.xml": "<slide><p><t>PPT-" + token + "</t></p></slide>"}), "PPT-" + token},
	}
	for _, name := range []string{"proof.pdf", "proof.png"} {
		b, er := os.ReadFile(filepath.Join(fixture, name))
		if er != nil {
			t.Fatal(er)
		}
		files = append(files, struct {
			name string
			data []byte
			want string
		}{name, b, "7319"})
	}
	var session string
	for i := 0; i < len(files); i += 3 {
		var refs, wants []string
		for _, f := range files[i : i+3] {
			ref, er := s.StagePersonalUpload(p.DefaultSessionID, f.name, f.data)
			if er != nil {
				t.Fatal(er)
			}
			refs = append(refs, ref)
			wants = append(wants, f.want)
		}
		m := submitRoute(t, s, Incoming{Source: "web", ConnectionID: "web", ActorID: "owner", SessionID: p.DefaultSessionID, EventID: newID(), Text: "这是隔离附件内容验收。请逐一调用 read_message_attachment 读取本条消息的3个附件（序号0、1、2），每个文件回复原文校验短语或数字。必须从工具结果取得，不要猜，不调用其他工具或外部服务。图片请明确只是OCR。", Attachments: refs})
		r, er := w.claim()
		if er != nil || r.ID != m.RunID {
			t.Fatal("claim", er)
		}
		w.execute(ctx, r)
		r, er = s.Run(r.ID)
		if er != nil || r.State != "completed" {
			t.Fatal("real attachment round failed", r.State, r.Error, er)
		}
		for _, want := range wants {
			if !strings.Contains(r.Result, want) {
				t.Fatal("actual content missing from response", files[i].name)
			}
		}
		ss, er := s.Session(r.SessionID)
		if er != nil {
			t.Fatal(er)
		}
		if session != "" && session != ss.HarnessID {
			t.Fatal("native session changed")
		}
		session = ss.HarnessID
		t.Log("real attachment round completed", i/3+1)
	}
	for _, kind := range []string{"wechat", "feishu"} {
		c := channel(t, s, kind)
		want := kind + "-" + newID()
		ref, er := s.StageUpload(c.ID, c.OwnerID, "渠道附件.txt", []byte(want))
		if er != nil {
			t.Fatal(er)
		}
		in := incoming(c, "本次隔离附件验收：请调用read_message_attachment读取序号0，原样回复其中唯一校验短语，不调用其他工具、不发送外部消息。")
		in.Attachments = []string{ref}
		m := submitRoute(t, s, in)
		again, er := s.Submit(in)
		if er != nil || again.ID != m.ID {
			t.Fatal("channel duplicate", er)
		}
		r, er := w.claim()
		if er != nil || r.ID != m.RunID {
			t.Fatal("channel claim", er)
		}
		w.execute(ctx, r)
		r, er = s.Run(r.ID)
		ss, se := s.Session(r.SessionID)
		if er != nil || se != nil || r.State != "completed" || !strings.Contains(r.Result, want) || ss.HarnessID != session {
			t.Fatal("channel attachment content/session", kind, r.State, r.Error, er, se)
		}
		t.Log("isolated channel ingress with real model and attachment passed", kind)
	}
	b, _, e := s.ReadFile(business.ID, "事实.md")
	if e != nil || !bytes.Equal(b, original) {
		t.Fatal("business file changed", e)
	}
	var n int
	_ = s.DB.QueryRow("SELECT count(*) FROM harness_events WHERE data LIKE '%read_message_attachment%'").Scan(&n)
	if n < 6 {
		t.Fatal("missing real attachment tool evidence", n)
	}
	t.Log("PASS: six actual formats and two channel replays through real model and MCP, stable native session, unchanged business file", n)
}
