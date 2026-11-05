package toolplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

// Fixed original-protocol paths only. Callers supply package identities, never
// arbitrary URLs, SQL, approvals or a replacement send implementation.
func (s *Server) sourceActivityCall(ctx context.Context, method, path string, body map[string]any) (map[string]any, error) {
	allowed := map[string]bool{"/api/local/v3/codex-copy-jobs": true, "/api/local/copy/batch-runs": true, "/api/local/v3/copy-package": true, "/api/local/v3/copy-package-review": true, "/api/local/v3/work-packages": true}
	if !allowed[path] {
		return nil, errors.New("活动原接口未登记")
	}
	var input io.Reader
	if method == "GET" {
		q := url.Values{}
		for k, v := range body {
			q.Set(k, fmt.Sprint(v))
		}
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
	} else {
		input = strings.NewReader(encode(body))
	}
	res, e := s.sourceRequest(ctx, method, path, input)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, (32<<20)+1))
	if e != nil {
		return nil, errors.New("原接口读取中断")
	}
	if len(raw) > 32<<20 {
		return nil, errors.New("原活动响应超过32MiB")
	}
	var out map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&out) != nil {
		return nil, errors.New("原活动响应不是JSON")
	}
	if ok, exists := out["ok"]; exists && ok == false {
		return nil, sourceBusinessRejection{}
	}
	return out, nil
}
func (s *Server) prepareSourceActivity(ctx context.Context, j job, a activity) (string, error) {
	prepared, e := s.sourceActivityCall(ctx, "POST", "/api/local/v3/codex-copy-jobs", map[string]any{"action": "prepare", "package_id": str(j.Request, "package_id"), "week": str(j.Request, "week"), "selected_user_refs": j.Request["items"]})
	if e != nil {
		return "", e
	}
	origin := str(prepared, "run_id")
	if origin == "" {
		return "", errors.New("原工作包未返回有效生成批次")
	}
	// Build an independently keyed draft from original bound requests. Reusing an
	// already reviewed package would turn a new activity into an old approval.
	template, e := s.sourceActivityCall(ctx, "GET", "/api/local/copy/batch-runs", map[string]any{"run_id": origin})
	if e != nil {
		return "", e
	}
	run := object(template, "run")
	rows, _ := run["items"].([]any)
	refs := j.Request["items"].([]any)
	if len(rows) != len(refs) {
		return "", errors.New("原有效名单与所选人数不一致")
	}
	config := object(run, "config")
	copyConfig := map[string]any{}
	for k, v := range config {
		copyConfig[k] = v
	}
	copyConfig["batch_reference"] = "workbench-activity:" + j.ID
	copyConfig["external_generator"] = "codex"
	for _, key := range []string{"test_package", "sample_run", "cohort_sample_only"} {
		if copyConfig[key] == true {
			return "", errors.New("原测试包不能作为真实活动发送来源")
		}
	}
	items := []any{}
	seen := map[string]bool{}
	for _, v := range rows {
		row, ok := v.(map[string]any)
		if !ok {
			return "", errors.New("原名单条目格式异常")
		}
		request := object(row, "request")
		ref := str(request, "safe_user_ref")
		if !containsAny(refs, ref) || seen[ref] {
			return "", errors.New("原名单身份与新数仓选集不符")
		}
		seen[ref] = true
		items = append(items, map[string]any{"item_key": ref, "customer_key": ref, "request": request})
	}
	created, e := s.sourceActivityCall(ctx, "POST", "/api/local/copy/batch-runs", map[string]any{"action": "create_or_resume", "config": copyConfig, "items": items})
	if e != nil {
		return "", e
	}
	own := object(created, "run")
	id := str(own, "run_id")
	if id == "" {
		return "", errors.New("原中心未确认独立活动批次")
	}
	if str(object(own, "config"), "batch_reference") != "workbench-activity:"+j.ID {
		return "", errors.New("原活动批次键不匹配")
	}
	// External-generator drafts are explicitly paused; no original online model
	// worker may race the new warehouse-backed generation.
	_, e = s.sourceActivityCall(ctx, "POST", "/api/local/copy/batch-runs", map[string]any{"action": "pause", "run_id": id})
	return id, e
}
func (s *Server) activityDispatch(ctx context.Context, j job) (any, error) {
	g, ok := s.grantForJob(j)
	if !ok {
		return nil, errors.New("原安装授权已撤销")
	}
	a, e := s.activity(ctx, g, j.Request)
	if e != nil {
		return nil, e
	}
	if a.Digest == "" || a.Digest != str(j.Request, "digest") {
		return nil, errors.New("活动审核版本已变化")
	}
	if a.State == "cancelled" {
		return nil, errors.New("已取消批次不能发送")
	}
	if str(j.Request, "approval_event") == "" {
		return nil, errors.New("缺少受管本人确认事件")
	}
	if a.State == "awaiting_review" {
		// Compare the complete original package before recording the owner request.
		current, e := s.sourceActivityCall(ctx, "GET", "/api/local/v3/copy-package", map[string]any{"run_id": a.SourceRun, "preflight": 1})
		if e != nil {
			return nil, e
		}
		pkg := object(current, "package")
		if str(pkg, "digest") != a.SourceDigest {
			return nil, errors.New("原名单、文案或附件已改变，请重新抽检")
		}
		if e = s.checkJobEpoch(ctx, j.ID); e != nil {
			return nil, e
		}
		a.State = "approving"
		a.Data["approval_event"] = str(j.Request, "approval_event")
		a.Data["approval_job"] = j.ID
		a.Data["approval_note"] = "workbench-owner:" + str(j.Request, "approval_event")
		if e = s.saveActivityExecution(ctx, j.ID, a); e != nil {
			return nil, e
		}
	}
	if str(a.Data, "approval_job") != j.ID {
		return nil, errors.New("同一活动已有原发送任务，不能另建执行")
	}
	if a.State == "approving" || a.State == "unknown" {
		// This operation is approval+idempotent handoff, not a provider-send retry.
		// On an ambiguous response, the original package is read before any retry.
		current, e := s.sourceActivityCall(ctx, "GET", "/api/local/v3/copy-package", map[string]any{"run_id": a.SourceRun, "preflight": 1})
		if e != nil {
			return nil, e
		}
		pkg := object(current, "package")
		review := object(pkg, "review")
		handoff := object(pkg, "handoff")
		if str(pkg, "digest") != a.SourceDigest {
			return nil, errors.New("已确认原文案版本不再一致")
		}
		if str(handoff, "id") != "" && str(review, "decision") == "approved" && str(review, "digest") == a.SourceDigest {
			a.State = "waiting_receipts"
			a.Data["handoff_id"] = str(handoff, "id")
		} else if str(a.Data, "formal_job") != "" {
			result, e := s.sourceActivityCall(ctx, "POST", "/api/local/v3/codex-copy-jobs", map[string]any{"action": "formal_status", "job_id": str(a.Data, "formal_job")})
			if e != nil {
				return s.activityReceiptResult(a), errActivityWait
			}
			if result["pending"] == true {
				return s.activityReceiptResult(a), errActivityWait
			}
			a.State = "waiting_receipts"
		} else {
			// Persist a sticky submit flag first. A lost synchronous result is looked
			// up from the original review/handoff; it is never blindly resubmitted.
			if a.Data["approval_submitted"] == true {
				a.State = "unknown"
				if e = s.saveActivityExecution(ctx, j.ID, a); e != nil {
					return nil, e
				}
				return s.activityReceiptResult(a), errActivityWait
			}
			a.Data["approval_submitted"] = true
			if number(a.Data, "total", 0) > 20 {
				a.Data["formal_job"] = "formal-preparation:" + hash(encode([]any{"approve_formal_batch", a.SourceRun, a.SourceDigest, "approved", []any{}, str(a.Data, "approval_note"), []any{}, "owner_approve_and_handoff"}))[:32]
			}
			if e = s.saveActivityExecution(ctx, j.ID, a); e != nil {
				return nil, e
			}
			approved, e := s.sourceActivityCall(ctx, "POST", "/api/local/v3/copy-package-review", map[string]any{"run_id": a.SourceRun, "digest": a.SourceDigest, "decision": "approved", "excluded_item_keys": []any{}, "note": str(a.Data, "approval_note"), "handoff_on_approval": true})
			if e != nil {
				a.State = "unknown"
				a.Data["last_error"] = "原批准结果未确认，只查回原批次"
				_ = s.saveActivityExecution(ctx, j.ID, a)
				return s.activityReceiptResult(a), errActivityWait
			}
			if approved["pending"] == true {
				if str(approved, "job_id") == "" {
					return nil, errors.New("原异步审核任务缺失编号")
				}
				a.Data["formal_job"] = str(approved, "job_id")
			} else {
				a.State = "waiting_receipts"
			}
			if e = s.saveActivityExecution(ctx, j.ID, a); e != nil {
				return nil, e
			}
		}
	}
	// Read-only status lookup remains active through unknown, phone pending and
	// delayed provider receipts. No state below re-enters the approval POST.
	receipt, e := s.sourceActivityCall(ctx, "GET", "/api/local/v3/copy-package", map[string]any{"run_id": a.SourceRun, "status_only": 1})
	if e != nil {
		return s.activityReceiptResult(a), errActivityWait
	}
	status := object(receipt, "delivery_summary")
	if str(status, "version") != "original-review-delivery-status.v1" || str(status, "run_id") != a.SourceRun {
		return nil, errors.New("原回执协议或批次不匹配")
	}
	if str(status, "digest") != "" && str(status, "digest") != a.SourceDigest {
		return nil, errors.New("原回执不属于本次已确认版本")
	}
	a.Data["receipt"] = status
	total := number(status, "requested_count", 0)
	sent := number(status, "sent_count", 0)
	failed := number(status, "failed_count", 0)
	switch str(status, "status") {
	case "provider_accepted", "mobile_pending":
		a.State = "awaiting_phone"
	case "unknown", "blocked", "final_failed":
		a.State = "unknown"
	case "awaiting_authorized_connector", "preparing", "in_flight", "provider_pending":
		a.State = "waiting_receipts"
	}
	if total != 0 && total != number(a.Data, "total", 0) {
		return nil, errors.New("原回执人数与冻结批准名单不一致")
	}
	if total > 0 && sent >= 0 && failed >= 0 && sent+failed == total {
		details, er := s.readActivityReceipts(ctx, a)
		if er != nil || str(details, "state") != "verified" {
			a.Data["last_error"] = "汇总已到，逐人核证回执待查回"
			if e = s.saveActivityExecution(ctx, j.ID, a); e != nil {
				return nil, e
			}
			return s.activityReceiptResult(a), errActivityWait
		}
		actualSent, actualFailed := 0, 0
		for _, v := range object(details, "items") {
			row, _ := v.(map[string]any)
			if str(row, "state") == "sent" {
				actualSent++
			}
			if str(row, "state") == "failed" {
				actualFailed++
			}
		}
		if actualSent != sent || actualFailed != failed {
			return nil, errors.New("逐人回执与原汇总不一致")
		}
		a.Data["item_receipts"] = details
		if sent == total {
			a.State = "sent"
		} else {
			a.State = "completed_with_failures"
		}
	}
	if e = s.saveActivityExecution(ctx, j.ID, a); e != nil {
		return nil, e
	}
	result := s.activityReceiptResult(a)
	if a.State == "sent" || a.State == "completed_with_failures" {
		return result, nil
	}
	return result, errActivityWait
}
func (s *Server) activityReceiptResult(a activity) map[string]any {
	return map[string]any{"activity": a.ID, "name": str(a.Data, "name"), "state": a.State, "summary": activitySummary(a.State), "source_run": a.SourceRun, "source_digest": a.SourceDigest, "receipt": a.Data["receipt"], "item_receipts_available": a.Data["item_receipts"] != nil, "last_error": a.Data["last_error"], "phone_confirmation_required": a.State == "awaiting_phone", "automatic_send_allowed": false}
}
func (s *Server) saveActivityExecution(ctx context.Context, jobID string, a activity) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var epoch, global int64
	var state string
	var cancelled bool
	if e = tx.QueryRowContext(ctx, "SELECT j.lease_epoch,j.state,j.cancel_requested,w.epoch FROM plugin_jobs j JOIN plugin_worker_epoch w ON w.id=1 WHERE j.id=? FOR UPDATE", jobID).Scan(&epoch, &state, &cancelled, &global); e != nil {
		return e
	}
	if epoch != s.epoch || global != s.epoch || state != "running" || cancelled {
		return errors.New("执行租约或停止状态已改变")
	}
	res, e := tx.ExecContext(ctx, "UPDATE plugin_activities SET state=?,digest=?,source_run=?,source_digest=?,data=?,updated_at=? WHERE id=? AND state<>'cancelled'", a.State, a.Digest, a.SourceRun, a.SourceDigest, encode(a.Data), time.Now().UnixMilli(), a.ID)
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return e
	}
	if n == 0 {
		return errors.New("活动已停止或状态已改变")
	}
	return tx.Commit()
}

func activityWeek(a map[string]any) map[string]any {
	v := map[string]any{}
	if week := str(a, "week"); week != "" {
		v["week"] = week
	}
	return v
}

type sourceBusinessRejection struct{}

func (sourceBusinessRejection) Error() string {
	return "原活动业务校验未通过；请核对原工作包与版本"
}
func definiteSourceRejection(err error) bool {
	var h sourceHTTPError
	var b sourceBusinessRejection
	return errors.As(err, &b) || errors.As(err, &h) && h.Status >= 400 && h.Status < 500 && h.Status != 408 && h.Status != 429
}
