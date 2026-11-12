package api

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"strings"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"

	"github.com/google/uuid"
)

type operationsExpert struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	DisplayName    string         `json:"displayName"`
	Description    string         `json:"description"`
	Content        string         `json:"content"`
	Source         string         `json:"source"`
	ProjectID      string         `json:"projectId"`
	Revision       string         `json:"revision"`
	Editable       bool           `json:"editable"`
	FixedOutput    bool           `json:"fixedOutput"`
	Usage          []string       `json:"usage"`
	Strategies     []string       `json:"strategies,omitempty"`
	RequiredFields []string       `json:"requiredFields,omitempty"`
	Tested         bool           `json:"tested,omitempty"`
	Ref            core.RecordRef `json:"-"`
	Method         core.Method    `json:"-"`
}

func operationsExpertResource(project, name string) string {
	return "ops-expert-" + core.Digest(project + "/" + name)[:36]
}

func operationsTrialKey(project, name string) string {
	return "expert-trial-" + core.Digest(project + "/" + name)[:36]
}

func (a *Server) loadOperationsExpert(ctx context.Context, cfg operationsConfig, name string, local bool) (operationsExpert, error) {
	var out operationsExpert
	cycle := cfg.Cycle
	if cycle == nil || cycle.MethodSource == nil || len(cycle.MethodSource.Sections) != 1 || !core.ValidID(name) {
		return out, core.ErrInvalid
	}
	ref, exists := cycle.MethodRefs[name]
	if !exists {
		return out, core.ErrInvalid
	}
	kind := "original"
	ref.Ref = core.Ref{ResourceID: cycle.MethodSource.ResourceID, Section: cycle.MethodSource.Sections[0]}
	if local {
		kind = "local"
		ref = core.RecordRef{ID: name, Ref: core.Ref{ResourceID: operationsExpertResource(cfg.ProjectID, name), Section: "method"}}
		resource, err := a.Store.Resource(ctx, ref.Ref.ResourceID)
		if err != nil {
			return out, err
		}
		if resource.CurrentVersion == "" {
			return out, sql.ErrNoRows
		}
		ref.Ref.Version = resource.CurrentVersion
	}
	method, resolved, err := a.Store.LoadMethod(ctx, operationsPrincipal(cfg.ProjectID), ref)
	if err != nil {
		return out, err
	}
	ref.Ref = resolved
	out = operationsExpert{ID: "operations:" + cfg.ProjectID + ":" + kind + ":" + name, Name: name,
		DisplayName: method.Name, Description: "运营批量备稿方法", Content: method.Instructions, Source: "operations-" + kind,
		ProjectID: cfg.ProjectID, Revision: resolved.Version, Editable: local, FixedOutput: method.FixedOutputPointer != "", Ref: ref, Method: method}
	if out.DisplayName == "" {
		out.DisplayName = name
	}
	out.Strategies = append([]string{}, cycle.StrategyKeys...)
	if cycle.AdditionalBatch != nil {
		out.Strategies = append(out.Strategies, cycle.AdditionalBatch.Strategy)
	}
	out.RequiredFields = method.RequiredFields
	if local {
		var tested string
		e := a.Store.DB.QueryRowContext(ctx, `SELECT JSON_UNQUOTE(JSON_EXTRACT(body,'$.version')) FROM wb_settings WHERE id=?`, operationsTrialKey(cfg.ProjectID, name)).Scan(&tested)
		if e != nil && !core.IsMissing(e) {
			return out, e
		}
		out.Tested = tested == out.Revision
		for strategy, entries := range cfg.ExpertOverrides {
			if entries[name] == ref {
				out.Usage = append(out.Usage, strategy)
			}
		}
	} else {
		for _, strategy := range out.Strategies {
			if cfg.ExpertOverrides[strategy][name].ID == "" {
				out.Usage = append(out.Usage, strategy)
			}
		}
	}
	sort.Strings(out.Usage)
	return out, nil
}

func (a *Server) operationsExpertsCatalog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !principal(ctx).IsOwner() {
		respond(w, nil, core.ErrDenied)
		return
	}
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT JSON_UNQUOTE(JSON_EXTRACT(body,'$.projectId')) FROM wb_settings WHERE id LIKE 'operations-%' AND JSON_EXTRACT(body,'$.cycle.methodSource') IS NOT NULL`)
	if err != nil {
		respond(w, nil, err)
		return
	}
	var projects []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		projects = append(projects, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		respond(w, nil, err)
		return
	}
	out := []operationsExpert{}
	unavailable := []string{}
	for _, id := range projects {
		cfg, e := a.readOperations(ctx, id)
		if e != nil || cfg.Cycle == nil {
			unavailable = append(unavailable, id)
			continue
		}
		missing := false
		for name := range cfg.Cycle.MethodRefs {
			for _, local := range []bool{false, true} {
				row, e := a.loadOperationsExpert(ctx, cfg, name, local)
				if e != nil {
					missing = missing || !local || !core.IsMissing(e)
					continue
				}
				out = append(out, row)
			}
		}
		if missing {
			unavailable = append(unavailable, id)
		}
	}
	respond(w, map[string]any{"items": out, "unavailable": unavailable}, nil)
}

type operationsExpertInput struct {
	ID       string         `json:"id"`
	Action   string         `json:"action"`
	Revision string         `json:"revision"`
	Content  *string        `json:"content,omitempty"`
	Sample   map[string]any `json:"sample,omitempty"`
	Strategy string         `json:"strategy,omitempty"`
}

func (a *Server) operationsExpertSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	owner := principal(ctx)
	var in operationsExpertInput
	if !decode(w, r, &in) {
		return
	}
	parts := strings.Split(in.ID, ":")
	if !owner.IsOwner() || len(parts) != 4 || parts[0] != "operations" || (parts[2] != "original" && parts[2] != "local") {
		respond(w, nil, core.ErrDenied)
		return
	}
	project, local, name := parts[1], parts[2] == "local", parts[3]
	if in.Action == "trial" {
		a.operationsExpertTrial(w, r, in, project, name, local)
		return
	}
	a.operationsMu.Lock()
	defer a.operationsMu.Unlock()
	cfg, err := a.readOperations(ctx, project)
	if err != nil {
		respond(w, nil, err)
		return
	}
	row, err := a.loadOperationsExpert(ctx, cfg, name, local)
	if err != nil || row.Revision != in.Revision {
		if err == nil {
			err = core.ErrConflict
		}
		respond(w, nil, err)
		return
	}
	switch in.Action {
	case "copy", "update":
		if local != (in.Action == "update") {
			respond(w, nil, core.ErrDenied)
			return
		}
		id := operationsExpertResource(project, name)
		var resource core.Resource
		resource, err = a.Store.Resource(ctx, id)
		if !local && core.IsMissing(err) {
			err = a.Store.PutResource(ctx, owner, core.Resource{ID: id, Name: "运营专家 · " + name, OwnerProject: project, Relation: core.OperationsExpertRelation,
				Sections: []core.Section{{ID: "method", Name: "方法", Fields: []string{"method"}}}})
		} else if err == nil && (resource.OwnerProject != project || resource.Relation != core.OperationsExpertRelation || !local && resource.CurrentVersion != "") {
			err = core.ErrConflict
		}
		if err != nil {
			respond(w, nil, err)
			return
		}
		method := row.Method
		if in.Content != nil {
			method.Instructions = *in.Content
		}
		if strings.TrimSpace(method.Instructions) == "" || len(method.Instructions) > 64000 {
			respond(w, nil, core.ErrInvalid)
			return
		}
		method.Digest = ""
		version := uuid.NewString()
		if err = a.Store.BeginSnapshot(ctx, owner, core.Snapshot{ResourceID: id, Version: version}); err == nil {
			err = a.Store.Stage(ctx, owner, id, version, []core.Record{{ID: name, Fields: map[string]any{"method": method}}})
		}
		if err == nil {
			_, err = a.Store.Publish(ctx, owner, id, version, 1)
		}
		if err == nil {
			row, err = a.loadOperationsExpert(ctx, cfg, name, true)
		}
	case "enable":
		if !local {
			err = core.ErrDenied
			break
		}
		if _, ok := cfg.Cycle.Batch(in.Strategy); !ok {
			err = core.ErrInvalid
			break
		}
		var tested string
		err = a.Store.DB.QueryRowContext(ctx, `SELECT JSON_UNQUOTE(JSON_EXTRACT(body,'$.version')) FROM wb_settings WHERE id=?`, operationsTrialKey(project, name)).Scan(&tested)
		if err != nil || tested != row.Revision {
			err = core.ErrConflict
			break
		}
		if cfg.ExpertOverrides == nil {
			cfg.ExpertOverrides = map[string]map[string]core.RecordRef{}
		}
		if cfg.ExpertOverrides[in.Strategy] == nil {
			cfg.ExpertOverrides[in.Strategy] = map[string]core.RecordRef{}
		}
		cfg.ExpertOverrides[in.Strategy][name] = row.Ref
		if err = a.saveOperations(ctx, cfg); err == nil {
			row, err = a.loadOperationsExpert(ctx, cfg, name, true)
		}
	default:
		err = core.ErrInvalid
	}
	respond(w, row, err)
}

func (a *Server) operationsExpertTrial(w http.ResponseWriter, r *http.Request, in operationsExpertInput, project, name string, local bool) {
	ctx := r.Context()
	cfg, err := a.readOperations(ctx, project)
	if err != nil || !local || len(in.Sample) == 0 || len(in.Sample) > 100 {
		respond(w, nil, core.ErrInvalid)
		return
	}
	row, err := a.loadOperationsExpert(ctx, cfg, name, true)
	if err != nil || row.Revision != in.Revision {
		respond(w, nil, core.ErrConflict)
		return
	}
	records := []core.Record{{ID: "expert-trial", Fields: in.Sample}}
	input, err := core.PrepareInput(row.Method, records)
	var text string
	var fixed bool
	if err == nil {
		text, fixed, err = row.Method.FixedOutput(records)
	}
	if err == nil && !fixed {
		if model, ok := a.Host.(engine.Model); ok {
			var result engine.ModelResult
			result, err = model.Generate(ctx, engine.ModelRequest{RunID: uuid.NewString(), ItemID: "expert-trial", Lane: "owner", Method: row.Method, Input: input})
			text = result.Text
		} else {
			err = core.ErrDenied
		}
	}
	if err == nil {
		err = row.Method.ValidateOutput(text)
	}
	if err == nil && !fixed {
		a.operationsMu.Lock()
		defer a.operationsMu.Unlock()
		current, e := a.loadOperationsExpert(ctx, cfg, name, true)
		if e != nil || current.Revision != row.Revision {
			err = core.ErrConflict
		} else {
			_, err = a.Store.DB.ExecContext(ctx, `INSERT INTO wb_settings (id,body) VALUES(?,JSON_OBJECT('version',?)) ON DUPLICATE KEY UPDATE body=VALUES(body)`, operationsTrialKey(project, name), row.Revision)
		}
	}
	respond(w, map[string]any{"revision": row.Revision, "text": text, "fixed": fixed}, err)
}
