"""Selected original rules; source I/O is supplied by the host as frozen inputs."""
from order_time_contract import service_time
import json, hashlib
from datetime import datetime, timedelta
from zoneinfo import ZoneInfo
from hxc_operating_events import first_activation

TZ = ZoneInfo('Asia/Shanghai')

def stamp(value):
    try:
        d = datetime.fromisoformat(str(value).replace('Z', '+00:00'))
        return d.replace(tzinfo=TZ) if d.tzinfo is None else d.astimezone(TZ)
    except (ValueError, TypeError):
        return None

def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, ensure_ascii=False, default=str).encode()).hexdigest()[:24]

def apply_pending_activation(evidence, uid, current_rights, review):
    pending=(review or {}).get('activation_followup')or{}
    rights=current_rights or{}
    if (uid and pending.get('state')=='pending_activation' and pending.get('hxc_user_id')==uid
            and rights.get('observed')is True and rights.get('active')is False
            and rights.get('status')in {'no_current_paid_rights','expired'}):
        evidence.update(activation_state='confirmed_not_activated',activation_eligible=True,
                        activation_followup_review=dict(pending))

def enrich(user, evidence, sources, review=None, now=None, *, private_record=None):
    now = now or datetime.now(TZ)
    out=dict(user);e=dict(evidence or {});ref=out.get('safe_user_ref') or out.get('workbench_user_ref')
    private=(private_record if private_record is not None else sources['users'].get(ref)) or {}
    ids=[str(v) for v in private.get('_hxcAccountIds') or []]
    if not ids and private.get('黄小璨用户ID'): ids=[str(private['黄小璨用户ID'])]
    if review and review.get('identity'): ids=[review['identity']['hxc_user_id']]
    uid=ids[0] if len(ids)==1 and ids[0] in sources['accounts'] else ''
    h=dict(private.get('_hxcSummary') or out.get('_hxcSummary') or {})
    e['routine_workbench_ref']=ref
    e['routine_account_id']=uid
    e['routine_account_exact']=bool(uid)
    # Published user segmentation is authoritative; no second runtime scoring.
    published=(h.get('operating_state') or {})
    e['activity']=(dict(published,as_of=published.get('period_end') or sources['as_of'])
                   if published.get('activity_segment') in {'A','B','C','D','unknown'}
                   else sources['metrics'].get(uid) or dict(activity_segment='unknown',as_of=sources['as_of']))
    e['source_product_grants']=sources.get('product_grants_by_user',{}).get(uid,[])
    current_rights=(sources.get('current_entitlements_by_user')or{}).get(uid)
    if current_rights:
        e['current_hxc_entitlement']=dict(current_rights)
        e['formal_service_source']=current_rights.get('formal_service_source')is True
    apply_pending_activation(e,uid,current_rights,review)
    if review and review.get('entitlement'):
        entry_relationship=out.get('service_relationship_tier') in {'entry_active','entry_expired'}
        rights=review['entitlement'];h.update(service_status=rights['status'], service_expires_at=rights.get('expires_at') or '')
        if rights.get('recorded_date_sync') is True:
            h['registered_entitlement']={k:rights.get(k) for k in ['status','starts_at','expires_at','source_ref','source_account_relation','current_wechat_access_verified']}
        if out.get('service_relationship_tier') in ['entry_active','entry_expired'] or (rights.get('recorded_date_sync') is True and out.get('service_relationship_tier') in {'low_profile_no_current_service','profile_no_current_service','historical_other_purchase','needs_verification'}):
            out['service_relationship_tier']='entry_active' if rights['status']=='active' else 'entry_expired' if rights['status']=='expired' else 'needs_verification'
        if rights['status']=='none':
            e['grants']=[]
            if entry_relationship:
                out['service_relationship_tier']='profile_no_current_service' if any(out.get(k) for k in ['身份阶段','行业','业务焦点']) else 'low_profile_no_current_service'
        else:e['grants']=[dict(category=rights['rights_type'],granted_at=rights['starts_at'],expires_at=rights['expires_at'],evidence=rights['source_ref'])]
    e['first_service_activated_at']=((review or {}).get('activation') or {}).get('activated_at') or private.get('first_service_activated_at') or h.get('first_service_activated_at')
    e['activation_source_ref']=((review or {}).get('activation') or {}).get('source_ref') or private.get('first_service_activation_source_ref') or h.get('first_service_activation_source_ref')
    indexed = sources.get('entitlement_events_by_user')
    events = indexed.get(uid, []) if indexed is not None else [x for x in sources.get('entitlement_events') or [] if str(x.get('user_id') or '') == uid]
    authoritative=first_activation(events,now=now)
    if not e['first_service_activated_at'] and authoritative:
        e['first_service_activated_at']=authoritative['activated_at'];e['activation_source_ref']=authoritative['source_ref']
    last_send=max([x for x in sources.get('sends',{}).get(uid,[]) if stamp(x.get('sent_at')) and stamp(x['sent_at'])<=now],key=lambda x:stamp(x['sent_at']),default=None)
    if last_send:
        existing=stamp(out.get('last_outreach_at'))
        if not existing or existing<stamp(last_send['sent_at']):
            out['last_outreach_at']=last_send['sent_at'];out['last_outreach_source_ref']=last_send.get('source_ref')or last_send.get('dispatch_id')or ''
    first=stamp((sources['accounts'].get(uid) or {}).get('first_login_at'))
    dates=[stamp(x.get('granted_at')) for x in e.get('grants') or []]
    dates += [stamp(service_time(x)) for x in e.get('paid_entry') or []]
    dates += sources['histories'].get(uid) or []
    e['first_login_at']=first.isoformat() if first else ''
    e['account_created_at']=(sources['accounts'].get(uid) or {}).get('created_at') or ''
    survey_dates=[stamp(v.get('submitted_at')) for v in e.get('surveys') or []]
    e['questionnaire_recorded_at']=min([v for v in survey_dates if v], default=None)
    if e['questionnaire_recorded_at']: e['questionnaire_recorded_at']=e['questionnaire_recorded_at'].isoformat()
    e['earliest_rights_record']=min([t for t in dates if t], default=None)
    registration=stamp(e.get('account_created_at'))
    questionnaire=stamp(e.get('questionnaire_recorded_at'))
    anchor=registration or questionnaire
    e['onboarding_anchor_at']=anchor.isoformat() if anchor else ''
    e['onboarding_anchor_basis']='registration' if registration else 'questionnaire' if questionnaire else 'missing'
    e['onboarding_dates_agree']=registration.date()==questionnaire.date() if registration and questionnaire else None
    e['onboarding_anchor_source_ref']=('hxc_account:'+uid+':created_at') if registration else ('activation_evidence:'+str(ref)+':survey') if questionnaire else ''
    # Raw, exactly mapped user messages are sufficient evidence of starting
    # a conversation; a missing legacy meaningful-session aggregate is not a block.
    source_at=stamp(sources.get('as_of'))
    observed=[t if isinstance(t,datetime) else stamp(t) for t in sources.get('user_message_times',{}).get(uid,[])]
    e['onboarding_conversation_observed']=bool(uid and anchor
        and source_at and source_at<=now and any(t and anchor<=t<min(anchor+timedelta(days=7),source_at+timedelta(microseconds=1)) for t in observed))
    # A registered completion task survives leaving the seven-day newcomer list.
    request=(sources.get('completion_requests')or{}).get(ref)or{}
    # The original registered task owns the D7 clock, including unactivated
    # customers without an HXC account. Actual messages need no global negative-coverage gate.
    task_anchor=stamp(request.get('anchor_at'))
    finish=task_anchor+timedelta(days=7) if task_anchor else None
    e['onboarding_completion_due']=bool(finish and request.get('status')=='pending'
        and stamp(request.get('window_end'))==finish and now>=finish)
    if e['onboarding_completion_due']:
        e['onboarding_anchor_at']=task_anchor.isoformat()
        e['onboarding_anchor_basis']='registered_original_completion_task'
    paid299=[stamp(service_time(o)) for o in private.get('_orderHistory') or [] if isinstance(o,dict)
        and (o.get('product_key')=='soak299' or str(o.get('product_code'))=='202608052341')
        and o.get('is_paid') is True and o.get('is_refunded') is False]
    first299=min([t for t in paid299 if t],default=None)
    positive=(sources['histories'].get(uid) or [])+[t for t in observed if t]
    e['skip_299_newcomer']=bool(first299 and any(t<first299 for t in positive))
    if e['skip_299_newcomer']:e['onboarding_completion_due']=False

    out['_hxcSummary']=h
    if review and review.get('identity'):out['customer_mapping_status']='resolved'
    from sku299_owner_service import apply as apply_owner_service
    policy_user=dict(private,**out)
    policy_user['_orderHistory']=private.get('_orderHistory') or out.get('_orderHistory') or out.get('order_history') or []
    admitted,e,admission=apply_owner_service(policy_user,e,now)
    out['_hxcSummary']=admitted.get('_hxcSummary')or h
    out['service_relationship_tier']=admitted.get('service_relationship_tier')or out.get('service_relationship_tier')
    if admission:
        out['operational_service_admission']=admission
        out['_hxcSummary']=admitted.get('_hxcSummary') or h
        out['service_relationship_tier']=admitted.get('service_relationship_tier') or out.get('service_relationship_tier')
    return out,e

def current_catalog_candidates(evidence,sources,*,week,now,profile=None,latest_available=False,preferred_id=""):
    """Fresh published candidates within the existing approved service audience."""
    uid=evidence.get('routine_account_id');ref=evidence.get('routine_workbench_ref')
    monday=datetime.strptime(week+'-1','%G-W%V-%u').replace(tzinfo=TZ)
    if latest_available:monday=now-timedelta(days=30)
    period_end=now+timedelta(microseconds=1) if latest_available else min(now+timedelta(microseconds=1),monday+timedelta(days=7))
    content_history=sources.get('content_history_by_ref',{}).get(ref,set())
    pool=sources.get('catalog_by_key') or {(str(x.get('content_type')or''),str(x.get('content_id')or'')):x for x in sources.get('catalog',{}).values()}
    result=[]
    for key,c in pool.items():
        pub=stamp(c.get('published_at'));end=stamp(c.get('valid_until'))
        if (not pub or not monday<=pub<period_end or c.get('deleted') or c.get('status')!='published'
            or c.get('pushable') is not True or c.get('path_status')!='verified_route'
            or not c.get('base_target_path') or not c.get('content_summary') or end and end<=now
            or key in content_history or key[1] in sources.get('seen',{}).get(uid,set())):continue
        rights=c.get('required_entitlement')
        # Unknown product-specific gates cannot be assumed from an active label.
        if rights and not (isinstance(rights,dict) and rights.get('policy')=='existing_content_view_gate' and rights.get('checked_at_dispatch') is True):continue
        result.append(c)
    # Only a sourced, recent user-stated topic may justify a personal content
    # recommendation. Broad audience eligibility alone does not match material.
    conv=(profile or {}).get('conversation')or{}
    facts=[]
    for group in ['fields','needs']:
        for field in (conv.get(group)or{}).values():
            if not isinstance(field,dict):continue
            at=stamp(field.get('source_at'))
            if (field.get('status') in {'user_stated','confirmed','user_confirmed','human_confirmed','explicit_confirmed','current_confirmed'}
                and field.get('evidence_refs') and at and timedelta(0)<=now-at<=timedelta(days=90)):
                facts.append(str(field.get('value')or''))
    topics=['知识库','语料','RAG','定位','获客','组织','交付','AI员工','商业模式','产品','营销','增长','社群','流量','运营','团队']
    def matched(c):
        combined=str(c.get('title')or'')+' '+str(c.get('content_summary')or'')
        return [term for term in topics if term.lower() in combined.lower()
                and any(term.lower() in fact.lower() for fact in facts)]
    result=[c for c in result if matched(c)]
    def rank(c):
        return (-len(matched(c)),0 if str(c['content_id'])==preferred_id else 1,
                -stamp(c['published_at']).timestamp(),str(c['content_type']),str(c['content_id']))
    return sorted(result,key=rank)

def unified_week_material(evidence,sources,*,week,now,profile=None):
    """Use a published owned review or recent available content; never invent a future publication.

    This helper is reached only for exact, active ongoing-service decisions. The
    original snapshot identity/contact/rights gates run before this selection.
    """
    uid=str(evidence.get('routine_account_id')or'')
    if evidence.get('routine_account_exact') is not True or not uid:return None
    for pack in sources.get('packs',{}).values():
        rows=[r for r in pack.get('rows')or[] if str(r.get('hxc_user_id'))==uid]
        if any(isinstance(r.get('redline_flags'),list) and r['redline_flags'] for r in rows):return None
    monday=datetime.strptime(week+'-1','%G-W%V-%u').replace(tzinfo=TZ)
    report=(sources.get('growth_reports') or {}).get(uid)
    if report:
        from hxc_report_delivery import material as report_material
        selected=report_material(report,week=week)
        if selected:return selected
    material_monday=monday-timedelta(days=7)
    material_week=material_monday.strftime('%G-W%V')
    material_sunday=(monday-timedelta(days=1)).date()
    candidates=[]
    for r in sources.get('reviews',{}).get(uid,[]):
        end=stamp(r.get('period_end'));start=stamp(r.get('period_start'));generated=stamp(r.get('generated_at'))
        if not(r.get('unified_published') and end and start and generated and generated<=now
               and start.date()==material_monday.date() and end.date()==material_sunday
               and end<now
               and r.get('base_target_path') and r.get('summary')):continue
        rid=str(r.get('review_id')or'')
        if not rid or any(str(x.get('content_id')or'')==rid for x in sources.get('sends',{}).get(uid,[])):continue
        candidates.append(r)
    if candidates:
        r=max(candidates,key=lambda x:(str(x.get('period_end')),str(x.get('generated_at'))))
        return dict(kind='review',content_id=str(r['review_id']),review_id=str(r['review_id']),content_type='review',
            title=r.get('report_name')or'你的每周学习总结',summary=r['summary'],specific_help=r['summary'],
            period_start=r['period_start'],period_end=r['period_end'],generated_at=r['generated_at'],
            base_target_path=r['base_target_path'],source_ref=r.get('resource_key')or'hxc:weekly_review:'+str(r['review_id']),
            version=r.get('content_version')or r.get('version'),selection_basis='本人已发布的最近完整周期复盘；展示真实周期')
    fresh=current_catalog_candidates(evidence,sources,week=material_week,now=now,profile=profile)
    if not fresh:return None
    c=fresh[0]
    return dict(kind='content',content_id=str(c['content_id']),content_type=c['content_type'],title=c['title'],
        summary=c['content_summary'],specific_help=c['content_summary'],base_target_path=c['base_target_path'],
        published_at=c['published_at'],required_entitlement=c.get('required_entitlement'),version=c.get('content_version'),
        source_ref=c.get('source_ref')or'catalog:'+str(c['content_id']),
        selection_relevance='上一完整周新发布内容；只在本人近期真实议题与内容相符时推荐',
        selection_basis='上一完整周已发布且未记录已读的内容；没有逐人关联则留待选材',
        read_history_status='排除已记录的阅读、讨论和推荐；历史未覆盖部分仍未知')
