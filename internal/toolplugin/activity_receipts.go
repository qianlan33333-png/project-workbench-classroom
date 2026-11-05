package toolplugin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"
)

func (s *Server) readActivityReceipts(ctx context.Context, a activity) (map[string]any, error) {
	if len(s.Config.ReceiptCommand) == 0 {
		return nil, errors.New("逐人原回执读取CLI尚未配置")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.Config.ReceiptCommand[0], s.Config.ReceiptCommand[1:]...)
	cmd.Stdin = strings.NewReader(encode(map[string]any{"run_id": a.SourceRun, "digest": a.SourceDigest}))
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		return nil, e
	}
	if e = cmd.Start(); e != nil {
		return nil, errors.New("原回执CLI无法启动")
	}
	raw, e := io.ReadAll(io.LimitReader(stdout, 2_000_001))
	if e != nil || len(raw) > 2_000_000 {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, errors.New("原回执读取超限或中断")
	}
	if e = cmd.Wait(); e != nil {
		return nil, errors.New("原逐人回执尚未核证")
	}
	var out map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&out) != nil || str(out, "version") != "workbench-item-receipts.v1" || str(out, "run_id") != a.SourceRun || str(out, "digest") != a.SourceDigest {
		return nil, errors.New("逐人回执协议或绑定不匹配")
	}
	if str(out, "state") != "verified" {
		return out, nil
	}
	items := object(out, "items")
	finals, _ := a.Data["final_items"].([]any)
	if len(items) != len(finals) {
		return nil, errors.New("逐人回执数量与批准名单不符")
	}
	for _, v := range finals {
		f, ok := v.(map[string]any)
		if !ok {
			return nil, errors.New("批准名单记录损坏")
		}
		if _, ok = items[str(f, "user_ref")]; !ok {
			return nil, errors.New("逐人回执身份不匹配")
		}
	}
	return out, nil
}
func (s *Server) activityReceipts(ctx context.Context, g Grant, args map[string]any) (any, error) {
	a, e := s.activity(ctx, g, args)
	if e != nil {
		return nil, e
	}
	receipts := object(a.Data, "item_receipts")
	if len(receipts) == 0 {
		receipts, e = s.readActivityReceipts(ctx, a)
		if e != nil {
			return nil, e
		}
	}
	items := object(receipts, "items")
	keys := []string{}
	for k := range items {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	offset := number(args, "offset", 0)
	limit := number(args, "limit", 20)
	if offset < 0 || offset > len(keys) || limit < 1 || limit > 50 {
		return nil, errors.New("回执分页参数无效")
	}
	rows := []any{}
	for _, k := range keys[offset:min(len(keys), offset+limit)] {
		rows = append(rows, map[string]any{"user_ref": k, "receipt": items[k]})
	}
	return success("原渠道逐人核证回执", map[string]any{"state": receipts["state"], "checked_at": receipts["checked_at"], "total": len(keys), "items": rows, "next_offset": offset + len(rows)}), nil
}
