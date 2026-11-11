"""Selected original pure rules; I/O and mutation entry points are intentionally excluded."""
from __future__ import annotations
import re


REF = re.compile(r"[A-Za-z0-9_.:-]{1,160}")


def handoff(value, *, strict=False):
    if not isinstance(value,dict):
        if strict:raise ValueError('invalid_handoff_context')
        return {'handoff_scope':'global'}
    scope=value.get('handoff_scope','global')
    if scope=='global' and not set(value)-{'handoff_scope'}:return {'handoff_scope':'global'}
    field={'product':'product_key','matter':'matter_ref'}.get(scope)
    if field and set(value)=={'handoff_scope',field} and isinstance(value[field],str) and REF.fullmatch(value[field]):
        return dict(value)
    if strict:raise ValueError('invalid_handoff_context')
    return {'handoff_scope':'global'}


def blocks(context, *, product_key='', matter_ref=''):
    context=handoff(context);scope=context['handoff_scope']
    if scope=='global':return True
    target=product_key if scope=='product' else matter_ref
    # An unknown destination cannot safely be declared unrelated.
    return not isinstance(target,str) or not REF.fullmatch(target) or target==context['product_key' if scope=='product' else 'matter_ref']
