"""Original deterministic functions; sources are bound by the adapter."""
from pathlib import Path
from datetime import datetime
from zoneinfo import ZoneInfo
import copy, hashlib, json

ROOT=Path(__file__).resolve().parents[1]

ASSET='docs/product-operation-packs/shared/returning-registered-invitation.v1.json'

RULES={'conditional_account_check','conditional_entry_check','owner_distill_conditional','confirmed_not_activated','not_activated','repair_unknown','repair_confirmed'}

CODES={'31314141':('6.8','公开课',14,'q-392ae6183bbd81b104cb7a8a1ede2715'),'334465678':('99','共创计划',30,'q-e0fef64351c03718804f205f9876e434')}

def at(v):
 try:
  d=datetime.fromisoformat(str(v).replace('Z','+00:00'));return d.replace(tzinfo=ZoneInfo('Asia/Shanghai'))if d.tzinfo is None else d
 except (ValueError,TypeError):return None

def scope(data):
 b=data.get('sku_pack_binding')or{};code=str(b.get('product_code')or'')
 return code in CODES and b.get('primary_identity')==CODES[code][0]and b.get('node')in {'D2','D4','D6','REPAIR_ONCE'}and b.get('copy_rule')in RULES

def bind(data,root=None,sources=None):
 if not scope(data):return data
 out=copy.deepcopy(data);p=out.setdefault('profile_context',{});p.pop('returning_registration',None)
 if p.get('status')not in {'exact_archive','exact_archive_and_conversation'}:return out
 if sources is None:
  import hxc_routine_policy
  try:sources=hxc_routine_policy.load_sources(root or ROOT)
  except (OSError,ValueError,RuntimeError):return out
 ref=out.get('safe_user_ref')or p.get('safe_user_ref');u=(sources.get('users')or{}).get(ref)or{}
 ids={str(x)for x in u.get('_hxcAccountIds')or[]if x}
 if not ids and u.get('黄小璨用户ID'):ids={str(u['黄小璨用户ID'])}
 if u.get('customer_mapping_status')!='resolved'or len(ids)!=1 or u.get('_customerIdentityConflict')or u.get('identity_conflict'):return out
 account=(sources.get('accounts')or{}).get(next(iter(ids)))or{};reg=at(account.get('created_at'));paid=at(out['sku_pack_binding'].get('paid_at'))
 if reg and paid and reg<paid:
  p['returning_registration']={'exact_account':True,'registered_at':reg.isoformat(),'paid_at':paid.isoformat(),'source':'hxc.users.created_at','purchase_is_not_activation':True}
 return out

def render(data):
 if not scope(data):return None
 p=data.get('profile_context')or{};proof=p.get('returning_registration')or{};b=data['sku_pack_binding']
 reg,paid=at(proof.get('registered_at')),at(b.get('paid_at'))
 if not(proof.get('exact_account')is True and proof.get('source')=='hxc.users.created_at'and reg and paid and reg<paid and at(proof.get('paid_at'))==paid):return None
 from salutation_policy import resolved_send_name
 code=str(b['product_code']);_,activity,days,slug=CODES[code]
 raw=(ROOT/ASSET).read_bytes();asset=json.loads(raw)
 known=(data.get('provisional_purchase')or{}).get('questionnaire_confirmed')is True or ((p.get('newcomer_sop')or{}).get('user_states')or{}).get('questionnaire')=='submitted'
 entry=asset['known_questionnaire']if known else asset['unknown_questionnaire'].replace('{{问卷链接}}','https://www.youcangogogo.com/h5/auth.html?slug='+slug)
 text=asset['text']
 for key,value in {'称呼':resolved_send_name(data),'活动名称':activity,'体验天数':str(days),'开通承接':entry}.items():text=text.replace('{{'+key+'}}',value)
 if '{{'in text:raise ValueError('returning_invitation_unfilled')
 return {'text':text,'source':ASSET,'version':asset['version'],'sha256':hashlib.sha256(raw).hexdigest()}

def apply_result(data,result):
 if not result or not result.get('ok'):return result
 rendered=render(data)
 if not rendered:return result
 out=copy.deepcopy(result)
 for c in out.get('candidates')or[]:c['push_copy']=rendered['text']
 out.setdefault('template_result',{})['returning_copy_asset']={k:v for k,v in rendered.items()if k!='text'}
 return out
