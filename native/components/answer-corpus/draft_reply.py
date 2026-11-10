"""Keep evidence validation in the existing gate; retain an expert's final draft.

This checks structure, not semantic entailment. The same responding expert must
check all factual wording against the evidence and native conversation history.
"""
import fcntl
import hashlib
import json
import re


def apply_draft(request, result):
    if 'draft' not in request or result.get('action') not in ('answer', 'ack'):
        return result
    draft = request['draft']
    def reject(reason):
        return {'action': 'needs_interpretation', 'reason': reason}
    if not isinstance(draft, str) or not draft.strip() or len(draft) > 4000:
        return reject('invalid_draft')
    # Preserve the legacy gate's deterministic output protection over the whole
    # final text, including conversational additions outside the fact sentences.
    if re.search(r'(?<!\d)1[3-9]\d{9}(?!\d)|[\w.+-]+@[\w.-]+\.[A-Za-z]{2,}', draft):
        return reject('private_contact_in_generated_reply')
    # The gate has already verified these exact fact sentences. Do not silently
    # drop their qualifications or let a different version become the final text.
    claims = result.get('claim_evidence', [])
    texts = [claim.get('text') for claim in claims]
    if not texts:
        texts = [result.get('reply', '')]
    if any(not isinstance(text, str) or not text or text not in draft for text in texts):
        return reject('draft_must_preserve_verified_claims')
    return {**result, 'reply': draft,
            'draft_sha256': hashlib.sha256(draft.encode()).hexdigest(),
            'draft_check': 'verified_claims_preserved; remaining_semantics_by_responding_expert'}


def record_draft(root, request, result):
    if 'draft' not in request:
        return
    path = root / '运营记录/答疑依据审计.jsonl'
    path.parent.mkdir(parents=True, exist_ok=True)
    # The original gate records its fact answer. This linked final record makes
    # the actual reply distinguishable without altering the original audit.
    entry = {'kind': 'final_draft', 'request_id': request.get('request_id', ''),
             'question': request.get('question', ''), **result}
    with path.open('a') as f:
        fcntl.flock(f, fcntl.LOCK_EX)
        f.write(json.dumps(entry, ensure_ascii=False) + '\n')
