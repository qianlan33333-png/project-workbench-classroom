package workbench

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestScopedToolsReferencesAndVersions(t *testing.T) {
	s := testStore(t)
	a := testProject(t, s, "青山")
	b := testProject(t, s, "青海")
	_, e := s.SaveFile(b.ID, "定位.md", []byte("青海品牌事实"), "")
	if e != nil {
		t.Fatal(e)
	}
	wx := channel(t, s, "wechat")
	in := Incoming{Source: "web", SessionID: a.DefaultSessionID, EventID: newID(), Text: "只引用品牌定位", References: []FileReference{{ProjectID: b.ID, Path: "定位.md"}}}
	m := submitRoute(t, s, in)
	if m.SessionID != a.DefaultSessionID || len(m.References) != 1 {
		t.Fatal(m)
	}
	again, e := s.Submit(in)
	if e != nil || again.ID != m.ID {
		t.Fatal("routing changed original input", e)
	}
	r, _ := s.Run(m.RunID)
	ss, _ := s.Session(m.SessionID)
	rt := RuntimeInput{Run: r, Project: a, Session: ss}
	v, e := s.appTool(rt, "read_reference", json.RawMessage(`{"index":0}`))
	if e != nil || !strings.Contains(encode(v), "青海品牌事实") {
		t.Fatal(v, e)
	}
	if _, e = s.appTool(rt, "read_reference", json.RawMessage(`{"index":1}`)); e == nil {
		t.Fatal("ungranted reference read")
	}
	other := submitRoute(t, s, Incoming{Source: "web", SessionID: b.DefaultSessionID, EventID: newID(), Text: "另一个项目"})
	if _, e = s.appTool(rt, "read_run", json.RawMessage(encode(map[string]string{"runId": other.RunID}))); e == nil {
		t.Fatal("cross-project history exposed")
	}
	m2 := submitRoute(t, s, Incoming{Source: "web", SessionID: a.DefaultSessionID, EventID: newID(), Text: "继续"})
	r2, _ := s.Run(m2.RunID)
	if _, e = s.appTool(RuntimeInput{Run: r2, Project: a}, "read_reference", json.RawMessage(`{"index":0}`)); e == nil {
		t.Fatal("grant leaked into next input")
	}
	ex, e := s.CreateExpert("中文专家", "通用方法", "读项目资料")
	if e != nil {
		t.Fatal(e)
	}
	a.ExpertIDs = []string{ex.ID}
	a, _ = s.UpdateProject(a.ID, a)
	m3 := submitRoute(t, s, Incoming{Source: "web", EventID: newID(), SessionID: a.DefaultSessionID, Text: "用专家方法"})
	r3, _ := s.Run(m3.RunID)
	if r3.ExpertVersions[ex.ID] != ex.Digest {
		t.Fatal("all enabled expertise must be pinned")
	}
	staged, e := s.StageUpload(wx.ID, wx.OwnerID, "切换到青海.txt", []byte("仅附件"))
	if e != nil {
		t.Fatal(e)
	}
	im := incoming(wx, "青山：读取附件")
	im.Attachments = []string{staged}
	m4 := submitRoute(t, s, im)
	r4, _ := s.Run(m4.RunID)
	if e = s.MaterializeAttachments(&r4); e != nil || len(r4.Attachments) != 1 {
		t.Fatal(e)
	}
	if _, _, e = s.ReadFile(b.ID, r4.Attachments[0]); e == nil {
		t.Fatal("attachment routed through its name")
	}
	// User resolution is already tested against a stale semantic result; unbound peers stay rejected.
	c, e := s.SaveConnection(Connection{Kind: "feishu", Enabled: true}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.BindConnection(c.ID, "wrong", "actor"); e == nil {
		t.Fatal("wrong pairing code accepted")
	}
	bound, e := s.BindConnection(c.ID, strings.TrimPrefix(c.OwnerID, "pairing:"), "actor")
	if e != nil || bound.OwnerID != "actor" {
		t.Fatal(e)
	}
	if _, e = s.BindConnection(c.ID, strings.TrimPrefix(c.OwnerID, "pairing:"), "other"); e == nil {
		t.Fatal("pairing replay accepted")
	}
	_, _ = s.Route(context.Background(), m4.ID, nil)
	request := json.RawMessage(`{"schedule":{"name":"日总结","instruction":"总结资料","cron":"0 9 * * *","enabled":false}}`)
	first, e := s.appTool(rt, "save_schedule", request)
	if e != nil {
		t.Fatal(e)
	}
	second, e := s.appTool(rt, "save_schedule", request)
	if e != nil || encode(first) != encode(second) {
		t.Fatal("retried tool created a second schedule", e)
	}
	replayed := incoming(wx, "旧系统已经处理过")
	s.DB.Exec("INSERT INTO legacy_consumed VALUES(?)", digest([]byte(replayed.Source+":"+replayed.EventID)))
	legacy, e := s.Submit(replayed)
	if e != nil || legacy.State != "handled" || legacy.RunID != "" {
		t.Fatal("legacy event replayed", legacy, e)
	}
}
