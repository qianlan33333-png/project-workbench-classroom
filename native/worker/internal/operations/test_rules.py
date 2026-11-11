import importlib.util
import unittest
import json,os
from pathlib import Path
spec=importlib.util.spec_from_file_location('operations_rules',Path(__file__).with_name('rules.py'))
rules=importlib.util.module_from_spec(spec);spec.loader.exec_module(rules)

class OriginalRulesTests(unittest.TestCase):
    def setUp(self):
        self.ref='usr_0123456789ab'
        self.user={'safe_user_ref':self.ref,'identity_resolved':True}
        self.policy={'id':'p','fields':{'key':'theme:test','status':'published','expires_at':'2026-11-09T12:00:00+08:00','audience_filter':{'population':'canonical_archive','exact_safe_user_refs':[self.ref]}}}
        self.history=[{'id':self.ref,'fields':{'complete':True,'actions':[]}}]
    def selected(self,user=None,policy=None,history=None,keys=('theme:test',)):
        return rules.select([{'id':self.ref,'fields':user or self.user}],[policy or self.policy],history if history is not None else self.history,'2026-11-08T12:00:00+08:00',keys)[0]['fields']
    def test_explicit_original_audience_and_no_automatic_priority(self):
        self.assertTrue(self.selected()['selected'])
        out=self.selected(keys=());self.assertFalse(out['selected']);self.assertIn('theme:test',out['available_strategies'])
        user={**self.user,'safe_user_ref':'usr_0123456789ab','strategy_signals':{}}
        policy={'id':'p','fields':{**self.policy['fields'],'audience_filter':{'population':'canonical_archive','audience_segments':['observed_campaign']}}}
        self.assertFalse(self.selected(user,policy)['selected'])
        user['strategy_signals']['observed_campaign']=True
        self.assertTrue(self.selected(user,policy)['selected'])
    def test_expired_or_unverified_campaign_never_enters_generation(self):
        for expiry,reason in ((None,'strategy_validity_unverified'),('2026-11-07T12:00:00+08:00','strategy_expired')):
            policy={'id':'p','fields':{**self.policy['fields'],'expires_at':expiry}}
            out=self.selected(policy=policy);self.assertFalse(out['selected']);self.assertIn(reason,str(out))
    def test_original_audience_projection_preserves_need_readiness_and_service_only(self):
        source={'safe_user_ref':self.ref,'customer_mapping_status':'resolved','_identityMatch':{'confidence':'high','matched_by':'verified_identity'},'has_current_need':True,'profile':{'industry':'fixture'},'allocation_safety':{}}
        user={**self.user,'audience_source':source}
        policy={'id':'p','fields':{**self.policy['fields'],'audience_filter':{'population':'canonical_archive','exact_safe_user_refs':[self.ref],'need_state':'present','service_readiness_tiers':['personalized_ready']}}}
        self.assertTrue(self.selected(user,policy)['selected'])
        policy['fields']['audience_filter']['need_state']='missing';self.assertFalse(self.selected(user,policy)['selected'])
        policy['fields']['audience_filter']['need_state']='present';source['allocation_safety']['contact_policy']='service_only'
        self.assertIn('service_only_suppressed',str(self.selected(user,policy)))
    def test_nested_contact_policy_and_operational_exclusion(self):
        for location in ('safety','allocation_safety'):
            for flag in ({'do_not_contact':True},{'contact_preference':'do_not_contact'},{'high_risk':True}):
                with self.subTest(location=location,flag=flag):self.assertFalse(self.selected({**self.user,location:flag})['selected'])
        self.assertFalse(self.selected({**self.user,'operational_excluded':True})['selected'])
    def test_existing_original_action_is_preserved(self):
        for state in ('pending','done','rejected','manual_handoff','blocked'):
            history=[{'id':self.ref,'fields':{'complete':True,'actions':[{'action_id':'original-id','week':'2026-W45','product_key':'hxc','status':state,'owner':'original-owner'}]}}]
            out=self.selected(history=history)
            self.assertFalse(out['selected']);self.assertEqual(out['exclusions'][0]['reason'],'original_action_preserved')
        self.assertEqual(self.selected(history=[{'id':self.ref,'fields':{'complete':True}}])['reason'],'execution_history_incomplete')
    def test_scoped_handoff_preserves_unrelated_product(self):
        user={**self.user,'allocation_safety':{'manual_handoff':True},'handoff_context':{'handoff_scope':'product','product_key':'unrelated'}}
        self.history[0]['fields']['handoffs']=[{'workbench_user_ref':self.ref,'status':'manual_handoff','product_service_key':'unrelated','handoff_context':dict(user['handoff_context'])}]
        self.assertTrue(self.selected(user)['selected'])
        user['handoff_context']['product_key']='hxc';self.assertFalse(self.selected(user)['selected'])
    def test_clean_retry_uses_frozen_clock_even_with_empty_original_time(self):
        for stamp in (None,'','2024-01-01T00:00:00Z'):
            row={'id':self.ref,'fields':{**self.user,'generated_at':stamp}}
            first=rules.clean([row],'2026-11-08T12:00:00+08:00')
            second=rules.clean([row],'2026-11-08T12:00:00+08:00')
            self.assertEqual(first,second);self.assertEqual(first[0]['fields']['archive']['generated_at'],'2026-11-08T12:00:00+08:00')
    def test_clean_preserves_confirmed_facts_and_rejects_wrong_identity(self):
        raw={'safe_user_ref':'usr_0123456789ab','facts':[{'field':'current_need','value':'原始需求','state':'user_confirmed'}]}
        row={'id':raw['safe_user_ref'],'fields':{'raw':raw}}
        result=rules.clean([row],"2026-11-08T12:00:00+08:00")[0]['fields']
        self.assertEqual(result['raw'],raw)
        self.assertEqual(result['archive']['needs']['current_need'],'原始需求')
        with self.assertRaises(ValueError):rules.clean([{'id':'other','fields':raw}],"2026-11-08T12:00:00+08:00")
    def test_missing_history_never_means_contact_allowed(self):
        row={'id':'usr_0123456789ab','fields':{'safe_user_ref':'usr_0123456789ab'}}
        policy={'id':'p','fields':{'key':'theme:test','status':'published'}}
        out=rules.select([row],[policy],[],'2026-11-08T12:00:00+08:00')[0]['fields']
        self.assertFalse(out['selected']);self.assertEqual(out['reason'],'contact_history_incomplete')
    def test_original_priority_and_cooldown_are_not_reimplemented(self):
        candidates=[{'safe_user_ref':'usr_0123456789ab','action':action,'product_service_key':'hxc'} for action in ['content_push','weekly_review']]
        self.assertEqual(rules.choose_actions(candidates)[0]['action'],'weekly_review')
        now=rules.datetime.fromisoformat('2026-11-08T12:00:00+08:00')
        self.assertFalse(rules.potential_cooldown({'last_user_reply_at':'2026-11-07T12:00:00+08:00'},now)['eligible'])

class OriginalQueueBusinessTests(unittest.TestCase):
    setUp = OriginalRulesTests.setUp
    selected = OriginalRulesTests.selected
    def manual(self, key='manual:potential_followup', service='relationship'):
        policy={'id':'p','fields':{'key':key,'status':'published','audience_filter':{'population':'canonical_archive','audience_segments':['potential' if 'potential' in key else 'intent']},'generation_config':{'skill_key':'personal_followup'}}}
        todo={'safe_user_ref':self.ref,'type':'potential_review' if 'potential' in key else 'intent_followup','product_service_key':service,'why_now':'fixture current request','evidence_refs':['fixture:message'],'last_outreach_at':'2026-10-01T12:00:00+08:00'}
        user={**self.user,'strategy_signals':{'potential':True,'intent':True},'priority_todos_complete':True,'priority_todos':[todo]}
        return user,policy
    def test_evergreen_manual_reuses_real_todo_priority_and_cooldown(self):
        for key,priority in [('manual:potential_followup',70),('manual:intent_sales',80)]:
            user,policy=self.manual(key)
            out=self.selected(user,policy,keys=())
            self.assertTrue(out['selected'],out);self.assertEqual(out['priority'],priority)
            self.assertEqual(out['actions'][0]['generation']['skill_key'],'personal_followup')
        user,policy=self.manual();user['last_user_reply_at']='2026-11-07T12:00:00+08:00'
        out=self.selected(user,policy,keys=());self.assertFalse(out['selected']);self.assertIn('replied_cooling_30d',str(out))
    def test_manual_needs_current_todo_and_preserves_product_retirement(self):
        user,policy=self.manual();user['priority_todos']=[]
        self.assertIn('current_priority_todo_required',str(self.selected(user,policy,keys=())))
        user,policy=self.manual();user.pop('priority_todos_complete')
        self.assertIn('priority_todo_source_incomplete',str(self.selected(user,policy,keys=())))
        user,policy=self.manual(service='hxc')
        self.assertFalse(self.selected(user,policy,keys=())['selected'])
        user,policy=self.manual();user['priority_todos'][0].pop('last_outreach_at')
        self.assertIn('history_unknown_human_review',str(self.selected(user,policy,keys=())))
    def test_current_handoff_blocks_manual_queue_without_segment_preview(self):
        for key in ('manual:potential_followup','manual:intent_sales'):
            user,policy=self.manual(key);user.pop('strategy_signals')
            self.assertTrue(self.selected(user,policy,keys=())['selected'])
            user['priority_todos'].append({'safe_user_ref':self.ref,'type':'handoff','why_now':'fixture unresolved human request','evidence_refs':['fixture:hold']})
            out=self.selected(user,policy,keys=());self.assertFalse(out['selected']);self.assertEqual(out['internal_actions'][0]['action'],'manual_handoff')
    def test_all_original_todos_are_considered_and_multiple_actions_stay_explicit(self):
        user,policy=self.manual();user['priority_todos'][0]['last_user_reply_at']='2026-11-07T12:00:00+08:00'
        user['priority_todos'].append({'safe_user_ref':self.ref,'type':'potential_review','product_service_key':'fde','why_now':'fixture other service','last_outreach_at':'2026-10-01T12:00:00+08:00'})
        out=self.selected(user,policy,keys=());self.assertTrue(out['selected']);self.assertEqual(out['actions'][0]['product_service_key'],'fde')
        user['priority_todos'][0].pop('last_user_reply_at');out=self.selected(user,policy,keys=())
        self.assertFalse(out['selected']);self.assertEqual(len(out['actions']),2);self.assertEqual(out['reason'],'strategy_coordination_required')
    def test_proposed_handoff_scope_does_not_replace_persisted_human_record(self):
        user,policy=self.manual('manual:intent_sales')
        user['priority_todos'].append({'safe_user_ref':self.ref,'type':'handoff','product_service_key':'human-case','why_now':'fixture hold','handoff_context':{'handoff_scope':'product','product_key':'another'}})
        self.assertFalse(self.selected(user,policy,keys=())['selected'])
        self.history[0]['fields']['handoffs']=[{'workbench_user_ref':self.ref,'status':'manual_handoff','product_service_key':'human-case','handoff_context':{'handoff_scope':'product','product_key':'another'}}]
        self.assertTrue(self.selected(user,policy,keys=())['selected'])

    def test_todo_cannot_be_rebound_to_another_person(self):
        user,policy=self.manual();user['priority_todos'][0]['safe_user_ref']='usr_111111111111'
        out=self.selected(user,policy,keys=());self.assertFalse(out['selected']);self.assertEqual(out['reason'],'priority_todo_identity_mismatch')
        user['priority_todos'][0].pop('safe_user_ref');self.assertEqual(self.selected(user,policy,keys=())['reason'],'priority_todo_identity_mismatch')
    def test_explicit_selection_does_not_revive_retired_services(self):
        for key in ['new_user_onboarding','weekly_review','content_push','subscription_activation','tier:entry_active']:
            policy={'id':'p','fields':{'key':key,'status':'published','audience_filter':{'population':'canonical_archive'}}}
            out=self.selected(policy=policy,keys=(key,));self.assertFalse(out['selected']);self.assertIn('legacy_service_strategy_retired',str(out))
    def test_conflicting_human_work_keeps_original_priority(self):
        user,intent=self.manual('manual:intent_sales');_,potential=self.manual()
        user['priority_todos'].append({'safe_user_ref':self.ref,'type':'potential_review','product_service_key':'relationship','why_now':'fixture','evidence_refs':['fixture:2']})
        out=rules.select([{'id':self.ref,'fields':user}],[intent,potential],self.history,'2026-11-08T12:00:00+08:00')[0]['fields']
        self.assertEqual(out['actions'][0]['action'],'intent_followup');self.assertEqual(out['priority'],80)

class PublishedStrategyTests(unittest.TestCase):
    def published(self):
        path=os.environ.get('WORKBENCH_RULE_TEST_STRATEGIES')
        if not path:self.skipTest('original private strategy export not configured')
        policies=json.loads(Path(path).read_text());self.assertEqual(len(policies),42)
        return policies

    def test_all_original_campaigns_and_retired_routes_keep_their_business_status(self):
        counts={'campaign':0,'retired':0}
        for key,policy in self.published().items():
            if key.startswith(('hxc:','manual:')):continue
            group='campaign'if key.startswith('theme:')else'retired';counts[group]+=1
            audience=policy.get('audience_filter')or{}
            ref=(audience.get('exact_safe_user_refs')or['usr_0123456789ab'])[0]
            user={'safe_user_ref':ref,'canonical_user_id':'fixture-customer','identity_resolved':True,
                  'service_relationship_tier':(audience.get('service_relationship_tiers')or[''])[0],
                  'service_readiness_tier':(audience.get('service_readiness_tiers')or[''])[0]}
            if audience.get('need_state')=='present':user['current_need']='fixture explicit need'
            with self.subTest(strategy=key):
                output=rules.select([{'id':ref,'fields':user}],[{'id':'policy','fields':policy}],
                    [{'id':ref,'fields':{'complete':True,'actions':[]}}],'2026-11-11T12:00:00+08:00',[key])[0]['fields']
                self.assertFalse(output['selected'])
                expected='strategy_validity_unverified'if group=='campaign'else'legacy_service_strategy_retired'
                self.assertIn(expected,str(output['exclusions']))
        self.assertEqual(counts,{'campaign':22,'retired':12})

    def test_each_original_live_or_internal_route_and_its_missing_and_conflicting_input(self):
        policies=self.published();fixture=FinalHXCAdmissionTests();manual=OriginalQueueBusinessTests();manual.setUp()
        for key in [k for k in policies if k.startswith(('hxc:','manual:'))]:
            with self.subTest(strategy=key):
                if key.startswith('manual:'):
                    user,_=manual.manual(key);records=[{'id':'policy','fields':policies[key]}]
                    history={'id':manual.ref,'fields':{'complete':True,'actions':[]}}
                else:
                    user,records,history=fixture.fixture();records[0]={'id':'policy','fields':policies[key]}
                    sources=user['hxc_context']['sources'];uid='fixture-account'
                    if key in ('hxc:first_profile','hxc:known_no_rights','hxc:onboarding'):
                        user.update(_hxcAccountIds=[],service_relationship_tier='profile_no_current_service',_hxcSummary={'account_present':False,'service_status':'none'})
                        for name in ('accounts','metrics','current_entitlements_by_user'):sources[name]={}
                        if key=='hxc:known_no_rights':user['行业']='fixture historical background'
                        if key=='hxc:onboarding':
                            user.update(企微首次添加时间='2026-11-01T00:00:00+08:00',企微昵称='测试')
                            user['order_history']=[{'product_code':'31314141','is_paid':True,'is_refunded':False,'paid_at':'2026-11-05T00:00:00+08:00','order_ref':'fixture:order'}]
                    elif key=='hxc:expiry':
                        user['_hxcSummary']['service_expires_at']='2026-11-10T00:00:00+08:00'
                        sources['current_entitlements_by_user'][uid]['expires_at']='2026-11-10T00:00:00+08:00'
                    elif key=='hxc:recovery':
                        user['service_relationship_tier']='entry_expired'
                        user['_hxcSummary'].update(service_status='expired',service_expires_at='2026-10-01T00:00:00+08:00')
                        sources['current_entitlements_by_user'][uid].update(active=False,status='expired',expires_at='2026-10-01T00:00:00+08:00')
                    sources['users']={user['safe_user_ref']:{k:v for k,v in user.items()if k!='hxc_context'}}
                ref=user['safe_user_ref']
                def select():return rules.select([{'id':ref,'fields':user}],records,[history],'2026-11-08T12:00:00+08:00',[key])[0]['fields']
                expected=key not in ('hxc:first_profile','hxc:known_no_rights','hxc:recovery')
                result=select();self.assertEqual(result['selected'],expected,result)
                if expected:self.assertEqual(result['actions'][0]['strategy_key'],key)
                history['fields']['complete']=False;self.assertFalse(select()['selected'])
                history['fields']['complete']=True
                history['fields']['handoffs']=[{'workbench_user_ref':ref,'status':'manual_handoff','handoff_context':{'handoff_scope':'global'}}]
                self.assertFalse(select()['selected'])

    def test_every_published_strategy_preserves_exclusions(self):
        path=os.environ.get('WORKBENCH_RULE_TEST_STRATEGIES')
        if not path:self.skipTest('original private strategy export not configured')
        policies=json.loads(Path(path).read_text())
        self.assertTrue(policies)
        for key,policy in policies.items():
            for blocked in ['identity','contact','history']:
                with self.subTest(strategy=key,blocked=blocked):
                    ref='usr_0123456789ab'
                    user={'safe_user_ref':ref,'identity_resolved':blocked!='identity','do_not_contact':blocked=='contact'}
                    history=[] if blocked=='history' else [{'id':ref,'fields':{'complete':True,'actions':[]}}]
                    out=rules.select([{'id':ref,'fields':user}],[{'id':'policy','fields':policy}],history,'2026-11-08T12:00:00+08:00')[0]['fields']
                    self.assertFalse(out['selected'])
                    self.assertTrue(out['reason'])



class FinalHXCAdmissionTests(unittest.TestCase):
    def test_same_person_context_can_use_frozen_history_reference_without_duplication(self):
        user,policies,history=self.fixture()
        first=self.select(user,policies,history)
        history['fields']['hxc_context']=user.pop('hxc_context')
        second=self.select(user,policies,history)
        self.assertEqual(first,second)
        history['fields']['handoffs']=[{'workbench_user_ref':user['safe_user_ref'],'status':'manual_handoff','handoff_context':{'handoff_scope':'global'},'source_ref':'fixture-original-copy-review'}]
        self.assertFalse(self.select(user,policies,history)['selected'])
    def fixture(self):
        ref='usr_0123456789ab';uid='fixture-account'
        user={'safe_user_ref':ref,'identity_resolved':True,'customer_mapping_status':'resolved','_hxcAccountIds':[uid],'source_snapshot_id':'source-one','service_relationship_tier':'entry_active','_hxcSummary':{'account_present':True,'service_status':'active','service_expires_at':'2027-01-01T00:00:00+08:00'}}
        sources={'users':{ref:dict(user)},'accounts':{uid:{'created_at':'2026-10-01T00:00:00+08:00'}},'metrics':{uid:{'activity_segment':'A'}},'histories':{},'packs':{},'as_of':'2026-11-08T11:00:00+08:00','current_entitlements_by_user':{uid:{'observed':True,'active':True,'status':'active','expires_at':'2027-01-01T00:00:00+08:00'}}}
        sources.update(complete=True,entitlement_events=[])
        for key in ('seen','reviews','sends','catalog','product_grants_by_user','user_message_times','growth_reports','correspondence','content_history_by_ref','completion_requests','entitlement_events_by_user'):sources.setdefault(key,{})
        user['hxc_context']={'complete':True,'snapshot_id':'source-one','sources':sources,'fact_reviews':{},'editorial_assignments':{},'profile':{'conversation':{'needs':{'focus':{'value':'真实用户提出的运营问题','status':'user_confirmed','evidence_refs':['fixture:message:1'],'source_at':'2026-11-07T12:00:00+08:00'}}}},'evidence':{}}
        policy={'id':'p','fields':{'key':'hxc:ongoing','status':'published','audience_filter':{'population':'canonical_archive','exact_safe_user_refs':[ref]}}}
        owner={'id':'owner-service-policy','fields':{'policy':{'legacy_product_codes':[],'legacy_mapping_reason':'fixture','expired_overrides':{},'confirmed_special_service_refs':[],'version':'fixture','source':'fixture'}}}
        inputs={'id':'original-operation-inputs','fields':{'product_service_scope_policy':{'version':'fixture','active_service_packs':{'299':'202608052341','99':'334465678','6.8':'31314141'}},'sku99_assets':{},'sku299_assets':{},'renewal_policy':{'version':'fixture','effective_at':'2026-01-01T00:00:00+08:00','renewal_url':'https://example.invalid/fixture'}}}
        history={'id':ref,'fields':{'complete':True,'actions':[],'hxc_ledger_complete':True,'copy_index':{},'legacy_successful_cycles':[],'renewal_slots':{},'previous_cycle_ends':{}}}
        return user,[policy,owner,inputs],history
    def select(self,user,policies,history):
        return rules.select([{'id':user['safe_user_ref'],'fields':user}],policies,[history],'2026-11-08T12:00:00+08:00')[0]['fields']
    def test_current_formal_service_preserves_final_material_and_binding(self):
        u,p,h=self.fixture();out=self.select(u,p,h)
        self.assertTrue(out['selected'],out)
        a=out['actions'][0];self.assertEqual(a['sku_pack_binding']['primary_identity'],'299')
        self.assertEqual(a['generation']['material']['kind'],'followup')
        self.assertEqual(a['generation']['skill_key'],a['skill_key'])
        self.assertTrue(a['generation']['decision_fingerprint'])
    def test_original_copy_slot_and_manual_defer_stop_new_copy(self):
        u,p,h=self.fixture();out=self.select(u,p,h);binding=out['actions'][0]['sku_pack_binding']
        h['fields']['copy_index']={u['safe_user_ref']:[{'run_id':'original-run','item_key':u['safe_user_ref'],'slot_key':binding['slot_key']}]}
        out=self.select(u,p,h);self.assertFalse(out['selected']);self.assertIn('original_copy_reuse_required',str(out))
        u,p,h=self.fixture();u['hxc_context']['fact_reviews']={u['safe_user_ref']:{'activation':{'decision':'defer'}}}
        self.assertFalse(self.select(u,p,h)['selected'])
    def test_incomplete_context_and_original_retirement_do_not_reopen_outbound(self):
        u,p,h=self.fixture();u.pop('hxc_context');out=self.select(u,p,h);self.assertFalse(out['selected']);self.assertIn('hxc_rule_context_incomplete',str(out))
        u,p,h=self.fixture();u['_hxcSummary']['service_status']='expired';u['hxc_context']['sources']['current_entitlements_by_user']['fixture-account'].update(active=False,status='expired',expires_at='2026-10-01T00:00:00+08:00')
        p[0]['fields']['key']='hxc:recovery';self.assertFalse(self.select(u,p,h)['selected'])



    def test_missing_internal_indexes_are_not_empty_history(self):
        u,p,h=self.fixture()
        for key in ('sends','current_entitlements_by_user','content_history_by_ref','complete'):
            value=u['hxc_context']['sources'].pop(key)
            out=self.select(u,p,h);self.assertFalse(out['selected']);self.assertIn('hxc_rule_context_incomplete',str(out))
            u['hxc_context']['sources'][key]=value
    def test_shared_catalog_must_belong_to_same_frozen_source(self):
        user,policies,history=self.fixture();user['hxc_context']['catalog_from_shared']=True
        policies[2]['fields']['shared_sources']={'source_snapshot_id':'source-one','catalog':{}}
        self.assertTrue(self.select(user,policies,history)['selected'])
        policies[2]['fields']['shared_sources']['source_snapshot_id']='another-source'
        out=self.select(user,policies,history);self.assertFalse(out['selected']);self.assertIn('hxc_shared_source_mismatch',str(out))

    def test_growth_report_material_uses_original_published_contract(self):
        import hxc_growth_report as contract
        u,p,h=self.fixture()
        content={'schema_version':contract.VERSION,'report_type':'commercial-personal-ai-growth','overview':{'priority':{'text':'本期建议先核对真实业务方向'}}}
        for name,labels in contract.LABELS.items():content[name]={key:{'name':label}for key,label in labels.items()}
        digest=contract.digest(content)
        report={'schema_version':contract.VERSION,'report_type':content['report_type'],'review_status':'accepted','report_ref':'fixture-report','version':1,'derived_not_user_fact':True,'rule_version':'fixture','content':content,'content_digest':digest,'source_at':'2026-11-07T00:00:00+08:00','weekly_selection':{'version':'growth-report-weekly-selection.v1','primary_service':'growth_report','replace_weekly_review':True,'period_start':'2026-11-02','period_end':'2026-11-08','automatic_send_allowed':False},'delivery_material':{'purpose':'growth_report','content_type':'link','content_id':'fixture-report','report_version':1,'content_digest':digest,'requires_dispatch_signature':True,'base_target_path':'/pages/outcome/outcome?id=fixture'}}
        u['hxc_context']['sources']['growth_reports']['fixture-account']=report
        out=self.select(u,p,h);self.assertTrue(out['selected'],out);self.assertEqual(out['actions'][0]['generation']['material']['purpose'],'growth_report')
    def test_catalog_json_keys_cannot_bypass_read_history(self):
        u,p,h=self.fixture();sources=u['hxc_context']['sources'];u['hxc_context']['profile']={'conversation':{'needs':{'focus':{'value':'需要运营内容','status':'user_confirmed','evidence_refs':['fixture:1'],'source_at':'2026-11-07T12:00:00+08:00'}}}}
        item={'content_type':'article','content_id':'fixture-content','published_at':'2026-11-06T12:00:00+08:00','status':'published','pushable':True,'path_status':'verified_route','base_target_path':'/fixture','content_summary':'运营方法','title':'运营方法'}
        sources['catalog']={'fixture-content':item};sources['catalog_by_key']={'bad-json-key':item}
        sources['content_history_by_ref']={u['safe_user_ref']:[['article','fixture-content']]}
        out=self.select(u,p,h);self.assertTrue(out['selected'],out);self.assertNotEqual(out['actions'][0].get('material',{}).get('content_id'),'fixture-content')

if __name__=='__main__':unittest.main()
