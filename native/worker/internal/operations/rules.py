#!/usr/bin/env python3
"""Private adapter for the original pure rules. No network or database writes."""
import json
import sys
from datetime import datetime
from pathlib import Path
from zoneinfo import ZoneInfo
sys.path.insert(0, str(Path(__file__).with_name('original')))
sys.path.insert(0, str(Path(__file__).parent))
from hxc_original import decide as decide_hxc
from decision_contract import _action_id
from user_operating_archive import build_user_operating_archive
from agent_user_scheduler import choose_actions, potential_cooldown
from hxc_lifecycle_strategy import evaluate
from v3_strategy_drafts import audience_filter_result, derive_service_relationship_tier
from hxc_communication_context import blocks, handoff
from audience_projection import project as project_audience


def clean(records, as_of):
    out = []
    for record in records:
        raw = record['fields'].get('raw', record['fields'])
        if not isinstance(raw, dict) or raw.get('safe_user_ref') != record['id']:
            raise ValueError('source_identity_mismatch')
        normalized = dict(raw)
        normalized['generated_at'] = as_of
        out.append({'id': record['id'], 'fields': {'raw': raw, 'archive': build_user_operating_archive(normalized)}})
    return out


def select(records, releases, histories, as_of, strategy_keys=()):
    now = datetime.fromisoformat(as_of.replace('Z', '+00:00'))
    if now.tzinfo is None:
        raise ValueError('timezone_required')
    import agent_user_scheduler
    agent_user_scheduler.potential_cooldown = lambda item: potential_cooldown(item, now)
    history = {r['id']: r['fields'] for r in histories}
    policies = [r['fields'].get('release', r['fields']) for r in releases if r['id'] not in ('owner-service-policy','original-operation-inputs') and not r['id'].startswith(('original-catalog-','original-writing-'))]
    operation_inputs = dict(next((r['fields'] for r in releases if r['id'] == 'original-operation-inputs'), {}))
    operation_inputs['writing_assets']={r['fields']['path']:r['fields']['asset']for r in releases if r['id'].startswith('original-writing-')}
    chunks=[r['fields'] for r in releases if r['id'].startswith('original-catalog-')]
    if chunks:
        shared=dict(operation_inputs.get('shared_sources')or{})
        if any(c.get('source_snapshot_id')!=shared.get('source_snapshot_id') or not isinstance(c.get('catalog_entries'),list) for c in chunks):
            raise ValueError('shared_catalog_snapshot_mismatch')
        items=[item for chunk in chunks for item in chunk['catalog_entries']]
        shared['catalog']={str(x.get('content_id')):x for x in items}
        shared['catalog_by_key']={str(x.get('content_type'))+':'+str(x.get('content_id')):x for x in items}
        operation_inputs['shared_sources']=shared
    owner_policy = next((r['fields'].get('policy') for r in releases if r['id'] == 'owner-service-policy'), None)
    import sku299_owner_service
    sku299_owner_service._POLICY = owner_policy
    if not policies or any(not isinstance(p, dict) or not p.get('key') for p in policies):
        raise ValueError('published_strategies_required')
    requested = set(strategy_keys)
    if requested - {p['key'] for p in policies}:
        raise ValueError('unknown_strategy_key')
    results = []
    for record in records:
        user = dict(record['fields'].get('raw', record['fields']))
        if user.get('safe_user_ref') != record['id']:
            raise ValueError('source_identity_mismatch')
        past = history.get(record['id'])
        profile=user.get('generation_profile')
        if profile is not None and (not isinstance(profile,dict) or profile.get('safe_user_ref')!=record['id'] or profile.get('customer_id')!=user.get('customer_id')):
            raise ValueError('generation_profile_identity_mismatch')
        fields = {'selected': False, 'reason': '', 'priority': 0, 'actions': [], 'exclusions': [], 'available_strategies': [], 'profile': dict(profile) if profile is not None else {k:v for k,v in user.items() if k not in ('hxc_context','hxc_evidence')}}
        result = {'id': record['id'], 'fields': fields}
        results.append(result)
        if past and past.get('local_hold_reason'):
            fields['reason']=past['local_hold_reason'];continue
        if not past or past.get('complete') is not True:
            fields['reason'] = 'existing_crm_service_unknown' if past and past.get('history_problem') == 'existing_crm_service_unknown' else 'contact_history_incomplete'
            continue
        if not isinstance(past.get('actions'), list):
            fields['reason'] = 'execution_history_incomplete'
            continue
        if user.get('operational_excluded') is True:
            fields['reason'] = 'operational_excluded'
            continue
        for key in ('last_user_reply_at', 'last_outreach_at', 'last_light_followup_at', 'new_evidence_at', 'callback_due_at'):
            if key in past:
                incoming=past[key]
                if key!='callback_due_at' and user.get(key):
                    try:
                        if datetime.fromisoformat(str(user[key]).replace('Z','+00:00'))>datetime.fromisoformat(str(incoming).replace('Z','+00:00')):continue
                    except (ValueError,TypeError):pass
                user[key] = incoming
                fields['profile'][key] = incoming
        safety = (user.get('safety') or {}, (record['fields'].get('archive') or {}).get('safety') or {}, user.get('allocation_safety') or {})
        blocked = user.get('do_not_contact') is True or user.get('contact_preference') == 'do_not_contact' or any(s.get('do_not_contact') is True or s.get('is_do_not_contact') is True or s.get('contact_preference') == 'do_not_contact' or s.get('contact_policy') in ('blocked', 'do_not_contact', 'opt_out') for s in safety)
        risk = user.get('high_risk') is True or any(s.get('high_risk') is True for s in safety)
        held = past.get('manual_handoff') is True or user.get('manual_handoff') is True or any(s.get('manual_handoff') is True for s in safety)
        if user.get('identity_resolved') is not True or user.get('identity_conflict'):
            fields['reason'] = 'identity_unresolved'
            continue
        if blocked or risk:
            fields['reason'] = 'contact_prohibited' if blocked else 'high_risk'
            continue
        if not isinstance(user.get('priority_todos', []),list):
            fields['reason']='priority_todo_source_incomplete'
            continue
        if any(not isinstance(todo,dict) or (todo.get('safe_user_ref') or agent_user_scheduler.safe_ref(todo.get('canonical_user_id'))) != record['id'] for todo in user.get('priority_todos', [])):
            fields['reason']='priority_todo_identity_mismatch'
            continue
        user['service_relationship_tier'] = derive_service_relationship_tier(user)
        fields['profile']['service_relationship_tier']=user['service_relationship_tier']
        holds=past.get('handoffs',[])
        if not isinstance(holds,list) or any(not isinstance(h,dict) for h in holds):
            fields['reason']='contact_history_incomplete'
            continue
        holds=[h for h in holds if h.get('workbench_user_ref')==record['id'] and h.get('status')=='manual_handoff']
        context = past.get('handoff_context') or user.get('handoff_context')
        if context and not any(handoff(context)==handoff(h.get('handoff_context')) for h in holds):
            context=None  # An AI-proposed scope cannot narrow a real global hold.
        # The original lifecycle predates scoped handoffs. Remove only an
        # explicitly unrelated product hold from its input; contact prohibitions
        # and risks remain intact. Unknown/matter/global holds stay blocking.
        admitted_user = dict(user)
        if held and not blocks(context, product_key='hxc', matter_ref=''):
            admitted_user['manual_handoff'] = False
            admitted_user['allocation_safety'] = {**(user.get('allocation_safety') or {}), 'manual_handoff': False}
        lifecycle = {'eligible': False, 'reason': 'owner_service_policy_missing'}
        if any(p['key'].startswith('hxc:') for p in policies) and owner_policy is not None:
            lifecycle = decide_hxc(admitted_user, past, policies, operation_inputs, now)
        handoffs = [agent_user_scheduler.priority_todo_candidate(todo, record['id'])
                    for todo in user.get('priority_todos', []) if isinstance(todo, dict) and todo.get('type') == 'handoff']
        handoffs = [todo for todo in handoffs if todo]
        for todo in handoffs:
            known=[h['handoff_context'] for h in holds if h.get('product_service_key')==agent_user_scheduler._service_for(todo)]
            if known:todo['handoff_context']=known[0] if all(h==known[0] for h in known) else {'handoff_scope':'global'}
            else:todo.pop('handoff_context',None)
        proposals, explicit = list(handoffs), []
        for policy in policies:
            key = policy['key']
            reason = 'strategy_not_requested' if requested and key not in requested else ''
            if not reason and policy.get('status') != 'published':
                reason = 'strategy_not_published'
            expiry = policy.get('expires_at') or (policy.get('operating_config') or {}).get('expires_at')
            # The two original manual work queues are evergreen, source-backed
            # todos. Only campaigns and explicit one-off manual work need expiry.
            manual_queue = key in ('manual:intent_sales', 'manual:potential_followup')
            if not reason and (key.startswith('theme:') or key.startswith('manual:') and not manual_queue) and not expiry:
                reason = 'strategy_validity_unverified'
            if not reason and policy.get('starts_at'):
                start=datetime.fromisoformat(policy['starts_at'].replace('Z','+00:00'))
                if start.tzinfo is None:reason='strategy_start_timezone_missing'
                elif start>now:reason='strategy_not_started'
            if not reason and expiry:
                end = datetime.fromisoformat(expiry.replace('Z', '+00:00'))
                if end.tzinfo is None:
                    reason = 'strategy_expiry_timezone_missing'
                elif end <= now:
                    reason = 'strategy_expired'
            if not reason and not manual_queue and not key.startswith('hxc:'):
                audience_user=admitted_user
                if 'audience_source'in user:
                    if user['audience_source'].get('safe_user_ref')!=record['id']:raise ValueError('original_audience_identity_mismatch')
                    audience_user=project_audience(user['audience_source'],policy.get('audience_filter')or{})
                reason = audience_filter_result(audience_user, policy.get('audience_filter') or {}, require_segment=key.startswith(('theme:', 'manual:')), allow_identity_issues=key == 'tier:needs_verification')
            candidates = []
            if not reason and key.startswith('hxc:'):
                if lifecycle.get('strategy_key') != key or not lifecycle.get('eligible'):
                    reason = lifecycle.get('reason') or 'lifecycle_not_matched'
                elif not lifecycle.get('outbound_candidate'):
                    reason = lifecycle.get('preparation_reason') or 'internal_action_required'
                else:
                    candidates = [{**lifecycle, 'execution_action': lifecycle.get('action'), 'action': 'hxc_lifecycle'}]
            elif not reason and manual_queue:
                if user.get('priority_todos_complete') is not True or not isinstance(user.get('priority_todos'), list):
                    reason = 'priority_todo_source_incomplete'
                else:
                    todos = [agent_user_scheduler.priority_todo_candidate(todo, record['id'], policy)
                             for todo in user['priority_todos'] if isinstance(todo, dict)]
                    todos = [todo for todo in todos if todo and todo.get('strategy_key') == key]
                    # Reuse the current product-scope filter. In particular, the
                    # retired mechanical HXC potential rotation stays retired.
                    from product_service_scope import filter_queue_candidates
                    todos = filter_queue_candidates(todos)
                    candidates = todos
                    if not candidates:
                        reason = 'current_priority_todo_required'
            elif not reason:
                if key.startswith('tier:') or key in ('content_push', 'weekly_review', 'new_user_onboarding', 'subscription_activation'):
                    reason = 'legacy_service_strategy_retired_use_current_product_pack'
                audience = policy.get('audience_filter') or {}
                if not reason and audience.get('population') == 'service_packs':
                    packs = set(user.get('service_packs') or []) | {user.get('strategy_source_pack')}
                    if not packs.intersection(audience.get('source_packs') or [key]):
                        reason = 'service_pack_source_missing'
                if not reason:
                    fields['available_strategies'].append(key)
                    # Tier/theme have original audience and generation entries,
                    # but no original automatic queue priority. Do not revive
                    # retired service-pack scheduling or fabricate a priority.
                    if key not in requested:
                        reason = 'explicit_strategy_selection_required'
                    else:
                        generation = policy.get('generation_config') or {}
                        material = next((m for m in user.get('materials', []) if m.get('content_id') == generation.get('material_ref') and m.get('value_hook') and m.get('snapshot_id')), None)
                        if key.startswith('tier:') and generation.get('skill_key') in ('content_service', 'activation_recall') and not material:
                            reason = 'current_verified_material_required_for_tier_copy'
                        else:
                            candidates = [{'strategy_key': key, 'product_service_key': 'hxc', 'action': key, 'instruction': (policy.get('fields') or {}).get('primary_action'), 'material': material}]
            if reason:
                fields['exclusions'].append({'strategy': key, 'reason': reason})
                continue
            for candidate in candidates:
                service=candidate.get('product_service_key') or 'hxc'
                matter=candidate.get('matter_ref','')
                if held and blocks(context, product_key=service, matter_ref=matter) or any(blocks(h.get('handoff_context'), product_key=service, matter_ref=matter) for h in handoffs) or any(blocks(h.get('handoff_context'),product_key=service,matter_ref=matter) for h in holds):
                    fields['exclusions'].append({'strategy': key, 'reason': 'manual_handoff'})
                    continue
                week = now.astimezone(ZoneInfo('Asia/Shanghai')).strftime('%G-W%V')
                existing = [a for a in past['actions'] if a.get('week') == week and a.get('product_key') == service and not (a.get('status') == 'blocked' and a.get('reason') in ('candidate_no_longer_present', 'service_fact_review_replan_required'))]
                if existing:
                    fields['exclusions'].append({'strategy': key, 'reason': 'original_action_preserved', 'action_ids': [a.get('action_id') for a in existing]})
                    continue
                candidate.update({k: user[k] for k in ('last_user_reply_at', 'last_outreach_at', 'last_light_followup_at', 'new_evidence_at', 'callback_due_at') if user.get(k)})
                if candidate['action']=='potential_review':
                    cooldown=potential_cooldown(candidate,now)
                    if not cooldown['eligible'] or cooldown['state']=='history_unknown_human_review':
                        fields['exclusions'].append({'strategy':key,'reason':cooldown['state'],'product_key':service})
                        continue
                candidate.update(safe_user_ref=record['id'], strategy_key=key, strategy_version=policy.get('version', 0), generation=policy.get('generation_config') or {})
                candidate.setdefault('source','original_strategy_rules')
                (explicit if key in requested and not key.startswith('hxc:') and not manual_queue else proposals).append(candidate)
        actions = choose_actions(proposals)
        def candidate_key(action):return tuple(action.get(k) for k in ('strategy_key','product_service_key','action','source'))
        by_key={}
        for candidate in proposals:by_key.setdefault(candidate_key(candidate),candidate)
        actions = [{**by_key.get(candidate_key(action), {}), **action} for action in actions]
        for action in actions:
            action['generation'] = {**action.get('generation', {}), **{k: action[k] for k in ('scene','skill_key','material','sku_pack_binding','material_requirement','execution_action','operation_pack_scope','decision_fingerprint') if k in action}}
            action['generation']['original_aliases']=(past.get('contact_history_scope')or{}).get('original_aliases')or[record['id']]
        internal=[a for a in actions if a['action']=='manual_handoff']
        actions=[a for a in actions if a['action']!='manual_handoff']
        if internal:fields['internal_actions']=internal
        if explicit:
            if len(explicit) == 1 and not actions:
                actions = explicit
            else:
                fields['exclusions'].append({'reason': 'strategy_coordination_required'})
                actions = []
        fields['actions'] = actions
        if actions:
            fields['crmSlot']=(actions[0].get('sku_pack_binding')or{}).get('slot_key') or _action_id(now.astimezone(ZoneInfo('Asia/Shanghai')).strftime('%G-W%V'),record['id'],actions[0].get('product_service_key')or'hxc')
        fields['selected'] = len(actions) == 1
        if len(actions)==1 and actions[0].get('writing'):
            writing=actions[0]['writing']
            if 'fixed_output'in writing:fields['fixedOutput']=writing['fixed_output']
            fields['attachments']=writing.get('attachments')or[]
            if writing.get('action_expires_at'):fields['actionExpiresAt']=writing['action_expires_at']
            if writing.get('delivery_blocked'):fields['deliveryBlocked']=writing['delivery_blocked']
            fields['profile'].update(writing.get('profile_context')or{})
            actions[0]['generation']['original_fact_reviews']=fields['profile'].get('original_fact_reviews')
            actions[0]['generation']['hxc_service_binding']=fields['profile'].get('hxc_service_binding')
            actions[0]['generation']['hxc_rights_fingerprint']=fields['profile'].get('hxc_rights_fingerprint')
        fields['priority'] = max((x.get('priority', 0) for x in actions), default=0)
        fields['reason'] = '' if fields['selected'] else ('strategy_coordination_required' if len(actions)>1 else 'manual_coordination' if internal else 'no_applicable_strategy')
    return results


def main():
    try:
        request = json.load(sys.stdin)
        if request['action'] == 'data_refresh':
            records = clean(request['records'], request['asOf'])
        elif request['action'] == 'strategy_select':
            records = select(request['records'], request['strategies'], request['histories'], request['asOf'], request.get('strategyKeys') or ())
        else:
            raise ValueError('unsupported_operation')
        json.dump({'records': records}, sys.stdout, ensure_ascii=False, allow_nan=False)
    except Exception:
        print('{"error":"original_rule_input_rejected"}')
        return 1
    return 0

if __name__ == '__main__':
    raise SystemExit(main())
