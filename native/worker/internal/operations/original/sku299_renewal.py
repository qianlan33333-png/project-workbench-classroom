"""Original deterministic functions; sources are bound by the adapter."""
import calendar, hashlib, json
from order_time_contract import service_time
from datetime import datetime, timedelta
from zoneinfo import ZoneInfo

TZ=ZoneInfo('Asia/Shanghai')

NODES={'RENEW_3D','RENEW_EXPIRED'}

def at(value):
 try:
  t=datetime.fromisoformat(str(value).replace('Z','+00:00'))
  return t.replace(tzinfo=TZ)if t.tzinfo is None else t.astimezone(TZ)
 except(ValueError,TypeError):return None

def key(ref,end,node):
 return hashlib.sha256(json.dumps([ref,'299_RENEWAL',end.isoformat(),node],ensure_ascii=False).encode()).hexdigest()[:24]

def thresholds(end):
 y,m=(end.year-1,12)if end.month==1 else(end.year,end.month-1)
 first=end.replace(year=y,month=m,day=min(end.day,calendar.monthrange(y,m)[1]))
 return [first,*[end-timedelta(days=x)for x in(15,7,3,0)]]

def deadline_window(end,now):
 t=thresholds(end)
 for i,node in enumerate(('R30','R15','R7','R3','R0')):
  upper=t[i+1]if i<4 else end+timedelta(days=3)
  if t[i]<=now<upper:return dict(node=node,start=t[i].isoformat(),end=upper.isoformat(),mode='service_ending_only'if i<3 else'standalone_confirmation')
 return None

def stop_requested(user,evidence):
 # Only explicit structured preferences; no keyword guessing from messages.
 return any(x.get('renewal_opt_out')is True or x.get('renewal_declined')is True for x in(user,evidence))

def fresh_formal_payment(user,end,now):
 # A completed formal purchase stops the pitch even while app grants are syncing.
 for order in user.get('_orderHistory')or user.get('order_history')or[]:
  if not isinstance(order,dict)or order.get('is_paid')is not True or order.get('is_refunded')is not False:continue
  codes=set(str(order.get('product_code')or'').split('|'))
  formal='202608052341'in codes or(order.get('product_key')=='soak299'and any(x.startswith('wxshop-')for x in codes))
  paid=at(service_time(order))
  if formal and paid and thresholds(end)[0]<=paid<=now:return True
 return False

def occupied(ref,end,node,sources):
 keys=sources.get('sku299_renewal_slots',{}).get(ref,set())
 if key(ref,end,node)in keys:return True
 if node=='R7':
  legacy=hashlib.sha256(json.dumps([ref,'299_EXPIRY',end.isoformat()],ensure_ascii=False,sort_keys=True,default=str).encode()).hexdigest()[:24]
  return legacy in keys
 return False

def describe(user,evidence,sources,now,*,allow_expired=False):
 if stop_requested(user,evidence):return None
 a=evidence.get('operational_service_admission')or{}
 end=at(a.get('expires_at')or(evidence.get('current_hxc_entitlement')or{}).get('expires_at'))
 if not end:return None
 if fresh_formal_payment(user,end,now):return None
 ref=user['safe_user_ref'];window=deadline_window(end,now)
 if not window or occupied(ref,end,window['node'],sources):return None
 if now>=end:
  if not allow_expired or end<at(policy()['effective_at']):return None
  # A later current grant always defeats an old cycle's expiry.
  current=evidence.get('current_hxc_entitlement')or{}
  if current.get('active')is True and(not at(current.get('expires_at'))or at(current['expires_at'])>now):return None
  previous=sources.get('sku299_previous_cycle_ends',{}).get(ref,set())
  if a.get('status')!='expired'and end.isoformat()not in previous:return None
 elif a.get('status')!='active':return None
 return dict(window,slot_key=key(ref,end,window['node']),entitlement_end=end.isoformat(),policy_version=policy()['version'],url=policy()['renewal_url'])

def bind(binding,renewal):
 return dict(binding,renewal_due=True,renewal_policy_version=renewal['policy_version'],renewal_node=renewal['node'],renewal_mode=renewal['mode'],renewal_slot_key=renewal['slot_key'],renewal_url=renewal['url'],renewal_window_start=renewal['start'],renewal_window_end=renewal['end'])

def standalone(user,evidence,sources,result,now):
 if not result.get('eligible'):return None
 r=describe(user,evidence,sources,now,allow_expired=True)
 if not r or r['mode']!='standalone_confirmation':return None
 from sku299_operations import VERSION,route
 node='RENEW_3D'if r['node']=='R3'else'RENEW_EXPIRED'
 b=bind(dict(version=VERSION,product_code='202608052341',primary_identity='299',node=node,cycle_key=key(user['safe_user_ref'],at(r['entitlement_end']),'cycle'),slot_key=r['slot_key'],entitlement_end=r['entitlement_end'],window_start=r['start'],window_end=r['end'],skip_newcomer=True,automatic_send_allowed=False,expiry_confirmation_only=node=='RENEW_EXPIRED'),r)
 out=route(result,user,evidence,b,'hxc:expiry','paid_expiry','299 '+r['node']+' 独立续费确认')
 out['entitlement_state']='expired'if node=='RENEW_EXPIRED'else'active'
 out['material']={'kind':'followup','title':'到期确认'if node=='RENEW_EXPIRED'else'临近3天续费确认','specific_help':'准确期限及299一年购买入口','summary':'仅续费周期确认，不生成在服总结或召回','source_ref':'299-renewal:'+r['slot_key']}
 return out

def is_expiry_confirmation(result):
 b=result.get('sku_pack_binding')or{}
 return b.get('primary_identity')=='299'and b.get('node')=='RENEW_EXPIRED'and b.get('expiry_confirmation_only')is True and b.get('renewal_mode')=='standalone_confirmation'

def fixed_response(data,bundle,request_id):
 b=data.get('sku_pack_binding')or{}
 if b.get('node')not in NODES:return None
 if data.get('do_not_contact')or data.get('contact_policy')in {'blocked','do_not_contact','opt_out'}:return {'ok':False,'candidates':[],'warning':'contact_blocked','model_calls':0}
 name=(data.get('salutation_decision')or{}).get('send_name')
 if not name:return {'ok':False,'candidates':[],'warning':'salutation_missing','model_calls':0}
 p=policy();end=at(b['entitlement_end']);now=datetime.now(TZ)
 if b['node']=='RENEW_EXPIRED'and now<end:return {'ok':False,'candidates':[],'warning':'not_yet_expired','model_calls':0}
 body=p['standalone_templates']['R3'if b['node']=='RENEW_3D'else'R0']
 display=f'{end.month}月{end.day}日'+('0点'if end.hour==0 and end.minute==0 else f'{end.hour}点{end.minute:02d}分')
 body=body.replace('{{称呼}}',name).replace('{{到期时间}}',display)
 return {'ok':True,'request_id':request_id,'scene':data.get('scene'),'generated_by':'owner_approved_fixed_template','preview_only':bool(data.get('preview_only')or data.get('generation_mode')=='debug'),'candidates':[{'variant':p['version']+':'+b['renewal_node'],'push_copy':body,'risk_flags':[]}],'attachments':[],'generation_trace':bundle['trace'],'judgment_card':bundle['common_copy_card'],'template_result':{'status':'ready_for_review','template_key':p['version']+':'+b['renewal_node'],'asset_version':p['version'],'attachments':[]},'model_calls':0,'automatic_send_allowed':False,'fallback_used':False}
