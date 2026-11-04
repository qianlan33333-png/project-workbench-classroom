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

type suitePreviewExecutor struct{}

func (*suitePreviewExecutor) Execute(_ context.Context, in RuntimeInput, _ RuntimeHooks) (RuntimeResult, error) {
	return RuntimeResult{Text: "这是隔离预览，已收到这条消息。\n\n- 保留当前项目和对话。\n- 下一步可以核对资料中的实际结果。"}, nil
}
func (*suitePreviewExecutor) Cleanup(context.Context, string) error { return nil }

// A real disposable Store/API, fictional content and deterministic executor.
// No model credentials, provider API, platform sender or live business data.
func TestWorkbenchSuitePreview(t *testing.T) {
	dir := os.Getenv("PW_SUITE_PREVIEW")
	if dir == "" {
		t.Skip("opt-in full UI preview")
	}
	dir, _ = filepath.Abs(dir)
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	s := testStore(t)
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	p, e := s.CreateProject("产品验证", "验证核心价值，收集用户反馈并确定下一步。", nil)
	must(e)
	q, e := s.CreateProject("内容计划", "规划内容主题、素材与发布节奏。", nil)
	must(e)
	x, e := s.CreateExpert("写作教练", "帮助梳理表达结构，形成清楚、可读的内容。", "先理解目标读者，再提出有依据的表达建议。")
	must(e)
	y, e := s.CreateExpert("产品顾问", "从用户反馈中提取关键问题，支持产品判断。", "先核对事实，再形成可以验证的下一步。")
	must(e)
	p.ExpertIDs = []string{x.ID, y.ID}
	p.DefaultExpertID = y.ID
	p, e = s.UpdateProject(p.ID, p)
	must(e)
	_, e = s.SaveFile(p.ID, "资料/访谈纪要.md", []byte("# 用户访谈纪要\n\n## 访谈范围\n2026年11月4日，三位虚构用户。\n\n## 主要发现\n- 先明确产品价值，再决定功能顺序。\n- 简化首次使用步骤。\n\n校验短语：SUITE-7319\n"), "")
	must(e)
	_, e = s.SaveFile(p.ID, "资料/验证结论.md", []byte("# 验证结论\n\n以用户反馈为依据，先完成一个可验证的小步骤。"), "")
	must(e)
	_, e = s.SaveFile(q.ID, "选题清单.md", []byte("# 下周选题\n\n- 产品体验复盘\n- 一次小范围访谈"), "")
	must(e)
	ss, e := s.CreateSession(p.ID, "方案迭代建议", "topic", "")
	must(e)
	seed := func(project Project, session, text string) {
		m := submitRoute(t, s, Incoming{Source: "web", ConnectionID: "web", ActorID: "owner", SessionID: session, EventID: newID(), Text: text})
		r, er := s.Run(m.RunID)
		must(er)
		r.State = "completed"
		r.Result = "我整理出了两点关键发现。\n\n用户首先关心产品价值是否清楚，以及首次使用是否顺畅。建议先验证这两点，再安排新的功能。\n\n- 把核心价值写成一句可以验证的说明。\n- 用一次短访谈核对首次使用中的障碍。"
		r.FinishedAt = now()
		must(s.saveRun(r))
	}
	seed(p, p.DefaultSessionID, "请根据访谈纪要，给出下一步验证建议。")
	seed(p, ss.ID, "下一轮该先核对哪些问题？")
	seed(q, q.DefaultSessionID, "请整理下周内容计划。")
	var tasks []ProjectTask
	for i, title := range []string{"整理第二轮访谈问题清单", "汇总现有用户反馈并分类", "输出可用性测试方案", "已完成的资料整理"} {
		state := "pending"
		if i == 3 {
			state = "completed"
		}
		task, er := s.SaveTask(ProjectTask{ProjectID: p.ID, Title: title, Description: "## 目标\n完成一轮小范围核对，记录可验证的发现。\n\n## 背景\n本条为虚构验收数据，不包含真实业务资料。", State: state, ScheduledDate: "2026-11-04", DueDate: "2026-11-06", Operation: newID()})
		must(er)
		tasks = append(tasks, task)
	}
	sc, e := s.SaveSchedule(Schedule{ProjectID: p.ID, Name: "每周核对资料", Instruction: "仅核对项目资料的实际变化并记录来源。", Cron: "0 9 * * 5", Enabled: true})
	must(e)
	failed, e := s.RunSchedule(sc.ID, newID())
	must(e)
	fr, e := s.Run(failed.RunID)
	must(e)
	fr.State = "failed"
	fr.Error = "隔离演示：校验资料尚未补齐"
	fr.FinishedAt = now()
	must(s.saveRun(fr))
	for _, kind := range []string{"wechat", "feishu"} {
		label := "微信"
		if kind == "feishu" {
			label = "飞书"
		}
		c, er := s.SaveConnection(Connection{Kind: kind, Label: label, OwnerID: "fixture-" + kind, Enabled: true}, nil)
		must(er)
		must(s.Heartbeat(c.ID, "connected"))
	}
	_, e = s.SavePlugin(Plugin{Name: "示例资料工具", Description: "隔离测试，仅用于验证配置界面。", Transport: "http", URL: "https://example.invalid/mcp"})
	must(e)
	nowDay := time.Now().In(calendarZone())
	at := time.Date(nowDay.Year(), nowDay.Month(), nowDay.Day(), 18, 0, 0, 0, calendarZone()).UnixMilli()
	_, e = s.changeEvent(context.Background(), "create", CalendarEvent{Operation: newID(), ProjectID: p.ID, Title: "产品验证讨论", Description: "虚构日程，核对访谈发现。", Start: at, End: at + 3600000, Destination: "cloud"}, "")
	must(e)
	personal, e := s.PersonalSpace()
	must(e)
	seed(personal, personal.DefaultSessionID, "今天先推进哪件事？")
	for _, text := range []string{"先明确范围，再分步推进。", "你倾向先看结论，再看必要的依据。"} {
		_, e = s.SaveMemory(PersonalMemory{Text: text, Kind: "fact"})
		must(e)
	}
	hash, e := PasswordHash("isolated-suite-preview")
	must(e)
	g := gin.New()
	web, _ := filepath.Abs("../../web/dist")
	cfg := Config{DataDir: s.Root, WebDir: web, PasswordHash: hash, RuntimeMode: "native"}
	w := NewWorker(s, &suitePreviewExecutor{}, nil)
	(&API{Store: s, Worker: w, Config: cfg}).Mount(g)
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	must(e)
	server := &http.Server{Handler: http.HandlerFunc(func(out http.ResponseWriter, req *http.Request) {
		req.SetBasicAuth("owner", "isolated-suite-preview")
		g.ServeHTTP(out, req)
	})}
	t.Cleanup(func() { server.Close() })
	go server.Serve(ln)
	meta := map[string]any{"url": "http://" + ln.Addr().String(), "projects": []Project{p, q}, "experts": []Expert{x, y}, "session": ss, "tasks": tasks, "rule": sc, "dataRoot": s.Root, "mode": "isolated Store/API, fictional data, deterministic executor; no platform/model"}
	raw, _ := json.Marshal(meta)
	must(os.WriteFile(filepath.Join(dir, "preview.json"), raw, 0600))
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
			s.dispatchDecisions()
			if ms, er := s.Inbox(); er == nil {
				for _, m := range ms {
					if m.State == "unrouted" {
						_, _ = s.Route(ctx, m.ID, nil)
					}
				}
			}
			if r, er := w.claim(); er == nil {
				w.execute(ctx, r)
			}
		}
	}
}
