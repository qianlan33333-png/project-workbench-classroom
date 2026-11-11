"""Selected original rules; source I/O is supplied by the host as frozen inputs."""
from order_time_contract import service_time
from datetime import datetime, timezone
from hxc_routine_policy import stamp, digest

FACT_KINDS={'identity','entitlement','activation'}

def source_fingerprint(ref,sources):
    p=sources['users'].get(ref) or {}
    return digest([p.get('_hxcAccountIds'),p.get('黄小璨用户ID'),p.get('customer_mapping_status'),
                   {k:(p.get('_hxcSummary') or {}).get(k) for k in ['service_status','service_expires_at','first_service_activated_at']},p.get('first_service_activated_at')])

def purchase_fingerprint(p):
    orders=p.get('_orderHistory')or p.get('order_history')or[]
    return digest(sorted([str(o.get('product_code')),str(service_time(o)),str(o.get('is_paid')),str(o.get('is_refunded'))]
                         for o in orders if str(o.get('product_code'))in {'31314141','334465678','202608052341'}))

def effective(records, ref, sources):
    facts={k:dict(v['value'],reviewed_at=v['reviewed_at'],source_ref=v['source_ref'],revision=v['revision'])
            for k,v in (records.get(ref) or {}).items() if v['decision'] in ['confirm','correct']
            and v['source_fingerprint']==source_fingerprint(ref,sources)}
    followup=facts.get('activation_followup')
    if followup and followup.get('purchase_fingerprint')!=purchase_fingerprint(sources['users'].get(ref)or{}):
        facts.pop('activation_followup')
    rights=facts.get('entitlement')
    if rights and rights.get("status") != "none":
        now=datetime.now(timezone.utc);start=stamp(rights.get('starts_at'));end=stamp(rights.get('expires_at'))
        rights['status']='active' if start and end and start<=now<end else 'expired' if end and now>=end else 'not_started'
    return facts

def conflicted(records,ref,sources):
    # No confirmed/corrected review can be stale when none exists. Avoid loading
    # a second full private row for every unreviewed user during list reads.
    if not any(v['decision'] in ['confirm','correct'] for k,v in (records.get(ref)or{}).items() if k in FACT_KINDS):
        return False
    p=sources['users'].get(ref)or{}
    ids={str(x)for x in p.get('_hxcAccountIds')or[]if x}
    if not ids and p.get('黄小璨用户ID'):ids={str(p['黄小璨用户ID'])}
    exact=(len(ids)==1 and p.get('customer_mapping_status')=='resolved'
           and not any(p.get(k)for k in ['_customerIdentityConflict','identity_conflict']))
    latest=(sources.get('current_entitlements_by_user')or{}).get(next(iter(ids)))if exact else None
    observed=(isinstance(latest,dict)and latest.get('observed')is True
              and latest.get('status')in {'active','expired'})
    for kind,v in(records.get(ref)or{}).items():
        if kind not in FACT_KINDS:continue
        if v['decision']not in ['confirm','correct']or v['source_fingerprint']==source_fingerprint(ref,sources):continue
        # A live, exactly linked account supersedes an old date-only review.
        # Keep the audit row; never turn normal expiry/extension into a new
        # manual approval. Identity conflicts and missing observations still hold.
        if kind=='entitlement'and observed:continue
        return True
    return False
