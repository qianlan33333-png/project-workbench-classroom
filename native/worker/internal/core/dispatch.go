package core

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-openapi/jsonpointer"
	"github.com/google/uuid"
)

// The source section is an immutable content manifest. Eligibility comes from
// a separately registered source, checked mechanically with JSON Schema.
type DispatchPlan struct {
	Check             RecordCheck       `json:"check"`
	Source            Ref               `json:"source"`
	Eligibility       Ref               `json:"eligibility"`
	EligibilitySchema map[string]any    `json:"eligibilitySchema"`
	ConnectionID      string            `json:"connectionId"`
	TextPointer       string            `json:"textPointer"`
	TargetPointers    map[string]string `json:"targetPointers"`
	DecodeJSONField   string            `json:"decodeJsonField,omitempty"`
	RecordIDs         []string          `json:"recordIds,omitempty"`
}

func (p DispatchPlan) Validate() error {
	if len(p.RecordIDs) > 0 {
		if _, e := BoundedSelection(&Selection{RecordIDs: p.RecordIDs}); e != nil {
			return e
		}
	}
	if p.Check.ID == "" || p.Check.Section != p.Eligibility.Section || Digest(p.Check.Schema) != Digest(p.EligibilitySchema) || Digest(p.Check.Bindings) != Digest(p.TargetPointers) {
		return ErrInvalid
	}
	if e := p.Source.Validate(); e != nil {
		return e
	}
	if e := p.Eligibility.Validate(); e != nil {
		return e
	}
	if !ValidID(p.ConnectionID) || len(p.EligibilitySchema) == 0 || len(p.TargetPointers) == 0 || len(p.TargetPointers) > 8 {
		return ErrInvalid
	}
	if _, e := (Method{OutputSchema: p.EligibilitySchema}).ResolveOutput(); e != nil {
		return e
	}
	if _, e := jsonpointer.New(p.TextPointer); e != nil {
		return ErrInvalid
	}
	for key, path := range p.TargetPointers {
		if !ValidID(key) {
			return ErrInvalid
		}
		if _, e := jsonpointer.New(path); e != nil {
			return ErrInvalid
		}
	}
	if p.DecodeJSONField != "" && !ValidID(p.DecodeJSONField) {
		return ErrInvalid
	}
	return nil
}
func (p DispatchPlan) Content(fields map[string]any, identities map[string]any) (string, map[string]string, error) {
	value := map[string]any{}
	for k, v := range fields {
		value[k] = v
	}
	if p.DecodeJSONField != "" {
		raw, ok := value[p.DecodeJSONField].(string)
		if !ok {
			return "", nil, ErrInvalid
		}
		var decoded any
		if e := json.Unmarshal([]byte(raw), &decoded); e != nil {
			return "", nil, ErrInvalid
		}
		value[p.DecodeJSONField] = decoded
	}
	read := func(path string) (string, error) {
		pointer, e := jsonpointer.New(path)
		if e != nil {
			return "", e
		}
		v, _, e := pointer.Get(value)
		if e != nil {
			return "", ErrInvalid
		}
		text, ok := v.(string)
		if !ok || text == "" {
			return "", ErrInvalid
		}
		return text, nil
	}
	text, e := read(p.TextPointer)
	if e != nil || len(text) > 16000 {
		return "", nil, ErrInvalid
	}
	target := map[string]string{}
	for key, path := range p.TargetPointers {
		pointer, e := jsonpointer.New(path)
		if e != nil {
			return "", nil, e
		}
		v, _, e := pointer.Get(identities)
		if e != nil {
			return "", nil, ErrInvalid
		}
		value, ok := v.(string)
		if !ok || value == "" {
			return "", nil, ErrInvalid
		}
		target[key] = value
	}
	return text, target, nil
}
func DeliveryItemID(run, item string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(run+"/delivery/"+item)).String()
}

// SupersedeDispatchPrepared fences the old manifest before a new review.
// Only prepared rows have never been claimed for an external call.
func (s *Store) SupersedeDispatchPrepared(ctx context.Context, p Principal, run string) error {
	r, e := s.Run(ctx, p, run)
	if e != nil {
		return e
	}
	if r.Dispatch == nil || r.ParentRunID == "" {
		return ErrInvalid
	}
	_, e = s.DB.ExecContext(ctx, `UPDATE wb_deliveries
		SET state='superseded',body=JSON_SET(body,'$.state','superseded')
		WHERE run_id=? AND state='prepared'`, run)
	return e
}
func (s *Store) Deliveries(ctx context.Context, p Principal, run, after string) ([]Delivery, error) {
	r, e := s.Run(ctx, p, run)
	if e != nil {
		return nil, e
	}
	if e = s.RememberRunRead(ctx, p, r); e != nil {
		return nil, e
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT body FROM wb_deliveries WHERE run_id=? AND item_id>? ORDER BY item_id LIMIT 200`, run, after)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		v, e := scanJSON[Delivery](rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type DeliveryStats struct {
	Total  int            `json:"total"`
	States map[string]int `json:"states"`
}

func (s *Store) DeliveryStats(ctx context.Context, p Principal, id string) (DeliveryStats, error) {
	out := DeliveryStats{States: map[string]int{}}
	run, e := s.Run(ctx, p, id)
	if e != nil {
		return out, e
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT state,COUNT(*) FROM wb_deliveries WHERE run_id=? GROUP BY state`, id)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var count int
		if e = rows.Scan(&state, &count); e != nil {
			return out, e
		}
		out.States[state] = count
		out.Total += count
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	rows.Close()
	return out, s.RememberRunRead(ctx, p, run)
}
func (s *Store) DispatchReview(ctx context.Context, r Run) (Decision, error) {
	stats, e := s.DeliveryStats(ctx, r.Principal, r.ID)
	if e != nil {
		return Decision{}, e
	}
	var excluded int
	if e = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_items WHERE run_id=? AND state='excluded'`, r.ID).Scan(&excluded); e != nil {
		return Decision{}, e
	}
	samples, e := s.Deliveries(ctx, r.Principal, r.ID, "")
	if e != nil {
		return Decision{}, e
	}
	q := fmt.Sprintf("本批固定 %d 条待发送消息，另有 %d 条未通过资料资格校验。\n仅准备，尚未向客户发送。\n采用已登记条件：%s\n资格资料：%s / %s\n计划摘要：%s\n", stats.Total, excluded, r.Dispatch.Check.Name, r.Dispatch.Eligibility.ResourceID, r.Dispatch.Eligibility.Section, Digest(r.Dispatch))
	for i, d := range samples {
		if i >= 3 {
			break
		}
		text := []rune(d.Text)
		if len(text) > 800 {
			text = text[:800]
		}
		q += fmt.Sprintf("\n样本 %d · %s\n目标 %s\n%s\n", i+1, d.ItemID, jsonText(d.Target), string(text))
	}
	q += "\n可在原对话查询全部固定稿件与逐条回执。确认本批后回复：发 " + r.ID[:8] + "。企微创建手机待发任务，实际发送以手机确认后的回执为准。"
	if stats.Total == 0 {
		return Decision{}, fmt.Errorf("%w: no eligible messages", ErrInvalid)
	}
	return s.Ask(ctx, r, 1, q)
}
