package workbench

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

//go:embed skills/project-brief/SKILL.md
var projectBriefSkill string

func includesInput(scope, id string) bool {
	if strings.TrimSpace(scope) == "" {
		return true
	}
	for _, line := range strings.Split(scope, "\n") {
		key := strings.TrimSuffix(strings.TrimSpace(line), "/")
		if key == "" {
			continue
		}
		if key == "@对话" && strings.HasPrefix(id, "run:") || key == "@任务" && strings.HasPrefix(id, "task:") || key == "@人工说明" && id == "manual" {
			return true
		}
		if id == "file:"+key || strings.HasPrefix(id, "file:"+key+"/") {
			return true
		}
	}
	return false
}

func (s *Store) scheduleSources(p Project, sc Schedule) ([]ContextSource, error) {
	return s.scheduleSourcesContext(context.Background(), p, sc)
}
func (s *Store) scheduleSourcesContext(ctx context.Context, p Project, sc Schedule) ([]ContextSource, error) {
	out, e := s.scheduleNonFiles(p, sc)
	if e != nil {
		return nil, e
	}
	root, e := s.openRoot(p.ID)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	files, e := scheduleFiles(ctx, root, sc.SourceScope)
	if e != nil {
		return nil, e
	}
	out = append(out, files...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (s *Store) scheduleNonFiles(p Project, sc Schedule) ([]ContextSource, error) {
	var out []ContextSource
	if includesInput(sc.SourceScope, "manual") {
		c, e := s.ProjectContext(p.ID)
		if e != nil {
			return nil, e
		}
		if c.Manual != "" {
			out = append(out, ContextSource{ID: "manual", Hash: digest([]byte(c.Manual)), At: c.ManualAt, Status: "read"})
		}
	}
	if includesInput(sc.SourceScope, "run:test") {
		runs, e := listJSON[Run](s.DB, `SELECT r.data FROM runs r JOIN sessions ss ON ss.id=r.session_id WHERE r.project_id=? AND coalesce(json_extract(ss.data,'$.recycledAt'),0)=0 AND r.state IN ('completed','failed','cancelled','interrupted') AND coalesce(json_extract(r.data,'$.scheduleId'),'')!=? AND coalesce(json_extract(r.data,'$.scheduleConfig.output'),'')!='project_context'`, p.ID, sc.ID)
		if e != nil {
			return nil, e
		}
		for _, r := range runs {
			out = append(out, ContextSource{ID: "run:" + r.ID, Hash: digest([]byte(encode([]string{r.Text, r.State, r.Result}))), At: r.FinishedAt, Status: "read"})
		}
	}
	if includesInput(sc.SourceScope, "task:test") {
		tasks, e := s.Tasks(p.ID)
		if e != nil {
			return nil, e
		}
		for _, t := range tasks {
			t = taskEvidence(t)
			out = append(out, ContextSource{ID: "task:" + t.ID, Hash: digest([]byte(encode(t))), At: t.UpdatedAt, Status: "read"})
		}
	}
	return out, nil
}
func taskEvidence(t ProjectTask) ProjectTask {
	t.DraftText = ""
	t.DraftVersion = 0
	t.SessionID = ""
	t.Operation = ""
	return t
}
func scheduleFingerprint(src []ContextSource) string {
	h := sha256.New()
	for _, v := range src {
		fmt.Fprintf(h, "%s\x00%s\n", v.ID, v.Hash)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func briefExcerpt(id string, b []byte, complete bool) (string, string) {
	if !complete && len(b) > 0 {
		i := len(b) - 1
		for i > 0 && !utf8.RuneStart(b[i]) {
			i--
		}
		if !utf8.FullRune(b[i:]) {
			b = b[:i]
		}
	}
	if !utf8.Valid(b) || strings.IndexByte(string(b), 0) >= 0 {
		return "仅核对文件版本，未解析内容", "unparsed"
	}
	text := string(b)
	ext := strings.ToLower(path.Ext(id))
	if len(b) > 6000 && ext == ".json" {
		dec := json.NewDecoder(strings.NewReader(text))
		tok, e := dec.Token()
		if e != nil {
			return "JSON 无法解析", "unparsed"
		}
		count := 0
		var fields []string
		for dec.More() {
			key := ""
			if tok == json.Delim('{') {
				k, e := dec.Token()
				if e != nil {
					complete = false
					break
				}
				key = fmt.Sprint(k)
			}
			var value json.RawMessage
			if dec.Decode(&value) != nil {
				complete = false
				break
			}
			count++
			if len(fields) >= 24 || (key == "" && count > 1) {
				continue
			}
			label := clipText(key, 80)
			if len(value) > 0 {
				switch value[0] {
				case '[':
					var items []json.RawMessage
					if json.Unmarshal(value, &items) == nil {
						label += fmt.Sprintf("（%d 项）", len(items))
						if len(items) > 0 {
							value = items[0]
						}
					}
				}
				if len(value) > 0 && value[0] == '{' {
					var row map[string]json.RawMessage
					if json.Unmarshal(value, &row) == nil {
						names := make([]string, 0, len(row))
						for k := range row {
							names = append(names, clipText(k, 60))
						}
						sort.Strings(names)
						label += " 字段 " + strings.Join(names[:min(len(names), 12)], "、")
					}
				}
			}
			if label != "" {
				fields = append(fields, label)
			}
		}
		return fmt.Sprintf("JSON 结构（未逐项解读）：根 %v，已识别 %d 项，结构 %s，完整计数 %t", tok, count, strings.Join(fields, "；"), complete), "partial"
	}
	if len(b) > 6000 && ext == ".csv" {
		r := csv.NewReader(strings.NewReader(text))
		header, e := r.Read()
		if e == nil {
			return "CSV 字段（未逐行解读）：" + clipText(strings.Join(header, "、"), 600), "partial"
		}
	}
	rs := []rune(text)
	if len(rs) <= 2400 && complete {
		return text, "read"
	}
	headings := []string{}
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "#") && len(headings) < 16 {
			headings = append(headings, clipText(l, 80))
		}
	}
	return "局部摘录；目录：" + strings.Join(headings, " / ") + "\n" + clipText(text, 1500), "partial"
}
func clipText(text string, n int) string {
	rs := []rune(text)
	if len(rs) > n {
		return string(rs[:n])
	}
	return text
}

type scheduleBrief struct {
	inputs  []ContextSource
	base    ProjectContext
	prompt  string
	replace bool
}

func (s *Store) prepareBrief(p Project, r Run, src []ContextSource) (scheduleBrief, error) {
	b := scheduleBrief{inputs: append([]ContextSource(nil), src...)}
	var e error
	b.base, e = s.ProjectContext(p.ID)
	if e != nil {
		return b, e
	}
	previous := map[string]string{}
	raw := ""
	e = s.DB.QueryRow("SELECT sources FROM schedule_inputs WHERE schedule_id=?", r.ScheduleID).Scan(&raw)
	if e != nil && e != sql.ErrNoRows {
		return b, e
	}
	var prior []ContextSource
	if raw != "" {
		if e = json.Unmarshal([]byte(raw), &prior); e != nil {
			return b, e
		}
	}
	for _, v := range prior {
		previous[v.ID] = v.Hash
	}
	current := map[string]bool{}
	for _, v := range src {
		current[v.ID] = true
	}
	samePublication := b.base.PublishedBy == r.ScheduleID && b.base.PublishedVersion == b.base.Version
	// A different writer or a manual edit replaces the baseline for incremental synthesis.
	readAll := !samePublication || len(prior) == 0
	b.replace = b.base.State == "invalid"
	for k := range previous {
		if !current[k] {
			b.replace = true
		}
	}
	// Rebuilding after withdrawal cannot silently discard a second task's
	// contribution, and withdrawn evidence must not be fed back to the model.
	if b.replace && b.base.Version > 0 {
		published, e := getJSON[ProjectContext](s.DB, "SELECT data FROM context_versions WHERE project_id=? AND version=?", p.ID, b.base.Version)
		if e != nil && !errors.Is(e, ErrNotFound) {
			return b, e
		}
		for _, v := range published.Sources {
			if !includesInput(r.ScheduleConfig.SourceScope, v.ID) {
				return b, errors.New("现有说明还引用其他资料，当前范围无法安全重建。请扩充资料范围或先人工修订说明；现有正文已保留")
			}
		}
	}
	oldStatus := map[string]string{}
	for _, v := range prior {
		oldStatus[v.ID] = v.Status
	}
	root, e := s.openRoot(p.ID)
	if e != nil {
		return b, e
	}
	defer root.Close()
	var evidence []ContextSource
	for i, v := range b.inputs {
		if !b.replace && !readAll && previous[v.ID] == v.Hash {
			b.inputs[i].Status = oldStatus[v.ID]
			continue
		}
		if strings.HasPrefix(v.ID, "file:") {
			file, e := root.Open(strings.TrimPrefix(v.ID, "file:"))
			if e != nil {
				return b, e
			}
			const bound = 4 << 20
			data, e := io.ReadAll(io.LimitReader(file, bound+1))
			file.Close()
			if e != nil {
				return b, e
			}
			v.Text, v.Status = briefExcerpt(v.ID, data[:min(len(data), bound)], len(data) <= bound)
		} else {
			str, e := s.loadContextEvidence(p, b.base, v)
			if e != nil {
				return b, e
			}
			v.Text, v.Status = briefExcerpt(v.ID, []byte(str), true)
		}
		b.inputs[i].Status = v.Status
		evidence = append(evidence, v)
	}
	// Prioritize first-party notes and current decisions before archives when the scope is wide.
	sort.SliceStable(evidence, func(i, j int) bool {
		rank := func(id string) int {
			switch {
			case id == "manual", strings.HasPrefix(id, "task:"), strings.Contains(strings.ToLower(id), "readme"):
				return 0
			case strings.Contains(id, "归档"), strings.Contains(id, "/"):
				return 2
			}
			return 1
		}
		return rank(evidence[i].ID) < rank(evidence[j].ID)
	})
	// Keep one short evidence packet even if the selected directory has thousands of entries.
	const excerptWindow = 60000
	chars := 0
	shown := evidence[:0]
	omitted := map[string]bool{}
	for _, item := range evidence {
		size := utf8.RuneCountInString(item.ID) + utf8.RuneCountInString(item.Text) + 64
		if chars+size > excerptWindow {
			omitted[item.ID] = true
			continue
		}
		chars += size
		shown = append(shown, item)
	}
	evidence = shown
	for i := range b.inputs {
		if omitted[b.inputs[i].ID] {
			b.inputs[i].Status = "unreviewed"
		}
	}
	skill := projectBriefSkill
	if r.ExpertID != "" {
		_, e := s.ExpertVersion(r.ExpertID, r.ExpertDigest)
		if e != nil {
			return b, e
		}
		f, e := os.ReadFile(filepath.Join(s.Root, "experts", r.ExpertID, r.ExpertDigest, "SKILL.md"))
		if e != nil {
			return b, e
		}
		resources, e := expertReferences(filepath.Join(s.Root, "experts", r.ExpertID, r.ExpertDigest), string(f))
		if e != nil {
			return b, e
		}
		skill += "\n附加专家方法：\n" + string(f) + resources
	}
	priorText := b.base.Description
	if b.replace {
		priorText = b.base.Manual
	}
	b.prompt = skill + "\n任务指令：" + r.Text + "\n已有说明：" + priorText + "\n本轮资料：" + encode(evidence) + fmt.Sprintf("\n另有 %d 份资料只核对了版本，未参与本轮内容提炼。", len(omitted))
	return b, nil
}

// Commit the run, its input checkpoint and optional project description together.
// This also closes the automatic gate before a failed run releases its lease.
func (s *Store) finishConditional(r *Run, src []ContextSource, b *scheduleBrief) error {
	cfg := r.ScheduleConfig
	var next *ProjectContext
	if r.State == "completed" && cfg.Output == "project_context" {
		c, e := s.validateBrief(*r, b)
		if e != nil {
			r.State = "failed"
			r.Error = e.Error()
		} else {
			next = &c
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	if next != nil {
		// Task edits do not take the project's agent lease; recheck their versions after locking.
		nonFiles, e := s.scheduleNonFiles(Project{ID: r.ProjectID}, *cfg)
		if e != nil {
			return e
		}
		current := map[string]string{}
		for _, v := range nonFiles {
			current[v.ID] = v.Hash
		}
		for _, v := range src {
			if strings.HasPrefix(v.ID, "file:") {
				continue
			}
			if current[v.ID] != v.Hash {
				r.State = "failed"
				break
			}
			delete(current, v.ID)
		}
		if len(current) > 0 || r.State != "completed" {
			r.State = "failed"
			r.Error = "执行期间资料发生变化，原说明未更新"
			next = nil
		}
	}
	commit := func(tx *sql.Tx) error {
		var raw string
		if e := tx.QueryRow("SELECT data FROM schedules WHERE id=?", r.ScheduleID).Scan(&raw); e != nil {
			return e
		}
		sc, e := decodeJSON[Schedule](raw)
		if e != nil {
			return e
		}
		if next != nil {
			var pRaw, cRaw string
			e = tx.QueryRow("SELECT p.data,coalesce(c.data,'') FROM projects p LEFT JOIN project_contexts c ON p.id=c.project_id WHERE p.id=?", r.ProjectID).Scan(&pRaw, &cRaw)
			if e != nil {
				return e
			}
			p, e := decodeJSON[Project](pRaw)
			if e != nil {
				return e
			}
			var current ProjectContext
			if cRaw != "" {
				if e = json.Unmarshal([]byte(cRaw), &current); e != nil {
					return e
				}
			}
			if p.Description != b.base.Description || current.Version != b.base.Version {
				return ErrConflict
			}
			// Task events are synchronous, but do not increment the description version.
			seen := map[ContextChange]bool{}
			for _, item := range b.base.Recent {
				seen[item] = true
			}
			for _, item := range next.Recent {
				seen[item] = true
			}
			for _, item := range rollingChanges(current.Recent, now()) {
				if !seen[item] {
					next.Recent = append(next.Recent, item)
					seen[item] = true
				}
			}
			p.Description = next.Description
			if _, e = tx.Exec("UPDATE projects SET data=? WHERE id=?", encode(p), p.ID); e != nil {
				return e
			}
			if _, e = tx.Exec("INSERT INTO project_contexts VALUES(?,?) ON CONFLICT(project_id) DO UPDATE SET data=excluded.data", p.ID, encode(next)); e != nil {
				return e
			}
			if _, e = tx.Exec("INSERT INTO context_versions VALUES(?,?,?)", p.ID, next.Version, encode(next)); e != nil {
				return e
			}
			src = b.inputs
		}
		if r.State == "completed" {
			if !sc.Enabled && strings.HasPrefix(sc.PausedReason, "执行已暂停：") {
				sc.PausedReason = "手动执行成功，定时仍暂停"
				if _, e = tx.Exec("UPDATE schedules SET data=? WHERE id=?", encode(sc), sc.ID); e != nil {
					return e
				}
			}
			if cfg.Output == "" {
				src, e = includeOwnRevisions(tx, *r, src)
				if e != nil {
					return e
				}
			}
			if _, e = tx.Exec("INSERT INTO schedule_inputs VALUES(?,?,?) ON CONFLICT(schedule_id) DO UPDATE SET fingerprint=excluded.fingerprint,sources=excluded.sources", sc.ID, scheduleFingerprint(src), encode(src)); e != nil {
				return e
			}
		} else {
			sc.Enabled = false
			sc.PausedReason = "执行已暂停：" + r.Error
			if _, e = tx.Exec("UPDATE schedules SET enabled=0,data=? WHERE id=?", encode(sc), sc.ID); e != nil {
				return e
			}
		}
		if _, e = tx.Exec("UPDATE runs SET state=?,data=? WHERE id=?", r.State, encode(r), r.ID); e != nil {
			return e
		}
		if _, e = tx.Exec("UPDATE schedule_checks SET outcome=?,detail=? WHERE schedule_id=? AND run_id=? AND outcome<>'active'", r.State, r.Error, sc.ID, r.ID); e != nil {
			return e
		}
		_, e = tx.Exec("DELETE FROM project_locks WHERE run_id=?", r.ID)
		return e
	}
	e := s.Transaction(context.Background(), commit)
	if errors.Is(e, ErrConflict) {
		next = nil
		r.State = "failed"
		r.Error = "项目说明已被修改，发布取消；请核对后手动执行"
		e = s.Transaction(context.Background(), commit)
	}
	if e != nil {
		return e
	}
	return s.Emit(r.SessionID, r.ID, "run.updated", *r)
}
func (s *Store) validateBrief(r Run, b *scheduleBrief) (ProjectContext, error) {
	if b == nil {
		return ProjectContext{}, errors.New("缺少运行时资料，未发布")
	}
	var v struct {
		Description string          `json:"description"`
		Recent      []ContextChange `json:"recent"`
		Error       string          `json:"error"`
	}
	if e := json.Unmarshal([]byte(jsonPayload(r.Result)), &v); e != nil {
		return ProjectContext{}, errors.New("整理方法未返回有效 JSON，原说明未更新")
	}
	if v.Error != "" {
		return ProjectContext{}, fmt.Errorf("整理未完成：%s", clipText(v.Error, 500))
	}
	if strings.TrimSpace(v.Description) == "" || len(v.Description) > 100000 {
		return ProjectContext{}, errors.New("项目说明为空或超过长度限制，未发布")
	}
	p, e := s.Project(r.ProjectID)
	if e != nil {
		return b.base, e
	}
	fresh, e := s.scheduleSources(p, *r.ScheduleConfig)
	if e != nil {
		return b.base, e
	}
	if scheduleFingerprint(fresh) != r.InputFingerprint {
		return b.base, errors.New("执行期间资料发生变化，原说明未更新")
	}
	c := b.base
	if b.replace {
		c.Recent = nil
	}
	dates := map[string]int64{}
	for _, x := range b.inputs {
		dates[x.ID] = x.At
	}
	for _, item := range v.Recent {
		at, ok := dates[item.Source]
		if !ok {
			return c, errors.New("动态来源无法核对，原说明未更新")
		}
		item.At = at
		c.Recent = append(c.Recent, item)
	}
	c.Recent = rollingChanges(c.Recent, now())
	c.Sources = append([]ContextSource(nil), b.inputs...)
	if !b.replace {
		known := map[string]bool{}
		for _, v := range c.Sources {
			known[v.ID] = true
		}
		for _, v := range b.base.Sources {
			if !known[v.ID] {
				c.Sources = append(c.Sources, v)
			}
		}
	}
	c.PublishedBy = r.ScheduleID
	c.PublishedVersion = c.Version + 1
	c.Fingerprint = r.InputFingerprint
	c.Version++
	c.State = "ready"
	c.Error = ""
	c.UpdatedAt = now()
	c.Description = v.Description
	return c, nil
}

// Older releases could invalidate a published description for an unrelated file.
// Restore only the same publication, with all its original hashes revalidated.
func restoreUnchangedBrief(tx *sql.Tx, project, fingerprint string) error {
	var liveRaw, savedRaw, description string
	e := tx.QueryRow(`SELECT c.data,v.data,p.data FROM project_contexts c JOIN projects p ON p.id=c.project_id JOIN context_versions v ON v.project_id=c.project_id AND v.version=json_extract(c.data,'$.version') WHERE c.project_id=? AND json_extract(c.data,'$.state')='invalid'`, project).Scan(&liveRaw, &savedRaw, &description)
	if e == sql.ErrNoRows {
		return nil
	}
	if e != nil {
		return e
	}
	c, e := decodeJSON[ProjectContext](liveRaw)
	if e != nil {
		return e
	}
	old, e := decodeJSON[ProjectContext](savedRaw)
	if e != nil {
		return e
	}
	p, e := decodeJSON[Project](description)
	if e != nil {
		return e
	}
	if old.Fingerprint != fingerprint || scheduleFingerprint(old.Sources) != fingerprint || p.Description != old.Description {
		return nil
	}
	c.State = "ready"
	c.Error = ""
	c.Sources = old.Sources
	c.Fingerprint = fingerprint
	_, e = tx.Exec("UPDATE project_contexts SET data=? WHERE project_id=?", encode(c), project)
	return e
}
