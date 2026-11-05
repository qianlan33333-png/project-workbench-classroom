package workbench

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// The host keeps invocation references only. Plugins own their execution and data.
const componentSchema = `
CREATE TABLE IF NOT EXISTS component_plugins(id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS component_calls(id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE, plugin_id TEXT NOT NULL, operation_key TEXT NOT NULL, state TEXT NOT NULL, updated_at INTEGER NOT NULL, data TEXT NOT NULL, next_poll INTEGER NOT NULL DEFAULT 0, UNIQUE(session_id,operation_key));
CREATE INDEX IF NOT EXISTS component_pending ON component_calls(state,next_poll);
CREATE TRIGGER IF NOT EXISTS component_session_delete_guard BEFORE DELETE ON sessions
WHEN EXISTS(SELECT 1 FROM component_calls WHERE session_id=OLD.id AND state NOT IN ('succeeded','failed','cancelled'))
BEGIN SELECT RAISE(ABORT,'组件仍有在途任务，请先核查并停止'); END;
CREATE TRIGGER IF NOT EXISTS component_plugin_delete_guard BEFORE DELETE ON component_plugins
WHEN EXISTS(SELECT 1 FROM component_calls WHERE plugin_id=OLD.id AND state NOT IN ('succeeded','failed','cancelled'))
BEGIN SELECT RAISE(ABORT,'组件仍有在途任务，请先核查并停止'); END;

`

type ComponentSpec struct {
	OwnerReviewAction       string            `json:"ownerReviewAction,omitempty"`
	OwnerNotifyConnectionID string            `json:"ownerNotifyConnectionId,omitempty"`
	Key                     string            `json:"key,omitempty"`
	Version                 string            `json:"version"`
	Actions                 []ComponentAction `json:"actions"`
	StatusAction            string            `json:"statusAction,omitempty"`
	LookupAction            string            `json:"lookupAction,omitempty"`
	CancelAction            string            `json:"cancelAction,omitempty"`
}
type ComponentAction struct {
	ReadOnly      bool   `json:"readOnly,omitempty"`
	RequiresOwner bool   `json:"requiresOwner,omitempty"`
	Name          string `json:"name"`
	Description   string `json:"description"`
}
type ComponentCall struct {
	ReviewRejected bool           `json:"reviewRejected,omitempty"`
	OwnerConfirmed bool           `json:"ownerConfirmed,omitempty"`
	Arguments      map[string]any `json:"arguments,omitempty"`
	ID             string         `json:"id"`
	SessionID      string         `json:"sessionId"`
	ProjectID      string         `json:"projectId"`
	PluginID       string         `json:"pluginId"`
	Name           string         `json:"name"`
	Version        string         `json:"version"`
	Action         string         `json:"action"`
	OperationKey   string         `json:"operationKey"`
	InvocationKey  string         `json:"invocationKey"`
	// Integrity hashes carry no raw credentials or request payload.
	Binding   string          `json:"binding"`
	InputHash string          `json:"inputHash"`
	State     string          `json:"state"`
	JobRef    string          `json:"jobRef,omitempty"`
	Summary   string          `json:"summary"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	CreatedAt int64           `json:"createdAt"`
	UpdatedAt int64           `json:"updatedAt"`
}
type ComponentRequest struct {
	Config         *ComponentRunConfig `json:"-"`
	OwnerConfirmed bool                `json:"ownerConfirmed,omitempty"`
	PluginID       string              `json:"pluginId"`
	Action         string              `json:"action"`
	Arguments      map[string]any      `json:"arguments"`
	OperationKey   string              `json:"operationKey"`
}

func validateComponent(p Plugin) error {
	c := p.Component
	if c == nil {
		return nil
	}
	if p.ProjectID == "" || p.Transport != "http" {
		return errors.New("工具组件必须绑定一个项目并使用HTTPS MCP")
	}
	if c.Version == "" || len(c.Version) > 100 || len(c.Actions) == 0 || len(c.Actions) > 32 {
		return errors.New("请填写固定版本及1至32个允许动作")
	}
	seen := map[string]bool{}
	for _, a := range c.Actions {
		if !toolIdentifier.MatchString(a.Name) || seen[a.Name] {
			return errors.New("动作标识无效或重复")
		}
		seen[a.Name] = true
	}
	for _, a := range []string{c.StatusAction, c.LookupAction, c.CancelAction} {
		if a != "" && !toolIdentifier.MatchString(a) {
			return errors.New("状态、查回或取消动作无效")
		}
	}
	if (c.StatusAction == "") != (c.LookupAction == "") {
		return errors.New("异步组件需要同时配置状态和查回动作")
	}
	if c.CancelAction != "" && c.StatusAction == "" {
		return errors.New("取消动作需要异步状态能力")
	}
	if c.OwnerReviewAction != "" {
		valid := false
		for _, a := range c.Actions {
			if a.Name == c.OwnerReviewAction && a.RequiresOwner && !a.ReadOnly {
				valid = true
			}
		}
		if !valid || c.StatusAction == "" {
			return errors.New("审核动作须登记为本人确认，并提供后台查回能力")
		}
	}
	return nil
}
func componentBinding(p Plugin) string {
	return digest([]byte(encode(struct {
		ProjectID, URL string
		Headers        map[string]string
		Spec           *ComponentSpec
	}{p.ProjectID, p.URL, p.Headers, p.Component})))
}
func (s *Store) Components(project string) ([]Plugin, error) {
	ps, e := s.Plugins()
	out := []Plugin{}
	for _, p := range ps {
		if p.Component != nil && p.ProjectID == project {
			p.URL = ""
			p.Command = ""
			p.Args = nil
			p.Headers = nil
			p.Env = nil
			out = append(out, p)
		}
	}
	return out, e
}
func componentEnabled(p Plugin) bool { return p.Component != nil && (p.Enabled == nil || *p.Enabled) }
func componentActionAllowed(p Plugin, action string) bool {
	if action != "" && (action == p.Component.StatusAction || action == p.Component.LookupAction || action == p.Component.CancelAction) {
		return true
	}
	for _, a := range p.Component.Actions {
		if a.Name == action {
			return true
		}
	}
	return false
}
func componentTerminal(state string) bool {
	return state == "succeeded" || state == "failed" || state == "cancelled"
}
func (s *Store) componentSession(id string) (Session, error) {
	ss, e := s.Session(id)
	if e != nil {
		return ss, e
	}
	if ss.Draft || ss.RecycledAt > 0 || ss.Lifecycle != "" {
		return ss, errors.New("会话已归档、回收或尚未保存")
	}
	p, e := s.Project(ss.ProjectID)
	if e != nil {
		return ss, e
	}
	if p.Archived {
		return ss, errors.New("项目已归档")
	}
	if _, e = s.gatewaySession(id); e == nil {
		return ss, errors.New("客户会话不能调用工具组件")
	}
	return ss, nil
}
func (s *Store) componentPlugin(sessionID, pluginID string) (Session, Plugin, error) {
	ss, e := s.componentSession(sessionID)
	if e != nil {
		return ss, Plugin{}, e
	}
	p, e := s.Plugin(pluginID)
	if e != nil {
		return ss, p, e
	}
	if !componentEnabled(p) || p.ProjectID != ss.ProjectID {
		return ss, p, errors.New("组件已停用或未授权给当前项目")
	}
	return ss, p, nil
}
func (s *Store) ComponentCalls(session string) ([]ComponentCall, error) {
	if _, e := s.Session(session); e != nil {
		return nil, e
	}
	return listJSON[ComponentCall](s.DB, "SELECT data FROM component_calls WHERE session_id=? ORDER BY updated_at DESC LIMIT 50", session)
}
func (s *Store) ComponentCall(id string) (ComponentCall, error) {
	return getJSON[ComponentCall](s.DB, "SELECT data FROM component_calls WHERE id=?", id)
}
func (s *Store) saveComponentCall(v ComponentCall) (ComponentCall, error) {
	previous := v.UpdatedAt
	if componentTerminal(v.State) || v.JobRef != "" {
		v.Arguments = nil
	}
	v.UpdatedAt = max(now(), previous+1)
	res, e := s.DB.Exec("UPDATE component_calls SET state=?,updated_at=?,data=? WHERE id=? AND updated_at=?", v.State, v.UpdatedAt, encode(v), v.ID, previous)
	if e != nil {
		return v, e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return v, e
	}
	if n == 0 {
		return s.ComponentCall(v.ID)
	}
	e = s.Emit(v.SessionID, "", "component.updated", v)
	return v, e
}
func (s *Store) SubmitComponent(ctx context.Context, session string, q ComponentRequest) (ComponentCall, error) {
	return s.submitComponent(ctx, session, q, false)
}
func (s *Store) submitComponent(ctx context.Context, session string, q ComponentRequest, deferExecution bool) (ComponentCall, error) {
	var v ComponentCall
	ss, p, e := s.componentPlugin(session, q.PluginID)
	if e != nil {
		return v, e
	}
	if !componentActionAllowed(p, q.Action) {
		return v, errors.New("此组件动作未授权")
	}
	needsOwner := false
	for _, a := range p.Component.Actions {
		if a.Name == q.Action && a.RequiresOwner && !q.OwnerConfirmed {
			needsOwner = true
		}
	}
	if len(q.OperationKey) < 8 || len(q.OperationKey) > 160 {
		return v, errors.New("需要8至160字符的稳定操作编号")
	}
	if q.Arguments == nil {
		q.Arguments = map[string]any{}
	}
	for _, key := range []string{"invocation_key", "project_id", "session_id", "job_ref", "workbench_method", "owner_confirmed"} {
		if _, ok := q.Arguments[key]; ok {
			return v, errors.New("调用身份和任务引用由工作台绑定")
		}
	}
	raw := encode(q.Arguments)
	if len(raw) > 512<<10 {
		return v, errors.New("组件输入超过512KiB")
	}
	if q.Config != nil {
		snapshot, er := s.componentMethodSnapshot(*q.Config)
		if er != nil {
			return v, er
		}
		parameters := map[string]any{}
		for k, x := range q.Arguments {
			parameters[k] = x
		}
		parameters["workbench_method"] = snapshot
		q.Arguments = parameters
	}
	fp := digest([]byte(q.PluginID + "\n" + q.Action + "\n" + raw + "\n" + encode(q.Config)))
	created := false
	e = s.Transaction(ctx, func(tx *sql.Tx) error {
		fresh, er := getJSON[Session](tx, "SELECT data FROM sessions WHERE id=?", session)
		if er != nil {
			return er
		}
		if fresh.RecycledAt > 0 || fresh.Lifecycle != "" || fresh.Draft {
			return errors.New("会话已失效")
		}
		project, er := getJSON[Project](tx, "SELECT data FROM projects WHERE id=?", fresh.ProjectID)
		if er != nil || project.Archived {
			return errors.New("项目已归档，不能新建组件调用")
		}
		current, er := getJSON[Plugin](tx, "SELECT data FROM component_plugins WHERE id=?", p.ID)
		if er != nil || !componentEnabled(current) || componentBinding(current) != componentBinding(p) {
			return errors.New("组件配置已变化，请重新读取")
		}
		old, er := getJSON[ComponentCall](tx, "SELECT data FROM component_calls WHERE session_id=? AND operation_key=?", session, q.OperationKey)
		if er == nil {
			if old.InputHash != fp {
				return ErrConflict
			}
			v = old
			return nil
		}
		if !errors.Is(er, ErrNotFound) {
			return er
		}

		v = ComponentCall{Arguments: q.Arguments, ID: newID(), SessionID: session, ProjectID: ss.ProjectID, PluginID: p.ID, Name: p.Name, Version: p.Component.Version, Action: q.Action, OperationKey: q.OperationKey, InvocationKey: newID(), Binding: componentBinding(p), OwnerConfirmed: q.OwnerConfirmed, InputHash: fp, State: "submitting", Summary: "正在提交；响应不明时请查回原任务", CreatedAt: now(), UpdatedAt: now()}
		if deferExecution {
			v.State = "ready"
		}
		if needsOwner {
			v.State = "awaiting_owner"
			v.Summary = "等待本人确认。可在页面确认，或回复：确认组件 " + v.ID[:8]
		}
		_, er = tx.Exec("INSERT INTO component_calls(id,session_id,plugin_id,operation_key,state,updated_at,data) VALUES(?,?,?,?,?,?,?)", v.ID, session, p.ID, q.OperationKey, v.State, v.UpdatedAt, encode(v))
		created = er == nil
		return er
	})
	if e != nil || !created {
		return v, e
	}
	if deferExecution || needsOwner {
		return v, nil
	}
	return s.executeComponentInvocation(ctx, v, p, q.Arguments)
}
func (s *Store) executeComponentInvocation(ctx context.Context, v ComponentCall, p Plugin, arguments map[string]any) (ComponentCall, error) {
	args := map[string]any{}
	for k, x := range arguments {
		args[k] = x
	}
	args["invocation_key"] = v.InvocationKey
	// Credentials scope the plugin; project/session metadata is host-owned, not model input.
	args["project_id"] = v.ProjectID
	args["session_id"] = v.SessionID
	if v.OwnerConfirmed {
		copy := map[string]string{}
		for k, x := range p.Headers {
			copy[k] = x
		}
		copy["X-Workbench-Owner-Confirmed"] = "true"
		p.Headers = copy
	}
	result, e := s.runComponent(ctx, p, v.Action, args)
	if e != nil {
		v.State = "unknown"
		v.Error = "提交响应未确认；请刷新查回原任务，不要另建操作编号重跑"
	} else {
		e = applyComponentResult(&v, result, p.Component.StatusAction != "")
		if e != nil {
			v.State = "unknown"
			v.Error = "组件返回格式无效；请查回原任务"
		}
	}
	return s.saveComponentCall(v)
}
func applyComponentResult(v *ComponentCall, raw json.RawMessage, async bool) error {
	var out struct {
		JobRef  string          `json:"job_ref"`
		Status  string          `json:"status"`
		Summary string          `json:"summary"`
		Result  json.RawMessage `json:"result"`
	}
	if e := json.Unmarshal(raw, &out); e != nil {
		return e
	}
	if len(out.Summary) > 8000 || len(out.JobRef) > 512 {
		return errors.New("组件结果过大")
	}
	if out.Status == "" {
		if async {
			return errors.New("缺少异步状态")
		}
		out.Status = "succeeded"
	}
	if out.Status == "not_found" && v.JobRef == "" && v.Arguments != nil {
		v.State = "ready"
		v.Error = ""
		v.Summary = "插件确认未受理，使用原编号重试"
		return nil
	}
	switch out.Status {
	case "queued", "running", "unknown", "succeeded", "failed", "cancelled":
	default:
		return errors.New("未知组件状态")
	}
	if !componentTerminal(out.Status) && out.JobRef == "" {
		return errors.New("缺少任务引用")
	}
	if v.JobRef != "" && out.JobRef != "" && v.JobRef != out.JobRef {
		return errors.New("任务引用变化")
	}
	if out.JobRef != "" {
		v.JobRef = out.JobRef
	}
	v.State = out.Status
	v.Summary = out.Summary
	v.Result = out.Result
	v.Error = ""
	return nil
}
func (s *Store) RefreshComponent(ctx context.Context, id string, cancel bool) (ComponentCall, error) {
	v, e := s.ComponentCall(id)
	if e != nil {
		return v, e
	}
	ss, e := s.rawSession(v.SessionID)
	if e != nil {
		return v, e
	}
	p, e := s.Plugin(v.PluginID)
	if e == nil && (p.Component == nil || p.ProjectID != ss.ProjectID) {
		e = errors.New("组件授权已变化")
	}
	if e != nil {
		return v, e
	}
	if componentBinding(p) != v.Binding {
		return v, errors.New("组件连接、版本或授权已变化，请恢复原配置后核查在途任务")
	}
	if (v.State == "ready" || v.State == "awaiting_owner") && cancel {
		v.State = "cancelled"
		v.Summary = "尚未提交，已取消"
		return s.saveComponentCall(v)
	}
	if v.State == "awaiting_owner" || v.State == "ready" {
		return v, nil
	}
	if componentTerminal(v.State) {
		return v, nil
	}
	action := p.Component.StatusAction
	args := map[string]any{"job_ref": v.JobRef}
	if v.JobRef == "" {
		action = p.Component.LookupAction
		args = map[string]any{"invocation_key": v.InvocationKey}
	}
	if cancel {
		if v.JobRef == "" || p.Component.CancelAction == "" {
			return v, errors.New("当前任务不支持取消，请先查回任务")
		}
		action = p.Component.CancelAction
	}
	if action == "" {
		return v, errors.New("此组件未配置状态查回，请人工核对结果")
	}
	args["project_id"] = v.ProjectID
	args["session_id"] = v.SessionID
	raw, e := s.runComponent(ctx, p, action, args)
	if e != nil {
		v.Error = "状态暂不可读，保留最后已知结果"
	} else if e = applyComponentResult(&v, raw, true); e != nil {
		v.Error = "组件状态格式异常，保留最后已知结果"
	}
	// Compare-and-swap: a late refresh must not undo a newer cancellation/terminal result.
	old, er := s.ComponentCall(id)
	if er != nil {
		return v, er
	}
	if old.UpdatedAt != v.UpdatedAt || componentTerminal(old.State) {
		return old, nil
	}
	v.UpdatedAt = max(now(), v.UpdatedAt+1)
	res, er := s.DB.Exec("UPDATE component_calls SET state=?,updated_at=?,data=? WHERE id=? AND updated_at=?", v.State, v.UpdatedAt, encode(v), v.ID, old.UpdatedAt)
	if er == nil {
		n, _ := res.RowsAffected()
		if n == 0 {
			return s.ComponentCall(id)
		}
		er = s.Emit(v.SessionID, "", "component.updated", v)
	}
	return v, er
}
func (s *Store) pollComponents(ctx context.Context) {
	defer s.pollComponentReviews(ctx)
	rows, e := listJSON[ComponentCall](s.DB, "SELECT data FROM component_calls WHERE state IN ('ready','queued','running','unknown','submitting') AND next_poll<? ORDER BY next_poll,updated_at LIMIT 4", now())
	if e != nil {
		return
	}
	for _, v := range rows {
		if ctx.Err() != nil {
			return
		}
		// Reserve the next check even when configuration errors prevent a refresh.
		_, e = s.DB.Exec("UPDATE component_calls SET next_poll=? WHERE id=?", now()+60000, v.ID)
		if e != nil {
			continue
		}
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		if v.State == "ready" {
			_, p, er := s.componentPlugin(v.SessionID, v.PluginID)
			if er == nil && componentBinding(p) == v.Binding {
				old := v.UpdatedAt
				v.State = "submitting"
				v.UpdatedAt = max(now(), old+1)
				res, er := s.DB.Exec("UPDATE component_calls SET state=?,updated_at=?,data=? WHERE id=? AND updated_at=? AND state='ready'", v.State, v.UpdatedAt, encode(v), v.ID, old)
				if er == nil {
					n, _ := res.RowsAffected()
					if n == 1 {
						_, _ = s.executeComponentInvocation(attempt, v, p, v.Arguments)
					}
				}
			}
		} else {
			_, _ = s.RefreshComponent(attempt, v.ID, false)
		}
		cancel()
	}
}
func (s *Store) componentTool(ctx context.Context, in RuntimeInput, tool string, raw json.RawMessage) (any, error) {
	if in.Routing || in.Run.Gateway != nil || in.Run.FollowupID != "" {
		return nil, errors.New("此执行不允许调用工具组件")
	}
	r, e := s.Run(in.Run.ID)
	if e != nil || r.State != "running" || r.SessionID != in.Session.ID {
		return nil, errors.New("执行已停止或身份不匹配")
	}
	if tool == "confirm_component" {
		if in.Run.Agent != nil {
			return nil, errors.New("Agent不能代替本人确认")
		}
		m, e := s.Message(in.Run.MessageID)
		if e != nil {
			return nil, e
		}
		text := strings.TrimSpace(m.Incoming.Text)
		if m.Incoming.Source != "web" && m.Incoming.Source != "wechat" && m.Incoming.Source != "feishu" {
			return nil, errors.New("仅本人消息可确认")
		}
		if !strings.HasPrefix(text, "确认组件 ") {
			return nil, errors.New("需要本人原话：确认组件 加任务短码")
		}
		code := strings.TrimSpace(strings.TrimPrefix(text, "确认组件 "))
		if len(code) < 8 || len(code) > 36 || !regexp.MustCompile(`^[a-f0-9-]+$`).MatchString(code) {
			return nil, errors.New("确认短码无效")
		}
		rows, e := listJSON[ComponentCall](s.DB, "SELECT data FROM component_calls WHERE id LIKE ? AND session_id=? AND state='awaiting_owner' LIMIT 2", code+"%", in.Session.ID)
		if e != nil {
			return nil, e
		}
		if len(rows) != 1 {
			return nil, errors.New("没有唯一待确认任务，请使用完整编号")
		}
		return s.ConfirmComponent(ctx, rows[0].ID)
	}
	if tool == "read_component_results" {
		rows, e := s.ComponentCalls(in.Session.ID)
		if e != nil {
			return nil, e
		}
		out := []any{}
		for i, v := range rows {
			if i >= 10 {
				break
			}
			item := map[string]any{"id": v.ID, "name": v.Name, "state": v.State, "summary": v.Summary, "updatedAt": v.UpdatedAt}
			if len(v.Result) < 8192 {
				item["result"] = v.Result
			} else {
				item["resultOmitted"] = true
			}
			out = append(out, item)
		}
		return out, nil
	}
	if tool == "list_components" {
		ps, e := s.Components(r.ProjectID)
		return ps, e
	}
	var q struct {
		ComponentRequest
		ID string `json:"id"`
	}
	if e = json.Unmarshal(raw, &q); e != nil {
		return nil, e
	}
	if tool == "call_component" {
		if len(q.OperationKey) < 8 || len(q.OperationKey) > 100 {
			return nil, errors.New("需要8至100字符的稳定操作编号")
		}
		if communicationReadOnly(in.Run) || in.Run.ComponentConfig != nil && in.Run.ComponentConfig.Mode == "expert" {
			p, e := s.Plugin(q.PluginID)
			if e != nil {
				return nil, e
			}
			permitted := false
			if p.Component != nil {
				for _, a := range p.Component.Actions {
					if a.Name == q.Action && a.ReadOnly && !a.RequiresOwner {
						permitted = true
					}
				}
			}
			if !permitted {
				return nil, errors.New("只读协作不能执行此组件动作")
			}
		}
		if in.Run.ComponentConfig != nil && q.PluginID == in.Run.ComponentConfig.PluginID && q.Action == in.Run.ComponentConfig.Action {
			q.Config = in.Run.ComponentConfig
			q.Arguments = in.Run.ComponentConfig.Arguments
		}
		q.OwnerConfirmed = false
		q.OperationKey = in.Run.ID + ":" + q.OperationKey
		return s.SubmitComponent(ctx, in.Session.ID, q.ComponentRequest)
	}
	if communicationReadOnly(in.Run) {
		return nil, errors.New("只读协作只能查看结果，不能刷新或取消业务任务")
	}
	v, e := s.ComponentCall(q.ID)
	if e != nil {
		return nil, e
	}
	if v.SessionID != in.Session.ID {
		return nil, errors.New("只能读取当前会话的组件任务")
	}
	if tool == "component_status" {
		return s.RefreshComponent(ctx, q.ID, false)
	}
	if tool == "cancel_component" {
		return s.RefreshComponent(ctx, q.ID, true)
	}
	return nil, fmt.Errorf("未知组件工具: %s", tool)
}

func (s *Store) runComponent(ctx context.Context, p Plugin, action string, args map[string]any) (json.RawMessage, error) {
	if s.componentRunner != nil {
		return s.componentRunner(ctx, p, action, args)
	}
	return componentMCP(ctx, p, action, args)
}

func (s *Store) ConfirmComponent(ctx context.Context, id string) (ComponentCall, error) {
	v, e := s.ComponentCall(id)
	if e != nil {
		return v, e
	}
	if v.State != "awaiting_owner" {
		return v, errors.New("该任务已确认、已停止或不需要确认")
	}
	_, p, e := s.componentPlugin(v.SessionID, v.PluginID)
	if e != nil {
		return v, e
	}
	if componentBinding(p) != v.Binding {
		return v, errors.New("组件配置已改变，请核对后重新提出请求")
	}
	old := v.UpdatedAt
	v.OwnerConfirmed = true
	v.State = "ready"
	v.Summary = "本人已确认，等待执行"
	v.UpdatedAt = max(now(), old+1)
	res, e := s.DB.ExecContext(ctx, "UPDATE component_calls SET state=?,updated_at=?,data=? WHERE id=? AND state='awaiting_owner' AND updated_at=?", v.State, v.UpdatedAt, encode(v), v.ID, old)
	if e != nil {
		return v, e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return s.ComponentCall(id)
	}
	return v, s.Emit(v.SessionID, "", "component.updated", v)
}
