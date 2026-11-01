package workbench

import "database/sql"

func isChatChannel(source string) bool { return source == "wechat" || source == "feishu" }

func (s *Store) routeChannelToToday(m Message) (Message, error) {
	c, e := s.Connection(m.Incoming.ConnectionID)
	if e != nil || !c.Enabled || c.Kind != m.Incoming.Source || c.OwnerID != m.Incoming.ActorID {
		return m, ErrNotFound
	}
	p, e := s.PersonalSpace()
	if e != nil {
		return m, e
	}
	if e = s.SetSetting("today_enabled", "true"); e != nil {
		return m, e
	}
	in := m.Incoming
	var anchor string
	explicit := in.ReplyTo != ""
	if explicit {
		e = s.DB.QueryRow(`SELECT data->>'$.runId' FROM deliveries WHERE connection_id=? AND data->>'$.actorId'=? AND data->>'$.conversationId'=? AND state IN ('sent','accepted') AND (data->>'$.receipt'=? OR id=? OR EXISTS(SELECT 1 FROM json_each(deliveries.data,'$.receipts') WHERE value=?)) ORDER BY created_at DESC LIMIT 1`, in.ConnectionID, in.ActorID, in.ConversationID, in.ReplyTo, in.ReplyTo, in.ReplyTo).Scan(&anchor)
		if e == sql.ErrNoRows {
			e = s.DB.QueryRow(`SELECT coalesce(data->>'$.runId','') FROM inbox WHERE entry_key=? AND event_id=? AND state<>'deleted'`, entryKey(in), in.ReplyTo).Scan(&anchor)
		}
	} else {
		e = s.DB.QueryRow(`SELECT data->>'$.runId' FROM deliveries WHERE connection_id=? AND data->>'$.actorId'=? AND data->>'$.conversationId'=? AND data->>'$.sessionId'=? AND data->>'$.kind'='answer' AND state IN ('sent','accepted') ORDER BY created_at DESC,rowid DESC LIMIT 1`, in.ConnectionID, in.ActorID, in.ConversationID, p.DefaultSessionID).Scan(&anchor)
	}
	if e != nil && e != sql.ErrNoRows {
		return m, e
	}
	return s.queue(m, p.DefaultSessionID, anchor, anchor != "" && explicit)
}

// Scheduled notifications and transport control receipts have no personal Run.
// Display their original delivery once, without replaying it or creating a model turn.
func (s *Store) todayChannelNotes() ([]Delivery, error) {
	return listJSON[Delivery](s.DB, `SELECT d.data FROM deliveries d JOIN connections c ON c.id=d.connection_id
 WHERE d.data->>'$.kind' IN ('notification','stopped') AND d.data->>'$.actorId'=c.data->>'$.ownerId'
 AND (coalesce(d.data->>'$.sessionId','')='' OR EXISTS(SELECT 1 FROM sessions s WHERE s.id=d.data->>'$.sessionId' AND coalesce(s.data->>'$.recycledAt',0)=0))
 ORDER BY d.created_at DESC,d.rowid DESC LIMIT 30`)
}
