import sys
from pathlib import Path
import unittest
import json,tempfile
from unittest.mock import patch
sys.path.insert(0,str(Path(__file__).parent))
from source_common import original_aliases, original_reviews, alias_execution_requires_review, write_json, original_pack_redlines, generation_profile, generation_method

class OriginalIdentityHistoryTests(unittest.TestCase):
    def test_committed_merge_preserves_old_human_defer_and_history_alias(self):
        aliases=original_aliases({'new':{'customer_id':20,'_mergedCustomerIDs':[10]}},
            {'old':{'customer_id':10},'unrelated':{'customer_id':30}})
        self.assertEqual(aliases['new'],['new','old'])
        review={'decision':'defer','source_fingerprint':'original'}
        self.assertEqual(original_reviews({'old':{'entitlement':review}},aliases['new']),{'entitlement':review})
    def test_conflicting_alias_reviews_are_not_overwritten_by_iteration_order(self):
        records={'old':{'entitlement':{'decision':'defer'}},'new':{'entitlement':{'decision':'confirm'}}}
        for aliases in [['old','new'],['new','old']]:
            self.assertEqual(original_reviews(records,aliases)['entitlement']['decision'],'defer')
    def test_missing_customer_id_cannot_join_two_unrelated_people(self):
        self.assertEqual(original_aliases({'new':{}},{'old':{}}),{'new':['new']})
        for missing in ('',None,0,'0'):
            self.assertEqual(original_aliases({'new':{'customer_id':missing}},{'old':{'customer_id':missing}}),{'new':['new']})
    def test_opaque_old_slots_and_legacy_cycles_cannot_be_rekeyed_silently(self):
        for history in [{'renewal_slots':{'old':['opaque-slot']}},
                        {'copy_index':{'old':[{'state':'sent'}]}},
                        {'legacy_successful_cycles':[['old','original-date']]}]:
            self.assertTrue(alias_execution_requires_review(history,'new',['new','old']))
            self.assertFalse(alias_execution_requires_review(history,'old',['old']))
    def test_interrupted_checkpoint_never_publishes_partial_success_marker(self):
        with tempfile.TemporaryDirectory()as folder:
            path=Path(folder)/'receipt.json';write_json(path,{'complete':True,'sha256':'original'})
            def interrupted(value,stream,**kwargs):
                stream.write('{');raise OSError('injected interruption')
            with patch('source_common.json.dump',interrupted):
                with self.assertRaises(OSError):write_json(path,{'complete':True,'sha256':'changed'})
            self.assertEqual(json.loads(path.read_text())['sha256'],'original')
            write_json(path,{'complete':True,'sha256':'resumed'})
            self.assertEqual(json.loads(path.read_text())['sha256'],'resumed')
    def test_original_redlines_stay_attached_to_exact_account(self):
        assets={'var/service-packs/content-service-pack-latest.json':{'rows':[
            {'hxc_user_id':'mine','redline_flags':['original_hold'],'copy':'private'},
            {'hxc_user_id':'other','redline_flags':['not_mine']} ]}}
        packs=original_pack_redlines(assets,['mine'])
        self.assertEqual(packs['content']['rows'],[{'hxc_user_id':'mine','redline_flags':['original_hold']}])
    def test_unattributed_classification_cannot_override_dated_conversation_facts(self):
        user={'safe_user_ref':'fixture','customer_id':'10','核心痛苦场景':'unattributed inference','业务焦点':'old classification'}
        conversation={'fields':{'business_focus':{'value':'dated fact','status':'user_stated','source_at':'2026-05-01','evidence_refs':['fixture:source']}}}
        profile=generation_profile(user,conversation)
        self.assertNotIn('核心痛苦场景',profile);self.assertNotIn('业务焦点',profile)
        self.assertEqual(profile['conversation'],conversation)
        method=generation_method('fixture',[{'instructions':['original first','original second']}])
        self.assertTrue(method['instructions'].startswith('original first\noriginal second'))
        self.assertEqual(method['outputSchema']['properties']['status']['enum'],['draft','blocked'])
