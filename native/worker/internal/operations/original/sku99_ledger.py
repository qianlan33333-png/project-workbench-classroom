"""Selected original rules; source I/O is supplied by the host as frozen inputs."""
from datetime import timedelta
from sku99_operations import at, VERSION

def keys(b):return list(dict.fromkeys([b['slot_key']]+([b['weekly_slot_key']]if b.get('node')=='W4_CONTINUATION'and b.get('weekly_slot_key')else[])))

def decision_guard(result,index,now):
 b=result.get('sku_pack_binding')or{}
 if b.get('version')!=VERSION or b.get('primary_identity')!='99'or not result.get('outbound_candidate'):return result
 from sku99_operations import hold
 rows=index.get(result.get('safe_user_ref'))or[]
 for r in rows:
  old=r.get('binding')or{}
  # Exact slot always protected, including old actual receipts/cancelled reservations.
  if r.get('slot_key')in keys(b)or old.get('slot_key')in keys(b):
   if (old.get('node')or r.get('node'))==b.get('node'):
    # Keep the current decision stable so the owning draft can be reviewed/imported.
    # The existing-slot gate rejects every other run without invalidating this run.
    if b.get('node')=='REPAIR_ONCE':
     original_window={k:old.get(k)or r.get(k)for k in ['window_start','window_end','occupies_week']if old.get(k)or r.get(k)}
     if original_window.get('window_start')and not original_window.get('occupies_week'):original_window['occupies_week']=at(original_window['window_start']).strftime('%G-W%V')
     b=dict(b,**original_window)
    return dict(result,sku_pack_binding=b,existing_copy={'run_id':r['run_id'],'item_key':r['item_key'],'reuse_required':True})
   return hold(result,b,'sku99_slot_owned_by_existing_copy:'+r['run_id'])
  if r.get('state')=='sent'and(t:=at(r.get('sent_at')))and now-t<timedelta(hours=24):return hold(result,b,'actual_send_cooldown_24h')
  # Any unconfirmed handoff prevents a second automatic customer touch.
  if r.get('handoff')and r.get('state')not in {'sent','cancelled'}:return hold(result,b,'existing_delivery_or_review_pending:'+r['run_id'])
  if b.get('node')=='REPAIR_ONCE'and(old.get('node')=='REPAIR_ONCE'or r.get('node')=='REPAIR_ONCE'):return hold(result,b,'sku99_recovery_already_owned:'+r['run_id'])
  # Current-week formal service drafts, including pre-pack/6.8 jobs, own this week's service.
  if r.get('state')=='sent':
   if b.get('node')in {'WEEKLY','CONTENT','W4_CONTINUATION','REPAIR_ONCE'}and(t:=at(r.get('sent_at')))and t.strftime('%G-W%V')==now.strftime('%G-W%V')and(r.get('scene')in {'weekly_review','content_service'}or(old.get('node')or r.get('node'))in {'REPAIR_ONCE','WEEKLY','CONTENT','W4_CONTINUATION'}):return hold(result,b,'current_week_service_already_delivered')
   continue
  if r.get('state')=='cancelled':continue
  if not r.get('slot_key')and r.get('week')==now.strftime('%G-W%V'):
   return hold(result,b,'existing_current_week_copy_preserved:'+r['run_id'])
  if r.get('window_start')and r.get('window_end')and at(r['window_start'])<=now<at(r['window_end']) and (b.get('node')=='REPAIR_ONCE'or r.get('node')=='REPAIR_ONCE'):return hold(result,b,'current_service_week_already_owned')
 return result
