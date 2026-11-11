import sys,unittest
from pathlib import Path
from datetime import datetime
from zoneinfo import ZoneInfo
sys.path.insert(0,str(Path(__file__).parent))
sys.path.insert(0,str(Path(__file__).parent/'original'))
from writing import action_deadline
from hxc_original import sku68_history_guard
from sku68_operations import VERSION

class FrozenBusinessActionTests(unittest.TestCase):
    def test_newcomer_draft_expires_at_next_node_not_annual_rights_end(self):
        base={'paid_at':'2026-11-01T10:00:00+08:00','entitlement_end':'2027-11-01T10:00:00+08:00'}
        for identity,node,expected in [('6.8','D2','2026-11-05'),('99','D4','2026-11-07'),('299','D4','2026-11-08'),('299','D7','2026-11-09')]:
            with self.subTest(identity=identity,node=node):
                deadline=action_deadline(dict(base,primary_identity=identity,node=node))
                self.assertEqual(deadline,expected+'T10:00:00+08:00')
        self.assertIsNone(action_deadline({'primary_identity':'6.8','node':'D2','entitlement_end':base['entitlement_end']}))
        self.assertEqual(action_deadline(dict(base,primary_identity='6.8',node='D2',anchor_at='2026-11-02T10:00:00+08:00')),'2026-11-06T10:00:00+08:00')
    def test_old_unknown_plan_blocks_next_node_and_sent_original_slot_is_never_rekeyed(self):
        now=datetime(2026,11,6,10,tzinfo=ZoneInfo('Asia/Shanghai'))
        binding={'version':VERSION,'primary_identity':'6.8','cycle_key':'purchase','node':'D4','slot_key':'d4'}
        decision={'safe_user_ref':'user','outbound_candidate':True,'sku_pack_binding':binding}
        old={'run_id':'original','item_key':'one','handoff':'original-plan','state':'unknown','binding':dict(binding,node='D2',slot_key='d2')}
        self.assertEqual(sku68_history_guard(decision,{'user':[old]},now)['preparation_reason'],'sku68_previous_delivery_outcome_pending')
        old.update(state='sent',sent_at='2026-11-05T09:00:00+08:00')
        self.assertTrue(sku68_history_guard(decision,{'user':[old]},now)['outbound_candidate'])
        old['binding']=binding
        self.assertTrue(sku68_history_guard(decision,{'user':[old]},now)['existing_copy']['reuse_required'])
        old['binding']=dict(binding,node='D2',slot_key='d2');old['sent_at']='2026-11-05T11:00:00+08:00'
        self.assertEqual(sku68_history_guard(decision,{'user':[old]},now)['preparation_reason'],'actual_send_cooldown_24h')
