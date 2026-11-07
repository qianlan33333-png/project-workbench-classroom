package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"workbench-native/internal/core"
)

type Review struct {
	State     string `json:"state"`
	Summary   string `json:"summary"`
	Question  string `json:"question,omitempty"`
	NextHours int    `json:"nextHours,omitempty"`
}

func (a *Activities) ReviewFollowup(ctx context.Context, in FollowupInput) (FollowupInput, error) {
	stop := keepAlive(ctx)
	defer stop()
	r, e := a.Store.ExecutionRun(ctx, in.RunID)
	if e != nil {
		return in, failure(e)
	}
	key := fmt.Sprintf("review-%d", in.Round)
	old, e := a.Store.Item(ctx, r.ID, key)
	var reply string
	if e == nil {
		reply = old.Text
	} else if !core.IsMissing(e) {
		return in, e
	} else {
		checkpoint, e := a.Store.Checkpoint(ctx, r, key)
		if core.IsMissing(e) {
			facts := []core.Page{}
			refs := []core.Ref{}
			targets := []map[string]any{}
			for _, targetID := range r.TargetRunIDs {
				status, err := InspectRun(ctx, a.Store, a.Temporal, r.Principal, targetID)
				if err != nil {
					return in, failure(err)
				}
				related, truncated, err := a.Store.RelatedRuns(ctx, r.Principal, targetID)
				if err != nil {
					return in, failure(err)
				}
				steps := []RunOverview{}
				for _, child := range related {
					v, err := InspectRun(ctx, a.Store, a.Temporal, r.Principal, child.ID)
					if err != nil {
						return in, failure(err)
					}
					steps = append(steps, v)
				}
				target := map[string]any{"run": status, "relatedRuns": steps, "relatedRunsTruncated": truncated}
				if status.Operation == "dispatch" {
					original, err := a.Store.Run(ctx, r.Principal, targetID)
					if err != nil {
						return in, failure(err)
					}
					report, err := a.Store.Checkpoint(ctx, original, "delegation-report")
					if err == nil {
						target["assistantReport"] = report.Input
						refs = core.UnionRefs(refs, report.Refs)
					} else if !core.IsMissing(err) {
						return in, failure(err)
					}
					target["executionMeaning"] = "派发执行完成仅代表目标Session接收；业务完成须看relatedRuns与真实回执，assistantReport仅为助手汇报。"
				}
				targets = append(targets, target)
			}
			for _, ref := range r.Sources {
				resource, err := a.Store.Resource(ctx, ref.ResourceID)
				if err != nil {
					return in, err
				}
				ref.Version = resource.CurrentVersion
				page, err := a.Store.Read(ctx, r.Principal, ref, "", 5)
				if err != nil {
					return in, failure(err)
				}
				facts = append(facts, page)
				refs = append(refs, ref)
			}
			answers, err := a.Store.Answers(ctx, r)
			if err != nil {
				return in, err
			}
			lineage, err := a.Store.SessionSources(ctx, r.Principal)
			if err != nil {
				return in, err
			}
			checkpoint, e = a.Store.FreezeCheckpoint(ctx, r, key, core.Checkpoint{Refs: core.UnionRefs(refs, lineage), Input: map[string]any{"goal": r.Goal, "evidence": facts, "evidenceScope": "每个资源只取首页最多5条样本，不代表整库；具体任务以targetRuns为准。", "targetRuns": targets, "check": in.Checks, "ownerAnswers": answers}})
		}
		if e != nil {
			return in, failure(e)
		}
		method := core.Method{Name: "followup-review", Instructions: "核实给出的目标与来源。targetRuns来自真实执行/发布/回执读取；Unknown、截断、未提供的状态都不能推断完成；派发Completed仅为已接收，不是业务完成。evidence仅为抽样，不能代表整库。仅输出 JSON：state 为 completed、waiting 或 followup；summary 为有证据的简短结果；waiting 时给 question；followup 时给 nextHours（1-720）。资料不足不得宣称完成，不执行外部操作。", MaxInputBytes: 64000, MaxOutputTokens: 1200}
		result, e := a.Model.Generate(ctx, ModelRequest{RunID: r.ID, ItemID: key, Method: method, Input: checkpoint.Input})
		if e != nil {
			return in, e
		}
		reply = result.Text
		if _, e = validReview(reply); e != nil {
			return in, e
		}
		if e = a.Store.RecordItem(ctx, r.ID, core.ItemResult{ID: key, State: "succeeded", Text: reply, InputHash: core.Digest(struct {
			ID    string
			Check int
		}{r.ID, in.Round})}); e != nil {
			return in, e
		}
		canonical, e := a.Store.Item(ctx, r.ID, key)
		if e != nil {
			return in, e
		}
		reply = canonical.Text
	}
	if _, e = a.Store.Checkpoint(ctx, r, key); e != nil {
		return in, failure(e)
	}
	review, e := validReview(reply)
	if e != nil {
		return in, e
	}
	if review.Summary == "" {
		return in, failure(core.ErrInvalid)
	}
	in.NextAt = time.Time{}
	in.Waiting = false
	switch review.State {
	case "completed":
	case "waiting":
		d, e := a.Store.Ask(ctx, r, int64(in.Round), review.Question)
		if e != nil {
			return in, e
		}
		in.Waiting = true
		in.DecisionID = d.ID
	case "followup":
		if review.NextHours < 1 || review.NextHours > 720 {
			return in, failure(core.ErrInvalid)
		}
		canonical, e := a.Store.Item(ctx, r.ID, key)
		if e != nil {
			return in, e
		}
		in.NextAt = canonical.CreatedAt.Add(time.Duration(review.NextHours) * time.Hour)
	default:
		return in, failure(core.ErrInvalid)
	}
	e = a.Store.Emit(ctx, core.RunEvent{ID: r.ID + "-" + key, SessionID: r.Principal.SessionID, RunID: r.ID, Kind: "followup." + review.State, Text: review.Summary, Details: map[string]any{"decisionId": in.DecisionID, "nextAt": in.NextAt, "checks": in.Checks}})
	return in, e
}
func (a *Activities) ContinueFollowup(ctx context.Context, in FollowupInput, answer Decision) (FollowupInput, error) {
	r, e := a.Store.ExecutionRun(ctx, in.RunID)
	if e != nil {
		return in, failure(e)
	}
	if answer.ID != in.DecisionID {
		return in, nil
	}
	d, e := a.Store.Decision(ctx, r.Principal, answer.ID)
	if e != nil {
		return in, e
	}
	if d.State != "answered" || d.Revision != answer.Revision || d.Digest != answer.Digest {
		return in, nil
	}
	// The decision becomes an evidence record; continuing uses the same durable run.
	e = a.Store.Emit(ctx, core.RunEvent{ID: d.ID + "-answered", SessionID: r.Principal.SessionID, RunID: r.ID, Kind: "decision.answered", Text: d.Answer})
	if e != nil {
		return in, e
	}
	in.Waiting = false
	in.Manual = true
	in.DecisionID = ""
	in.NextAt = time.Now().UTC()
	return in, nil
}
func (a *Activities) FollowupLimit(ctx context.Context, in FollowupInput) (FollowupInput, error) {
	r, e := a.Store.ExecutionRun(ctx, in.RunID)
	if e != nil {
		return in, failure(e)
	}
	d, e := a.Store.Ask(ctx, r, int64(1000000+in.Round), "已达到三次自动复查上限。若需要继续，请说明下一步；也可以停止此事项。")
	if e != nil {
		return in, e
	}
	in.Waiting = true
	in.DecisionID = d.ID
	in.NextAt = time.Time{}
	e = a.Store.Emit(ctx, core.RunEvent{ID: d.ID + "-limit", SessionID: r.Principal.SessionID, RunID: r.ID, Kind: "followup.limit", Text: "已达到三次自动复查上限，事项保留，等待本人决定下一步。", Details: map[string]any{"decisionId": d.ID, "checks": in.Checks}})
	return in, e
}

func validReview(text string) (Review, error) {
	var v Review
	if e := json.Unmarshal([]byte(text), &v); e != nil {
		return v, fmt.Errorf("review returned invalid JSON")
	}
	if v.Summary == "" {
		return v, core.ErrInvalid
	}
	switch v.State {
	case "completed":
	case "waiting":
		if v.Question == "" {
			return v, core.ErrInvalid
		}
	case "followup":
		if v.NextHours < 1 || v.NextHours > 720 {
			return v, core.ErrInvalid
		}
	default:
		return v, core.ErrInvalid
	}
	return v, nil
}
