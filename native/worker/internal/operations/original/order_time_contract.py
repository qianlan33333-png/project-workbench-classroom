"""Selected original pure rules; I/O and mutation entry points are intentionally excluded."""
from __future__ import annotations
def service_time(order):
    # A field explicitly present but unknown cannot fall through to another clock.
    return order.get('service_reference_at') if 'service_reference_at'in order else order.get('paid_at')
