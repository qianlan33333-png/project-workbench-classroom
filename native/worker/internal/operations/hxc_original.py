"""Adapt the original final admission chain to frozen read-only source inputs."""
from copy import deepcopy
from datetime import datetime
from pathlib import Path
from zoneinfo import ZoneInfo
import hashlib,json,tempfile
import hxc_lifecycle_strategy as lifecycle
import hxc_routine_policy as routine
import hxc_service_reviews as reviews
import product_operation_packs as packs
import product_service_scope
import sku68_operations,sku99_operations,sku299_operations,sku299_renewal
import sku99_ledger,sku299_service_state
from v3_strategy_drafts import normalize_operating_config


def decide(user, past, policies, inputs, now):
    now=now.astimezone(ZoneInfo('Asia/Shanghai'))
    context=user.get('hxc_context') or past.get('hxc_context') or {}
    if past.get('identity_lineage_requires_review')is True:
        return {'eligible':False,'reason':'merged_identity_execution_requires_verification'}
    # Complete means sources, service reviews, existing copies/slots, source
    # binding and material readiness have all been read, including empty lists.
    if (context.get('complete') is not True or past.get('hxc_ledger_complete') is not True
        or not user.get('source_snapshot_id') or context.get('snapshot_id') != user['source_snapshot_id']):
        return {'eligible':False,'reason':'hxc_rule_context_incomplete'}
    if any(not isinstance(context.get(k),dict) for k in ('sources','fact_reviews','evidence','profile','editorial_assignments')) or any(not isinstance(past.get(k),dict) for k in ('copy_index','renewal_slots','previous_cycle_ends')) or not isinstance(past.get('legacy_successful_cycles'),list):
        return {'eligible':False,'reason':'hxc_rule_context_incomplete'}
    required=('product_service_scope_policy','sku99_assets','sku299_assets','renewal_policy')
    if any(not isinstance(inputs.get(k),dict) for k in required):
        return {'eligible':False,'reason':'original_product_policy_missing'}
    sources=deepcopy(context['sources'])
    if context.get('private_from_record') is True:
        if sources.get('users')!={}:return {'eligible':False,'reason':'hxc_rule_context_incomplete'}
        sources['users']={user['safe_user_ref']:{k:v for k,v in user.items() if k not in ('hxc_context','generation_profile')}}
    # The catalog belongs to the same frozen source bundle. Keep it once in the
    # already pinned original-operation-inputs record, not in every customer row.
    if context.get('catalog_from_shared') is True:
        shared=inputs.get('shared_sources') or {}
        if shared.get('source_snapshot_id')!=user['source_snapshot_id'] or not isinstance(shared.get('catalog'),dict):
            return {'eligible':False,'reason':'hxc_shared_source_mismatch'}
        sources['catalog']=shared['catalog']
    indexes=('users','accounts','metrics','histories','packs','seen','reviews','sends','catalog','product_grants_by_user','current_entitlements_by_user','user_message_times','growth_reports','correspondence','content_history_by_ref','completion_requests','entitlement_events_by_user')
    if sources.get('complete') is not True or any(not isinstance(sources.get(key),dict) for key in indexes) or not isinstance(sources.get('entitlement_events'),list):
        return {'eligible':False,'reason':'hxc_rule_context_incomplete'}
    if any(not isinstance(rows,list) for index in ('copy_index','renewal_slots','previous_cycle_ends') for rows in past[index].values()):
        return {'eligible':False,'reason':'hxc_rule_context_incomplete'}
    ref=user['safe_user_ref']
    if set(sources['users'])!={ref} or not sources.get('as_of'):raise ValueError('original_source_identity_missing')
    source_at=routine.stamp(sources['as_of'])
    if not source_at or source_at>now:raise ValueError('source_time_invalid')
    for uid,values in sources['histories'].items():sources['histories'][uid]=[routine.stamp(v) for v in values if routine.stamp(v)]
    sources['content_history_by_ref']={k:{tuple(x) for x in rows}for k,rows in sources.get('content_history_by_ref',{}).items()}
    catalog_rows=(inputs.get('shared_sources')or{}).get('catalog_by_key') if context.get('catalog_from_shared')is True else None
    sources['catalog_by_key']={(str(x.get('content_type')or''),str(x.get('content_id')or'')):x for x in (catalog_rows or sources['catalog']).values()}
    sources['seen']={k:set(v)for k,v in sources['seen'].items()}
    sources['sku299_renewal_slots']=past.get('renewal_slots') or {}
    sources['sku299_previous_cycle_ends']=past.get('previous_cycle_ends') or {}
    product_service_scope.policy=lambda:inputs['product_service_scope_policy']
    sku99_operations.assets=lambda:inputs['sku99_assets']
    sku299_operations.assets=lambda:inputs['sku299_assets']
    sku299_renewal.policy=lambda:inputs['renewal_policy']
    sku68_operations.legacy_v3_successful_cycles=lambda:{tuple(v)for v in past.get('legacy_successful_cycles',[])}
    # Original review expiry uses the wall clock. Bind it to this task's frozen
    # evaluation time without changing any review or source record.
    class FrozenDateTime(datetime):
        @classmethod
        def now(cls,tz=None):return now.astimezone(tz) if tz else now.replace(tzinfo=None)
    reviews.datetime=FrozenDateTime
    facts=context.get('fact_reviews') or {}
    reviewed=reviews.effective(facts,ref,sources)
    user,evidence=routine.enrich(user,context.get('evidence') or {},sources,reviewed,now=now)
    configs={p['key']:normalize_operating_config(p.get('operating_config') or {})for p in policies if p['key'].startswith('hxc:')}
    thresholds=dict(lifecycle.DEFAULT_THRESHOLDS)
    for key,strategy in {'paid_expiry_days':'hxc:expiry','gift_expiry_days':'hxc:expiry','trial_expiry_days':'hxc:expiry','low_use_days':'hxc:ongoing','long_silent_days':'hxc:recovery','recovery_cooldown_days':'hxc:recovery'}.items():
        thresholds[key]=(configs.get(strategy,{}).get('lifecycle_thresholds') or {}).get(key,thresholds[key])
    evaluate=lambda u,e,today:lifecycle.evaluate(u,e,today=today,thresholds=thresholds)
    result=evaluate(user,evidence,now)
    result=packs.before_material(user,evidence,sources,result,now=now,evaluate=evaluate)
    if any(v.get('decision')=='defer'for v in facts.get(ref,{}).values()):result={'eligible':False,'reason':'manual_service_fact_deferred'}
    if reviews.conflicted(facts,ref,sources):result={'eligible':False,'reason':'manual_fact_source_changed'}
    if result.get('eligible') and result.get('strategy_key')=='hxc:ongoing' and result.get('branch')in {'normal_service','low_use','usage_unknown'}:
        # Reuse its fixed-file editorial input in a disposable private root.
        with tempfile.TemporaryDirectory(prefix='workbench-material-') as root:
            editorial=Path(root)/'var/private-data/sku299-owner-editorial-20261021.json'
            editorial.parent.mkdir(parents=True,mode=0o700)
            editorial.write_text(json.dumps(context['editorial_assignments'],ensure_ascii=False));editorial.chmod(0o600)
            original_root=sku299_operations.ROOT;sku299_operations.ROOT=Path(root)
            try:
                material=sku299_operations.choose_material(evidence,sources,week=now.strftime('%G-W%V'),now=now,profile=context.get('profile') or {},binding=result.get('sku_pack_binding') or {})
            finally:sku299_operations.ROOT=original_root
        if material and material.get('held'):result=dict(result,eligible=False,outbound_candidate=False,reason=material['held'],preparation_reason=material['held'])
        elif material:
            branch={'review':'weekly_review_ready','content':'content_ready','recall':'recall_ready','followup':'normal_service'}[material['kind']]
            action,scene,skill=lifecycle.BRANCHES[branch]
            result.update(branch=branch,action=action,scene=scene,skill_key=skill,material=material,outbound_candidate=True,why_now='本周服务：'+lifecycle.BRANCH_LABELS[branch],evidence_refs=[material['source_ref'],context['snapshot_id']])
    result=packs.after_material(result)
    index=past.get('copy_index') or {}
    result=sku68_history_guard(result,index,now)
    result=sku99_ledger.decision_guard(result,index,now)
    result=sku299_service_state.decision_guard(result,index,now)
    if result.get('eligible'):
        result['service_fact_reviews']=reviewed
        route=lifecycle.branch_route(result['branch'],None if result.get('material') else configs.get(result['strategy_key'],{}).get('branch_mapping'))
        result.update(route)
        if result.get('existing_copy',{}).get('reuse_required'):
            result.update(outbound_candidate=False,preparation_reason='original_copy_reuse_required')
        if result.get('outbound_candidate'):
            from writing import prepare
            result['writing']=prepare(user,result,sources,evidence,inputs,now)
    return result


def sku68_history_guard(result,index,now):
    """Read-only admission from original sku68 reserve_slots, without writes.

    The original DB reservation is intentionally not migrated. Its already
    verified slot/receipt facts are the same copy_index supplied to 99/299.
    """
    from datetime import timedelta
    from sku68_operations import VERSION, at
    b=result.get('sku_pack_binding')or{}
    if b.get('version')!=VERSION or b.get('primary_identity')!='6.8'or not result.get('outbound_candidate'):
        return result
    def hold(reason):return dict(result,outbound_candidate=False,preparation_reason=reason)
    prior=[]
    for row in index.get(result.get('safe_user_ref'))or[]:
        old=row.get('binding')or{}
        if old.get('cycle_key')!=b.get('cycle_key'):continue
        slot=old.get('slot_key')or row.get('slot_key')
        if slot==b.get('slot_key'):
            return dict(result,existing_copy={'run_id':row['run_id'],'item_key':row['item_key'],'reuse_required':True})
        if row.get('state')=='draft':continue
        if row.get('handoff')and row.get('state')not in {'sent','cancelled'}:
            return hold('sku68_previous_delivery_outcome_pending')
        if row.get('state')=='sent':
            sent=at(row.get('sent_at'))
            if sent is None:return hold('sku68_previous_delivery_time_unverified')
            if now-sent<timedelta(hours=24):return hold('actual_send_cooldown_24h')
        prior.append(old.get('node')or row.get('node'))
    first={'D2','D4','D6'};second={'W2_WEEKLY','W2_CONTENT','EXPIRY'}
    if b.get('node')in first and sum(x in first for x in prior)>=3:return hold('sku68_first_week_budget_exhausted')
    if b.get('node')in second and sum(x in second for x in prior)>=2:return hold('sku68_second_week_budget_exhausted')
    return result
