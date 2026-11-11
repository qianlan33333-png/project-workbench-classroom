"""Build the directory view from this collection's fresh local archive."""
import hashlib
import importlib.util
import json
from pathlib import Path


def profile_rows(root, users, contexts, links, snapshot, assets):
    from archive_browser_projection import project
    from operator_contact_view import _contacts, source_signature
    from routine_read_model import select_source

    hxc_path = root / 'raw/hxc-latest.json'
    contacts, observed_at = _contacts(str(hxc_path), source_signature(hxc_path))
    approval = assets.get('var/hxc-profile-merge-approvals.json') or {}
    approved = (set(approval.get('workbench_ref_sha256') or [])
                if approval.get('status') == 'approved' and approval.get('rule_version') == 'hxc-profile-merge.v1'
                else set())
    assessments = json.loads((root / 'original/var/private-data/questionnaire-assessment.json').read_text())['items']
    exact_sources = {target: source for source, target in links.items()}

    def append(public):
        ref = public['safe_user_ref']
        private = users[ref]
        source = exact_sources.get(ref)
        relation = None
        context_rows = contexts.get(ref, [])
        if source and len(context_rows) == 1:
            context = {key: context_rows[0].get(key) for key in ('profile_facts', 'focuses')}
            relation = {'mapped': True, 'merge_approved': hashlib.sha256(ref.encode()).hexdigest() in approved,
                        'merge_rule_version': 'hxc-profile-merge.v1', 'context': context}
        account = str(private.get('黄小璨用户ID') or '')
        phones = contacts.get(account, set()) if private.get('customer_mapping_status') == 'resolved' else set()
        phone = next(iter(phones), '') if len(phones) == 1 else ''
        public['operator_contact'] = {'phone': phone, 'observed_at': observed_at if phones else '',
                                     'status': 'available' if phone else 'conflict' if len(phones) > 1 else 'missing'}
        assessment = assessments.get(source) or {}
        listing = project(public)
        fields = (
            'safe_user_ref', '企微昵称', '企微首次添加时间', '黄小璨昵称', 'CRM展示名', '源姓名（昵称）',
            'operator_contact', '_hxcSummary', '_wecomInteraction', '_hxcCoachInteraction',
            'entitlement_status', 'service_relationship_tier', 'active_product_keys',
            '_orderSummary', '身份阶段', '行业', '收入/营收阶段', '收入阶段', '业务形态', '业务阶段',
            '业务焦点', '核心痛苦场景', '当前需求', '_profileFactProvenance',
            '_conversationFieldCandidates', '_profileFieldCandidates', 'operating_growth', 'product_fit',
            '记忆摘要', '最近一次问题', '诊断问题',
        )
        person = {key: public[key] for key in fields if key in public}
        person['order_history'] = (public.get('order_history') or [])[:20]
        private['_localProfileInput'] = {
            'position': len(positions), 'search': json.loads(listing['search'] or '[]'),
            'listing': {key: value for key, value in json.loads(listing['body']).items()
                        if key in ('企微昵称', '黄小璨昵称', 'CRM展示名', '源姓名（昵称）')},
            'user': person, 'snapshot': snapshot, 'relationship': relation,
            'assessment': assessment.get('assessment'),
        }
        private['_localProfileEvidence'] = profile_evidence(private)
        positions.append(ref)

    positions = []
    select_source(root / 'original/public/data/canonical_users_live.json', {'users': append})
    return positions


def profile_evidence(user):
    records = user.get('_warehouseSourceRecords') or {}
    rows = []

    def add(channel, ref, at, value):
        if ref and at and isinstance(value, str) and value.strip():
            rows.append({'ref': channel + ':' + str(ref), 'at': str(at), 'text': value.strip()[:800]})

    if not (user.get('_customerIdentityConflict') or user.get('identity_conflict')):
        for submission in (records.get('questionnaire_submissions') or [])[:12]:
            if submission.get('identity_scope_status') == '身份待核':
                continue
            answers = []
            for answer in (submission.get('answers') or [])[:24]:
                value = answer.get('text_value') or '、'.join(answer.get('selected_option_texts_snapshot') or [])
                if value:
                    answers.append(str(answer.get('question_title_snapshot') or '') + '：' + value)
            add('CRM问卷', submission.get('record_ref'), submission.get('observed_at'), '\n'.join(answers))
        for message in (records.get('chat_records') or [])[:40]:
            if message.get('direction') == '用户消息':
                add('企微', message.get('record_ref'), message.get('observed_at'), message.get('content'))
        if user.get('customer_mapping_status') == 'resolved' and len(user.get('_hxcAccountIds') or []) == 1:
            for message in user.get('_hxcRecentUserMessages') or []:
                add('黄小璨', message.get('message_id'), message.get('time'), message.get('text'))

    # Only customer statements tied to this exact customer enter the expert.
    latest = sorted(rows, key=lambda row: (row['at'], row['ref']), reverse=True)
    unique = {}
    for row in latest:
        unique.setdefault(row['ref'], row)
    return list(unique.values())[:8]


def render(module, rows):
    spec = importlib.util.spec_from_file_location('profile_directory', module)
    directory = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(directory)
    for row in rows:
        user, snapshot = row['user'], row['snapshot']
        view = directory.project_profile(
            {'user': user, 'resolved_ref': user['safe_user_ref'], 'snapshot_id': snapshot},
            {'profile-overrides': {}, 'conversation-intelligence': {}, 'operator-followup': {'current': {
                'current_need': user.get('当前需求'), 'pain_point': user.get('核心痛苦场景'),
                'business_focus': user.get('业务焦点'),
             }},
             'hxc-unified/user': row['relationship'],
             'fde-campaign-assessment': {'assessment': row['assessment'], 'snapshot_id': snapshot}
             if row['assessment'] else None}, archive='工作台发布档案')
        yield {'id': user['safe_user_ref'], 'fields': {
            'position': row['position'], 'search': row['search'], 'nickname': directory.nickname(row['listing']),
            'detailNickname': view['nickname'], 'groups': view['groups'],
            'missingSources': view['missingSources'], 'sourceSnapshot': snapshot,
        }}


if __name__ == '__main__':
    import sys
    print(json.dumps(list(render(Path(sys.argv[1]), json.load(sys.stdin))), ensure_ascii=False))
