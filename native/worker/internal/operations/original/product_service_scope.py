"""Selected original rules; source I/O is supplied by the host as frozen inputs."""


def reject(result,reason,next_package=None):
 out=dict(result,eligible=False,outbound_candidate=False,reason=reason,preparation_reason=reason)
 out['operation_pack_scope']={'version':policy()['version'],'pack':None,'status':'outside_service','next_package':next_package,'automatic_generation_allowed':False}
 return out

def enforce(result,user,evidence,now):
 if not result.get('eligible'):return result
 from sku299_renewal import is_expiry_confirmation
 if is_expiry_confirmation(result):return dict(result,operation_pack_scope={'version':policy()['version'],'pack':'299','status':'expiry_confirmation_only','automatic_send_allowed':False,'in_service':False})
 b=result.get('sku_pack_binding')or{}
 # Admission comes from the original enriched evidence, never a temporary
 # re-evaluation with the low-price order deliberately removed.
 a=(evidence or{}).get('operational_service_admission')or{}
 identity=b.get('primary_identity');pack=None
 # Trust the current SKU adapter's primary identity, never raw price/history.
 if identity in {'6.8','99'}:pack=identity
 elif identity=='299' or a.get('status')=='active':pack='299'
 elif identity=='existing_hxc' and b.get('product_code') in {'31314141','334465678'}:
  pack='99' if b['product_code']=='334465678' else '6.8'
 elif not identity:
  from sku68_operations import primary_299
  if primary_299(user,evidence or{},now):pack='299'
 if result.get('entitlement_state')=='expired' or result.get('branch')=='expired' or (a.get('status')=='expired' and pack not in {'6.8','99'}):
  return reject(result,'expired_outside_active_product_packs','expired_recovery_separate')
 if pack is None:return reject(result,'legacy_generic_strategy_retired_use_current_product_pack')
 # An approved product-specific repair is part of SKU99, not the retired
 # unbound no-rights/recall audience. Keep its immutable binding and ledger.
 approved_repair=(pack=='99' and b.get('version')=='sku.334465678.rules.v1.0.0'
                  and b.get('product_code')=='334465678' and b.get('primary_identity')=='99'
                  and ((b.get('node')=='REPAIR_ONCE' and b.get('copy_rule')in {'repair_unknown','repair_confirmed'})
                       or (b.get('node')in {'D2','D4','D6'} and b.get('copy_rule')in {'not_activated','conditional_entry_check'})))
 if result.get('strategy_key')in {'hxc:first_profile','hxc:known_no_rights','hxc:recovery'} and not(approved_repair and result.get('strategy_key')=='hxc:known_no_rights'):
  return reject(result,'legacy_generic_strategy_retired_use_current_product_pack')
 return dict(result,operation_pack_scope={'version':policy()['version'],'pack':pack,'product_code':policy()['active_service_packs'][pack],'status':'in_service','automatic_send_allowed':False})

def final_guard(result):
 if not result.get('eligible'):return result
 from sku299_renewal import is_expiry_confirmation
 if is_expiry_confirmation(result)and(result.get('operation_pack_scope')or{}).get('status')=='expiry_confirmation_only':return result
 scope=result.get('operation_pack_scope')or{}
 if scope.get('pack')not in policy()['active_service_packs']or scope.get('status')!='in_service':
  return reject(result,'legacy_generic_strategy_retired_use_current_product_pack')
 if result.get('entitlement_state')=='expired'or result.get('strategy_key')=='hxc:recovery'or result.get('branch')=='expired':
  return reject(result,'expired_outside_active_product_packs','expired_recovery_separate')
 return result

def filter_queue_candidates(candidates):
 """Current HXC service uses product-pack decisions; keep real human/reply work.

 Old mechanical potential/weekly/content rotation must not select expired
 people or displace a paid service. Other service lines retain their queues.
 """
 return [x for x in candidates if x.get('product_service_key')!='hxc'
         or x.get('action')in {'hxc_lifecycle','manual_handoff','intent_followup'}]
