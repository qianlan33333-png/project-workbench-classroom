package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func reviewFixture(t *testing.T) (*Store, Project, Plugin, ComponentReview, *atomic.Int64) {
	t.Helper()
	s := testStore(t)
	p := testProject(t, s, "活动")
	plugin := componentFixture(t, s, p)
	plugin.Component.OwnerReviewAction = "dispatch"
	plugin.Component.Actions = append(plugin.Component.Actions, ComponentAction{Name: "dispatch", RequiresOwner: true})
	if _, e := s.SavePlugin(plugin); e != nil {
		t.Fatal(e)
	}
	count := &atomic.Int64{}
	s.componentRunner = func(ctx context.Context, p Plugin, action string, args map[string]any) (json.RawMessage, error) {
		if action == "dispatch" {
			if p.Headers["X-Workbench-Owner-Confirmed"] != "true" {
				t.Error("missing host authorization")
			}
			count.Add(1)
			return json.RawMessage(`{"job_ref":"dispatch-one","status":"running","summary":"等待原回执"}`), nil
		}
		return json.RawMessage(encode(map[string]any{"job_ref": "generation-one", "status": "succeeded", "result": map[string]any{"owner_review": map[string]any{"task_ref": "generation-one", "digest": strings.Repeat("a", 64), "title": "本周活动", "preview": "真实冻结样例：示例甲"}}})), nil
	}
	c, e := s.SubmitComponent(context.Background(), p.DefaultSessionID, ComponentRequest{PluginID: plugin.ID, Action: "start", OperationKey: "review-generation-one"})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.materializeComponentReview(c); e != nil {
		t.Fatal(e)
	}
	r, e := s.ComponentReview(c.ID)
	if e != nil {
		t.Fatal(e)
	}
	return s, p, plugin, r, count
}
func TestComponentReviewFrozenApprovalAndDuplicate(t *testing.T) {
	s, p, _, r, count := reviewFixture(t)
	if _, e := s.ApproveComponentReview(context.Background(), r.ID, strings.Repeat("b", 64), "web:approval"); e == nil {
		t.Fatal("old digest authorized")
	}
	if _, e := s.QueueArchive(p.DefaultSessionID); e == nil {
		t.Fatal("pending review archived")
	}
	approved, e := s.ApproveComponentReview(context.Background(), r.ID, r.Digest, "web:approval")
	s.pollComponents(context.Background())
	if e != nil || approved.State != "dispatching" || count.Load() != 1 {
		t.Fatal(approved.State, e, count.Load())
	}
	if _, e = s.ApproveComponentReview(context.Background(), r.ID, r.Digest, "web:duplicate"); e != nil || count.Load() != 1 {
		t.Fatal("duplicate owner request", e, count.Load())
	}
	if _, e = s.CancelComponentReview(r.ID); e == nil {
		t.Fatal("cancel inferred unsent after dispatch")
	}
}
func TestComponentReviewWechatReplyAndPersonalNotice(t *testing.T) {
	s, _, _, r, count := reviewFixture(t)
	wx := channel(t, s, "wechat")
	if _, e := s.Submit(incoming(wx, "本人私聊建立")); e != nil {
		t.Fatal(e)
	}
	if e := s.notifyComponentReview(r); e != nil {
		t.Fatal(e)
	}
	if e := s.notifyComponentReview(r); e != nil {
		t.Fatal(e)
	}
	d, e := s.ClaimDelivery(wx.ID)
	if e != nil || d == nil {
		t.Fatal(d, e)
	}
	if !strings.Contains(d.Text, "真实冻结样例") {
		t.Fatal("missing actual preview")
	}
	if e = s.FinishDelivery(d.ID, "sent", "review-platform-message", ""); e != nil {
		t.Fatal(e)
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM deliveries WHERE data->>'$.kind'='component-review'").Scan(&n)
	if n != 1 {
		t.Fatal("duplicate notification", n)
	}
	fresh, _ := s.ComponentReview(r.ID)
	notice, e := s.Message(fresh.PersonalMessageID)
	if e != nil || notice.Agent == nil || notice.Agent.Execution != "read" {
		t.Fatal("Personal Agent result attribution lost", e)
	}
	in := incoming(wx, "发")
	in.EventID = "owner-reply-once"
	in.ReplyTo = "wrong-old-message"
	m, e := s.Submit(in)
	if e != nil {
		t.Fatal(e)
	}
	if ok, e := s.handleComponentOwnerControl(context.Background(), m); !ok || e != nil || count.Load() != 0 {
		t.Fatal("unrelated quote approved", e)
	}
	in.EventID = "owner-right-quote"
	in.ReplyTo = "review-platform-message"
	m, e = s.Submit(in)
	if e != nil {
		t.Fatal(e)
	}
	if ok, e := s.handleComponentOwnerControl(context.Background(), m); !ok || e != nil {
		t.Fatal(e)
	}
	s.pollComponents(context.Background())
	if count.Load() != 1 {
		t.Fatal("real owner reply not admitted", e, count.Load())
	}
	m, e = s.Submit(in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.handleComponentOwnerControl(context.Background(), m); e != nil || count.Load() != 1 {
		t.Fatal("duplicate inbox reply repeated dispatch", e)
	}
}

func TestComponentReviewOldBareReplyCannotAuthorizeNewNotice(t *testing.T) {
	s, _, _, r, count := reviewFixture(t)
	wx := channel(t, s, "wechat")
	if _, e := s.Submit(incoming(wx, "本人私聊建立")); e != nil {
		t.Fatal(e)
	}
	stale := incoming(wx, "发")
	stale.EventID = "old-before-review"
	m, e := s.Submit(stale)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.notifyComponentReview(r); e != nil {
		t.Fatal(e)
	}
	d, e := s.ClaimDelivery(wx.ID)
	if e != nil || d == nil {
		t.Fatal(d, e)
	}
	if e = s.FinishDelivery(d.ID, "sent", "new-review-receipt", ""); e != nil {
		t.Fatal(e)
	}
	if _, e = s.handleComponentOwnerControl(context.Background(), m); e != nil {
		t.Fatal(e)
	}
	s.pollComponents(context.Background())
	if count.Load() != 0 {
		t.Fatal("queued old 发 authorized a later review")
	}
	fresh := incoming(wx, "发")
	fresh.EventID = "after-notice"
	m, e = s.Submit(fresh)
	if e != nil {
		t.Fatal(e)
	}
	m.CreatedAt = d.CreatedAt + 1000
	s.saveMessage(m)
	if _, e = s.handleComponentOwnerControl(context.Background(), m); e != nil {
		t.Fatal(e)
	}
	s.pollComponents(context.Background())
	if count.Load() != 1 {
		t.Fatal("new owner confirmation was not admitted")
	}
}
func TestComponentReviewCancellationAndFairNotification(t *testing.T) {
	s, _, _, r, _ := reviewFixture(t)
	wx := channel(t, s, "wechat")
	s.Submit(incoming(wx, "本人私聊建立"))
	for k := 0; k < 12; k++ {
		v := r
		v.ID = fmt.Sprintf("%08d-%s", k, r.ID)
		v.CallID = v.ID
		v.CreatedAt = int64(k + 1)
		v.UpdatedAt = v.CreatedAt
		// Insert actual successful call references, not detached synthetic review rows.
		c, _ := s.ComponentCall(r.CallID)
		c.ID = v.ID
		c.OperationKey = v.ID
		s.DB.Exec("INSERT INTO component_calls(id,session_id,plugin_id,operation_key,state,updated_at,data) VALUES(?,?,?,?,?,?,?)", c.ID, c.SessionID, c.PluginID, c.OperationKey, c.State, c.UpdatedAt, encode(c))
		s.DB.Exec("INSERT INTO component_reviews VALUES(?,?,?,?,?,?)", v.ID, v.CallID, v.SessionID, v.State, v.UpdatedAt, encode(v))
	}
	for k := 0; k < 3; k++ {
		s.pollComponentReviews(context.Background())
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM deliveries WHERE data->>'$.kind'='component-review'").Scan(&n)
	if n != 13 {
		t.Fatal("later review starved", n)
	}
	cancelled, e := s.CancelComponentReview(r.ID)
	if e != nil || cancelled.State != "cancelled" {
		t.Fatal(cancelled, e)
	}
	if _, e = s.ApproveComponentReview(context.Background(), r.ID, r.Digest, "web:too-late"); e == nil {
		t.Fatal("cancelled review approved")
	}
	s.pollComponents(context.Background())
	var pending int
	s.DB.QueryRow("SELECT count(*) FROM deliveries WHERE data->>'$.messageId'=? AND state='sending'", "component-review-notify:"+r.ID).Scan(&pending)
	if pending != 0 {
		t.Fatal("cancelled notification claimed")
	}
}

func TestActivityClosedLoopPreview(t *testing.T) {
	dir := os.Getenv("PW_ACTIVITY_PREVIEW")
	if dir == "" {
		t.Skip("opt-in isolated activity UI")
	}
	os.MkdirAll(dir, 0700)
	s, p, plugin, r, _ := reviewFixture(t)
	old := testProject(t, s, "原上下文能力")
	m, _ := s.Submit(Incoming{Source: "web", EventID: "history-preview", SessionID: old.DefaultSessionID, Text: "旧活动配置与历史"})
	m.State = "handled"
	s.saveMessage(m)
	historic := Run{ID: newID(), ProjectID: old.ID, SessionID: old.DefaultSessionID, MessageID: m.ID, State: "completed", Text: m.Incoming.Text, Result: "合并前的配置和记录完整保留", CreatedAt: now()}
	if _, e := s.DB.Exec("INSERT INTO runs VALUES(?,?,?,?,?,?,?,?)", historic.ID, old.ID, historic.SessionID, m.ID, "completed", 0, now(), encode(historic)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CommitProjectMerge(p.ID, ProjectMerge{SourceIDs: []string{old.ID}}); e != nil {
		t.Fatal(e)
	}
	s.componentRunner = func(_ context.Context, _ Plugin, action string, _ map[string]any) (json.RawMessage, error) {
		if action == "dispatch" {
			return json.RawMessage(`{"job_ref":"preview-send","status":"succeeded","summary":"已收回合成回执","result":{"state":"sent","receipt":{"requested_count":2000,"sent_count":2000,"failed_count":0}}}`), nil
		}
		return json.RawMessage(`{"status":"succeeded","summary":"仅隔离样例"}`), nil
	}
	r.Title = "11月体验活动 · 2000人"
	r.Preview = "2000条已通过原门禁，当前展示实际待发送样本。\n1. 合成用户甲\n你好，这是根据本条用户资料生成的活动邀请。\n附件：活动说明.png\n2. 合成用户乙\n你好，提醒你查看已报名的活动。\n微信回复发后只批准这批固定版本。"
	oldAt := r.UpdatedAt
	s.saveComponentReview(r, oldAt)
	hash, _ := PasswordHash("isolated-activity-preview")
	g := gin.New()
	web, _ := filepath.Abs("../../web/dist")
	(&API{Store: s, Config: Config{DataDir: s.Root, WebDir: web, PasswordHash: hash}}).Mount(g)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetBasicAuth("owner", "isolated-activity-preview")
		g.ServeHTTP(w, r)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.pollComponents(ctx)
			}
		}
	}()
	os.WriteFile(filepath.Join(dir, "preview.json"), []byte(encode(map[string]any{"url": server.URL, "project": p, "plugin": plugin.ID, "review": r.ID})), 0600)
	for {
		if _, e := os.Stat(filepath.Join(dir, "stop")); e == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
func TestComponentReviewOldWebCommandCannotApproveLaterReview(t *testing.T) {
	s, p, _, r, count := reviewFixture(t)
	m, e := s.Submit(Incoming{Source: "web", EventID: "queued-before-generation", SessionID: p.DefaultSessionID, Text: "发"})
	if e != nil {
		t.Fatal(e)
	}
	m.CreatedAt = r.CreatedAt - 1
	s.saveMessage(m)
	if _, e = s.handleComponentOwnerControl(context.Background(), m); e != nil {
		t.Fatal(e)
	}
	s.pollComponents(context.Background())
	if count.Load() != 0 {
		t.Fatal("old web command authorized new review")
	}
}
