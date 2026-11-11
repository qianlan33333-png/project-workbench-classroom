"""Bind original fixed copy and writing facts to the selected action only."""
from copy import deepcopy
from datetime import datetime, timedelta
from pathlib import Path
import json, tempfile
import newcomer_copy_contract as newcomer
import owner_service_copy, returning_registered_copy, salutation_policy
import sku68_operations, sku99_operations, sku299_renewal
import hxc_delivery_card


def prepare(user, decision, sources, evidence, inputs, now):
    assets=inputs.get('writing_assets')or{}
    if not assets:
        return {'fixed_output':blocked('original_writing_assets_missing')}
    ref=user['safe_user_ref']
    profile=deepcopy(user.get('generation_profile')or{})
    # Identity is already checked by the original selection. Old conversation
    # inference remains labelled; it never becomes a current confirmed field.
    profile.update(status='exact_archive',safe_user_ref=ref,
                   archive_captured_at=sources['as_of'])
    salutation=profile.get('salutation')or salutation_policy.suggest_salutation(
        wecom=user.get('企微昵称')or user.get('微信昵称')or'',
        hxc=user.get('黄小璨昵称')or user.get('黄小璨 AI 昵称')or'')
    profile['salutation']=salutation
    data={key:deepcopy(decision[key])for key in ('strategy_key','policy_version','scene','skill_key',
          'provisional_purchase','sku_pack_binding','rights_type','evidence_refs')if key in decision}
    data.update(safe_user_ref=ref,strategy_branch=decision.get('branch'),profile_context=profile,
                salutation_decision=salutation,display_name=salutation['send_name'],writing_as_of=now.isoformat())
    class FrozenDateTime(datetime):
        @classmethod
        def now(cls,tz=None):return now.astimezone(tz)if tz else now.replace(tzinfo=None)
    for module in (newcomer,sku68_operations,sku99_operations,sku299_renewal):module.datetime=FrozenDateTime
    with tempfile.TemporaryDirectory(prefix='workbench-writing-')as directory:
        root=Path(directory)
        for path,asset in assets.items():
            target=root/path
            if not target.resolve().is_relative_to(root):raise ValueError('writing_asset_path_invalid')
            target.parent.mkdir(parents=True,exist_ok=True,mode=0o700)
            target.write_text(json.dumps(asset,ensure_ascii=False));target.chmod(0o600)
        owner_service_copy.ROOT=root;returning_registered_copy.ROOT=root
        newcomer.__file__=str(root/'ops/newcomer_copy_contract.py')
        data=newcomer.attach_server_state(data,root,now=now.isoformat(),sources=sources,evidence=evidence)
        binding=data.get('sku_pack_binding')or{}
        if binding.get('version')==sku68_operations.VERSION:data=sku68_operations.prepare_data(data)
        elif binding.get('version')==sku99_operations.VERSION:data=sku99_operations.prepare_data(data)
        data=returning_registered_copy.bind(data,root,sources)
        bundle={'trace':{},'common_copy_card':{}}
        fixed=None
        if binding.get('version')==sku68_operations.VERSION:fixed=sku68_operations.fixed_response(data,bundle,root,ref)
        elif binding.get('version')==sku99_operations.VERSION:fixed=sku99_operations.fixed_response(data,bundle,root,ref)
        elif binding.get('node')in sku299_renewal.NODES:fixed=sku299_renewal.fixed_response(data,bundle,ref)
        fixed=returning_registered_copy.apply_result(data,fixed)
        if fixed is not None:
            candidates=fixed.get('candidates')or[]
            if fixed.get('ok')and len(candidates)==1:
                fixed={'status':'ready_for_review','push_copy':candidates[0]['push_copy'],
                       'attachments':fixed.get('attachments')or(fixed.get('template_result')or{}).get('attachments')or[]}
            else:fixed={'status':'blocked','blockers':[fixed.get('warning')or'original_fixed_template_blocked']}
        elif data.get('scene')=='new_user_onboarding'and binding.get('primary_identity')!='299':
            reference=inputs.get('writing_reference')
            asset=assets.get('prompts/runtime/'+str(reference))
            if asset is None:return {'fixed_output':blocked('original_newcomer_reference_missing')}
            fixed=newcomer.fixed_candidate(data,asset)
        result={'profile_context':data['profile_context'],'salutation':salutation,'attachments':[],
                'action_expires_at':action_deadline(binding)}
        if fixed is not None:
            if fixed.get('status')!='ready_for_review':
                result['fixed_output']=blocked(';'.join(fixed.get('blockers')or['original_fixed_template_blocked']))
            else:
                result['fixed_output']={'status':'draft','text':fixed['push_copy'],'blocked_reason':'',
                                        'evidence_refs':decision.get('evidence_refs')or[user['source_snapshot_id']]}
                result['attachments']=fixed.get('attachments')or[]
        elif isinstance(data.get('entry'),dict):result['attachments']=[data['entry']]
        material=decision.get('material')or{}
        if material.get('kind')not in (None,'','followup'):
            # A full source body can be used for drafting before CRM material
            # preparation. Missing delivery binding must never discard that
            # body or silently turn a content action into a text-only send.
            if material.get('body_status')!='ready' or not material.get('content_body'):
                result['fixed_output']=blocked('selected_material_body_required')
            try:
                card=hxc_delivery_card.descriptor({'scene':decision.get('scene'),
                    'content_material':material,'review_material':material})
                if not card:raise ValueError('selected_material_descriptor_missing')
                # The existing page reads using the customer's own login.
                # This is an owned source route, not a dispatch token or a
                # grant of access. CRM prepares its local material separately.
                card.update(status='source_readonly_route',verified=False)
                result['attachments']=[card]
            except ValueError:
                result['delivery_blocked']='selected_material_delivery_binding_required'
        return result


def blocked(reason):
    return {'status':'blocked','text':'','blocked_reason':reason,'evidence_refs':[]}


def action_deadline(binding):
    """Carry the original node's upper bound to the delayed CRM submission.

    Boundaries: sku68_operations.earliest_delivery, sku99_ledger admission,
    sku299_operations.before_material. Rights expiry alone cannot retain D2
    after D4 begins. A late D7 completion without an explicit window is held.
    """
    values=[]
    for key in ('window_end','entitlement_end'):
        if binding.get(key):values.append(datetime.fromisoformat(binding[key].replace('Z','+00:00')))
    identity=binding.get('primary_identity');node=binding.get('node')
    if identity in ('6.8','99','299')and node in ('D2','D4','D6','D7'):
        anchor=binding.get('anchor_at')or binding.get('paid_at')
        hours=({'D2':96,'D4':168,'D7':192}if identity=='299'else{'D2':96,'D4':144,'D6':168}).get(node)
        if not anchor or hours is None:return None
        values.append(datetime.fromisoformat(anchor.replace('Z','+00:00'))+timedelta(hours=hours))
    if any(v.tzinfo is None for v in values):raise ValueError('original_deadline_timezone_missing')
    return min(values).isoformat()if values else None
