import json
import tempfile
import unittest
from pathlib import Path
from draft_reply import apply_draft, record_draft


class DraftReplyTests(unittest.TestCase):
    def setUp(self):
        self.fact = '该操作只适用于测试环境。'
        self.result = {'action': 'answer', 'reply': self.fact,
                       'source_ids': ['s1'], 'claim_evidence': [{'text': self.fact}]}

    def test_legacy_result_unchanged(self):
        self.assertIs(apply_draft({}, self.result), self.result)

    def test_keep_fact_and_natural_question_exactly(self):
        draft = self.fact + '\n你现在是在测试环境操作吗？'
        result = apply_draft({'draft': draft}, self.result)
        self.assertEqual(result['reply'], draft)
        self.assertEqual(result['source_ids'], ['s1'])
        self.assertIn('remaining_semantics_by_responding_expert', result['draft_check'])

    def test_missing_or_reworded_qualification_is_rejected(self):
        for draft in ['该操作适用于任何环境。', '你好', '', None, {}, 'a' * 4001]:
            result = apply_draft({'draft': draft}, self.result)
            self.assertEqual(result['action'], 'needs_interpretation')
            self.assertNotIn('reply', result)

    def test_draft_cannot_turn_handoff_or_retrieval_failure_into_answer(self):
        for action in ['human', 'needs_retrieval', 'needs_interpretation']:
            result = {'action': action, 'reason': 'unverified'}
            self.assertIs(apply_draft({'draft': '可以退款。'}, result), result)

    def test_fixed_answer_requires_preserving_existing_reply(self):
        self.assertEqual(apply_draft({'draft': '替换'}, {'action':'ack','reply':'收到'})['action'], 'needs_interpretation')

    def test_conversational_addition_cannot_bypass_contact_filter(self):
        for contact in ['customer@example.com', '13812345678']:
            result = apply_draft({'draft': self.fact + '\n请联系 ' + contact}, self.result)
            self.assertEqual(result['reason'], 'private_contact_in_generated_reply')
            self.assertNotIn('reply', result)

    def test_final_draft_audit_correlates_with_original_request(self):
        with tempfile.TemporaryDirectory() as directory:
            request = {'request_id':'r1','question':'范围？','draft':self.fact}
            result = apply_draft(request, self.result)
            record_draft(Path(directory), request, result)
            entry = json.loads((Path(directory)/'运营记录/答疑依据审计.jsonl').read_text())
            self.assertEqual(entry['request_id'], 'r1')
            self.assertEqual(entry['reply'], self.fact)
            self.assertEqual(entry['kind'], 'final_draft')


if __name__ == '__main__':
    unittest.main()
