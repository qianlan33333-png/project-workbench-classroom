"""Original deterministic functions; sources are bound by the adapter."""
from salutation_policy import resolved_send_name
from datetime import datetime, timedelta
from zoneinfo import ZoneInfo

TZ=ZoneInfo('Asia/Shanghai')

GIFT_QUESTIONNAIRE_URLS={
    '31314141':'https://www.youcangogogo.com/h5/auth.html?slug=q-392ae6183bbd81b104cb7a8a1ede2715',
    '334465678':'https://www.youcangogogo.com/h5/auth.html?slug=q-e0fef64351c03718804f205f9876e434',
}

def at(value):
    try:
        x=datetime.fromisoformat(str(value).replace('Z','+00:00'))
        return x.replace(tzinfo=TZ) if x.tzinfo is None else x.astimezone(TZ)
    except (ValueError,TypeError): return None

def state(data): return (data.get('profile_context') or {}).get('newcomer_sop') or {}

def questionnaire_focus(profile):
    for key in ('hxc_questionnaire_ai_pain','hxc_questionnaire_business_pain','hxc_questionnaire_growth_pain'):
        x=(profile.get('archive_fields') or {}).get(key) or {}
        if (x.get('state')=='historical_questionnaire_answer' and x.get('source_at')
            and (x.get('source_ref') or x.get('source_refs')) and not x.get('internal_reference_only')
            and isinstance(x.get('value'),str) and x['value'].strip() and len(x['value'])<=160):
            return {'value':x['value'].strip(),'source_at':x['source_at'],'source_kind':'questionnaire_option','not_verbatim_quote':True}
    return None

def render_fixed_template(profile,name,asset):
    focus=questionnaire_focus(profile)
    key='questionnaire_focus' if focus else 'questionnaire_no_focus'
    copy=asset['fixed_first_use_templates'][key].replace('{{称呼}}',name)
    if focus:copy=copy.replace('{{问卷明确关注点}}',focus['value'])
    if '{{' in copy or '}}' in copy:raise ValueError('fixed_template_unfilled_variable')
    return {'push_copy':copy,'template_key':key,'focus_evidence':focus,
            'asset_id':asset['id'],'asset_version':asset['version']}

def pending_activation_last_reminder(data):
    """Bind the existing A3 reference to the original confirmed D6 slot only."""
    b=data.get('sku_pack_binding')or{}
    if (b.get('version'),b.get('product_code'),b.get('primary_identity'),b.get('node'))!=('sku.31314141.rules.v1.3.0','31314141','6.8','D6'):
        return None
    profile=data.get('profile_context')or{};pending=(profile.get('verified_service_facts')or{}).get('activation_followup')or{}
    paid=at(b.get('paid_at'));now=datetime.now(TZ)
    valid=(b.get('sop_node')=='A3'and b.get('copy_rule')=='confirmed_not_activated'
           and b.get('cycle_key')and b.get('slot_key')and data.get('safe_user_ref')
           and data.get('strategy_key')=='hxc:onboarding'and profile.get('status')in {'exact_archive','exact_archive_and_conversation'}
           and pending.get('state')=='pending_activation'and pending.get('revision')and pending.get('source_ref')
           and paid and timedelta(hours=144)<=now-paid<timedelta(hours=168))
    if not valid:return {'status':'blocked','blockers':['本次待开通确认或原D6绑定不足'],'model_calls':0,'automatic_send_allowed':False}
    blockers=[x for x in state(data).get('blockers')or[]if x!='未对话来源已过时，执行前刷新']
    if data.get('do_not_contact')is True or data.get('contact_policy')in {'blocked','do_not_contact','opt_out','blocked_internal_check'}:blockers.append('联系许可未通过')
    if blockers:return {'status':'blocked','blockers':blockers,'model_calls':0,'automatic_send_allowed':False}
    import json,hashlib
    from pathlib import Path
    path=Path(__file__).resolve().parents[1]/'prompts/runtime/mirror-newcomer-sop-reference-v1.json'
    raw=path.read_bytes();asset=json.loads(raw);original=asset['original_references']['A3']['original_text']
    body=original.replace('{{客户名}}',resolved_send_name(data)).replace('怎么一直没等到你的消息呀，我都有一点点想你啦～最近还好吗？','这次黄小璨 AI 体验的开通，再提醒你一下。')
    confirmed=(data.get('provisional_purchase')or{}).get('questionnaire_confirmed')is True or (state(data).get('user_states')or{}).get('questionnaire')=='submitted'
    line=('你之前填过的问卷不用重复填写。如果小程序入口还没接上，直接告诉我，我帮你核对。'if confirmed else
          '如果这次公开课问卷还没填，可以从这里填写：'+GIFT_QUESTIONNAIRE_URLS['31314141']+'。填过就不用重复；如果入口还没接上，直接告诉我。')
    body=body.replace('👉 填完问卷开始使用：「问卷链接」',line)
    if '{{'in body or '「问卷链接」'in body:raise ValueError('original_A3_unfilled')
    from returning_registered_copy import render as returning_copy
    returning=returning_copy(data)
    if returning:body=returning['text']
    return dict(status='ready_for_review',push_copy=body,variant='confirmed_pending_activation_D6',template_key='mirror.original.A3.confirmed_pending',
                owner_copy_asset={k:v for k,v in returning.items()if k!='text'}if returning else {'source':'prompts/runtime/mirror-newcomer-sop-reference-v1.json','version':asset['version'],'key':'A3','sha256':hashlib.sha256(raw).hexdigest()},
                attachments=[],automatic_send_allowed=False,model_calls=0,requires_send_history_review=True,requires_entitlement_review=True)

def fixed_candidate(data,asset):
    last=pending_activation_last_reminder(data)
    if last is not None:return last
    s=state(data);node=s.get('node') or ''
    if data.get('strategy_branch')=='purchase_account_unlinked':
        purchase=data.get('provisional_purchase') or {}
        days=purchase.get('purchase_days')
        sku_binding=data.get('sku_pack_binding') or {}
        current_sku_order=(
            (sku_binding.get('version'),sku_binding.get('product_code'),sku_binding.get('primary_identity')) in {
                ('sku.31314141.rules.v1.3.0','31314141','6.8'),
                ('sku.334465678.rules.v1.0.0','334465678','99')}
            and sku_binding.get('node')=='D2' and sku_binding.get('sop_node')=='A1'
            and sku_binding.get('paid_at') and sku_binding.get('cycle_key')
            and sku_binding.get('slot_key') and data.get('safe_user_ref'))
        # The published SKU binding supplies the paid order and workbench
        # recipient reference. HXC account linking can legitimately be unknown;
        # the final handoff still checks the exact WeCom recipient and history.
        # Unknown account/survey state is handled by conditional wording.
        if current_sku_order and not purchase.get('paid_at'):
            purchase=dict(purchase,product_code=sku_binding['product_code'],paid_at=sku_binding['paid_at'])
        if current_sku_order and type(days) is not int:
            paid=at(sku_binding['paid_at'])
            days=max(1,int((datetime.now(TZ)-paid).total_seconds()//86400)+1) if paid else None
            purchase=dict(purchase,purchase_days=days)
        if current_sku_order and not node:node='A1'
        sku_uses_a1=(sku_binding.get('version') in {
            'sku.31314141.rules.v1.3.0','sku.334465678.rules.v1.0.0'}
            and sku_binding.get('sop_node')=='A1')
        expected_node=('HIST:checkin' if type(days) is int and days>=7 else
                       'A1' if data.get('policy_version')=='hxc-lifecycle.v4' or sku_uses_a1 else 'A0')
        code=str(purchase.get('product_code') or '')
        if (data.get('strategy_key')!='hxc:onboarding' or node!=expected_node
                or code not in {'31314141','334465678'}
                or not purchase.get('paid_at') or not (purchase.get('order_evidence_digest') or current_sku_order)
                or ((data.get('profile_context') or {}).get('status') not in {'exact_archive','exact_archive_and_conversation'} and not current_sku_order)):
            return {'status':'blocked','blockers':['商品购买与工作台收件身份未完整核准'],
                    'automatic_send_allowed':False,'model_calls':0}
        if data.get('do_not_contact') is True or data.get('contact_policy') in {'blocked','do_not_contact','opt_out','blocked_internal_check'}:
            return {'status':'blocked','blockers':['联系许可未通过'],
                    'automatic_send_allowed':False,'model_calls':0}
        name=resolved_send_name(data)
        reference=(asset.get('gifted_unknown_reference') or {}).get(code) or {}
        if (code=='31314141' and (reference.get('activity_name')!='公开课' or reference.get('gifted_days')!=14)
                or code=='334465678' and (reference.get('activity_name')!='共创计划' or reference.get('gifted_days')!=30)):
            return {'status':'blocked','blockers':['已核准活动参考版本不一致'],
                    'automatic_send_allowed':False,'model_calls':0}
        min_days=0 if expected_node=='A0' else 7 if expected_node=='HIST:checkin' else 1
        max_days=30 if expected_node=='HIST:checkin' else 7
        if type(days) is not int or not min_days<=days<max_days:
            return {'status':'blocked','blockers':['购买日不在对应服务窗口'],
                    'automatic_send_allowed':False,'model_calls':0}
        activity=reference['activity_name'];gifted_days=reference['gifted_days']
        opening=f'{name}，你今天刚报名{activity}。' if days==0 else f'{name}，从你报名{activity}到现在，已经{days}天啦。'
        questionnaire_confirmed=purchase.get('questionnaire_confirmed') is True
        survey=('你提交过的问卷我这里有记录，不用重复填。后面我会按你关注的方向找内容和跟进。'
                if questionnaire_confirmed else
                '如果这次'+activity+'的问卷还没填，直接从这里补上：\n'
                +GIFT_QUESTIONNAIRE_URLS[code]
                +'\n填过就不用重复。问卷能让我知道你更关注哪些问题，后面给你找内容和服务也会更贴合。')
        ending=('如果已经开通，就直接到黄小璨 AI 小程序里体验；'
                '如果小程序入口没接上、打不开，直接告诉我，我帮你核对。')
        copy=(opening+'\n\n有个事我确认下——你是不是已经开通黄小璨 AI 了？\n\n'
              f'这次{activity}包含黄小璨 AI 的{gifted_days}天体验安排。我这里还没核对到你的小程序账号和实际使用状态。黄小璨 AI 里会持续更新老黄关于 AI+OPC/OPT 创业的一手思考，'
              '也能查阅他沉淀的方法和案例；有具体问题没想清楚时，还可以试试深度咨询，把它当作 AI 商业学习教练来聊。\n\n'
              +survey+'\n\n'+ending)
        owner_copy=None
        if code=='31314141':
            from owner_service_copy import activation
            owner_copy=activation(opening,questionnaire_confirmed)
            copy=owner_copy['text']
        from returning_registered_copy import render as returning_copy
        returning=returning_copy(data)
        if returning:copy=returning['text'];owner_copy=returning
        return {'status':'ready_for_review','push_copy':copy,'variant':'purchase_account_unlinked',
                'template_key':'gifted-hxc-questionnaire-'+('known' if questionnaire_confirmed else 'unknown')+('.6.8.v2' if code=='31314141' else '.99.v2'),
                'owner_copy_asset':{k:v for k,v in owner_copy.items()if k!='text'}if owner_copy else None,
                'attachments':[],'automatic_send_allowed':False,'model_calls':0,
                'requires_send_history_review':True,'requires_entitlement_review':True,
                'note':'账号关联未知，购买日仅作临时新人窗口；审核后交接仍须核旧触达、真实权益、精确接收对象与联系许可。'}
    applies=(data.get('scene')=='new_user_onboarding' and
             (node in {'B0','B1'} or (not node and data.get('strategy_key')=='hxc:onboarding' and data.get('strategy_branch')=='opened_unused')))
    if not applies:return None
    user=s.get('user_states') or {}; usage=s.get('usage') or {}
    blockers=[]
    if user.get('account_registered') is not True:blockers.append('缺账号注册证据')
    if user.get('questionnaire')!='submitted':blockers.append('缺问卷提交证据')
    if usage.get('started_chat') is True:return None
    if usage.get('source_complete') is not True or user.get('usage')!='no_conversation_in_snapshot':blockers.append('不能把未知使用记录当作未对话')
    now=at(s.get('evaluation_at') or data.get('writing_as_of'));source=at(s.get('source_as_of'))
    if not now or not source or not timedelta(0)<=now-source<=timedelta(hours=1):blockers.append('未对话状态需要执行时点前一小时内的完整记录')
    card=s.get('writing_card') or {}
    if not data.get('entry') and not (card.get('本轮入口') or {}).get('verified'):blockers.append('“点下方小程序”需实际核准的小程序附件或入口')
    if data.get('do_not_contact') is True or data.get('contact_policy') in {'blocked','do_not_contact','opt_out','blocked_internal_check'}:blockers.append('联系许可未通过')
    blockers+=list(s.get('blockers') or [])
    if blockers:
        # A missing questionnaire or stale usage watermark cannot support the
        # old "you have not chatted" copy. It can support a conditional first
        # check-in for an exact recipient with a usable salutation.
        return conditional_first_use_candidate(data,blockers)
    name=resolved_send_name(data)
    return dict(render_fixed_template(data.get('profile_context') or {},name,asset),
                status='ready_for_review',automatic_send_allowed=False,model_calls=0,
                attachments=[data['entry']] if isinstance(data.get('entry'),dict) else [])

def conditional_first_use_candidate(data,source_blockers=()):
    if data.get('do_not_contact') is True or data.get('contact_policy') in {
            'blocked','do_not_contact','opt_out','blocked_internal_check'}:
        return {'status':'blocked','blockers':['联系许可未通过'],
                'automatic_send_allowed':False,'model_calls':0}
    if not (data.get('safe_user_ref') or data.get('hxc_user_id')):
        return {'status':'blocked','blockers':['缺精确接收对象'],
                'automatic_send_allowed':False,'model_calls':0}
    sop=state(data)
    node=sop.get('node') or ''
    if data.get('strategy_key')=='hxc:onboarding' and (
            node in {'A0','A1','B0','B1','B2','B3'} or node.startswith('C:') or
            (not node and data.get('strategy_branch') in {'opened_unused','used_once'})):
        return {'status':'blocked',
                'blockers':['原七天节点的资料或来源不足；保留原任务，核实后沿已发布新人 SOP 出稿，不套用历史补服务模板']+list(source_blockers),
                'automatic_send_allowed':False,'model_calls':0}
    name=resolved_send_name(data)
    name_review=name=='你好'
    card=data.get('entry') if isinstance(data.get('entry'),dict) and data['entry'].get('verified') is True else None
    copy=standard_unknown_first_contact(name,card, (state(data).get('verified_time_anchor') or {}))
    return {'status':'ready_for_review','push_copy':copy,'variant':'conditional_first_use',
            'template_key':'standard_unknown_first_contact.v2','attachments':[card] if card else [],
            'automatic_send_allowed':False,'requires_send_history_review':True,
            'requires_salutation_review':False,
            'model_calls':0,'source_blockers_for_review':list(source_blockers),
            'note':'资料不足首次确认参考稿；问卷与使用未知，不是本人已开通或从未使用的断言。旧触达须去重。'+('称呼须在审核时校准。' if name_review else '')}

def standard_unknown_first_contact(name,card=None,verified_time_anchor=None):
    """Owner-reviewed first check-in reference, never a delivery decision.

    A date anchor must come from a published, complete source projection with
    its evidence reference. Unverified dates fall back to a timeless opening.
    """
    anchor=verified_time_anchor if isinstance(verified_time_anchor,dict) else {}
    kind=anchor.get('kind');days=anchor.get('days')
    intro='我想跟你确认一件事。'
    if (anchor.get('source_complete') is True and anchor.get('evidence_ref')
            and type(days) is int):
        if kind=='first_wecom_add' and 1<=days<=30:
            intro=f'今天是你加到我的第{days}天，我想跟你确认一件事。'
        elif kind=='last_user_reply' and 7<=days<=90:
            intro=f'距离我们上次互动已经{days}天了，我想跟你确认一件事。'
    action=('你可以点下方的小程序卡片打开；如果想让我说明怎么用，回复“需要”就行。'
            if card else '如果需要入口或想知道怎么用，回复“需要”，我把入口和用法发给你。')
    return (f'{name}，{intro}我这里还没有核实到你最近使用黄小璨 AI 的情况，'
            '不确定你是不是已经打开并用起来了。\n\n'
            '黄小璨 AI 里会持续更新老黄关于 AI+OPC/OPT 创业的一手思考。'
            '你可以读他的最新判断，也可以查阅里面沉淀的方法和案例，接着讨论怎么用到自己身上。'
            '有具体的商业问题没想清楚时，也可以把它当作 AI 商业学习教练来聊一聊。\n\n'
            f'你现在打开和使用黄小璨 AI 顺利吗？{action}')

def conditional_d7_checkin(data):
    """A factual check-in when a personal seven-day summary has no usable facts.

    This is a review draft, never a claim that a survey or HXC use did not happen.
    The original D7 node and its prior handoff still govern delivery deduplication.
    """
    sop=state(data)
    if not (data.get('strategy_key')=='hxc:onboarding'
            and data.get('scene')=='personal_followup'
            and sop.get('node')=='D7:summary'):
        return None
    if data.get('do_not_contact') is True or data.get('contact_policy') in {
            'blocked','do_not_contact','opt_out','blocked_internal_check'}:
        return {'status':'blocked','blockers':['联系许可未通过'],
                'automatic_send_allowed':False,'model_calls':0}
    if not (data.get('safe_user_ref') or data.get('hxc_user_id')):
        return {'status':'blocked','blockers':['缺精确接收对象'],
                'automatic_send_allowed':False,'model_calls':0}
    name=resolved_send_name(data)
    name_review=name=='你好'
    known=(sop.get('user_states') or {}).get('questionnaire')=='submitted'
    if known:
        survey='你填过的问卷我这里有记录，不用再填。'
    else:
        survey='我这里还没有确认到你的问卷；如果已经填过，不用重复填，回我一声就行。如果还没填，补上后我才能更贴近你的关注点来推荐内容和跟进。'
    card=data.get('entry') if isinstance(data.get('entry'),dict) and data['entry'].get('verified') is True else None
    action=('想开始的话，可以点下方黄小璨 AI 小程序卡片。' if card else
            '想开始的话，直接回我，我把黄小璨 AI 的入口发你。')
    copy=(f'{name}，想跟你确认一下：这段时间黄小璨 AI 你用起来了吗？'
          '我这里还没有足够具体的使用反馈，所以先不替你下结论。\n\n'
          '为什么我还是想提醒你看看它？这里会持续更新老黄关于 AI+OPC/OPT 创业的一手思考；'
          '这些思考里的方法和案例会沉淀下来，以后随时能查、能继续讨论。'
          '你也可以把黄小璨当作 AI 商业学习教练，帮你看清自己在商业能力上最需要突破的地方。\n\n'
          f'{survey}{action}后面我会按你真正关心和使用的内容继续服务；'
          '如果你已经在用，告诉我最想继续看哪一块就好。')
    return {'status':'ready_for_review','push_copy':copy,
            'variant':'d7_conditional_checkin','template_key':'d7_conditional_checkin.v1',
            'attachments':[card] if card else [],'automatic_send_allowed':False,
            'requires_salutation_review':False,
            'requires_send_history_review':True,'model_calls':0,
            'note':'条件式回访；不是本人七天成果总结，审核和交接前仍须核对旧触达与当前权益。'+('称呼须在审核时校准。' if name_review else '')}

def approved_entry(root):
    """Server-owned card registry populated from a verified SCRM material read.

    A home entry is not a recipient attribution path or a content recommendation.
    No caller-supplied URL or verified flag can activate this registry.
    """
    import json,re
    from pathlib import Path
    try:
        e=json.loads((Path(root)/'var/newcomer-mini-program-entry.json').read_text())
    except (OSError,ValueError): return None
    v=e.get('verification') or {}
    if not (e.get('status')=='approved' and e.get('enabled') is True
            and re.fullmatch(r'wx[0-9a-f]{16}',str(e.get('appid') or ''))
            and e.get('page') in {'pages/login/login','pages/conversations/conversations'}
            and isinstance(e.get('material_id'),int) and e['material_id']>0
            and v.get('source')=='scrm_miniprogram_library'
            and v.get('http_status')==200 and at(v.get('checked_at'))
            and v.get('appid')==e['appid'] and v.get('page')==e['page']
            and v.get('material_id')==e['material_id']):return None
    return {k:e[k] for k in ('appid','page','title','material_id')} | {
        'type':'mini_program','verified':True,'source':'scrm_miniprogram_library',
        'verified_at':v['checked_at'],'purpose':'新人认识对话入口'}

def current_mainline_fields(fields):
    result=[]
    for key,field in (fields or {}).items():
        if not str(key).startswith('hxc_focus_') or not isinstance(field,dict):continue
        value=field.get('value') or {}
        if (field.get('state')=='current_confirmed' and isinstance(value,dict)
            and value.get('owner_type')=='user' and value.get('status')=='active'
            and value.get('focus_role')=='primary' and value.get('current_effective') is True and value.get('needs_review') is False
            and value.get('source_type') in {'user_confirmation','relationship_onboarding_confirmation','returning_calibration_confirmation'}
            and (field.get('source_ref') or field.get('source_refs')) and field.get('source_at')):result.append(field)
    return result

def existing_usage_guidance(sources,uid,anchor,finish,now,fields):
    """Evidence of a recorded use is not completion, satisfaction or real-person activation."""
    groups={'coach_conversation':[],'daily_lesson_discussion':[]};source=at(sources.get('as_of'))
    for row in (sources.get('feature_observations') or {}).get(uid,[]):
        t=at(row.get('occurred_at'));kind=row.get('kind')
        if kind in groups and row.get('source_ref') and t and source and anchor<=t<finish and t<=min(source,now):groups[kind].append(row)
    states={key:'observed' if rows else 'unknown' for key,rows in groups.items()}
    states['deep_consultation']='coach_conversation_observed' if groups['coach_conversation'] else 'unknown'
    confirmed=bool(current_mainline_fields(fields))
    # Same existing C node/limit. Select one approved introduction; no extra push.
    suggested = ('daily_discussion' if groups['coach_conversation'] and not groups['daily_lesson_discussion'] else
                 'deep_consultation' if groups['daily_lesson_discussion'] and not groups['coach_conversation'] else
                 'focus_support' if groups['coach_conversation'] and groups['daily_lesson_discussion'] and not confirmed else '')
    return {'feature_states':states,'recorded_message_counts':{key:len(rows) for key,rows in groups.items()},
        'completion_states':{'deep_consultation':'unknown','daily_lesson_discussion':'unknown'},
        'confirmed_mainline_available':confirmed,'suggested_feature':suggested,
        'rule':'仅依据本周期有归属的会话用户角色消息选择功能介绍；自动入口过滤未齐，只能描述有对话记录，不能断言真人已体验。教练咨询不证明已完成深度咨询；未记录仍未知。没有具体内容材料不编文章或卡片。不能据此计算真人激活率、满意度或学习成果。'}

def attach_server_state(data,root,*,now=None,sources=None,evidence=None):
    """Bind node to existing exact archive and cached read-only usage sources.

    Never trust a browser's questionnaire/usage booleans. Unknown receipts and
    feature use remain unknown; this adapter does not invent a send ledger.
    """
    if data.get('strategy_key')!='hxc:onboarding':return data
    data=dict(data);data.pop('entry',None)
    if data.get('strategy_branch')=='purchase_account_unlinked':
        purchase=data.get('provisional_purchase') or {}
        paid=at(purchase.get('paid_at'))
        profile=dict(data.get('profile_context') or {})
        if (profile.get('status') in {'exact_archive','exact_archive_and_conversation'}
                and purchase.get('product_code') in {'31314141','334465678'} and purchase.get('order_evidence_digest') and paid):
            now=at(now) if now else datetime.now(TZ)
            new_policy=data.get('policy_version')=='hxc-lifecycle.v4'
            purchase_days=purchase.get('purchase_days')
            if new_policy and (type(purchase_days) is not int or purchase_days<1):
                return data  # Day 0 is owned by Mirror, never a workbench A0.
            node='HIST:checkin' if type(purchase_days) is int and purchase_days>=7 else 'A1' if new_policy else 'A0'
            profile['newcomer_sop']={'node':node,'anchor_at':paid.isoformat(),
                'window_end':(paid+timedelta(days=7)).isoformat(),
                'anchor_basis':'first_valid_paid_order_provisional',
                'evaluation_at':now.isoformat(),'source_as_of':profile.get('archive_captured_at') or '',
                'user_states':{'account_registered':None,'questionnaire':'submitted' if purchase.get('questionnaire_confirmed') is True else 'unknown','usage':'unknown'},
                'usage':{'started_chat':None,'source_complete':False},
                'writing_card':{'服务':'首次条件式入口确认','开通':'未知，不断言未开通',
                                '问卷':'以已核证据为准；无论是否已填，不要求重复填','使用':'账号未精确关联，不能推断未使用'},
                'blockers':[]}
            data['profile_context']=profile
        return data  # No HXC-issued card may be attached without an exact HXC account.
    entry=approved_entry(root)
    if entry:data['entry']=entry
    profile=dict(data.get('profile_context') or {})
    # A read-time audit timestamp is not user evidence; keep factual source
    # timestamps intact while avoiding prompt churn on every read.
    if isinstance(profile.get('hxc_unified'),dict):
        profile['hxc_unified']=dict(profile['hxc_unified'])
        profile['hxc_unified'].pop('checked_at',None)
    data['profile_context']=profile
    if profile.get('status') not in {'exact_archive','exact_archive_and_conversation'}:return data
    ref=profile.get('safe_user_ref') or data.get('safe_user_ref')
    if not ref:return data
    import json
    from pathlib import Path
    import hxc_routine_policy as routine
    now=at(now) if now else datetime.now(TZ)
    sources=sources if sources is not None else routine.load_sources(root)
    private=(sources.get('users') or {}).get(ref) or {}
    ids={str(x) for x in private.get('_hxcAccountIds') or []}
    if not ids and private.get('黄小璨用户ID'):ids={str(private['黄小璨用户ID'])}
    if private.get('customer_mapping_status')!='resolved' or len(ids)!=1:return data
    uid=next(iter(ids));account=(sources.get('accounts') or {}).get(uid) or {}
    if not account:return data
    if evidence is None:
        ev=json.loads((Path(root)/'public/data/activation_evidence_live.json').read_text()).get('users') or {}
        if isinstance(ev,list):ev={x.get('safe_user_ref'):x for x in ev}
        evidence=ev.get(ref) or {}
    _,enriched=routine.enrich(private,evidence,sources,now=now)
    reg=at(account.get('created_at'));q=at(enriched.get('questionnaire_recorded_at'));anchor=reg or q
    if not anchor:return data
    finish=anchor+timedelta(days=7);source=at(sources.get('as_of'))
    observed=[x if isinstance(x,datetime) else at(x) for x in (sources.get('user_message_times') or {}).get(uid) or []]
    conversations=[x for x in observed if x and anchor<=x<min(finish,now+timedelta(microseconds=1))]
    # A0 is a real service node only when absence of observed use is backed by
    # a complete, current source and a verified entry. Otherwise retain the
    # original blocked B0 path; never turn a stale snapshot into a sendable A0.
    a0_ready=(branch:=data.get('strategy_branch'))=='opened_unused' and now<finish and not q and bool(entry) and bool(source) and sources.get('complete') is True and 'user_message_times' in sources and source>=anchor and timedelta(0)<=now-source<=timedelta(hours=1)
    new_policy=data.get('policy_version')=='hxc-lifecycle.v4'
    if new_policy and now-anchor<timedelta(days=1) and branch!='d7_review':
        return data  # Keep first-day service with Mirror for exact accounts too.
    node='D7:summary' if branch=='d7_review' else 'C:experience_followup' if conversations else 'B0' if q or branch=='opened_unused' and not a0_ready else ('A1' if new_policy else 'A0') if a0_ready else ''
    if not node:return data
    usage_guidance=existing_usage_guidance(sources,uid,anchor,finish,now,profile.get('archive_fields'))
    complete=sources.get('complete') is True and 'user_message_times' in sources and bool(source and source>=anchor and source<=now)
    context={'node':node,'evaluation_at':now.isoformat(),'source_as_of':sources.get('as_of'),'display_day':min(7,max(1,int((now-anchor).total_seconds()//86400)+1)),
       'anchor_at':anchor.isoformat(),'window_end':finish.isoformat(),
       'user_states':{'account_registered':bool(reg),'questionnaire':'submitted' if q else 'unknown','usage':'conversation_observed' if conversations else 'no_conversation_in_snapshot' if complete else 'unknown'},
       'usage':{'started_chat':bool(conversations),'source_complete':complete,'feature_states':usage_guidance['feature_states'],'existing_usage_guidance':usage_guidance},
       'blockers':[],'writing_card':{'服务':'第七天阶段总结' if node=='D7:summary' else '问卷待核与黄小璨价值说明' if node in {'A0','A1'} else '首次认识对话' if node=='B0' else '前七天使用回访','资料截至':sources.get('as_of'),'主线':'从统一档案的确认状态读取，未确认不说已锁定','功能使用':usage_guidance,'回顾窗口':{'开始':anchor.isoformat(),'结束':min(finish,source or now).isoformat()},'发送记录':'既有来源非完整节点账本；交接前核对，不把草稿当发送'}}
    if node in {'A0','A1'}:
        context['writing_card']['问卷状态']='覆盖来源未见提交或归属待核；只可说“如果还没填”，问卷不是账号使用前置。'
        if not entry:context['blockers'].append('缺已核准的黄小璨首页入口')
        data['specific_help']='先用老黄确认的一手思考或知识库说明为什么值得开始用黄小璨，再条件式邀请补问卷以便按本人方向提供后续服务。'
    if node=='D7:summary':
        context['writing_card']['总结范围']='只总结注册起完整七天；未交流则按问卷说明了解到什么，不编交流、成果或学习总结已发布，不承诺未核实的长期免费服务。'
    if entry:context['writing_card']['本轮入口']=entry
    if not conversations and (not source or now-source>timedelta(hours=1)):context['blockers'].append('未对话来源已过时，执行前刷新')
    if node=='D7:summary' and (now<finish or not source or source<finish):context['blockers'].append('尚未覆盖完整七天，仅预览；执行前补齐')
    if private.get('operational_excluded'):context['blockers'].append('档案标记不允许触达')
    if node=='D7:summary':
        data['specific_help']='基于前七天问卷与真实交流，总结了解到的用户背景和关注点，说明还需确认的重点，以及后续可以怎样支持；未交流不编回顾。'
        data['onboard_status_source']='verified'
    profile['newcomer_sop']=context
    return dict(data,profile_context=profile)
