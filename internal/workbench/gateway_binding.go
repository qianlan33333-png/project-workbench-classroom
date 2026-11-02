package workbench

import (
	"database/sql"
	"slices"
)

func gatewayContactAllowed(b GatewayBinding, contact string) bool {
	return len(b.ContactIDs) == 0 || slices.Contains(b.ContactIDs, contact)
}

// Only transfer of the account to another project starts a new ownership
// epoch. A policy change revokes captured run grants through the existing
// session version gate, retaining each conversation's mode and history.
func updateGatewayBindingWork(tx *sql.Tx, old, b GatewayBinding) error {
	all := b.ProjectID != old.ProjectID || old.Enabled && !b.Enabled || b.ExpertID != old.ExpertID ||
		!slices.Equal(b.KnowledgePaths, old.KnowledgePaths) || !slices.Equal(b.ToolIDs, old.ToolIDs) || !slices.Equal(b.PluginIDs, old.PluginIDs)
	narrowed := len(b.ContactIDs) > 0 && !slices.Equal(b.ContactIDs, old.ContactIDs)
	owned := `SELECT id FROM gateway_sessions WHERE binding_id=? AND json_extract(data,'$.bindingVersion')=?`
	if all || narrowed {
		affected := owned + ` AND (? OR json_extract(data,'$.contactId') NOT IN (SELECT value FROM json_each(?)))`
		args := []any{old.ID, old.Version, all, encode(b.ContactIDs)}
		if _, err := tx.Exec(`UPDATE gateway_sessions SET data=json_set(data,'$.version',json_extract(data,'$.version')+1) WHERE id IN (`+affected+`)`, args...); err != nil {
			return err
		}
		// Never lose the manual handoff alert on an expert/knowledge edit.
		// A disabled or reassigned contact no longer belongs to this route.
		if _, err := tx.Exec(`UPDATE deliveries SET state=`+revokedDeliveryState+`,data=json_set(data,'$.state',`+revokedDeliveryState+`)
		  WHERE state='pending' AND json_extract(data,'$.kind')='gateway.answer' AND json_extract(data,'$.sessionId') IN (`+affected+`)`, args...); err != nil {
			return err
		}
		if b.ProjectID != old.ProjectID || !b.Enabled || narrowed {
			ineligible := owned + ` AND (? OR json_extract(data,'$.contactId') NOT IN (SELECT value FROM json_each(?)))`
			if _, err := tx.Exec(`UPDATE deliveries SET state=`+revokedDeliveryState+`,data=json_set(data,'$.state',`+revokedDeliveryState+`)
			  WHERE state IN ('pending','sending') AND json_extract(data,'$.kind') IN ('gateway.handoff','gateway.crm') AND json_extract(data,'$.sessionId') IN (`+ineligible+`)`,
				old.ID, old.Version, b.ProjectID != old.ProjectID || !b.Enabled, encode(b.ContactIDs)); err != nil {
				return err
			}
		}
	}
	if b.NotifyChatID != old.NotifyChatID {
		// Do not retry an uncertain send to the old operator or reuse the same
		// idempotency key at a different destination. A late confirmation may
		// still refine the recorded outcome via the normal delivery CAS.
		if _, err := tx.Exec(`UPDATE deliveries SET state='unknown',data=json_set(data,'$.state','unknown','$.error','通知目标已变更，需核对原发送回执')
		  WHERE (state='sending' OR (state='pending' AND json_extract(data,'$.gateway.attempts')>0))
		    AND json_extract(data,'$.kind')='gateway.handoff' AND json_extract(data,'$.sessionId') IN (`+owned+`)`, old.ID, old.Version); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE deliveries SET data=json_set(data,'$.gateway.notifyChatId',?)
		  WHERE state='pending' AND json_extract(data,'$.kind')='gateway.handoff'
		  AND coalesce(json_extract(data,'$.gateway.attempts'),0)=0 AND json_extract(data,'$.sessionId') IN (`+owned+`)`, b.NotifyChatID, old.ID, old.Version)
		return err
	}
	return nil
}

// Attempts already made may have reached a device. Never label those as unsent.
const revokedDeliveryState = `CASE WHEN state='sending' OR coalesce(json_extract(data,'$.gateway.attempts'),0)>0 THEN 'unknown' ELSE 'cancelled' END`
