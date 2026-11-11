package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/api/serviceerror"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

const onboardingTitle = "新人前 7 天承接"

type operationsConfig struct {
	Cycle       *engine.OperationsCycleInput `json:"cycle,omitempty"`
	ReadSource  bool                         `json:"readSource,omitempty"`
	ProjectID   string                       `json:"projectId"`
	Input       core.Ref                     `json:"input"`
	Eligibility core.Ref                     `json:"eligibility"`
	Check       string                       `json:"check"`
	Knowledge   core.Ref                     `json:"knowledge"`
	Transport   string                       `json:"transport"`
	Staff       string                       `json:"staffId"`
	Profile     operationsSchedule           `json:"profile"`
	Strategy    operationsSchedule           `json:"strategy"`
}

type operationsSchedule struct {
	Expert string `json:"expert"`
	Time   string `json:"time"`
	ID     string `json:"id,omitempty"`
}

type operationsDraft struct {
	Interested bool     `json:"interested"`
	Focus      string   `json:"focus"`
	Reason     string   `json:"reason"`
	Evidence   []string `json:"evidence"`
	Message    string   `json:"message"`
}

type operationsRow struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname"`
	Status   string `json:"status"`
	Focus    string `json:"focus"`
	Reason   string `json:"reason"`
	Message  string `json:"message"`
	Receipt  string `json:"receipt,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Sendable bool   `json:"sendable"`
}

func operationsKey(project string) string { return "operations-" + project }

func operationsPrincipal(project string) core.Principal {
	return core.Principal{Kind: "project", ProjectID: project, SessionID: operationsKey(project)}
}

func operationsResource(project, suffix string) string {
	return operationsKey(project) + "-" + suffix
}

func (a *Server) readOperations(ctx context.Context, project string) (operationsConfig, error) {
	var cfg operationsConfig
	if !core.ValidID(project) {
		return cfg, core.ErrInvalid
	}
	var body []byte
	err := a.Store.DB.QueryRowContext(ctx, "SELECT body FROM wb_settings WHERE id=?", operationsKey(project)).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return cfg, core.ErrDenied
	}
	if err == nil {
		err = json.Unmarshal(body, &cfg)
	}
	return cfg, err
}

func (a *Server) saveOperations(ctx context.Context, cfg operationsConfig) error {
	body, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = a.Store.DB.ExecContext(ctx, "INSERT INTO wb_settings VALUES(?,?) ON DUPLICATE KEY UPDATE body=VALUES(body)", operationsKey(cfg.ProjectID), string(body))
	return err
}

func (a *Server) operationsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /operations/projects", a.operationsProjects)
	mux.HandleFunc("POST /operations/register", a.registerOperations)
	mux.HandleFunc("POST /operations/setup", a.setupOperations)
	mux.HandleFunc("POST /operations/schedule", a.saveOperationsSchedule)
	mux.HandleFunc("POST /operations/method", a.refreshOperationsMethod)
	mux.HandleFunc("POST /operations/settings", a.saveOperationsSettings)
	mux.HandleFunc("GET /operations/view", a.operationsView)
	mux.HandleFunc("GET /operations/source", a.operationsSource)
	mux.HandleFunc("GET /operations/source-today", a.operationsToday)
	mux.HandleFunc("GET /operations/batch-manifest", a.operationsBatchManifest)
	mux.HandleFunc("POST /operations/original-schedule", a.configureOriginalOperationsSchedule)
	mux.HandleFunc("POST /operations/source-confirm", a.confirmDailyOperations)
	mux.HandleFunc("GET /operations/source-package-status", a.operationsPackageStatus)
	mux.HandleFunc("POST /operations/source-package", a.startOperationsPackage)
	mux.HandleFunc("POST /operations/source-package-edit", a.editOperationsPackage)
	mux.HandleFunc("GET /operations/profiles", a.operationsProfiles)
	mux.HandleFunc("POST /operations/send", a.prepareOperationsSend)
}

func (a *Server) operationsProjects(w http.ResponseWriter, r *http.Request) {
	if !principal(r.Context()).IsOwner() {
		respond(w, nil, core.ErrDenied)
		return
	}
	rows, err := a.Store.DB.QueryContext(r.Context(), "SELECT JSON_UNQUOTE(JSON_EXTRACT(body,'$.projectId')) FROM wb_settings WHERE id LIKE 'operations-%'")
	if err != nil {
		respond(w, nil, err)
		return
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			respond(w, nil, err)
			return
		}
		ids = append(ids, id)
	}
	respond(w, ids, rows.Err())
}

func (a *Server) registerOperations(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProjectID string `json:"projectId"`
	}
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	if !principal(ctx).IsOwner() || !core.ValidID(in.ProjectID) {
		respond(w, nil, core.ErrDenied)
		return
	}
	cfg := operationsConfig{ProjectID: in.ProjectID, Profile: operationsSchedule{Time: "08:30"}, Strategy: operationsSchedule{Time: "09:00"}}
	body, _ := json.Marshal(cfg)
	_, err := a.Store.DB.ExecContext(ctx, "INSERT IGNORE INTO wb_settings VALUES(?,?)", operationsKey(in.ProjectID), string(body))
	respond(w, cfg, err)
}

// Setup is a worker-only owner action. The UI can change a schedule or choose
// existing resources, but cannot extend its population or grant itself access.
func (a *Server) setupOperations(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProjectID   string   `json:"projectId"`
		Input       core.Ref `json:"input"`
		Eligibility core.Ref `json:"eligibility"`
		Check       string   `json:"check"`
		Staff       string   `json:"staffId"`
		ReadSource  *bool    `json:"readSource"`
	}
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	if !principal(ctx).IsOwner() {
		respond(w, nil, core.ErrDenied)
		return
	}
	a.operationsMu.Lock()
	defer a.operationsMu.Unlock()
	cfg, err := a.readOperations(ctx, in.ProjectID)
	if err == nil {
		p := operationsPrincipal(in.ProjectID)
		var page core.Page
		page, err = a.readOperationsPage(ctx, p, in.Input, "", 50)
		if err == nil && (page.Count < 1 || page.Count > 50) {
			err = core.ErrInvalid
		}
		if err == nil && in.Eligibility.ResourceID != "" {
			_, err = a.readOperationsPage(ctx, p, in.Eligibility, "", 1)
			if err == nil && (!core.ValidID(in.Check) || !core.ValidID(in.Staff)) {
				err = core.ErrInvalid
			}
		}
		if err == nil {
			in.Input.Version, in.Eligibility.Version = "", ""
			if (cfg.Profile.ID != "" || cfg.Strategy.ID != "") && cfg.Input != in.Input {
				respond(w, nil, core.ErrConflict)
				return
			}
			cfg.Input, cfg.Eligibility, cfg.Check, cfg.Staff = in.Input, in.Eligibility, in.Check, in.Staff
			if in.ReadSource != nil {
				cfg.ReadSource = *in.ReadSource
			}
			err = a.saveOperations(ctx, cfg)
		}
	}
	respond(w, cfg, err)
}

func (a *Server) readOperationsPage(ctx context.Context, p core.Principal, ref core.Ref, after string, limit int) (core.Page, error) {
	if ref.Version == "" {
		resource, err := a.Store.Resource(ctx, ref.ResourceID)
		if err != nil {
			return core.Page{}, err
		}
		ref.Version = resource.CurrentVersion
	}
	return a.Store.Read(ctx, p, ref, after, limit)
}

func (a *Server) saveOperationsSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProjectID string   `json:"projectId"`
		Knowledge core.Ref `json:"knowledge"`
		Transport string   `json:"transport"`
		Staff     string   `json:"staffId"`
	}
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	if !principal(ctx).IsOwner() {
		respond(w, nil, core.ErrDenied)
		return
	}
	a.operationsMu.Lock()
	defer a.operationsMu.Unlock()
	cfg, err := a.readOperations(ctx, in.ProjectID)
	if err != nil {
		respond(w, nil, err)
		return
	}
	p := operationsPrincipal(in.ProjectID)
	if in.Knowledge.ResourceID != "" {
		page, err := a.readOperationsPage(ctx, p, in.Knowledge, "", 3)
		hasText := slices.ContainsFunc(page.Records, func(v core.Record) bool {
			value, _ := v.Fields["text"].(string)
			return strings.TrimSpace(value) != ""
		})
		if err != nil || !hasText {
			respond(w, nil, core.ErrInvalid)
			return
		}
	}
	if in.Transport != "" {
		t, err := a.Store.Transport(ctx, p, in.Transport)
		if err != nil || t.Kind != "crm-cli" || cfg.Staff == "" || in.Staff != cfg.Staff {
			respond(w, nil, core.ErrDenied)
			return
		}
	}
	in.Knowledge.Version = ""
	changed := cfg.Knowledge != in.Knowledge
	cfg.Knowledge, cfg.Transport = in.Knowledge, in.Transport
	if changed && cfg.Strategy.ID != "" {
		if _, err = a.configureOperationsSchedule(ctx, cfg, "strategy", cfg.Strategy); err != nil {
			respond(w, nil, err)
			return
		}
	}
	respond(w, cfg, a.saveOperations(ctx, cfg))
}

func operationsMethod(expert, instructions, kind string) core.Method {
	text := map[string]any{"type": "string"}
	var props map[string]any
	var required []string
	var variants []any
	contract := "\n只返回 JSON 对象。键只能是输入 profile_fields 中的原档案字段标签，值是字符串。仅整理证据明确的内容，空缺留空。示例 {\"主要卡点\":\"需要明确首个可执行动作\"}。没有可整理内容时返回 {}。"
	if kind == "profile" {
		props = make(map[string]any, len(operationsFields))
		for _, label := range operationsFields {
			props[label] = text
		}
	} else {
		props = map[string]any{
			"interested": map[string]any{"type": "boolean"}, "focus": text, "reason": text, "message": text,
			"evidence": map[string]any{"type": "array", "items": text, "maxItems": 8},
		}
		required = []string{"interested", "focus", "reason", "evidence", "message"}
		variants = []any{
			map[string]any{"properties": map[string]any{
				"interested": map[string]any{"const": false},
				"focus":      map[string]any{"const": ""}, "message": map[string]any{"const": ""},
				"evidence": map[string]any{"maxItems": 0},
			}},
			map[string]any{"properties": map[string]any{
				"interested": map[string]any{"const": true},
				"focus":      map[string]any{"minLength": 1}, "message": map[string]any{"minLength": 1},
				"reason": map[string]any{"minLength": 1}, "evidence": map[string]any{"minItems": 1},
			}},
		}
		contract = "\n只返回一个 JSON 对象，严格使用以下五个键。格式示例 {\"interested\":false,\"focus\":\"\",\"reason\":\"资料不足\",\"evidence\":[],\"message\":\"\"}。interested 表示是否关注，focus 是关注维度，reason 是真实入选依据，evidence 是输入 evidence 的键或 knowledge_excerpts 的 id，message 是草稿。不入选时 message 留空。"
	}
	schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) != 0 {
		schema["required"], schema["oneOf"] = required, variants
	}
	return core.Method{Name: expert, Instructions: instructions + contract, MaxInputBytes: 48000, MaxOutputTokens: 1600, OutputSchema: schema}
}

func operationsMethodRef(project, kind, expert string) core.RecordRef {
	key := uuid.NewSHA1(uuid.NameSpaceURL, []byte(expert)).String()[:12]
	return core.RecordRef{Ref: core.Ref{ResourceID: operationsResource(project, kind+"-"+key), Section: "method"}, ID: "expert"}
}

func (a *Server) publishOperationsMethod(ctx context.Context, owner core.Principal, cfg operationsConfig, kind, expert, instructions string) (core.RecordRef, error) {
	p := operationsPrincipal(cfg.ProjectID)
	ref := operationsMethodRef(cfg.ProjectID, kind, expert)
	id := ref.Ref.ResourceID
	method := operationsMethod(expert, instructions, kind)
	if !core.ValidID(expert) || strings.TrimSpace(instructions) == "" || len(method.Instructions) > 64000 {
		return ref, core.ErrInvalid
	}
	resource, err := a.Store.Resource(ctx, id)
	if core.IsMissing(err) {
		err = a.Store.PutResource(ctx, owner, core.Resource{ID: id, Name: kind + " 专家方法", OwnerProject: p.ProjectID, Sections: []core.Section{{ID: "method", Name: "方法", Fields: []string{"method"}}}})
	}
	if err != nil {
		return ref, err
	}
	if resource.CurrentVersion != "" {
		old, _, err := a.Store.LoadMethod(ctx, p, ref)
		if err != nil {
			return ref, err
		}
		old.Digest = ""
		if core.Digest(old) == core.Digest(method) {
			return ref, nil
		}
	}
	// Keep a new publication distinct when returning to an earlier expert revision.
	version := uuid.NewSHA1(uuid.NameSpaceURL, []byte(id+"/"+resource.CurrentVersion+"/"+core.Digest(method))).String()
	if err = a.Store.BeginSnapshot(ctx, p, core.Snapshot{ResourceID: id, Version: version}); err != nil {
		return ref, err
	}
	if err = a.Store.Stage(ctx, p, id, version, []core.Record{{ID: "expert", Fields: map[string]any{"method": method}}}); err != nil {
		return ref, err
	}
	_, err = a.Store.Publish(ctx, p, id, version, 1)
	return ref, err
}

func (a *Server) saveOperationsSchedule(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProjectID, Kind, Expert, Time, Instructions string
	}
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	owner := principal(ctx)
	if !owner.IsOwner() || (in.Kind != "profile" && in.Kind != "strategy") {
		respond(w, nil, core.ErrDenied)
		return
	}
	t, err := time.Parse("15:04", in.Time)
	if err != nil || t.Format("15:04") != in.Time {
		respond(w, nil, core.ErrInvalid)
		return
	}
	a.operationsMu.Lock()
	defer a.operationsMu.Unlock()
	cfg, err := a.readOperations(ctx, in.ProjectID)
	if err != nil || cfg.Input.ResourceID == "" {
		respond(w, nil, core.ErrInvalid)
		return
	}
	if _, err = a.publishOperationsMethod(ctx, owner, cfg, in.Kind, in.Expert, in.Instructions); err != nil {
		respond(w, nil, err)
		return
	}
	for _, kind := range []string{"profile", "strategy"} {
		destination := operationsResource(cfg.ProjectID, kind)
		if _, err = a.Store.Resource(ctx, destination); core.IsMissing(err) {
			err = a.Store.PutResource(ctx, owner, core.Resource{ID: destination, Name: kind + " 结果", OwnerProject: cfg.ProjectID, Sections: []core.Section{{ID: "text", Name: "结果", Fields: []string{"text", "methodDigest", "inputHash"}}}})
		}
		if err != nil {
			respond(w, nil, err)
			return
		}
	}
	value := operationsSchedule{Expert: in.Expert, Time: in.Time}
	value.ID, err = a.configureOperationsSchedule(ctx, cfg, in.Kind, value)
	if err == nil {
		if in.Kind == "profile" {
			cfg.Profile = value
		} else {
			cfg.Strategy = value
		}
		err = a.saveOperations(ctx, cfg)
	}
	respond(w, cfg, err)
}

func (a *Server) configureOperationsSchedule(ctx context.Context, cfg operationsConfig, kind string, schedule operationsSchedule) (string, error) {
	if cfg.Cycle != nil {
		return "", core.ErrConflict
	}
	t, err := time.Parse("15:04", schedule.Time)
	if err != nil {
		return "", err
	}
	title := onboardingTitle
	input := core.ResourceSelection{ResourceID: cfg.Input.ResourceID, Sections: []string{cfg.Input.Section}}
	if kind == "profile" {
		title = "用户档案清洗"
		resource, err := a.Store.Resource(ctx, cfg.Input.ResourceID)
		if err != nil {
			return "", err
		}
		input.Refresh = resource.ConnectionID != "" && a.Store.CheckResourceWrite(ctx, operationsPrincipal(cfg.ProjectID), input.ResourceID) == nil
	}
	method := operationsMethodRef(cfg.ProjectID, kind, schedule.Expert)
	req := ProgramRequest{Title: title, Destination: operationsResource(cfg.ProjectID, kind), LatestMethod: &method, Quiet: true,
		Selection: &core.Selection{Limit: 50}, Sources: []core.ResourceSelection{input}}
	if kind == "strategy" {
		if cfg.Knowledge.ResourceID != "" {
			req.Knowledge = &cfg.Knowledge
		}
		req.Sources = append(req.Sources, core.ResourceSelection{ResourceID: operationsResource(cfg.ProjectID, "profile"), Sections: []string{"text"}, Optional: true})
	}
	return a.configureProgramInputs(ctx, operationsPrincipal(cfg.ProjectID), ProgramSchedule{ID: kind, Program: req, Hours: []int{t.Hour()}, Minute: t.Minute()}, true)
}

func (a *Server) refreshOperationsMethod(w http.ResponseWriter, r *http.Request) {
	var in struct{ ProjectID, Expert, Instructions string }
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	owner := principal(ctx)
	if !owner.IsOwner() {
		respond(w, nil, core.ErrDenied)
		return
	}
	a.operationsMu.Lock()
	defer a.operationsMu.Unlock()
	cfg, err := a.readOperations(ctx, in.ProjectID)
	if err == nil {
		for kind, schedule := range map[string]operationsSchedule{"profile": cfg.Profile, "strategy": cfg.Strategy} {
			if schedule.Expert == in.Expert {
				_, err = a.publishOperationsMethod(ctx, owner, cfg, kind, in.Expert, in.Instructions)
				if err != nil {
					break
				}
			}
		}
	}
	respond(w, map[string]bool{"updated": err == nil}, err)
}

var operationsFields = []string{"对外称呼", "身份／角色", "收入／营收阶段", "行业", "业务形态", "业务阶段", "业务焦点", "主要卡点", "当前需求"}

type operationsProfileField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

func (a *Server) operationsProfiles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(ctx)
	cfg, err := a.readOperations(ctx, p.ProjectID)
	if p != operationsPrincipal(p.ProjectID) || err != nil {
		respond(w, nil, core.ErrDenied)
		return
	}
	if cfg.Input.ResourceID == "" {
		respond(w, map[string]any{"state": "unconfigured", "records": []core.Record{}}, nil)
		return
	}
	id := r.URL.Query().Get("id")
	if cfg.Cycle != nil && cfg.Cycle.ProfileDirectory != "" {
		a.localOperationsProfiles(w, r, p, cfg.Cycle.ProfileDirectory)
		return
	}
	if cfg.ReadSource {
		q := r.URL.Query()
		if id != "" {
			a.operationsDirectory(w, r, p, cfg, "profile", map[string]string{"id": id, "snapshot_id": q.Get("snapshot")})
		} else {
			a.operationsDirectory(w, r, p, cfg, "profiles", map[string]string{"q": q.Get("q"), "cursor": q.Get("after"), "snapshot_id": q.Get("snapshot")})
		}
		return
	}
	if id == "" {
		page, err := a.readOperationsPage(ctx, p, cfg.Input, r.URL.Query().Get("after"), 20)
		items := make([]map[string]string, 0, len(page.Records))
		for _, row := range page.Records {
			nickname, _ := row.Fields["nickname"].(string)
			items = append(items, map[string]string{"id": row.ID, "nickname": nickname})
		}
		respond(w, map[string]any{"state": "ready", "records": items, "next": page.Next}, err)
		return
	}
	input, err := a.Store.Resource(ctx, cfg.Input.ResourceID)
	if err != nil {
		respond(w, nil, err)
		return
	}
	cfg.Input.Version = input.CurrentVersion
	rows, err := a.Store.ReadIDs(ctx, p, cfg.Input, []string{id})
	if err != nil || len(rows) != 1 {
		respond(w, nil, core.ErrDenied)
		return
	}
	values, _ := rows[0].Fields["profile_fields"].(map[string]any)
	clean := map[string]string{}
	if resource, err := a.Store.Resource(ctx, operationsResource(p.ProjectID, "profile")); err == nil && resource.CurrentVersion != "" {
		ref := core.Ref{ResourceID: resource.ID, Version: resource.CurrentVersion, Section: "text"}
		if updated, err := a.Store.ReadIDs(ctx, p, ref, []string{id}); err == nil && len(updated) == 1 {
			var result map[string]string
			raw, _ := updated[0].Fields["text"].(string)
			if json.Unmarshal([]byte(raw), &result) == nil {
				clean = result
			}
		}
	}
	fields := make([]operationsProfileField, 0, len(operationsFields))
	for _, label := range operationsFields {
		value, _ := values[label].(string)
		if text := strings.TrimSpace(clean[label]); text != "" && (value == "" || label == "业务焦点" || label == "主要卡点" || label == "当前需求") {
			value = text
		}
		fields = append(fields, operationsProfileField{label, value})
	}
	respond(w, map[string]any{"id": id, "nickname": rows[0].Fields["nickname"], "fields": fields}, nil)
}

type operationsHistory struct {
	ID   string    `json:"id"`
	Date time.Time `json:"date"`
}

func (a *Server) operationsHistory(ctx context.Context, p core.Principal, after string) ([]operationsHistory, error) {
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT id,created_at FROM wb_runs
		WHERE project_id=? AND session_id=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.destination'))=?
		AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.operation'))='pipeline'
		AND (?='' OR (created_at,id)<(SELECT created_at,id FROM wb_runs WHERE id=? AND project_id=? AND session_id=?))
		ORDER BY created_at DESC,id DESC LIMIT 20`, p.ProjectID, p.SessionID, operationsResource(p.ProjectID, "strategy"), after, after, p.ProjectID, p.SessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []operationsHistory{}
	for rows.Next() {
		var entry operationsHistory
		if err = rows.Scan(&entry.ID, &entry.Date); err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

func operationsDay(t time.Time) string {
	return t.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02")
}

func (a *Server) operationsDispatches(ctx context.Context, p core.Principal, run string) ([]string, error) {
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT id FROM wb_runs
		WHERE project_id=? AND session_id=?
		AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.parentRunId'))=?
		AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.operation'))='dispatch_batch'
		ORDER BY created_at,id`, p.ProjectID, p.SessionID, run)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type operationsRun struct {
	ID            string          `json:"id"`
	Title         string          `json:"title"`
	State         string          `json:"state"`
	Rows          []operationsRow `json:"rows"`
	Pending       int             `json:"pending"`
	Version       string          `json:"version,omitempty"`
	DispatchID    string          `json:"dispatchId,omitempty"`
	Decision      *core.Decision  `json:"decision,omitempty"`
	Account       string          `json:"account,omitempty"`
	DispatchState string          `json:"dispatchState,omitempty"`
	ReviewCount   int             `json:"reviewCount,omitempty"`
	Resume        bool            `json:"resume,omitempty"`
	RetryReview   bool            `json:"retryReview,omitempty"`
}

func closedOperationsDispatch(state string) bool {
	switch state {
	case "Completed", "Failed", "Canceled", "Terminated", "TimedOut":
		return true
	}
	return false
}

func draftEvidenceValid(d operationsDraft, fields map[string]any, shared map[string]any) bool {
	if !d.Interested || strings.TrimSpace(d.Message) == "" || len(d.Message) > 16000 ||
		strings.TrimSpace(d.Focus) == "" || strings.TrimSpace(d.Reason) == "" || len(d.Evidence) == 0 {
		return false
	}
	facts, _ := fields["evidence"].(map[string]any)
	materials, _ := shared["knowledge_excerpts"].([]any)
	for _, id := range d.Evidence {
		if v, ok := facts[id]; ok {
			value, _ := json.Marshal(v)
			switch string(value) {
			case "", "null", "\"\"", "{}", "[]":
			default:
				continue
			}
		}
		if !slices.ContainsFunc(materials, func(v any) bool {
			row, _ := v.(map[string]any)
			return row["id"] == id
		}) {
			return false
		}
	}
	return true
}

func (a *Server) operationsResults(ctx context.Context, p core.Principal, cfg operationsConfig, id string) (operationsRun, core.Ref, core.Ref, error) {
	out := operationsRun{ID: id, Title: onboardingTitle, State: "awaiting", Rows: []operationsRow{}}
	var source, eligibility core.Ref
	if id == "" {
		if cfg.Input.ResourceID == "" {
			out.State = "unconfigured"
		}
		return out, source, eligibility, nil
	}
	if !core.ValidID(id) {
		return out, source, eligibility, core.ErrDenied
	}
	destination := operationsResource(p.ProjectID, "strategy")
	var childBody []byte
	var childID string
	err := a.Store.DB.QueryRowContext(ctx, `SELECT COALESCE(child.id,''),COALESCE(child.body,'{}')
		FROM wb_runs parent
		LEFT JOIN wb_runs child ON child.project_id=parent.project_id AND child.session_id=parent.session_id
			AND JSON_UNQUOTE(JSON_EXTRACT(child.body,'$.parentRunId'))=parent.id
			AND JSON_UNQUOTE(JSON_EXTRACT(child.body,'$.operation'))='generate'
		WHERE parent.id=? AND parent.project_id=? AND parent.session_id=?
			AND JSON_UNQUOTE(JSON_EXTRACT(parent.body,'$.operation'))='pipeline'
			AND JSON_UNQUOTE(JSON_EXTRACT(parent.body,'$.destination'))=?`, id, p.ProjectID, p.SessionID, destination).Scan(&childID, &childBody)
	if core.IsMissing(err) {
		return out, source, eligibility, core.ErrDenied
	}
	if err != nil {
		return out, source, eligibility, err
	}
	out.State = "running"
	snapshot, err := a.Store.Snapshot(ctx, destination, childID)
	if err != nil && !core.IsMissing(err) {
		return out, source, eligibility, err
	}
	if snapshot.State != "published" {
		switch a.operationsExecution(ctx, id) {
		case "Completed", "Failed", "Canceled", "Terminated", "TimedOut":
			out.State = "unavailable"
		}
		return out, source, eligibility, nil
	}
	var generation core.Run
	if json.Unmarshal(childBody, &generation) != nil || len(generation.Sources) == 0 {
		return out, source, eligibility, core.ErrConflict
	}
	source = core.Ref{ResourceID: destination, Section: "text", Version: childID}
	out.State, out.Version = "ready", childID
	drafts, err := a.Store.Read(ctx, p, source, "", 50)
	if err != nil {
		return out, source, eligibility, err
	}
	ids := make([]string, 0, len(drafts.Records))
	selected := make(map[string]operationsDraft, len(drafts.Records))
	for _, record := range drafts.Records {
		raw, _ := record.Fields["text"].(string)
		var draft operationsDraft
		if json.Unmarshal([]byte(raw), &draft) == nil && draft.Interested {
			ids = append(ids, record.ID)
			selected[record.ID] = draft
		}
	}
	facts := map[string]map[string]any{}
	if len(ids) > 0 {
		records, err := a.Store.ReadIDs(ctx, p, generation.Sources[0], ids)
		if err != nil {
			return out, source, eligibility, err
		}
		for _, record := range records {
			facts[record.ID] = record.Fields
		}
	}
	eligible := map[string]bool{}
	var transport core.Transport
	if len(ids) > 0 && cfg.Transport != "" {
		if allowed, err := a.Store.Transport(ctx, p, cfg.Transport); err == nil {
			transport = allowed
		}
	}
	if transport.Kind == "crm-cli" && cfg.Eligibility.ResourceID != "" {
		out.Account = transport.Name
		resource, err := a.Store.Resource(ctx, cfg.Eligibility.ResourceID)
		if err != nil {
			return out, source, eligibility, err
		}
		eligibility = cfg.Eligibility
		eligibility.Version = resource.CurrentVersion
		for _, check := range resource.Checks {
			if check.ID != cfg.Check || check.Section != eligibility.Section || check.Bindings["customerId"] != "/customerId" ||
				check.Bindings["staffId"] != "/staffId" || check.Bindings["businessSlot"] != "/businessSlot" {
				continue
			}
			validator, err := (core.Method{OutputSchema: check.Schema}).ResolveOutput()
			if err != nil {
				return out, source, eligibility, err
			}
			records, err := a.Store.ReadIDs(ctx, p, eligibility, ids)
			if err != nil {
				return out, source, eligibility, err
			}
			for _, record := range records {
				if validator.Validate(record.Fields) == nil && record.Fields["staffId"] == cfg.Staff {
					eligible[record.ID] = true
				}
			}
		}
	}
	deliveries := map[string]core.Delivery{}
	attempts, err := a.operationsDispatches(ctx, p, id)
	if err != nil {
		return out, source, eligibility, err
	}
	for _, dispatchID := range attempts {
		records, err := a.Store.Deliveries(ctx, p, dispatchID, "")
		if err != nil {
			return out, source, eligibility, err
		}
		for _, record := range records {
			// Once claimed, a record always follows that attempt's receipts.
			previous := deliveries[record.ItemID]
			if previous.ID == "" || previous.State == "superseded" {
				deliveries[record.ItemID] = record
			}
		}
		out.ReviewCount = len(records)
	}
	var previousPlan *core.DispatchPlan
	if len(attempts) > 0 {
		dispatchID := attempts[len(attempts)-1]
		out.DispatchID = dispatchID
		out.DispatchState = a.operationsExecution(ctx, dispatchID)
		out.RetryReview = closedOperationsDispatch(out.DispatchState)
		decision, err := a.Store.Decision(ctx, p, dispatchID+"-d1")
		if err != nil && !core.IsMissing(err) {
			return out, source, eligibility, err
		}
		out.Resume = out.DispatchState == "NotFound" && core.IsMissing(err)
		if err == nil && decision.State == "waiting" && out.DispatchState == "Running" {
			decision.Question = ""
			out.Decision = &decision
		}
		dispatch, err := a.Store.Run(ctx, p, dispatchID)
		if err != nil {
			return out, source, eligibility, err
		}
		previousPlan = dispatch.Dispatch
		if originalTransport, err := a.Store.Transport(ctx, p, previousPlan.ConnectionID); err == nil {
			out.Account = originalTransport.Name
		}
	}
	for _, id := range ids {
		draft := selected[id]
		if !draftEvidenceValid(draft, facts[id], generation.Shared) {
			continue
		}
		nickname, _ := facts[id]["nickname"].(string)
		row := operationsRow{ID: id, Nickname: nickname, Status: "待发送", Focus: draft.Focus, Reason: draft.Reason, Message: draft.Message, Sendable: eligible[id] && (out.DispatchID == "" || out.RetryReview)}
		if !eligible[id] {
			row.Detail = "未通过当前账号的发送资格核验"
		}
		if delivery, ok := deliveries[id]; ok {
			row.Receipt, row.Sendable = delivery.Receipt, false
			switch delivery.State {
			case "sent", "delivered":
				row.Status, row.Detail = "已发送", "CRM 已返回成功发送回执"
			case "failed":
				row.Status, row.Detail = "发送失败", delivery.Error
			case "pending_confirmation":
				row.Detail = "待企微手机确认"
			case "accepted", "unknown", "sending", "submitting":
				row.Detail = "核对原 CRM 回执中"
			case "prepared", "superseded":
				if out.RetryReview {
					row.Sendable = eligible[id]
					row.Detail = "原任务已结束，此人未提交"
					if eligibility.Version != "" && eligibility != previousPlan.Eligibility {
						row.Detail = "发送资格资料已更新，此人未提交"
					}
					if row.Sendable {
						row.Detail += "，可以重新核对并确认"
					} else {
						row.Detail += "，当前发送资格未通过"
					}
				} else {
					row.Detail = "等待本人确认或提交"
				}
			default:
				row.Detail = "等待本人确认或提交"
			}
		}
		if row.Sendable {
			out.Pending++
		}
		out.Rows = append(out.Rows, row)
	}
	return out, source, eligibility, nil
}

func (a *Server) operationsExecution(ctx context.Context, id string) string {
	if a.Temporal == nil {
		return ""
	}
	read, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	state, err := a.Temporal.DescribeWorkflowExecution(read, id, "")
	var missing *serviceerror.NotFound
	if errors.As(err, &missing) {
		return "NotFound"
	}
	if err != nil || state.GetWorkflowExecutionInfo() == nil {
		return ""
	}
	return state.GetWorkflowExecutionInfo().GetStatus().String()
}

func (a *Server) operationsView(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(ctx)
	cfg, err := a.readOperations(ctx, p.ProjectID)
	if p != operationsPrincipal(p.ProjectID) || err != nil {
		respond(w, nil, core.ErrDenied)
		return
	}
	history, err := a.operationsHistory(ctx, p, r.URL.Query().Get("after"))
	if err != nil {
		respond(w, nil, err)
		return
	}
	id := r.URL.Query().Get("runId")
	if id == "" && !cfg.ReadSource && len(history) > 0 && operationsDay(history[0].Date) == operationsDay(time.Now()) {
		id = history[0].ID
	}
	run, _, _, err := a.operationsResults(ctx, p, cfg, id)
	if err != nil {
		respond(w, nil, err)
		return
	}
	resources, err := a.Store.Resources(ctx, p)
	if err != nil {
		respond(w, nil, err)
		return
	}
	var knowledge []map[string]string
	for _, resource := range resources {
		if resource.CurrentVersion == "" || strings.HasPrefix(resource.ID, operationsKey(p.ProjectID)) {
			continue
		}
		for _, section := range resource.Sections {
			if slices.Contains(section.Fields, "text") {
				knowledge = append(knowledge, map[string]string{"resourceId": resource.ID, "section": section.ID, "label": resource.Name + " · " + section.Name})
			}
		}
	}
	next := map[string]time.Time{}
	for kind, schedule := range map[string]operationsSchedule{"profile": cfg.Profile, "strategy": cfg.Strategy} {
		if schedule.ID != "" {
			if state, err := a.Temporal.ScheduleClient().GetHandle(ctx, schedule.ID).Describe(ctx); err == nil && !state.Schedule.State.Paused && len(state.Info.NextActionTimes) > 0 {
				next[kind] = state.Info.NextActionTimes[0]
			}
		}
	}
	respond(w, map[string]any{"config": cfg, "run": run, "history": history, "next": next, "knowledge": knowledge, "staffName": operationsStaffName(cfg)}, nil)
}

func (a *Server) prepareOperationsSend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(ctx)
	var in struct {
		RunID string `json:"runId"`
	}
	if !decode(w, r, &in) {
		return
	}
	if p != operationsPrincipal(p.ProjectID) {
		respond(w, nil, core.ErrDenied)
		return
	}
	if !core.ValidID(in.RunID) {
		respond(w, nil, core.ErrInvalid)
		return
	}
	a.operationsMu.Lock()
	defer a.operationsMu.Unlock()
	cfg, err := a.readOperations(ctx, p.ProjectID)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if cfg.ReadSource {
		respond(w, nil, core.ErrDenied)
		return
	}
	run, source, eligibility, err := a.operationsResults(ctx, p, cfg, in.RunID)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if run.Resume {
		out, err := a.startDispatch(ctx, run.DispatchID)
		respond(w, out, err)
		return
	}
	operationKey := "ops-send-" + in.RunID
	if run.DispatchID != "" {
		if !run.RetryReview {
			respond(w, RunRef{RunID: run.DispatchID}, nil)
			return
		}
		if run.Pending == 0 {
			respond(w, nil, core.ErrConflict)
			return
		}
		previous := run.DispatchID
		if err = a.Store.SupersedeDispatchPrepared(ctx, p, previous); err != nil {
			respond(w, nil, err)
			return
		}
		// Re-read after fencing. If a claim won the race, its original receipt
		// remains authoritative and the record is excluded from the new review.
		run, source, eligibility, err = a.operationsResults(ctx, p, cfg, in.RunID)
		if err != nil || run.DispatchID != previous || !run.RetryReview {
			if err == nil {
				err = core.ErrConflict
			}
			respond(w, nil, err)
			return
		}
		operationKey = "ops-review-" + previous
	}
	ids := []string{}
	for _, row := range run.Rows {
		if row.Sendable {
			ids = append(ids, row.ID)
		}
	}
	if len(ids) == 0 || run.State != "ready" {
		respond(w, nil, core.ErrConflict)
		return
	}
	out, err := a.prepareDispatchForRun(ctx, p, DispatchRequest{OperationKey: operationKey, Title: onboardingTitle, Plan: DispatchSelection{
		Source: source, Eligibility: eligibility, EligibilityCheck: cfg.Check, ConnectionID: cfg.Transport,
		TextPointer: "/text/message", DecodeJSONField: "text", RecordIDs: ids,
	}}, in.RunID)
	respond(w, out, err)
}
