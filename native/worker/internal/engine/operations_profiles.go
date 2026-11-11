package engine

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
	"workbench-native/internal/core"
	"workbench-native/internal/operations"
)

const OperationsProfileRefresh = "operations_profile_refresh"

//go:embed methods/customer-profile.md
var customerProfileInstructions string

var profileLabels = map[string]string{
	"current_need": "当前需求", "business_stage": "业务阶段", "pain_point": "主要卡点",
}

type OperationsProfileStart struct {
	ID, CycleID, Destination string
	Principal                core.Principal
	Source                   core.Ref
}

func (a *Activities) OperationsStartProfiles(ctx context.Context, in OperationsProfileStart) error {
	if err := core.Authorize(ctx, a.Store, in.Principal, in.Source); err != nil {
		return failure(err)
	}
	method := core.Method{Name: "客户档案更新", Instructions: customerProfileInstructions,
		RequiredFields: []string{"evidence"}, MaxInputBytes: 24000, MaxOutputTokens: 700}
	method.Digest = core.Digest(method)
	r := core.Run{ID: in.ID, ParentRunID: in.CycleID, Principal: in.Principal, ExplicitInputs: true,
		Operation: OperationsProfileRefresh, Title: "更新运营用户档案", Destination: in.Destination,
		Sources: []core.Ref{in.Source}, Method: method}
	old, err := a.Store.Run(ctx, in.Principal, in.ID)
	if core.IsMissing(err) {
		err = a.Store.SaveRun(ctx, r)
	} else if err == nil && (old.Operation != r.Operation || old.ParentRunID != r.ParentRunID ||
		old.Destination != r.Destination || len(old.Sources) != 1 || old.Sources[0] != in.Source) {
		err = core.ErrConflict
	}
	if err != nil {
		return failure(err)
	}
	_, err = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: r.ID, TaskQueue: BatchQueue,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE},
		WriteTask, WriteInput{RunID: r.ID, ProjectID: r.Principal.ProjectID, Operation: r.Operation})
	var started *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &started) {
		return nil
	}
	return err
}

func OperationsProfiles(ctx workflow.Context, id string) (err error) {
	defer reportFailure(ctx, id, &err)()
	ctx = activityContext(ctx, BatchQueue)
	execution := workflow.GetInfo(ctx).WorkflowExecution.RunID
	defer finishWrite(ctx, id, execution, &err)()
	var fence core.WriteFence
	if err = workflow.ExecuteActivity(ctx, "BeginWrite", id, execution).Get(ctx, &fence); err != nil {
		return err
	}
	for after := ""; ; {
		var page OperationsPageResult
		if err = workflow.ExecuteActivity(ctx, "OperationsProfilePage", OperationsPage{id, after, fence}).Get(ctx, &page); err != nil {
			return err
		}
		if page.Next == "" {
			break
		}
		after = page.Next
	}
	return workflow.ExecuteActivity(ctx, "OperationsPublish", TaskInput{id, fence}).Get(ctx, nil)
}

func (a *Activities) profilePrevious(ctx context.Context, r core.Run) (core.Ref, error) {
	destination, err := a.Store.Resource(ctx, r.Destination)
	if err != nil {
		return core.Ref{}, err
	}
	var previous core.Ref
	if destination.CurrentVersion != "" {
		previous = core.Ref{ResourceID: destination.ID, Version: destination.CurrentVersion, Section: "main"}
		snapshot, err := a.Store.Snapshot(ctx, previous.ResourceID, previous.Version)
		if err != nil {
			return core.Ref{}, err
		}
		for _, ref := range snapshot.Dependencies {
			if ref.ResourceID != r.Sources[0].ResourceID {
				continue
			}
			var older bool
			err := a.Store.DB.QueryRowContext(ctx, `SELECT (candidate.created_at,candidate.id)<(latest.created_at,latest.id)
				FROM wb_runs candidate JOIN wb_runs latest ON latest.id=? WHERE candidate.id=?`, ref.Version, r.Sources[0].Version).Scan(&older)
			if err != nil {
				return core.Ref{}, err
			}
			if older {
				return core.Ref{}, core.ErrConflict
			}
		}
	}
	frozen, err := a.Store.FreezeCheckpoint(ctx, r, "profile-previous", core.Checkpoint{Input: map[string]any{"version": previous.Version}})
	if err != nil {
		return core.Ref{}, err
	}
	version, _ := frozen.Input["version"].(string)
	if version != previous.Version {
		return core.Ref{}, core.ErrConflict
	}
	return previous, nil
}

func (a *Activities) OperationsProfilePage(ctx context.Context, in OperationsPage) (OperationsPageResult, error) {
	ctx = core.WithFence(ctx, in.Fence)
	stop := keepAlive(ctx)
	defer stop()
	r, err := a.Store.ExecutionRun(ctx, in.RunID)
	if err != nil {
		return OperationsPageResult{}, failure(err)
	}
	if r.Operation != OperationsProfileRefresh || len(r.Sources) != 1 {
		return OperationsPageResult{}, failure(core.ErrInvalid)
	}
	if existing, err := a.Store.Snapshot(ctx, r.Destination, r.ID); err == nil && existing.State == "published" {
		return OperationsPageResult{Count: existing.Count}, nil
	} else if err != nil && !core.IsMissing(err) {
		return OperationsPageResult{}, failure(err)
	}
	previous, err := a.profilePrevious(ctx, r)
	if err != nil {
		return OperationsPageResult{}, failure(err)
	}
	if err = a.Store.BeginSnapshot(ctx, r.Principal, core.Snapshot{ResourceID: r.Destination, Version: r.ID,
		Dependencies: r.Sources, SourceWatermark: r.Sources[0].Version}); err != nil {
		return OperationsPageResult{}, failure(err)
	}
	page, err := a.Store.Read(ctx, r.Principal, r.Sources[0], in.After, 40)
	if err != nil {
		return OperationsPageResult{}, failure(err)
	}
	var inputs []any
	var ids []string
	for _, row := range page.Records {
		raw, _ := row.Fields["raw"].(map[string]any)
		input := raw["_localProfileInput"]
		if input == nil {
			return OperationsPageResult{}, failure(core.ErrInvalid)
		}
		inputs = append(inputs, input)
		ids = append(ids, row.ID)
	}
	if len(inputs) == 0 {
		return OperationsPageResult{}, nil
	}
	var projected []core.Record
	if err = operations.ProjectProfiles(ctx, inputs, &projected); err != nil {
		return OperationsPageResult{}, failure(err)
	}
	old := make(map[string]core.Record)
	if previous.Version != "" {
		rows, err := a.Store.ReadProfileUpdateInputs(ctx, r.Principal, previous, ids)
		if err != nil {
			return OperationsPageResult{}, failure(err)
		}
		for _, row := range rows {
			old[row.ID] = row
		}
	}
	for start := 0; start < len(projected); start += 4 {
		end := min(start+4, len(projected))
		var workers sync.WaitGroup
		errs := make([]error, end-start)
		for index := start; index < end; index++ {
			index := index
			workers.Add(1)
			go func() {
				defer workers.Done()
				raw, _ := page.Records[index].Fields["raw"].(map[string]any)
				evidence, _ := raw["_localProfileEvidence"].([]any)
				oldFields := old[projected[index].ID].Fields
				conflict := raw["_customerIdentityConflict"] == true || raw["identity_conflict"] == true
				if conflict {
					mergeProfileFields(projected[index].Fields, oldFields, nil, false)
					return
				}
				var semantic map[string]any
				semantic, errs[index-start] = a.profileSemantic(ctx, r, projected[index].ID, evidence, oldFields)
				mergeProfileFields(projected[index].Fields, oldFields, semantic, true)
			}()
		}
		workers.Wait()
		if err = errors.Join(errs...); err != nil {
			return OperationsPageResult{}, failure(err)
		}
	}
	if err = a.Store.Stage(ctx, r.Principal, r.Destination, r.ID, projected); err != nil {
		return OperationsPageResult{}, failure(err)
	}
	return OperationsPageResult{Next: page.Next, Count: len(projected)}, nil
}

func (a *Activities) profileSemantic(ctx context.Context, run core.Run, id string, evidence []any, old map[string]any) (map[string]any, error) {
	previous, _ := old["profileSemantic"].(map[string]any)
	if len(evidence) == 0 {
		return previous, nil
	}
	hash := core.Digest([]any{run.Method.Digest, evidence})
	if previous["hash"] == hash {
		return previous, nil
	}
	// Stage makes page retries immutable. Persist both the selected model output
	// and the fallback before staging, so a retry cannot rewrite the same page.
	item, err := a.Store.Item(ctx, run.ID, id)
	if core.IsMissing(err) {
		semantic := previous
		input, prepareErr := core.PrepareInput(run.Method, []core.Record{{ID: id, Fields: map[string]any{"evidence": evidence}}})
		if prepareErr == nil && a.Model != nil {
			call, cancel := context.WithTimeout(ctx, 12*time.Second)
			result, callErr := a.Model.Generate(call, ModelRequest{Lane: "batch", RunID: run.ID, ItemID: id, Method: run.Method, Input: input})
			cancel()
			if callErr == nil {
				if fields, ok := profileModelFields(result.Text, evidence, previous); ok {
					semantic = map[string]any{"hash": hash, "fields": fields}
				}
			}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		body, _ := json.Marshal(semantic)
		err = a.Store.RecordItem(ctx, run.ID, core.ItemResult{ID: id, State: "succeeded", Text: string(body), InputHash: hash})
		if err == nil {
			item, err = a.Store.Item(ctx, run.ID, id)
		}
	}
	if err != nil {
		return nil, err
	}
	var semantic map[string]any
	err = json.Unmarshal([]byte(item.Text), &semantic)
	return semantic, err
}

func profileModelFields(text string, evidence []any, previous map[string]any) (map[string]any, bool) {
	var response map[string]struct{ Value, Ref string }
	if json.Unmarshal([]byte(text), &response) != nil || response == nil {
		return nil, false
	}
	refs := make(map[string]string)
	for _, entry := range evidence {
		row, _ := entry.(map[string]any)
		ref, _ := row["ref"].(string)
		at, _ := row["at"].(string)
		if ref != "" && at != "" {
			refs[ref] = at
		}
	}
	fields := make(map[string]any)
	if prior, ok := previous["fields"].(map[string]any); ok {
		for key, value := range prior {
			fields[key] = value
		}
	}
	for key, entry := range response {
		label, allowed := profileLabels[key]
		value := strings.TrimSpace(entry.Value)
		if !allowed || len([]rune(value)) > 80 || value == "" || refs[entry.Ref] == "" {
			continue
		}
		fields[key] = map[string]any{"label": label, "value": value, "source": "工作台专家提炼",
			"state": "observed_candidate", "sourceRef": entry.Ref, "observedAt": refs[entry.Ref]}
	}
	return fields, true
}

func mergeProfileFields(fields, previous, semantic map[string]any, carryEvidence bool) {
	if semantic != nil {
		fields["profileSemantic"] = semantic
	}
	manual := make(map[string]map[string]any)
	var history []any
	var interactions []any
	oldGroups, _ := previous["groups"].([]any)
	for _, entry := range oldGroups {
		group, _ := entry.(map[string]any)
		items, _ := group["fields"].([]any)
		switch group["title"] {
		case "档案修订历史":
			history = items
		case "互动提炼记录":
			if carryEvidence {
				interactions = items
			}
		case "基础信息":
			for _, value := range items {
				item, _ := value.(map[string]any)
				if label, ok := item["label"].(string); ok && item["state"] == "user_confirmed" {
					manual[label] = item
				}
			}
		}
	}
	// Keep previously sourced semantic values even on the first local run,
	// before the workbench expert has written its own cache.
	suggestions, _ := semantic["fields"].(map[string]any)
	byLabel := make(map[string]map[string]any)
	for _, value := range suggestions {
		item, _ := value.(map[string]any)
		if label, ok := item["label"].(string); ok {
			byLabel[label] = item
		}
	}
	for _, entry := range oldGroups {
		group, _ := entry.(map[string]any)
		if !carryEvidence || group["title"] != "基础信息" {
			continue
		}
		items, _ := group["fields"].([]any)
		for _, value := range items {
			item, _ := value.(map[string]any)
			label, _ := item["label"].(string)
			if (label == "当前需求" || label == "业务阶段" || label == "主要卡点") &&
				byLabel[label] == nil && item["sourceRef"] != nil && item["sourceRef"] != "" && item["value"] != "" {
				byLabel[label] = item
			}
		}
	}
	groups, _ := fields["groups"].([]any)
	for _, entry := range groups {
		group, _ := entry.(map[string]any)
		if group["title"] != "基础信息" {
			continue
		}
		items, _ := group["fields"].([]any)
		for index, value := range items {
			item, _ := value.(map[string]any)
			label, _ := item["label"].(string)
			if confirmed := manual[label]; confirmed != nil {
				items[index] = confirmed
			} else if candidate := byLabel[label]; candidate != nil && (item["value"] == "" ||
				item["state"] == "observed_candidate" || profileObservationAfter(candidate, item)) {
				items[index] = candidate
			}
		}
	}
	if len(history) != 0 {
		groups = append(groups, map[string]any{"title": "档案修订历史", "fields": history})
	}
	if carryEvidence {
		seen := make(map[string]bool)
		var recent []any
		for _, key := range []string{"current_need", "business_stage", "pain_point"} {
			item, _ := suggestions[key].(map[string]any)
			if item != nil {
				entry := map[string]any{"label": "互动提炼 · 待核", "value": item["value"], "source": item["source"],
					"observedAt": item["observedAt"], "sourceRef": item["sourceRef"], "state": item["state"]}
				seen[core.Digest(entry)] = true
				recent = append(recent, entry)
			}
		}
		for _, entry := range interactions {
			if !seen[core.Digest(entry)] && len(recent) < 20 {
				recent = append(recent, entry)
			}
		}
		if len(recent) != 0 {
			groups = append(groups, map[string]any{"title": "互动提炼记录", "fields": recent})
		}
	}
	fields["groups"] = groups
}

func profileObservationAfter(candidate, current map[string]any) bool {
	if current["state"] == "user_confirmed" {
		return false
	}
	candidateAt, _ := candidate["observedAt"].(string)
	currentAt, _ := current["observedAt"].(string)
	next, err := time.Parse(time.RFC3339Nano, candidateAt)
	if err != nil {
		return false
	}
	old, err := time.Parse(time.RFC3339Nano, currentAt)
	return err == nil && next.After(old)
}
