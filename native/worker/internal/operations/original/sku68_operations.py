"""Original deterministic functions; sources are bound by the adapter."""
from order_time_contract import service_time
import copy, hashlib, json
from pathlib import Path
from datetime import datetime, timedelta
from zoneinfo import ZoneInfo

VERSION='sku.31314141.rules.v1.3.0'

CODE='31314141'

TZ=ZoneInfo('Asia/Shanghai')

ACTIVATION_URL='https://www.youcangogogo.com/h5/auth.html?slug=q-392ae6183bbd81b104cb7a8a1ede2715'

ENTRY_CONTRACT_VERSION='sku68.destinations.v1'

CONTINUATION_VERSION='sku68.continuation.299.v1'

CONTINUATION_URL='https://www.youcangogogo.com/p/202608052341'

TIMING_VERSION='sku68.timing.v2.24h'

TIMING_START=datetime(2026,10,19,tzinfo=TZ)

ANCHOR_SWITCH_AT=datetime(2026,10,19,14,0,tzinfo=TZ)

LEGACY_ANCHOR_VERSION='sku68.anchor.v3.account-after-20261019T1400'

ANCHOR_CONTRACT_VERSION='sku68.anchor.v4.order-window-activation-fact'

FIXED_INVITATION_SOURCE_VERSION='sku68.fixed-invitation-source.v1.20261025'

LEGACY_ABSENCE_BLOCKER='未对话来源已过时，执行前刷新'

def first_touch_hours(paid):return 24 if paid>=TIMING_START else 48

DISTILL='''{{称呼}}，再跟你分享一个黄小璨 AI 特别牛的用法。

在黄小璨AI里面，你可以持续蒸馏老黄，把ta的思考变成你的个人资产。

具体做法是这样——

每当看到老黄关于 AI＋OPC／OPT 的最新思考、方法，你都可以跟黄小璨ai聊一下，看看对你的工作和业务有什么参考和帮助。

聊完后，黄小璨ai小程序支持一键复制所有原文+讨论内容，这时你可以把ta给到你的ai，给ta一段蒸馏专用提示词，你就能持续蒸馏老黄了。

这个打开方式，用好了，每年至少价值几十万，我实在不忍心你错过。'''

def at(v):
 try:
  d=datetime.fromisoformat(str(v).replace('Z','+00:00'));return d.replace(tzinfo=TZ)if d.tzinfo is None else d.astimezone(TZ)
 except(ValueError,TypeError):return None

def prior_service_start(evidence,sources,paid,now):
 """Registration/questionnaire dates are not prior HXC service evidence."""
 if evidence.get('routine_account_exact')is not True:return None
 uid=str(evidence.get('routine_account_id')or'')
 dates=[at(evidence.get('first_service_activated_at'))]
 dates += [at(x)for x in (sources.get('histories')or{}).get(uid,[])]
 dates += [at(g.get('granted_at'))for g in (evidence.get('grants')or[])+(evidence.get('source_product_grants')or[])
           if at(g.get('expires_at'))and g.get('status')in {None,'active','enabled','granted','expired'}]
 return min((t for t in dates if t and t<paid-timedelta(days=1)and t<=now),default=None)

def opening_form_submitted(evidence,code,paid,now):
 slug={'31314141':'q-392ae6183bbd81b104cb7a8a1ede2715','334465678':'q-e0fef64351c03718804f205f9876e434'}.get(str(code))
 # The published activation projection uses `slug` and exact customer joins;
 # raw source rows use the snapshot field and an explicit resolved state.
 # A prior submission is not a new grant, but must not trigger repeat filling.
 return any((q.get('questionnaire_slug_snapshot')or q.get('slug'))==slug
            and (t:=at(q.get('submitted_at')))and t<=now
            and (q.get('source_identity_state')=='resolved'or
                 ('slug'in q and evidence.get('routine_account_exact')is True
                  and q.get('source_identity_state')in {None,'','resolved'}))
            for q in evidence.get('surveys')or[])

def paid_reentry_after_old_expiry(user,evidence,paid,now):
 h=user.get('_hxcSummary')or{};end=at(h.get('service_expires_at'))
 return bool(end and end<=paid and h.get('service_status')=='expired'
             and timedelta(0)<=now-paid<timedelta(days=7)
             and user.get('customer_mapping_status')=='resolved'
             and evidence.get('routine_account_exact')is True
             and not any(user.get(k)for k in ['_customerIdentityConflict','identity_conflict',
                         'do_not_contact','unresolved_user_issue','current_service_refunded','current_entitlement_revoked']))

def paid_reentry_decision(result,user):
 from hxc_lifecycle_strategy import BRANCHES,POLICY_VERSION
 action,scene,skill=BRANCHES['purchase_account_unlinked']
 return dict(result,eligible=True,safe_user_ref=user.get('safe_user_ref'),product_service_key='hxc',
             strategy_key='hxc:onboarding',branch='purchase_account_unlinked',action=action,scene=scene,
             skill_key=skill,policy_version=POLICY_VERSION,outbound_candidate=False,preparation_reason='',
             prior_entitlement_state='expired',entitlement_state='unverified',rights_type='gift',rights_end='')

def current_expiry_verified(evidence,end):
 current=evidence.get('current_hxc_entitlement')or{}
 return bool(end and evidence.get('routine_account_exact')is True
             and current.get('observed')is True and current.get('active')is True
             and at(current.get('expires_at'))==end)

def sha(v):return hashlib.sha256(json.dumps(v,ensure_ascii=False,sort_keys=True,default=str,separators=(',',':')).encode()).hexdigest()

def orders(user):return [o for o in user.get('_orderHistory')or user.get('order_history')or[]if isinstance(o,dict)and o.get('is_paid')is True and o.get('is_refunded')is False]

def purchase(user,now):
 xs=[(d,o)for o in orders(user)if str(o.get('product_code'))==CODE and(d:=at(service_time(o)))and d<=now]
 return min(xs,key=lambda x:x[0])if xs else None

def primary_299(user,evidence,now):
 """Product-correlated current grant only; history/price/high_ticket is insufficient."""
 from sku299_owner_service import resolve
 admission=resolve(user,evidence,now)
 if admission:
  if admission['status']=='active':
   return dict(identity='299',expires_at=admission.get('expires_at'),status='active',
     source_ref='owner-service-policy:'+admission['version'],join_basis=admission['basis'],
     order_ref=admission.get('order_ref'),requires_activation_receipt=False)
  if admission['status'] in {'expired','inactive','manual_account_link','identity_conflict','current_service_revoked'}:return None
 paid=[o for o in orders(user)if o.get('product_key')=='soak299'or str(o.get('product_code'))=='202608052341']
 if not paid:return None
 for g in (evidence.get('grants')or[])+(evidence.get('source_product_grants')or[]):
  start=at(g.get('granted_at'));end=at(g.get('expires_at'))
  if not(start and end and start<=now<end)or g.get('status')not in {None,'active','enabled','granted'}:continue
  correlated=(g.get('product_key')=='soak299'or str(g.get('product_code'))=='202608052341'
    or g.get('category')in {'paid_299','soak299','paid_365d','paid_annual'}
    or g.get('product_service_tag')=='深度思考群用户')
  # Exact order identifiers are allowed only inside this server-side join.
  refs={str(o[k])for o in paid for k in ['order_ref','order_id']if o.get(k)}
  correlated=correlated or any(str(g.get(k)or'')in refs for k in ['order_ref','order_id'])
  basis='explicit_product_or_order_reference'
  if not correlated and end-start>=timedelta(days=300):
   # Warehouse grant projection omits product IDs. A unique paid 299 order
   # immediately followed by its annual grant is a bounded source join, not
   # a guess based on today's generic active membership or historical price.
   near=[o for o in orders(user)if(t:=at(service_time(o)))and timedelta(0)<=start-t<=timedelta(seconds=60)]
   if len(near)==1 and near[0]in paid:
    correlated=True;basis='unique_299_order_to_annual_grant_within_60s'
  if correlated:return {'identity':'299','expires_at':end.isoformat(),'source_ref':g.get('evidence')or'','join_basis':basis}
 return None

def annotate(result,b):
 out=dict(result);out['sku_pack_binding']=b;return out

def before_material(user,evidence,sources,result,*,now,evaluate):
 private=(sources.get('users')or{}).get(user.get('safe_user_ref'))or user
 p=purchase(private,now)
 if not p:return result
 paid,order=p
 b={'version':VERSION,'product_code':CODE,'paid_at':paid.isoformat(),'primary_identity':'6.8',
    'node':None,'cycle_key':sha([user.get('safe_user_ref'),CODE,paid.isoformat()]),'copy_rule':'','automatic_send_allowed':False,'entry_contract_version':ENTRY_CONTRACT_VERSION}
 admission=result.get('service_admission')or{}
 if admission.get('primary_identity')=='299':
  return annotate(result,dict(b,primary_identity='299',node='ORIGINAL_SERVICE',copy_rule='keep_original_299_service'))
 reentry=paid_reentry_after_old_expiry(user,evidence,paid,now)
 if reentry and (result.get('eligible')or result.get('reason')in {'entitlement_or_service_unknown','current_expiry_sources_disagree'}):
  result=paid_reentry_decision(result,user)
 # A known paid first-week SKU does not need a separate authorization receipt
 # to offer the approved conditional entry. Preserve all real human/contact holds.
 if not result.get('eligible'):
  unclear=(result.get('reason')=='other_service_or_fact_review' and user.get('service_relationship_tier')=='needs_verification' and (user.get('_hxcSummary')or{}).get('service_status')in {None,'','unknown'})
  paid_entry=((now-paid).total_seconds()<168*3600 and user.get('customer_mapping_status')=='resolved'
              and(result.get('reason')in {'entitlement_or_service_unknown','current_expiry_sources_disagree'}or unclear)
              and not user.get('_customerIdentityConflict')and not user.get('identity_conflict')
              and not any(user.get(k)is True or private.get(k)is True for k in ['unresolved_user_issue','current_service_refunded','current_entitlement_revoked','do_not_contact']))
  if not paid_entry:
   if 168<=(now-paid).total_seconds()/3600<336:
    b.update(node='W2_MAIN',copy_rule='weekly_service_wait_for_current_rights')
   if result.get('reason')in {'other_service_or_fact_review','entitlement_or_service_unknown','current_expiry_sources_disagree'}and opening_form_submitted(evidence,CODE,paid,now)and (user.get('_hxcSummary')or{}).get('service_status')not in {'active','expired'}:
    return annotate(dict(result,outbound_candidate=False,preparation_reason='opening_form_submitted_rights_pending'),dict(b,node='ACTIVATION_FULFILLMENT',copy_rule='verify_submitted_opening_form'))
   return annotate(result,b)
  from hxc_lifecycle_strategy import BRANCHES,POLICY_VERSION
  action,scene,skill=BRANCHES['purchase_account_unlinked']
  result=dict(result,eligible=True,safe_user_ref=user.get('safe_user_ref'),product_service_key='hxc',strategy_key='hxc:onboarding',branch='purchase_account_unlinked',action=action,scene=scene,skill_key=skill,policy_version=POLICY_VERSION,outbound_candidate=True,preparation_reason='',manual_review_required=True,evidence_refs=result.get('evidence_refs')or[],entitlement_state='unverified',rights_type='gift')
 if paid>=TIMING_START:b['timing_version']=TIMING_VERSION
 premium=primary_299(private,evidence,now)
 has299=any(o.get('product_key')=='soak299'or str(o.get('product_code'))=='202608052341'for o in orders(private))
 original=prior_service_start(evidence,sources,paid,now)
 old=bool(original and original<paid-timedelta(days=1)and not reentry)
 if premium or old:
  b.update(primary_identity='299'if premium else'existing_hxc',node='ORIGINAL_SERVICE',copy_rule='keep_original_service_cycle')
  if premium:b['primary_entitlement']=premium
  # A later small-ticket purchase must not reset an established service cycle.
  if result.get('strategy_key')=='hxc:onboarding'and (premium or old):
   cleaned=dict(user,order_history=[o for o in orders(private)if str(o.get('product_code'))!=CODE],_orderHistory=[])
   e=dict(evidence,onboarding_completion_due=False)
   result=evaluate(cleaned,e,today=now)
  return annotate(result,b)
 if has299 and not reentry:
  # Keep existing routine work; ambiguous overlap never opens a second newbie flow.
  b.update(primary_identity='existing_service_identity_pending',node='ORIGINAL_SERVICE',copy_rule='verify_299_grant_no_duplicate_newcomer')
  if result.get('strategy_key')=='hxc:onboarding':result=dict(result,outbound_candidate=False,preparation_reason='299_entitlement_attribution_requires_review')
  return annotate(result,b)
 if opening_form_submitted(evidence,CODE,paid,now)and (user.get('_hxcSummary')or{}).get('service_status')not in {'active','expired'}:
  b.update(node='ACTIVATION_FULFILLMENT',copy_rule='verify_submitted_opening_form')
  return annotate(dict(result,outbound_candidate=False,preparation_reason='opening_form_submitted_rights_pending'),b)
 # One valid purchase starts the outreach window. HXC activation is a separate
 # service fact; unknown account linkage must not hide the purchase from D2/D4.
 if paid>=ANCHOR_SWITCH_AT:
  legacy=legacy_v3_successful_cycles()
  if legacy is None:
   b.update(node='SOURCE_REVIEW',copy_rule='protect_frozen_legacy_copy')
   return annotate(dict(result,outbound_candidate=False,preparation_reason='sku68_legacy_copy_ledger_unavailable'),b)
  service_anchor=at(evidence.get('first_service_activated_at')) if evidence.get('routine_account_exact')is True else None
  if (str(user.get('safe_user_ref')),paid.isoformat()) in legacy:
   # Exactly three existing successful v3 D2 copies retain their original
   # account-time binding and review path. An account-link regression holds.
   b.update(anchor_contract_version=LEGACY_ANCHOR_VERSION,anchor_kind='exact_hxc_activation')
   if not service_anchor:
    b.update(node='ACCOUNT_REVIEW',copy_rule='preserve_frozen_v3_copy')
    return annotate(dict(result,outbound_candidate=False,preparation_reason='sku68_previous_exact_account_now_unlinked'),b)
   anchor=service_anchor
   b['cycle_key']=sha([user.get('safe_user_ref'),CODE,paid.isoformat(),b['anchor_kind'],anchor.isoformat()])
  else:
   anchor=paid
   b.update(anchor_contract_version=ANCHOR_CONTRACT_VERSION,anchor_kind='first_valid_purchase',
            service_anchor_at=service_anchor.isoformat() if service_anchor else None,
            service_day=max(1,int((now-service_anchor).total_seconds()//86400)+1) if service_anchor else None)
 else:
  anchor=paid
  b.update(anchor_contract_version=ANCHOR_CONTRACT_VERSION,anchor_kind='historical_first_purchase')
 b['anchor_at']=anchor.isoformat()
 elapsed=(now-anchor).total_seconds()/3600
 # Generic HXC expiry defaults to seven days. This SKU explicitly uses 48h.
 end=at((user.get('_hxcSummary')or{}).get('service_expires_at'))
 active=(user.get('_hxcSummary')or{}).get('service_status')=='active'and evidence.get('routine_account_exact')is True
 later=[o for o in orders(private)if(o.get('product_key')in {'soak299','legacySubscription','hxcWeekTrial'}or str(o.get('product_code'))in {CODE,'334465678','202608052341'})and(t:=at(service_time(o)))and t>paid]
 if result.get('strategy_key')=='hxc:expiry'and(end is None or not now<end<=now+timedelta(hours=48)or later):
  result=dict(result,strategy_key='hxc:ongoing',branch='normal_service',outbound_candidate=False,preparation_reason='service_material_required')
 if elapsed>=168 and active and end and now<end<=now+timedelta(hours=48)and not later:
  exact_grant=any(at(g.get('expires_at'))==end and(t:=at(g.get('granted_at')))and t<=now and g.get('status')in {None,'active','enabled','granted'}for g in evidence.get('grants')or[]) or current_expiry_verified(evidence,end)
  if private.get('order_source_available')is True and sources.get('complete')is True and (source_time:=at(sources.get('as_of'))) and timedelta(0)<=now-source_time<=timedelta(hours=24) and exact_grant and result.get('branch')not in {'unanswered_issue','account_review','rights_review','service_start_review'}:
   result=dict(result,strategy_key='hxc:expiry',branch='gift_expiry',rights_type='gift',rights_end=end.isoformat(),outbound_candidate=True,preparation_reason='',why_now='6.8 配套体验真实权益48小时内到期，完整订单中未见后续续期')
   b.update(node='EXPIRY',copy_rule='verified_expiry_not_renewed',entitlement_end=end.isoformat(),renewal_state='not_renewed_in_complete_order_source')
   return continuation(result,b)
 if elapsed>=336:
  b.update(node='COMPLETE',copy_rule='regular_service_after_acquisition');return annotate(result,b)
 if elapsed<first_touch_hours(paid):
  b.update(node='D0',copy_rule='mirror_owns_welcome')
  return annotate(dict(result,outbound_candidate=False,preparation_reason='mirror_D0_or_not_due',why_now='6.8 商品包：首日由镜子承接，D2 尚未到期'),b)
 source_at=at(sources.get('as_of'))
 if sources.get('complete')is not True or not source_at or not timedelta(0)<=now-source_at<=timedelta(hours=24):
  return annotate(dict(result,outbound_candidate=False,preparation_reason='sku68_source_not_ready'),b)
 if elapsed<168:
  node='D2'if elapsed<96 else'D4'if elapsed<144 else'D6';b['node']=node
  active=(user.get('_hxcSummary')or{}).get('service_status')=='active'and evidence.get('routine_account_exact')is True
  used=evidence.get('onboarding_conversation_observed')is True
  if active and node=='D6':
   return annotate(dict(result,outbound_candidate=False,preparation_reason='D6_only_for_confirmed_not_activated',why_now='6.8 商品包：已开通，不追加第六天提醒'),b)
  if active:
   if result.get('branch')in {'unanswered_issue','open_blocked','account_review','rights_review','service_start_review'}:return annotate(result,b)
   # A2/B2 etc are stable, explicit node identities in the existing ledger.
   b['sop_node']=('C:sku68_'+node if used else 'B1'if node=='D2'else'B2')
   b['copy_rule']='owner_distill_active'if used and node=='D2'or not used and node=='D4'else'personal_followup_from_actual_use'if used else'first_use_invitation'
   result=dict(result,strategy_key='hxc:onboarding',branch='used_once'if used else'opened_unused',outbound_candidate=True,preparation_reason='')
  else:
   # Missing account is not evidence of non-activation. Historical purchases may
   # only receive a conditional account/entry check, never an opening assertion.
   if result.get('branch')!='purchase_account_unlinked':
    # A paid SKU 6.8 buyer can be classified by the generic HXC policy as a
    # known contact awaiting a relationship plan. That generic internal scene
    # must not hide this product's first-week conditional entry check.
    if result.get('branch') not in {'known_background','unknown_profile','long_silent'}:
     return annotate(result,b)
    from hxc_lifecycle_strategy import BRANCHES,POLICY_VERSION
    action,scene,skill=BRANCHES['purchase_account_unlinked']
    result=dict(result,strategy_key='hxc:onboarding',branch='purchase_account_unlinked',
                action=action,scene=scene,skill_key=skill,policy_version=POLICY_VERSION,
                outbound_candidate=True,preparation_reason='',
                why_now='6.8 有效订单已进入购后七天，账号开通状态未知；条件式确认入口与使用')
   confirmed=(evidence.get('activation_state')=='confirmed_not_activated' and evidence.get('activation_eligible')is True)
   if node=='D6' and not confirmed:
    return annotate(dict(result,outbound_candidate=False,preparation_reason='activation_unknown_no_D6_reminder'),b)
   b.update(sop_node='A1'if node=='D2'else'A2'if node=='D4'else'A3',
            copy_rule='conditional_account_check'if node=='D2'else'owner_distill_conditional'if node=='D4'else'confirmed_not_activated')
  result['why_now']='6.8 商品包 '+node+'：'+b['copy_rule']
  return annotate(result,b)
 monday=now.replace(hour=0,minute=0,second=0,microsecond=0)-timedelta(days=now.weekday())
 previous=monday-timedelta(days=7);uid=evidence.get('routine_account_id')
 report_expected=any(t and previous<=t<monday for v in(sources.get('user_message_times')or{}).get(uid,[])if(t:=v if isinstance(v,datetime)else at(v)))
 published=any(str(v.get('period_start'))[:10]==previous.date().isoformat()and str(v.get('period_end'))[:10]==(monday-timedelta(days=1)).date().isoformat()for v in(sources.get('reviews')or{}).get(uid,[]))
 b.update(node='W2_MAIN',copy_rule='weekly_four_parts_or_confirmed_absent_content',report_expected=bool(report_expected or published))
 # A recently registered account can still be in the generic first-use scene
 # after the purchase has already entered W2. The product's existing clock
 # owns this touch: request real weekly/content material before after_material.
 if active and result.get('strategy_key')=='hxc:onboarding'and result.get('branch')in {'opened_unused','used_once','purchase_account_unlinked'}:
  from hxc_lifecycle_strategy import BRANCHES
  action,scene,skill=BRANCHES['normal_service']
  result=dict(result,strategy_key='hxc:ongoing',branch='normal_service',action=action,scene=scene,skill_key=skill,
              outbound_candidate=False,preparation_reason='service_material_required',why_now='6.8购后第二周：当前权益已核，按原规则选择本人总结或相关内容')
 # Second-week existing material selection/entitlement gates are retained.
 return annotate(result,b)

def continuation(result,b):
 # The purchase page is owner-confirmed; exact current rights and no renewal
 # must still have been established in the original source-gated selection.
 if b.get('renewal_state')!='not_renewed_in_complete_order_source' or not at(b.get('entitlement_end')):
  return annotate(dict(result,outbound_candidate=False,preparation_reason='sku68_expiry_renewal_facts_required'),b)
 b=dict(b,continuation_contract_version=CONTINUATION_VERSION,
        continuation_entry={'kind':'h5_url','url':CONTINUATION_URL,'product_code':'202608052341','price_yuan':299,'period':'annual','owner_confirmed':True})
 return annotate(dict(result,outbound_candidate=True,preparation_reason=''),b)

def after_material(result):
 b=result.get('sku_pack_binding')
 if not b:return result
 b=dict(b);material=result.get('material')or{}
 # The generic onboarding decision can outlive this purchase's 14-day window.
 # COMPLETE is not a new onboarding slot. Preserve ordinary sourced ongoing
 # service and the separate verified EXPIRY route, but never backfill A1/A2.
 if b['node']=='COMPLETE'and result.get('strategy_key')=='hxc:onboarding':
  return annotate(dict(result,outbound_candidate=False,preparation_reason='sku68_acquisition_window_completed'),b)
 if b['node']=='W2_MAIN':
  if result.get('strategy_key')=='hxc:expiry':b.update(node='EXPIRY',copy_rule='verified_expiry_not_renewed')
  elif material.get('kind')=='review':b.update(node='W2_WEEKLY',copy_rule='weekly_four_parts')
  elif material.get('kind')=='content':
   if b.get('report_expected') and material.get('purpose')!='growth_report':return annotate(dict(result,outbound_candidate=False,preparation_reason='sku68_wait_for_expected_weekly_report'),b)
   b.update(node='W2_CONTENT',copy_rule='matched_content_only_if_no_report')
  elif material.get('kind')=='recall':return annotate(dict(result,outbound_candidate=False,preparation_reason='sku68_W2_no_recall_fallback'),b)
  else:return annotate(dict(result,outbound_candidate=False,preparation_reason='sku68_W2_material_not_ready'),b)
 if b['node']=='EXPIRY':
  result=continuation(result,b);b=result['sku_pack_binding']
 b['slot_key']=sha([b['cycle_key'],'W2_MAIN'if b['node']in {'W2_WEEKLY','W2_CONTENT'}else b['node']]+([b.get('entitlement_end')]if b['node']=='EXPIRY'else []))
 return annotate(result,b)

def bind_request(data,decision):
 data=dict(data);current=decision.get('sku_pack_binding')
 existing=data.get('sku_pack_binding')
 # Do not silently opt a frozen legacy request into new rules or rehash it.
 if data.get('expected_prompt_bundle_hash')and not existing:return data
 if existing and existing!=current:raise ValueError('sku68_node_or_identity_binding_stale')
 data.pop('sku_pack_binding',None)
 if current:data['sku_pack_binding']=copy.deepcopy(current)
 return data

def fixed_invitation_scope(binding):
 return (binding.get('version')==VERSION and binding.get('product_code')==CODE
  and binding.get('primary_identity')=='6.8' and binding.get('node')=='D4'
  and binding.get('copy_rule')in {'owner_distill_active','owner_distill_conditional'})

def prepare_data(data):
 b=data.get('sku_pack_binding')or{}
 if b.get('version')!=VERSION or not b.get('sop_node'):return data
 data=copy.deepcopy(data);profile=data.get('profile_context')or{};sop=profile.get('newcomer_sop')or{}
 if fixed_invitation_scope(b) and LEGACY_ABSENCE_BLOCKER in(sop.get('blockers')or[]):
  from product_source_readiness import check
  readiness=check(Path(__file__).resolve().parents[1])
  if readiness.get('ready')is True:
   # The invitation makes no absence assertion. Retain the observed usage and
   # every other blocker; only retire its obsolete one-hour absence gate.
   sop['blockers']=[v for v in sop['blockers']if v!=LEGACY_ABSENCE_BLOCKER]
   sop['invitation_source_policy']={'version':FIXED_INVITATION_SOURCE_VERSION,
    'snapshot_id':readiness.get('snapshot_id'),'archive_generated_at':readiness.get('archive_generated_at'),
    'absence_assertion_allowed':False}
 if not sop or sop.get('blockers'):return data
 sop.update(node=b['sop_node'],sku_pack_binding=b)
 if b.get('anchor_at'):
  sop['anchor_at']=b['anchor_at']
  sop['window_end']=(at(b['anchor_at'])+timedelta(days=7)).isoformat()
 profile['newcomer_sop']=sop;data['profile_context']=profile
 return data

def fixed_response(data,bundle,root,request_id):
 b=data.get('sku_pack_binding')or{}
 if b.get('copy_rule')not in {'owner_distill_active','owner_distill_conditional'}:return None
 profile=data.get('profile_context')or{};sop=profile.get('newcomer_sop')or{}
 current_unknown_order=(b.get('version')==VERSION and b.get('product_code')==CODE
  and b.get('primary_identity')=='6.8' and b.get('node')=='D4'
  and b.get('sop_node')=='A2' and b.get('copy_rule')=='owner_distill_conditional'
  and b.get('paid_at') and b.get('cycle_key') and b.get('slot_key')
  and data.get('safe_user_ref'))
 # D4 account-unlinked users have a proven product order but may not have an
 # exact HXC archive account yet. Only the approved conditional copy can use
 # that state; other source blockers still stop generation.
 if (profile.get('status')not in {'exact_archive','exact_archive_and_conversation'}
     and not current_unknown_order)or sop.get('blockers'):
  return {'ok':False,'candidates':[],'warning':'sku68_profile_or_node_not_ready','model_calls':0}
 if data.get('do_not_contact')is True or data.get('contact_policy')in {'blocked','do_not_contact','opt_out','blocked_internal_check'}:
  return {'ok':False,'candidates':[],'warning':'sku68_contact_blocked','model_calls':0}
 name=(data.get('salutation_decision')or{}).get('send_name')
 if not name:return {'ok':False,'candidates':[],'warning':'sku68_salutation_missing','model_calls':0}
 body=DISTILL.replace('{{称呼}}',name)
 from newcomer_copy_contract import approved_entry
 entry=approved_entry(root)if b['copy_rule']=='owner_distill_active'else None
 if b['copy_rule']=='owner_distill_active':
  if not entry:return {'ok':False,'candidates':[],'warning':'sku68_verified_entry_missing','model_calls':0}
  body+='\n\n你可以到黄小璨 AI 小程序里，选一篇与你眼下工作或业务有关的思考，聊完后把原文和讨论复制带走，开始蒸馏老黄。'
 else:
  body+='\n\n我这里还没核对到你的小程序账号。如果你已经开通，直接到黄小璨 AI 里试试；如果入口还没接上，回复我，我帮你核对一下。'
  from newcomer_copy_contract import questionnaire_focus
  if not questionnaire_focus(profile):
   body+='\n\n如果这次公开课问卷还没填，可以从这里补上：'+ACTIVATION_URL+'。填过就不用重复。'
 return {'ok':True,'request_id':request_id,'scene':data.get('scene'),'generated_by':'owner_approved_fixed_template','preview_only':bool(data.get('preview_only')or data.get('generation_mode')=='debug'),
 'candidates':[{'variant':b['copy_rule'],'push_copy':body,'risk_flags':[]}],
 'attachments':[entry]if entry else[],'generation_trace':bundle['trace'],'judgment_card':bundle['common_copy_card'],
 'template_result':{'status':'ready_for_review','template_key':b['copy_rule'],'asset_version':VERSION,'attachments':[entry]if entry else[]},
 'model_calls':0,'automatic_send_allowed':False,'fallback_used':False}
