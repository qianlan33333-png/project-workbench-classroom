"""Selected original rules; source I/O is supplied by the host as frozen inputs."""
from datetime import datetime
from zoneinfo import ZoneInfo

TZ=ZoneInfo('Asia/Shanghai')

def stamp(value):
    try:
        t=datetime.fromisoformat(str(value).replace('Z','+00:00'))
        return t.replace(tzinfo=TZ) if t.tzinfo is None else t.astimezone(TZ)
    except (ValueError,TypeError): return None

def first_activation(events, *, now):
    """A supplemental grant can be first; renewals/restorations never reset it.

    Historical absence must be explicitly attested by the entitlement owner.
    A list truncated to recent grants cannot establish first-ever entitlement.
    """
    candidates=[]
    for x in events or []:
        t=stamp(x.get('effective_at'))
        if (x.get('event_type')!='entitlement_effective' or x.get('status')!='effective'
            or not x.get('source_ref') or not t or t>now
            or x.get('reason') in {'renewal','restoration','extension','correction'}):continue
        if x.get('reason') not in {'initial_purchase','initial_gift','supplemental_grant'}:continue
        if x.get('first_ever') is not True or x.get('prior_entitlement_coverage')!='complete' or x.get('prior_effective_entitlement_count')!=0:continue
        candidates.append(x)
    chosen=min(candidates,key=lambda x:stamp(x['effective_at']),default=None)
    return {'activated_at':stamp(chosen['effective_at']).isoformat(),'source_ref':chosen['source_ref'],'reason':chosen['reason']} if chosen else {}
