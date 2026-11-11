"""Selected original rules; source I/O is supplied by the host as frozen inputs."""
import sku68_operations as old
import sku99_operations as co
import sku299_operations as formal

def handler(data):
 version=(data.get('sku_pack_binding')or{}).get('version')
 # Same closed set as the original plugins.products.product_for registry.
 # Its unbound compatibility route is 6.8; unknown non-empty bindings reject.
 return {formal.VERSION:formal,co.VERSION:co,old.VERSION:old}[version or old.VERSION]

def _before_material_legacy(user,evidence,sources,result,**kw):
 private=(sources.get('users')or{}).get(user.get('safe_user_ref'))or user
 if co.purchase(private,kw['now']):return co.before_material(user,evidence,sources,result,**kw)
 if old.purchase(private,kw['now']):return old.before_material(user,evidence,sources,result,**kw)
 # The broad legacy expired/recovery cohort has not been approved as a
 # product operation package. Monitoring remains available; no new copy.
 if result.get('strategy_key')=='hxc:recovery' or result.get('branch')=='expired':
  return dict(result,outbound_candidate=False,preparation_reason='unlaunched_recovery_package_paused_by_owner')
 return result

def _after_material_legacy(result):
 # Unbound legacy onboarding is not an approved product operation pack.
 if result.get('strategy_key')=='hxc:onboarding' and not (result.get('sku_pack_binding') or {}).get('primary_identity'):
  return dict(result,outbound_candidate=False,preparation_reason='unbound_legacy_onboarding_paused_pending_identity')
 # Recall has not yet been approved as an operational package. Keep its
 # evidence for review, but do not create a new automatic copy candidate.
 if (result.get('material')or{}).get('kind')=='recall' or result.get('branch')=='recall_ready':
  return dict(result,outbound_candidate=False,preparation_reason='recall_package_paused_by_owner')
 return handler(result).after_material(result)

def before_material(user,evidence,sources,result,**kw):
 from product_service_scope import enforce
 from sku299_renewal import standalone
 confirmation=standalone(user,evidence,sources,result,kw['now'])
 if confirmation:return enforce(confirmation,user,evidence,kw['now'])
 if(evidence.get('operational_service_admission')or{}).get('status')=='active':
  result=formal.before_material(user,evidence,sources,result,**kw)
 else:result=_before_material_legacy(user,evidence,sources,result,**kw)
 return enforce(result,user,evidence,kw['now'])

def after_material(result):
 from product_service_scope import final_guard
 return final_guard(_after_material_legacy(result))
