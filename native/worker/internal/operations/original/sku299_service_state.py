"""Selected original rules; source I/O is supplied by the host as frozen inputs."""
from datetime import datetime, timedelta
from zoneinfo import ZoneInfo

TZ=ZoneInfo('Asia/Shanghai')

VERSION='sku.202608052341.rules.v1.4.0'

def at(v):
 try:
  t=datetime.fromisoformat(str(v).replace('Z','+00:00'));return t.replace(tzinfo=TZ)if not t.tzinfo else t.astimezone(TZ)
 except(TypeError,ValueError):return None

def keys(b):return list(dict.fromkeys([b['slot_key']]+([b['renewal_slot_key']]if b.get('renewal_due')else[])))

def decision_guard(r,index,now):
 b=r.get('sku_pack_binding')or{}
 if b.get('version')!=VERSION or not r.get('outbound_candidate'):return r
 from sku299_operations import hold
 for x in index.get(r['safe_user_ref'],[]):
  old=x.get('binding')or{}
  if x.get('slot_key')in keys(b)or old.get('slot_key')in keys(b):return dict(r,existing_copy={'run_id':x['run_id'],'item_key':x['item_key'],'reuse_required':True})
  if x.get('state')=='cancelled':continue
  if x.get('handoff')and x.get('state')!='sent':return hold(r,b,'existing_delivery_or_review_pending:'+x['run_id'])
  if x.get('state')=='sent'and(t:=at(x.get('sent_at'))):
   if now-t<timedelta(hours=24):return hold(r,b,'actual_send_cooldown_24h')
   if old.get('node')not in {'RENEW_3D','RENEW_EXPIRED'}and b['node']in {'WEEKLY','CONTENT','D_DIGEST','FOLLOWUP'}and t.strftime('%G-W%V')==now.strftime('%G-W%V'):return hold(r,b,'current_week_service_already_delivered')
  elif b['node']not in {'RENEW_3D','RENEW_EXPIRED'}and x.get('week')==now.strftime('%G-W%V'):return hold(r,b,'existing_current_week_copy_preserved:'+x['run_id'])
 return r
