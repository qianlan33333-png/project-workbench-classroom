"""Selected original rules; source I/O is supplied by the host as frozen inputs."""
import json, hashlib
from pathlib import Path
from datetime import datetime, timedelta
from zoneinfo import ZoneInfo

VERSION='sku.202608052341.rules.v1.4.0'

TZ=ZoneInfo('Asia/Shanghai')

CODE='202608052341'

ROOT=Path(__file__).resolve().parents[1]

def at(v):
 try:
  t=datetime.fromisoformat(str(v).replace('Z','+00:00'));return t.replace(tzinfo=TZ)if not t.tzinfo else t.astimezone(TZ)
 except(TypeError,ValueError):return None

def sha(v):return hashlib.sha256(json.dumps(v,ensure_ascii=False,sort_keys=True,default=str).encode()).hexdigest()[:24]

def hold(r,b,reason):return dict(r,sku_pack_binding=b,outbound_candidate=False,preparation_reason=reason)

def route(r,u,e,b,strategy,branch,why):
 from hxc_lifecycle_strategy import BRANCHES,POLICY_VERSION
 action,scene,skill=BRANCHES[branch]
 return dict(r,eligible=True,safe_user_ref=u['safe_user_ref'],sku_pack_binding=b,product_service_key='hxc',strategy_key=strategy,branch=branch,action=action,scene=scene,skill_key=skill,policy_version=POLICY_VERSION,entitlement_state='active',rights_type='paid'if(e.get('operational_service_admission')or{}).get('original_order_refs')else'gift',outbound_candidate=True,manual_review_required=True,preparation_reason='',why_now=why,evidence_refs=[x for x in [u.get('snapshot_id'),(e.get('operational_service_admission')or{}).get('order_ref')]if x],rights_end=b.get('entitlement_end')or'')

def before_material(user,evidence,sources,result,*,now,evaluate):
 a=evidence.get('operational_service_admission')or user.get('operational_service_admission')or{}
 if a.get('status')!='active':return result
 # A prior identity/contact/manual prohibition is never overridden by pack selection.
 if not result.get('eligible'):return result
 ref=user['safe_user_ref'];uid=evidence.get('routine_account_id');paid=at(a.get('first_299_paid_at'))
 skip=bool(a.get('skip_newcomer')or evidence.get('skip_299_newcomer')or not paid)
 # New paid first cycles use purchase; established/upsell service keeps its
 # original service history and continues to skip newcomer. Gifts have no fake order.
 anchor=paid if paid and not skip else(at(evidence.get('first_service_activated_at'))or paid or at(evidence.get('onboarding_anchor_at')))
 end=at(a.get('expires_at'));week=now.strftime('%G-W%V');monday=now.replace(hour=0,minute=0,second=0,microsecond=0)-timedelta(days=now.weekday())
 b=dict(version=VERSION,product_code=CODE,primary_identity='299',node='MONITOR',cycle_key=sha([ref,'hxc_personal_service',anchor.isoformat()if anchor else'original_unknown']),skip_newcomer=skip,service_start=anchor.isoformat()if anchor else None,paid_at=paid.isoformat()if paid else None,entitlement_end=end.isoformat()if end else None,automatic_send_allowed=False)
 private=(sources.get('users')or{}).get(ref)or user
 relation=at(private.get('企微首次添加时间'))
 if relation and relation<=now:
  months=(now.year-relation.year)*12+now.month-relation.month
  import calendar
  months-=now.day<min(relation.day,calendar.monthrange(now.year,now.month)[1])
  b['relationship_context']={'source':'CRM企微首次添加时间','started_at':relation.isoformat(),'day':(now.date()-relation.date()).days+1,'month':months+1}
 b['service_stage']='ESTABLISHED'if skip else'FIRST_WEEK'if anchor and now-anchor<timedelta(days=7)else'FIRST_MONTH'if anchor and now-anchor<timedelta(days=28)else'ESTABLISHED'
 b['activation_class']='confirmed_not_activated'if(evidence.get('activation_state')=='confirmed_not_activated'or(user.get('_hxcSummary')or{}).get('activation_state')=='confirmed_not_activated')else'active'if a.get('current_hxc_rights',{}).get('active')else'unknown'
 if anchor and now>=anchor:b['service_week']=int((now-anchor).total_seconds()//604800)+1
 # Concrete transactional replies/bugs are handled by humans outside routine copy.
 from sku299_renewal import standalone
 confirmation=standalone(user,evidence,sources,result,now)
 if confirmation:return confirmation
 if end and end<=now:return dict(result,eligible=False,outbound_candidate=False,reason='expired_outside_active_product_packs',sku_pack_binding=b)
 elapsed=(now-anchor).total_seconds()/3600 if anchor else None
 # Windows replace missed nodes; never replay a string of expired newcomer messages.
 if not skip and elapsed is not None and (elapsed<192 or evidence.get('onboarding_completion_due')):
  if elapsed<24:return hold(result,dict(b,node='D0'),'mirror_D0_or_not_due')
  node='D2'if elapsed<96 else'D4'if elapsed<168 else'D7'
  b.update(node=node,slot_key=sha([b['cycle_key'],node]),copy_rule='owner_distill'if node=='D4'else'd7_all_states'if node=='D7'else'first_service_24h',sop_node='D7:summary'if node=='D7'else'A1'if node=='D2'else'A2')
  return route(result,user,evidence,b,'hxc:onboarding','d7_review'if node=='D7'else'used_once'if evidence.get('onboarding_conversation_observed')else'opened_unused','299首次周期 '+node)
 b.update(node='WEEK_MAIN',slot_key=sha([ref,'hxc_personal_service',week]),weekly_slot_key=sha([ref,'hxc_personal_service',week]),window_start=monday.isoformat(),window_end=(monday+timedelta(days=7)).isoformat(),copy_rule='hxc_weekly_service_first',activity_segment=(evidence.get('activity')or{}).get('activity_segment','unknown'))
 previous=monday-timedelta(days=7)
 b['report_expected']=any(previous<=t<monday for v in(sources.get('user_message_times')or{}).get(uid,[])if(t:=v if isinstance(v,datetime)else at(v)))or any(str(r.get('period_start'))[:10]==previous.date().isoformat()for r in(sources.get('reviews')or{}).get(uid,[]))
 from sku299_renewal import describe,bind
 renewal=describe(user,evidence,sources,now)
 if renewal and renewal['mode']=='service_ending_only':b=bind(b,renewal)
 r=route(result,user,evidence,b,'hxc:ongoing','normal_service','299本周主服务：沿HXC报告、周总结或交流支持承接')
 return hold(r,b,'service_material_required')

def choose_material(evidence,sources,*,week,now,profile=None,binding=None):
 from hxc_routine_policy import unified_week_material,current_catalog_candidates
 b=binding or{};uid=evidence.get('routine_account_id')
 monday=datetime.strptime(week+'-1','%G-W%V-%u').replace(tzinfo=TZ)
 # Existing exact send records own the service week.
 if any((t:=at(x.get('sent_at')))and monday<=t<monday+timedelta(days=7)for x in(sources.get('sends')or{}).get(uid,[])):return {'held':'already_sent_this_week'}
 material=unified_week_material(evidence,sources,week=week,now=now,profile=profile)
 if material and (material.get('kind')=='review' or material.get('purpose')=='growth_report'):return material
 if b.get('report_expected'):return {'held':'waiting_for_weekly_publication'}
 # An explicit one-person editorial assignment, still gated by the published
 # catalog, existing weekly slot, entitlement, send history and review queue.
 assignment_path = ROOT / 'var/private-data/sku299-owner-editorial-20261021.json'
 assignments = json.loads(assignment_path.read_text()) if assignment_path.exists() else {}
 assignment = (assignments.get('assignments') or {}).get(evidence.get('routine_workbench_ref')) or {}
 if assignment.get('week') == week:
     from hxc_routine_policy import stamp
     content_id = str(assignment.get('content_id') or '')
     pool = (sources.get('catalog_by_key') or {}).values() or (sources.get('catalog') or {}).values()
     item = next((row for row in pool if str(row.get('content_id') or '') == content_id), None)
     monday = datetime.strptime(week + '-1', '%G-W%V-%u').replace(tzinfo=TZ)
     published = stamp(item.get('published_at')) if item else None
     history = sources.get('content_history_by_ref', {}).get(evidence.get('routine_workbench_ref'), set())
     seen = sources.get('seen', {}).get(uid, set())
     if (not item or not published or not monday <= published <= now or item.get('status') != 'published'
         or item.get('deleted') or item.get('pushable') is not True or item.get('path_status') != 'verified_route'
         or not item.get('base_target_path') or (str(item.get('content_type') or ''), content_id) in history
         or content_id in seen):
         return {'held': 'owner_selected_content_unavailable_or_already_seen'}
     if str(item.get('content_version')) != str(assignment.get('content_version')):
         return {'held': 'owner_selected_content_version_changed'}
     rights = item.get('required_entitlement')
     if rights and not (isinstance(rights, dict) and rights.get('policy') == 'existing_content_view_gate'
                        and rights.get('checked_at_dispatch') is True):
         return {'held': 'owner_selected_content_rights_unverified'}
     return dict(kind='content', content_id=content_id, content_type=item['content_type'], title=item['title'],
                 summary=item['content_summary'], specific_help=assignment['writing_focus'],
                 base_target_path=item['base_target_path'], published_at=item['published_at'],
                 version=item['content_version'], source_ref=item.get('source_ref') or 'catalog:' + content_id,
                 selection_basis='owner_explicit_editorial_recommendation; no verified personal business topic')
 segment=b.get('activity_segment')
 if material:return material
 if segment in {'A','C'}:
  # A sourced issue can be followed up in WeCom even when no article matches.
  conv=(profile or{}).get('conversation')or{};facts=[]
  for group in ['fields','needs']:
   for x in(conv.get(group)or{}).values():
    if isinstance(x,dict)and x.get('value')and x.get('evidence_refs')and x.get('status')in {'user_stated','user_confirmed','human_confirmed','current_confirmed','explicit_confirmed'}:facts.append(x)
  if facts:return dict(kind='followup',title='跟进真实关注点',specific_help=facts[0]['value'],summary=facts[0]['value'],source_ref=facts[0]['evidence_refs'][0],source_at=facts[0].get('source_at'))
 # Inline renewal never creates material or a new service opportunity.

 return {'held':'no_verified_relevant_material'}

def after_material(result):
 b=dict(result.get('sku_pack_binding')or{})
 if b.get('version')!=VERSION:return result
 m=result.get('material')or{}
 if b['node']=='WEEK_MAIN':
  if m.get('kind')not in {'review','content','followup'}:return hold(result,b,'no_verified_relevant_material')
  b.update(node='WEEKLY'if m['kind']=='review'else'D_DIGEST'if m.get('digest_items')else'CONTENT'if m['kind']=='content'else'FOLLOWUP',copy_rule='own_summary'if m['kind']=='review'else'content_digest'if m.get('digest_items')else'content'if m['kind']=='content'else'sourced_followup')
  if m.get('digest_items'):b['digest_items']=m['digest_items']
 return dict(result,sku_pack_binding=b,preparation_reason=''if result.get('outbound_candidate')else result.get('preparation_reason',''))
