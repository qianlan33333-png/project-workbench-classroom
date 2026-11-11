"""Selected original pure rules; I/O and mutation entry points are intentionally excluded."""
from __future__ import annotations
from order_time_contract import service_time


import hashlib,json


from pathlib import Path


from datetime import datetime,timedelta


from zoneinfo import ZoneInfo


TZ=ZoneInfo('Asia/Shanghai');_POLICY=None


TZ=ZoneInfo('Asia/Shanghai');_POLICY=None


def load_policy(root=None):
 root=Path(root)if root is not None else Path(__file__).resolve().parents[1]
 public=json.loads((root/'ops/sku299_owner_service_policy.json').read_text())
 private_config=public['private_overrides'];path=root/private_config['path']
 if not path.resolve().is_relative_to((root/'var/private-data').resolve()):raise ValueError('private_policy_path_escape')
 wire=path.read_bytes()
 if hashlib.sha256(wire).hexdigest()!=private_config['sha256']:raise ValueError('private_policy_version_mismatch')
 private=json.loads(wire)
 if private.get('schema')!=private_config['schema'] or not isinstance(private.get('expired_overrides'),dict) or not isinstance(private.get('confirmed_special_service_refs'),list):raise ValueError('invalid_private_policy')
 return {**public,**{key:private[key]for key in ('expired_overrides','confirmed_special_service_refs')}}


def policy():
 global _POLICY
 if _POLICY is None:_POLICY=load_policy()
 return _POLICY


def at(v):
 try:
  x=datetime.fromisoformat(str(v).replace('Z','+00:00'));return x.replace(tzinfo=TZ)if x.tzinfo is None else x.astimezone(TZ)
 except(ValueError,TypeError):return None


def order_codes(o):return {x for x in str(o.get('product_code')or'').split('|')if x}


def valid_orders(user,now):
 return [o for o in user.get('_orderHistory')or user.get('order_history')or[]if isinstance(o,dict)and o.get('is_paid')is True and o.get('is_refunded')is False and at(service_time(o))and at(service_time(o))<=now and o.get('product_key')!='testOrder']


def is_legacy(o):
 p=policy();return bool(order_codes(o)&set(p['legacy_product_codes'])or(o.get('product_key')=='lifetime_learning'and o.get('mapping_state')=='confirmed_service_family')or(o.get('product_key')=='soak299'and o.get('mapping_reason')==p['legacy_mapping_reason']))


def current_rights(user,evidence,now):
 # Routine's existing source reader supplies actual subscription fields once per source.
 # NULL on a paid tier is explicitly valid in the app; it is not a missing expiry.
 x=evidence.get('current_hxc_entitlement')
 if isinstance(x,dict)and x.get('observed')is True:
  result=dict(x);end=at(result.get('expires_at'))
  if end and end<=now:result.update(active=False,status='expired')
  return result
 h=user.get('_hxcSummary')or{};end=at(h.get('authorization_snapshot_expires_at')if 'authorization_snapshot_expires_at'in h else h.get('service_expires_at'))
 status=h.get('authorization_snapshot_status')if 'authorization_snapshot_status'in h else h.get('service_status')
 return dict(observed=bool(h.get('account_present')or h.get('user_id')),active=bool(status=='active'and(not end or end>now)),status='expired'if end and end<=now else status or'unknown',tier=h.get('tier'),expires_at=end.isoformat()if end else None,source='published_hxc_summary',no_fixed_expiry=False)


def resolve(user,evidence=None,now=None):
 evidence=evidence or{};now=now or datetime.now(TZ);p=policy();orders=valid_orders(user,now)
 explicit=[o for o in orders if '202608052341'in order_codes(o)or(any(c.startswith('wxshop-')for c in order_codes(o))and o.get('product_key')=='soak299')]
 legacy=[o for o in orders if is_legacy(o)];bundle=[o for o in orders if 'SOAK-FDE-BUNDLE-S1'in order_codes(o)]
 ref=user.get('safe_user_ref')or user.get('workbench_user_ref');override=p['expired_overrides'].get(ref);special=ref in p.get('confirmed_special_service_refs',[])
 formal=bool(explicit or legacy or bundle or override)
 ids={str(x)for x in user.get('_hxcAccountIds')or[]if x}
 if not ids and user.get('黄小璨用户ID'):ids={str(user['黄小璨用户ID'])}
 exact=evidence.get('routine_account_exact')is True or(evidence.get('routine_account_exact')is not False and len(ids)==1)
 rights=current_rights(user,evidence,now)
 low=[o for o in orders if order_codes(o)&{'31314141','334465678'}]
 other=[o for o in orders if not(order_codes(o)&{'31314141','334465678'})and o.get('product_key')not in {'testOrder','deposit','seven_day_trial','hxcWeekTrial'}]
 # Valid actual rights + another subscription/paid product use formal personal service.
 # A low-price-only experience stays in its own product pack.
 rights_299=rights.get('active')is True and(formal or special or other or not low or evidence.get('formal_service_source')is True)
 if not(formal or special or rights_299):return None
 base=dict(version=p['version'],source=p['source'],primary_identity='299',requires_activation_receipt=False,requires_separate_hxc_grant=False,upstream_permission_changed=False,paid_299_purchase=bool(explicit),original_order_refs=[o.get('order_ref')or o.get('order_id')for o in orders],order_source_unknown=not bool(orders),current_hxc_rights=rights,entitlement_source_refs=rights.get('formal_source_refs')or[])
 if user.get('customer_mapping_status')=='conflict'or user.get('_customerIdentityConflict')or user.get('identity_conflict'):return dict(base,status='identity_conflict',reason='exact_identity_conflict')
 base['contact_identity_pending']=user.get('customer_mapping_status')!='resolved'
 if user.get('current_service_refunded')is True or user.get('current_entitlement_revoked')is True:return dict(base,status='current_service_revoked',reason='current_refund_or_revocation')
 if not exact:return dict(base,status='manual_account_link',reason='hxc_account_not_exactly_linked')
 if override and any(o.get('order_ref')==override['order_ref']for o in orders)and not explicit and not rights_299:
  return dict(base,status='expired',basis=override['basis'],expires_at=override['recorded_expires_at'],skip_newcomer=True,recovery_subtype='legacy_bundled_expired',reason='expired_outside_active_product_packs')
 if rights_299:
  basis=('valid_299_order_and_current_rights'if explicit else'owner_legacy_499_family_and_current_rights'if legacy else'owner_confirmed_other_order_or_gift'if special else'other_paid_subscription_and_current_rights'if other else'valid_rights_formal_source'if evidence.get('formal_service_source')else'valid_rights_order_unknown')
  end=rights.get('expires_at');latest=max(explicit or legacy or other or orders,key=lambda o:at(service_time(o)),default={})
  return dict(base,status='active',basis=basis,order_ref=latest.get('order_ref')or latest.get('order_id'),paid_at=service_time(latest),first_299_paid_at=min(at(service_time(o))for o in explicit).isoformat()if explicit else None,expires_at=end,skip_newcomer=True if not explicit else bool(evidence.get('skip_299_newcomer')),service_origin='paid_order'if orders else'valid_entitlement_order_unknown',preserved_app_tier=rights.get('tier'))
 if explicit:
  # A new valid 299 purchase is not vetoed by an unrelated old free/trial state.
  order=max(explicit,key=lambda o:at(service_time(o)));ends=[at(order.get(k))for k in ['service_expires_at','expires_at','service_end_at','valid_until']];ends=[t for t in ends if t and t>at(service_time(order))];end=max(ends)if ends else None
  # An actual expiry after the latest paid order ends that service cycle.
  # Historical trial expiry BEFORE a new order must not veto internal sync.
  observed_end=at(rights.get('expires_at'))
  # A later distinct low-price/trial purchase can own the app's old expiry.
  # It cannot cancel the paid299 service (owner-confirmed overlap policy).
  later_trial=any(o is not order and (order_codes(o)&{'31314141','334465678'} or o.get('product_key')in {'hxcWeekTrial','seven_day_trial','trial','subscription_trial_month'}) and at(service_time(order))<at(service_time(o)) and observed_end and timedelta(0)<observed_end-at(service_time(o))<=timedelta(days=37) for o in orders)
  current_formal_source=rights.get('formal_service_source')is True or rights.get('formal_service_expiry_source')is True or evidence.get('formal_service_source')is True
  if not end and rights.get('observed')is True and rights.get('status')=='expired' and observed_end and observed_end>at(service_time(order)) and(not later_trial or current_formal_source):end=observed_end
  ended=bool(end and end<=now)
  return dict(base,status='expired'if ended else'active',basis='valid_299_order_and_exact_account',order_ref=order.get('order_ref')or order.get('order_id'),paid_at=service_time(order),first_299_paid_at=min(at(service_time(o))for o in explicit).isoformat(),expires_at=end.isoformat()if end else None,skip_newcomer=bool(evidence.get('skip_299_newcomer')),internal_app_sync_required=not ended,recovery_subtype='formal299_expired'if ended else None,reason='expired_outside_active_product_packs'if ended else None)
 if legacy or bundle:
  # Owner moved 43 expired and 329 inactive linked legacy users (4 new299 sync cases retained)
  # to a separate recall subgroup; neither is an active service/copy candidate.
  expired=rights.get('status')=='expired';order=max(legacy or bundle,key=lambda o:at(service_time(o)))
  return dict(base,status='expired'if expired else'inactive',basis='owner_legacy_499_recovery_only',order_ref=order.get('order_ref'),paid_at=service_time(order),expires_at=rights.get('expires_at'),skip_newcomer=True,recovery_subtype='legacy499_hxc_expired'if expired else'legacy499_hxc_not_activated',reason='legacy499_recovery_pool_only')
 return None


def apply(user,evidence=None,now=None):
 evidence=dict(evidence or{});out=dict(user);now=now or datetime.now(TZ)
 # The same observed account dates must reach both the operational archive
 # projection and package routing, including low-price and order-unknown users.
 if isinstance(evidence.get('current_hxc_entitlement'),dict) and evidence['current_hxc_entitlement'].get('observed')is True and evidence.get('routine_account_exact')is not False:
  rights=current_rights(out,evidence,now)
  if rights.get('status')in {'active','expired'}:
   h=dict(out.get('_hxcSummary')or{});h.update(service_status=rights['status'],service_expires_at=rights.get('expires_at')or'',authorization_snapshot_status=rights['status'],authorization_snapshot_expires_at=rights.get('expires_at')or'',authorization_source=rights.get('source'),authorization_source_as_of=rights.get('as_of'))
   out['_hxcSummary']=h
   if out.get('service_relationship_tier')in {'entry_active','entry_expired'}:out['service_relationship_tier']='entry_active'if rights['status']=='active'else'entry_expired'
 d=resolve(out,evidence,now)
 if not d:return out,evidence,None
 out['operational_service_admission']=d;evidence['operational_service_admission']=d
 if d['status']in {'active','expired'}:
  h=dict(out.get('_hxcSummary')or{});h.setdefault('authorization_snapshot_status',h.get('service_status'));h.setdefault('authorization_snapshot_expires_at',h.get('service_expires_at'))
  h.update(service_status=d['status'],service_expires_at=d.get('expires_at')or'',service_status_basis=d['basis']);out['_hxcSummary']=h;out['service_relationship_tier']='entry_active'if d['status']=='active'else'entry_expired'
 if d.get('skip_newcomer'):evidence['skip_299_newcomer']=True;evidence['onboarding_completion_due']=False
 elif d.get('first_299_paid_at')and evidence.get('onboarding_anchor_basis')!='registered_original_completion_task':evidence['onboarding_anchor_at']=d['first_299_paid_at'];evidence['onboarding_anchor_basis']='original_299_paid_order'
 return out,evidence,d
