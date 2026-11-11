"""Original deterministic functions; sources are bound by the adapter."""
from order_time_contract import service_time
import copy
from datetime import datetime, timedelta
import sku68_operations as shared

at=shared.at

TZ=shared.TZ

sha=shared.sha

orders=shared.orders

VERSION='sku.334465678.rules.v1.0.0'

CODE='334465678'

ACTIVATION_URL='https://www.youcangogogo.com/h5/auth.html?slug=q-e0fef64351c03718804f205f9876e434'

CONTINUATION_URL=shared.CONTINUATION_URL

CONTINUATION_VERSION='sku99.continuation.299.v1'

def purchase(user,now):
 xs=[(d,o)for o in orders(user)if str(o.get('product_code'))==CODE and(d:=at(service_time(o)))and d<=now]
 return min(xs,key=lambda x:x[0])if xs else None

def annotate(result,b):return dict(result,sku_pack_binding=b)

def hold(result,b,reason):return annotate(dict(result,outbound_candidate=False,preparation_reason=reason),b)

def fresh(sources,now):
 t=at(sources.get('as_of'))
 return sources.get('complete')is True and t and timedelta(0)<=now-t<=timedelta(hours=24)

def route(result,user,evidence,strategy,branch,why):
 from hxc_lifecycle_strategy import BRANCHES,POLICY_VERSION
 action,scene,skill=BRANCHES[branch]
 out=dict(result,eligible=True,safe_user_ref=user.get('safe_user_ref'),product_service_key='hxc',strategy_key=strategy,branch=branch,action=action,scene=scene,skill_key=skill,policy_version=POLICY_VERSION,why_now=why,outbound_candidate=True,preparation_reason='',manual_review_required=True)
 out.setdefault('evidence_refs',[]);out.setdefault('entitlement_state','unverified');out.setdefault('rights_type','gift');out.setdefault('rights_start',evidence.get('first_service_activated_at')or'');out.setdefault('rights_end',(user.get('_hxcSummary')or{}).get('service_expires_at')or'')
 return out

def before_material(user,evidence,sources,result,*,now,evaluate):
 private=(sources.get('users')or{}).get(user.get('safe_user_ref'))or user
 found=purchase(private,now)
 if not found:return result
 paid,order=found;elapsed=(now-paid).total_seconds()/3600;ref=user.get('safe_user_ref')
 reentry=shared.paid_reentry_after_old_expiry(user,evidence,paid,now)
 if reentry and(result.get('eligible')or result.get('reason')in {'entitlement_or_service_unknown','current_expiry_sources_disagree'}):result=shared.paid_reentry_decision(result,user)
 b={'version':VERSION,'product_code':CODE,'paid_at':paid.isoformat(),'primary_identity':'99','node':'MONITOR','copy_rule':'','cycle_key':sha([ref,CODE,paid.isoformat()]),'automatic_send_allowed':False,'entry_contract_version':'sku99.destinations.v1','timing_version':'sku99.timing.24h'}
 if (result.get('service_admission')or{}).get('primary_identity')=='299':
  return annotate(result,dict(b,primary_identity='299',node='ORIGINAL_SERVICE',copy_rule='keep_original_299_service'))
 premium=shared.primary_299(private,evidence,now)
 if premium:
  b.update(primary_identity='299',node='ORIGINAL_SERVICE',copy_rule='keep_original_299_service',primary_entitlement=premium)
  # The purchase of another product never restarts an established annual service.
  if result.get('strategy_key')=='hxc:onboarding':
   cleaned=dict(user,order_history=[o for o in orders(private)if str(o.get('product_code'))!=CODE],_orderHistory=[])
   result=evaluate(cleaned,dict(evidence,onboarding_completion_due=False),today=now)
  return annotate(result,b)
 # Existing hard identity/contact/manual handling is not a copy opportunity.
 unclear_rights_only=(result.get('reason')=='other_service_or_fact_review' and user.get('service_relationship_tier')in {'needs_verification','high_ticket_historical','high_ticket_expired'} and (user.get('_hxcSummary')or{}).get('service_status')in {None,'','unknown'} and user.get('customer_mapping_status')=='resolved' and not user.get('_customerIdentityConflict') and not user.get('identity_conflict') and not any(user.get(k)is True for k in ['current_service_refunded','current_entitlement_revoked']))
 if not result.get('eligible') and result.get('reason')not in {'entitlement_or_service_unknown','current_expiry_sources_disagree'}and not unclear_rights_only:return annotate(dict(result,outbound_candidate=False),b)
 if result.get('branch')=='unanswered_issue':return hold(result,b,'respond_to_user_first')
 if user.get('_customerIdentityConflict')or user.get('identity_conflict')or any(user.get(k)is True or private.get(k)is True for k in ['unresolved_user_issue','current_service_refunded','current_entitlement_revoked']):return hold(result,b,'current_service_or_reply_blocked')
 if not fresh(sources,now):return hold(result,b,'sku99_source_not_ready')
 h=user.get('_hxcSummary')or{};end=at(h.get('service_expires_at'))
 active=h.get('service_status')=='active'and end and now<end and evidence.get('routine_account_exact')is True
 expired=h.get('service_status')=='expired'or bool(end and end<=now)
 if expired and not reentry:return hold(result,dict(b,node='COMPLETE'),'sku99_actual_service_ended')
 original=at(evidence.get('first_service_activated_at'))
 old=bool(shared.prior_service_start(evidence,sources,paid,now)and not reentry)
 b['existing_service_cycle']=old
 grants=[g for g in evidence.get('grants')or[]if(t:=at(g.get('granted_at')))and t<=now and(e:=at(g.get('expires_at')))and e>now and g.get('status')in {None,'active','enabled','granted'}]
 exact=[g for g in grants if at(g.get('expires_at'))==end]
 grant_start=min([at(g.get('granted_at'))for g in exact],default=None)
 start=original or grant_start
 if start:
  b.update(service_start=start.isoformat(),activation_week=int((now-start).total_seconds()//604800)+1,week_wording='开通黄小璨的'if original else'这次体验的')
 if end and not reentry:b['entitlement_end']=end.isoformat()
 # Nonactivation requires an explicit source fact, never missing account/grant.
 confirmed=(evidence.get('activation_state')=='confirmed_not_activated'or h.get('activation_state')=='confirmed_not_activated')and evidence.get('activation_eligible')is True
 b['activation_class']='active'if active else'confirmed_not_activated'if confirmed else'unknown'
 # Incomplete attribution to an annual product is monitored without adding a second flow.
 if not reentry and any(o.get('product_key')=='soak299'or str(o.get('product_code'))=='202608052341'for o in orders(private)):
  return hold(result,dict(b,primary_identity='existing_service_identity_pending',node='ORIGINAL_SERVICE'),'verify_current_299_identity_no_duplicate')
 if not active and shared.opening_form_submitted(evidence,CODE,paid,now):
  return hold(result,dict(b,node='ACTIVATION_FULFILLMENT',copy_rule='verify_submitted_opening_form'),'opening_form_submitted_rights_pending')
 if elapsed<24 and not old:return hold(result,dict(b,node='D0'),'mirror_D0_or_not_due')
 if not active:
  # Known registered rights with missing account remain unknown, not unopened.
  if elapsed>=168:
   b.update(window_start=now.replace(hour=0,minute=0,second=0,microsecond=0).isoformat(),window_end=(now.replace(hour=0,minute=0,second=0,microsecond=0)+timedelta(days=7)).isoformat(),node='REPAIR_ONCE',copy_rule='repair_confirmed'if confirmed else'repair_unknown',slot_key=sha([b['cycle_key'],'REPAIR_ONCE']),occupies_week=now.strftime('%G-W%V'))
  else:
   node='D2'if elapsed<96 else'D4'if elapsed<144 else'D6'
   if node=='D6'and not confirmed:return hold(result,dict(b,node=node),'activation_unknown_no_D6')
   b.update(node=node,copy_rule='not_activated'if confirmed else'conditional_entry_check',slot_key=sha([b['cycle_key'],node]))
  if not result.get('eligible'):
   result=route(result,user,evidence,'hxc:known_no_rights','known_background','已付99，开通状态未确认；一次条件式服务承接')
  else:result=route(result,user,evidence,'hxc:known_no_rights'if elapsed>=168 else'hxc:onboarding','known_background'if elapsed>=168 else'purchase_account_unlinked','99 共创体验：'+b['copy_rule'])
  return annotate(result,b)
 if elapsed<168 and not old:
  node='D2'if elapsed<96 else'D4'if elapsed<144 else'D6';b['node']=node
  if node=='D6':return hold(result,b,'D6_only_for_confirmed_not_activated')
  used=evidence.get('onboarding_conversation_observed')is True
  b.update(copy_rule='owner_distill_active'if(used and node=='D2')or(not used and node=='D4')else'personal_followup_from_actual_use'if used else'first_use_invitation',slot_key=sha([b['cycle_key'],node]),sop_node='C:sku99_'+node if used else'B1'if node=='D2'else'B2')
  return annotate(route(result,user,evidence,'hxc:onboarding','used_once'if used else'opened_unused','99 首周 '+node+'：'+b['copy_rule']),b)
 # The source-recorded end is authoritative. No length inference or rights mutation.
 later=[o for o in orders(private)if(o.get('product_key')in {'soak299','legacySubscription','hxcWeekTrial'}or str(o.get('product_code'))in {CODE,'202608052341'})and(t:=at(service_time(o)))and t>paid]
 can_renew=bool(private.get('order_source_available')is True and not later and (exact or shared.current_expiry_verified(evidence,end)))
 if can_renew and now<end<=now+timedelta(hours=48):
  b.update(node='EXPIRY',copy_rule='verified_expiry_not_renewed',renewal_state='not_renewed_in_complete_order_source')
  return continuation(route(result,user,evidence,'hxc:expiry','gift_expiry','99 配套体验实际到期前48小时，未见续期'),b)
 # Genuine entrants start weekly slots after purchase firstweek. Established users retain original cycle.
 anchor=start if old and start else max(paid+timedelta(days=7),grant_start or original or paid+timedelta(days=7))
 if now<anchor:return hold(result,b,'weekly_service_not_due')
 index=int((now-anchor).total_seconds()//604800);lo=anchor+timedelta(days=7*index);hi=lo+timedelta(days=7)
 if hi>end:return hold(result,b,'short_final_partial_week_no_extra_main_touch')
 week_key=sha([b['cycle_key'],'WEEKLY',lo.isoformat()]);b.update(node='WEEK_MAIN',copy_rule='personalized_weekly_or_relevant_content',weekly_slot_key=week_key,window_start=lo.isoformat(),window_end=hi.isoformat(),slot_key=week_key)
 monday=now.replace(hour=0,minute=0,second=0,microsecond=0)-timedelta(days=now.weekday());previous=monday-timedelta(days=7);uid=evidence.get('routine_account_id')
 b['report_expected']=any(previous<=t<monday for v in(sources.get('user_message_times')or{}).get(uid,[])if(t:=v if isinstance(v,datetime)else at(v)))or any(str(r.get('period_start'))[:10]==previous.date().isoformat()for r in(sources.get('reviews')or{}).get(uid,[]))
 # Week4 continuation replaces that week's service, with the same unique continuation slot as EXPIRY.
 recent=[t for v in(sources.get('user_message_times')or{}).get(uid,[])if(t:=v if isinstance(v,datetime)else at(v))and now-t<=timedelta(days=14)and t<=now]
 b['week4_value_candidate']=bool(b.get('activation_week')==4 and recent and can_renew)
 result=route(result,user,evidence,'hxc:ongoing','normal_service','99 当前完整服务周；本人总结优先，否则相关内容')
 result.update(outbound_candidate=False,preparation_reason='service_material_required',rights_type='gift',entitlement_state='active',rights_end=end.isoformat())
 return annotate(result,b)

def continuation(result,b):
 b=dict(b,continuation_contract_version=CONTINUATION_VERSION,continuation_entry={'kind':'h5_url','url':CONTINUATION_URL,'product_code':'202608052341','price_yuan':299,'period':'annual'},continuation_slot_key=sha([b['cycle_key'],'CONTINUATION',b['entitlement_end']]))
 b['slot_key']=b['continuation_slot_key']
 return annotate(result,b)

def after_material(result):
 b=result.get('sku_pack_binding')or{}
 if b.get('version')!=VERSION:return result
 b=dict(b);material=result.get('material')or{}
 if b.get('node')=='WEEK_MAIN':
  if material.get('kind')=='review':b.update(node='WEEKLY',copy_rule='personalized_own_weekly')
  elif material.get('kind')=='content'and (not b.get('report_expected') or material.get('purpose')=='growth_report'):b.update(node='CONTENT',copy_rule='relevant_content_no_report')
  else:return hold(result,b,'wait_for_own_report'if b.get('report_expected')else'no_verified_relevant_material')
  if b.get('week4_value_candidate') and (material.get('summary')or material.get('specific_help')):
   b.update(node='W4_CONTINUATION',copy_rule='week4_actual_value_continuation',renewal_state='not_renewed_in_complete_order_source',value_facts=[material.get('summary')or material.get('specific_help')])
   return continuation(result,b)
 return annotate(dict(result,preparation_reason='')if result.get('outbound_candidate')else result,b)

def bind_request(data,decision):
 data=dict(data);current=decision.get('sku_pack_binding');existing=data.get('sku_pack_binding')
 if data.get('expected_prompt_bundle_hash')and not existing:return data
 if existing and existing!=current:raise ValueError('sku99_node_or_identity_binding_stale')
 data.pop('sku_pack_binding',None)
 if current:data['sku_pack_binding']=copy.deepcopy(current)
 return data

def prepare_data(data):
 b=data.get('sku_pack_binding')or{}
 if b.get('version')!=VERSION or not b.get('sop_node'):return data
 data=copy.deepcopy(data);p=data.get('profile_context')or{};s=p.get('newcomer_sop')or{}
 if s and not s.get('blockers'):s.update(node=b['sop_node'],sku_pack_binding=b);p['newcomer_sop']=s;data['profile_context']=p
 return data

def fixed_response(data,bundle,root,request_id):
 b=data.get('sku_pack_binding')or{};rule=b.get('copy_rule');node=b.get('node')
 if b.get('primary_identity')!='99'or rule not in {'repair_unknown','repair_confirmed','conditional_entry_check','not_activated','owner_distill_active','first_use_invitation'}:return None
 p=data.get('profile_context')or{};sop=p.get('newcomer_sop')or{}
 if p.get('status')not in {'exact_archive','exact_archive_and_conversation'}or sop.get('blockers'):return {'ok':False,'candidates':[],'model_calls':0,'warning':'sku99_exact_profile_or_node_required'}
 if data.get('do_not_contact')is True or data.get('contact_policy')in {'blocked','do_not_contact','opt_out','blocked_internal_check'}:return {'ok':False,'candidates':[],'model_calls':0,'warning':'sku99_contact_blocked'}
 name=(data.get('salutation_decision')or{}).get('send_name')
 if not name:return {'ok':False,'candidates':[],'model_calls':0,'warning':'sku99_salutation_missing'}
 a=assets();entry=None
 if rule in {'repair_unknown','conditional_entry_check'}:body=a['repair_unknown']
 elif rule=='repair_confirmed':body=a['repair_confirmed']
 elif rule=='not_activated':body=a['not_activated_'+node]
 elif rule=='owner_distill_active':body=shared.DISTILL+'\n\n你可以到黄小璨 AI 小程序里，选一篇与你眼下工作或业务有关的思考，聊完后把原文和讨论复制带走，开始蒸馏老黄。'
 else:
  from newcomer_copy_contract import questionnaire_focus
  focus=questionnaire_focus(p)
  body=a['first_use_focus'].replace('{{问卷明确关注点}}',focus['value'])if focus else a['first_use_no_focus']
 if rule in {'owner_distill_active','first_use_invitation'}:
  from newcomer_copy_contract import approved_entry
  entry=approved_entry(root)
  if not entry:return {'ok':False,'candidates':[],'model_calls':0,'warning':'sku99_verified_home_missing'}
 body=body.replace('{{称呼}}',name).replace('{{99开通问卷入口}}',ACTIVATION_URL).replace('{{购后实际天数}}',str(max(0,int((datetime.now(TZ)-at(b['paid_at'])).total_seconds()//86400))))
 return {'ok':True,'request_id':request_id,'scene':data.get('scene'),'generated_by':'owner_approved_fixed_template','preview_only':bool(data.get('preview_only')or data.get('generation_mode')=='debug'),'candidates':[{'variant':rule,'push_copy':body,'risk_flags':[]}],'attachments':[entry]if entry else[],'generation_trace':bundle['trace'],'judgment_card':bundle['common_copy_card'],'template_result':{'status':'ready_for_review','template_key':rule,'asset_version':VERSION,'attachments':[entry]if entry else[]},'model_calls':0,'automatic_send_allowed':False,'fallback_used':False}
