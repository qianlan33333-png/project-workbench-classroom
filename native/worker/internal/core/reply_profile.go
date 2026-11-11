package core

import (
	"context"
	"encoding/json"
)

// ResolveReplyProfile runs only for the Host-bound message, never a model ID.
// Its caller supplies the single deadline shared by identity and source reads.
func (s *Store) ResolveReplyProfile(ctx context.Context, p Principal, m StoredMessage, resolve func(context.Context, Transport, string) (string, error)) ([]Record, error) {
	cfg, err := s.ReplySettingsForConnection(ctx, m.ConnectionID)
	if IsMissing(err) {
		return s.MessageContext(ctx, p, m.ConnectionID, m.EventID)
	}
	if err != nil {
		return nil, err
	}
	if cfg.ReadProfile != nil && !*cfg.ReadProfile {
		return []Record{}, nil
	}
	if cfg.Subject == nil || cfg.Subject.LookupTool == "" {
		return s.MessageContext(ctx, p, m.ConnectionID, m.EventID)
	}
	if !cfg.Enabled || cfg.ProjectID != p.ProjectID {
		return nil, ErrDenied
	}
	route, err := s.Route(ctx, m.RouteID)
	if err != nil || !route.Enabled || route.Principal != p {
		return nil, ErrDenied
	}
	id := m.ExternalUserID
	if id == "" {
		id = route.ExternalUserID
	}
	if id == "" {
		transport, e := s.Transport(ctx, p, m.ConnectionID)
		if e != nil {
			return nil, e
		}
		id, err = resolve(ctx, transport, route.ActorID)
		if err != nil {
			return nil, err
		}
	}
	if !ValidID(id) {
		return nil, ErrInvalid
	}
	if err = s.BindReplyIdentity(ctx, p, route.ID, id); err != nil {
		return nil, err
	}
	resource, err := s.Resource(ctx, cfg.Subject.ResourceID)
	if err != nil {
		return nil, err
	}
	ref := Ref{resource.ID, resource.CurrentVersion, cfg.Subject.Section}
	if err = Authorize(ctx, s, p, ref); err != nil {
		return nil, err
	}
	conn, err := s.sourceConnection(ctx, resource)
	if err != nil || conn.Kind != "mcp" {
		return nil, ErrDenied
	}
	raw, err := callMCPSource(ctx, conn, Run{Principal: p}, cfg.Subject.LookupTool, map[string]string{"externalUserId": id})
	if err != nil {
		return nil, err
	}
	var out struct {
		ExternalUserID string   `json:"externalUserId"`
		Records        []Record `json:"records"`
	}
	if json.Unmarshal(raw, &out) != nil || out.ExternalUserID != id || len(out.Records) > 1 {
		return nil, ErrInvalid
	}
	snap, err := s.Snapshot(ctx, ref.ResourceID, ref.Version)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, section := range snap.Sections {
		if section.ID == ref.Section {
			for _, field := range section.Fields {
				allowed[field] = true
			}
		}
	}
	for i, record := range out.Records {
		if !ValidID(record.ID) {
			return nil, ErrInvalid
		}
		fields := map[string]any{}
		for key, value := range record.Fields {
			if allowed[key] {
				fields[key] = value
			}
		}
		out.Records[i].Fields = fields
	}
	if len(jsonText(out.Records)) > 24000 {
		return nil, ErrInvalid
	}
	if err = Authorize(ctx, s, p, ref); err != nil {
		return nil, err
	}
	latest, err := s.ReplySettingsForConnection(ctx, m.ConnectionID)
	if err != nil {
		return nil, err
	}
	if latest.Revision != cfg.Revision || !latest.Enabled || (latest.ReadProfile != nil && !*latest.ReadProfile) {
		return nil, ErrDenied
	}
	if len(out.Records) > 0 {
		if err = s.RememberRead(ctx, p, []Ref{ref}); err != nil {
			return nil, err
		}
	}
	return out.Records, nil
}

// Update only identity under the same row lock as human takeover. No revision,
// recipient, enabled flag, Session or autoReply change accompanies this write.
func (s *Store) BindReplyIdentity(ctx context.Context, p Principal, routeID, id string) error {
	if !ValidID(id) {
		return ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	route, err := scanJSON[MessageRoute](tx.QueryRowContext(ctx, `SELECT body FROM wb_message_routes WHERE id=? FOR UPDATE`, routeID))
	if err != nil {
		return err
	}
	if route.Principal != p || !route.Enabled {
		return ErrDenied
	}
	if route.ExternalUserID != "" && route.ExternalUserID != id {
		return ErrConflict
	}
	if route.ExternalUserID == "" {
		_, err = tx.ExecContext(ctx, `UPDATE wb_message_routes SET body=JSON_SET(body,'$.externalUserId',?) WHERE id=?`, id, routeID)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
