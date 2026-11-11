import ast,contextlib,io,json,re,sqlite3,sys,tempfile,types,unittest
from pathlib import Path
from datetime import datetime,date,timedelta,timezone
from unittest.mock import patch
sys.path.insert(0,str(Path(__file__).parent))
from source_common import original_aliases,rights_fingerprint,fact_reviews_fingerprint,contact_prohibited
from original.hxc_communication_context import blocks,handoff

class ReadOnlyFinalGateTests(unittest.TestCase):
    def run_gate(self,*,history=None,holds=None,identity='10',binding_error=False,aliases=None,current=None,facts=None,page_patch=None,report=None,material=None,action_logs=None,head_watermark='1'):
        ref='usr_0123456789ab';old='usr_111111111111'
        item={'ref':ref,'customer_id':'10','slot':'slot','product':'hxc','matter':'hxc-service','aliases':aliases or[ref],'rights':{},'fact_reviews':fact_reviews_fingerprint({},[ref])}
        snapshot={'mapped':True,'archive_additions':{},'context':{}}
        if report:
            snapshot['context']['growth_reports']=[report]
            item.update(service_binding={'fixture':'report'},report={k:report[k]for k in ('report_ref','version','content_digest')},page='/pages/outcome/outcome?id=a',body_sha256='bodyhash')
        if binding_error:item['service_binding']={'fixture':'changed'}
        def read_index(db,refs,now,**kwargs):
            self.assertEqual(db.execute('PRAGMA query_only').fetchone()[0],1)
            return {r:history[r]for r in refs if r in(history or{})}
        def verify(*args,**kw):
            if binding_error:raise ValueError('guidance_changed')
        source='relusr_v1.'+'a'*24
        page={'watermark':'1','data_as_of':datetime.now(timezone.utc).isoformat(),'ok':True,'contract_version':'relationship.context-all.v1','publication_state':'current_projection','resource':'context','total':1,'items':[{'safe_user_ref':source,**snapshot['context']}]}
        page.update(page_patch or{})
        def get_json(*args,**kwargs):
            if args[2]['resource']=='sync-status':return {'resources':[{'resource':'context-all','status':'published','contract_version':'relationship.context-all.v1','watermark':head_watermark,'data_as_of':page['data_as_of']}]}
            self.assertEqual(args[2]['safe_user_ref'],source)
            self.assertEqual(args[2]['limit'],2)
            return page
        modules={
            'sku99_ledger':types.SimpleNamespace(copy_index=read_index),
            'hxc_communication_context':types.SimpleNamespace(open_records=lambda db:holds or[],blocks=blocks,handoff=handoff),
            'agent_user_scheduler':types.SimpleNamespace(safe_ref=lambda x:x),
            'routine_read_model':types.SimpleNamespace(select_source=lambda path,callbacks:callbacks['users']({'safe_user_ref':ref,'customer_id':identity,**(current or{})})),
            'hxc_unified_snapshot':types.SimpleNamespace(_brief_context=lambda v:v,_approved_merge=lambda ref:True,HXC_REF=re.compile(r'^relusr_v1\.[A-Za-z0-9_-]{24}$')),
            'authoritative_archive_links':types.SimpleNamespace(read_links=lambda *a:[{'mapping_status':'exact','workbench_user_ref':ref,'safe_user_ref':source,'_db_safe_user_ref':source}]),
            'user_reference':types.SimpleNamespace(current_archive=lambda:{},resolve_reference=lambda ref,archive:ref),
            'hxc_profile_archive_merge':types.SimpleNamespace(approved_hxc_archive_fields=lambda v:{}),
            'hxc_sync_bounds':types.SimpleNamespace(Budget=lambda **kw:None,get_json=get_json),
            'hxc_report_delivery':types.SimpleNamespace(material=lambda *a,**k:material),
            'hxc_service_binding':types.SimpleNamespace(verify=verify,capture=lambda *a,**k:None),
        }
        with tempfile.TemporaryDirectory()as directory:
            root=Path(directory);(root/'var').mkdir()
            with contextlib.closing(sqlite3.connect(root/'var/daru.sqlite'))as db:
                db.execute('CREATE TABLE hxc_service_fact_reviews(safe_user_ref,kind,payload_json)')
                for kind,value in(facts or{}).items():db.execute('INSERT INTO hxc_service_fact_reviews VALUES(?,?,?)',(ref,kind,json.dumps(value)))
                db.execute('CREATE TABLE action_logs(canonical_user_id,todo_type,status,result_json,action_type,created_at,action_id)')
                for row in action_logs or[]:db.execute('INSERT INTO action_logs VALUES(?,?,?,?,?,?,?)',row)
                db.commit()
            (root/'env').write_text('HXC_PRIVATE_DOMAIN_API_TOKEN=fixture')
            code=Path(__file__).with_name('source_read_gate.py').read_text().replace("root=Path('/opt/daru-v3-warehouse-candidate')",'root=Path('+repr(directory)+')').replace("Path('/etc/daru-ops-console.env')","Path("+repr(str(root/'env'))+")")
            output=io.StringIO()
            with patch.dict(sys.modules,modules),patch.object(sys,'stdin',io.StringIO(json.dumps([item]))),contextlib.redirect_stdout(output):
                exec(compile(code,'readonly-final-gate','exec'),{'original_aliases':original_aliases,'fact_reviews_fingerprint':fact_reviews_fingerprint,'contact_prohibited':contact_prohibited})
            return json.loads(output.getvalue())['ready']

    def test_old_consumer_status_is_not_a_prerequisite_for_independent_workbench(self):
        # There is deliberately no consumer status file or bridge flag.
        self.assertTrue(self.run_gate())
    def test_exact_alias_unknown_and_same_slot_are_checked(self):
        old='usr_111111111111'
        self.assertFalse(self.run_gate(aliases=['usr_0123456789ab',old],history={old:[{'slot_key':'other','handoff':'plan','state':'unknown'}]}))
        self.assertFalse(self.run_gate(history={'usr_0123456789ab':[{'slot_key':'slot','state':'sent','sent_at':'2026-01-01T00:00:00+08:00'}]}))
        self.assertFalse(self.run_gate(identity='20'))
    def test_scoped_human_takeover_and_changed_guidance(self):
        hold={'workbench_user_ref':'usr_0123456789ab','handoff_context':{'handoff_scope':'product','product_key':'other'}}
        self.assertTrue(self.run_gate(holds=[hold]))
        self.assertTrue(self.run_gate(holds=[hold],current={'manual_handoff':True,'handoff_context':hold['handoff_context']}))
        hold['handoff_context']['product_key']='hxc';self.assertFalse(self.run_gate(holds=[hold]))
        hold['handoff_context']={'handoff_scope':'matter','matter_ref':'other'};self.assertTrue(self.run_gate(holds=[hold]))
        self.assertFalse(self.run_gate(binding_error=True))
    def test_distinct_original_alias_holds_do_not_cancel_each_other(self):
        old='usr_111111111111';ref='usr_0123456789ab'
        rows=[(old,'todo','manual_handoff','{}','copy_review','2026-11-11T08:00:00','a'),(ref,'todo','reviewed','{}','copy_review','2026-11-11T09:00:00','b')]
        self.assertFalse(self.run_gate(aliases=[old,ref],action_logs=rows))
    def test_late_opt_out_risk_and_fact_review_stop_the_same_batch(self):
        for current in ({'allocation_safety':{'is_do_not_contact':True}},{'high_risk':True},{'operational_excluded':True},{'manual_handoff':True}):
            with self.subTest(current=current):self.assertFalse(self.run_gate(current=current))
        self.assertFalse(self.run_gate(facts={'activation':{'decision':'defer'}}))
    def test_stale_source_and_changed_report_do_not_pass(self):
        with self.assertRaisesRegex(ValueError,'current_hxc_context_unavailable'):self.run_gate(page_patch={'publication_state':'stale'})
        with self.assertRaisesRegex(ValueError,'current_hxc_context_unavailable'):self.run_gate(page_patch={'total':2,'next_cursor':'more'})
        with self.assertRaisesRegex(ValueError,'current_hxc_context_unavailable'):self.run_gate(page_patch={'items':[{'safe_user_ref':'another'}]})
        with self.assertRaisesRegex(ValueError,'current_hxc_context_stale'):self.run_gate(page_patch={'data_as_of':'2020-01-01T00:00:00Z'})
        with self.assertRaisesRegex(ValueError,'current_hxc_context_changed'):self.run_gate(head_watermark='2')
        report={'report_ref':'report','version':'v1','content_digest':'digest'}
        material={'base_target_path':'/pages/outcome/outcome?id=a','body_sha256':'bodyhash'}
        self.assertTrue(self.run_gate(report=report,material=material))
        self.assertFalse(self.run_gate(report=report,material={**material,'body_sha256':'changed'}))
        self.assertFalse(self.run_gate(report=report,material=None))
    def test_large_final_batch_reuses_paged_context_read_instead_of_per_person_gets(self):
        wanted={str(i)for i in range(5000)};calls=[];published_at=datetime.now(timezone.utc).isoformat()
        def get_json(url,token,params,**kw):
            calls.append(dict(params))
            if params['resource']=='sync-status':return {'resources':[{'resource':'context-all','status':'published','contract_version':'relationship.context-all.v1','watermark':'1','data_as_of':published_at}]}
            offset=int(params.get('cursor')or 0);end=min(5000,offset+params['limit'])
            return {'watermark':'1','data_as_of':published_at,'publication_state':'current_projection','items':[{'safe_user_ref':str(i)}for i in range(offset,end)],'next_cursor':str(end)if end<5000 else ''}
        def batches(getter,resource,params,page,budget):
            while True:
                yield page['items'],{}
                if not page['next_cursor']:break
                params={**params,'cursor':page['next_cursor']};page=getter(params)
        module=types.SimpleNamespace(resource_params=lambda resource:{'resource':'context','contract_version':'relationship.context-all.v1','limit':100},_fetch_page=lambda getter,params,budget:(getter(params),params),_batches=batches)
        tree=ast.parse(Path(__file__).with_name('source_read_gate.py').read_text())
        function=next(n for n in tree.body if isinstance(n,ast.FunctionDef)and n.name=='read_current_contexts')
        scope={'datetime':datetime,'now':datetime.now(timezone.utc),'get_json':get_json,'budget':object(),'values':{'HXC_PRIVATE_DOMAIN_API_TOKEN':'fixture'}}
        with patch.dict(sys.modules,{'hxc_unified_sync_store':module}):
            exec(compile(ast.Module(body=[function],type_ignores=[]),'gate-batch','exec'),scope)
            self.assertEqual(set(scope['read_current_contexts'](wanted)),wanted)
        self.assertEqual(len(calls),51)
        self.assertTrue(all('safe_user_ref'not in params for params in calls))

    def test_rights_fingerprint_ignores_usage_and_row_order_but_detects_grants_and_revocation(self):
        rows={'subscriptions':[{'user_id':10,'tier':'premium','status':'active','expires_at':datetime(2027,1,1),'peer_monthly_used':1}], 'memberships':[{'user_id':10,'status':'active','end_date':date(2027,1,1)}]}
        wire=json.loads(json.dumps(rows,default=lambda v:v.isoformat()))
        self.assertEqual(rights_fingerprint(rows),rights_fingerprint(wire))
        wire['subscriptions'][0]['peer_monthly_used']=99
        self.assertEqual(rights_fingerprint(rows),rights_fingerprint(wire))
        wire['subscriptions'][0]['created_at']='2026-01-01T00:00:00'
        self.assertNotEqual(rights_fingerprint(rows),rights_fingerprint(wire))
        wire['subscriptions'][0]['status']='cancelled'
        self.assertNotEqual(rights_fingerprint(rows),rights_fingerprint(wire))
        wire={'memberships':[]}
        self.assertNotEqual(rights_fingerprint(rows),rights_fingerprint(wire))

if __name__=='__main__':unittest.main()
