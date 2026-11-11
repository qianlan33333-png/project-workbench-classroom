"""Selected original pure rules; I/O and mutation entry points are intentionally excluded."""
from __future__ import annotations
import hashlib


import re


from datetime import datetime, timedelta, timezone


PRIORITY = {
  "manual_handoff": 100,
  "intent_followup": 80,
  "potential_review": 70,
  "new_user_onboarding": 65,
  "weekly_review": 60,
  "content_push": 50,
  "subscription_activation": 40,
  "hxc_lifecycle": 55,
}


SAFE_REF = re.compile(r"^usr_[a-f0-9]{12}$")


SERVICE_KEY = re.compile(r"^[a-z0-9_:.-]{1,100}$")


HXC_SERVICE_ACTIONS = {"new_user_onboarding", "weekly_review", "content_push", "subscription_activation", "hxc_lifecycle"}


def safe_ref(canonical_user_id: str) -> str:
  source = str(canonical_user_id or "").strip()
  return source if SAFE_REF.fullmatch(source) else f"usr_{hashlib.sha256(source.encode('utf-8')).hexdigest()[:12]}" if source else ""


def _service_for(item: dict) -> str:
  action = str(item.get("action") or "")
  return str(item.get("product_service_key") or ("hxc" if action in HXC_SERVICE_ACTIONS else "manual" if action == "manual_handoff" else "relationship"))


def priority_todo_candidate(todo: dict, ref: str, release: dict | None = None) -> dict | None:
  """Adapt published priorities without turning historical keyword hints into takeovers."""
  action = "manual_handoff" if todo.get("type") == "handoff" else "intent_followup" if todo.get("type") == "intent_followup" else "potential_review"
  evidence = todo.get("evidence") or []
  if isinstance(evidence, str): evidence = [evidence]
  refs = todo.get("trigger_evidence_refs") or todo.get("evidence_refs") or []
  why = str(todo.get("why_now") or "").strip()
  hold = todo.get("reply_hold")
  if action == "manual_handoff" and not (why or refs or hold):
    # Same classification as manual_task_policy: old broad keyword matches are
    # historical sales leads, not current unanswered requests or human takeovers.
    if any(str(value).startswith("出现人工判断信号（") for value in evidence): return None
    why = str(todo.get("reason") or (todo.get("stage") or {}).get("why") or next(iter(evidence), "")).strip()
    if not why: return None
  strategy = "manual:intent_sales" if action == "intent_followup" else "manual:potential_followup" if action == "potential_review" else ""
  result = {"safe_user_ref": ref, "action": action, "source": "workbench_priority_todo",
            "strategy_key": strategy if release else "", "strategy_version": release.get("version") if release else 0,
            "product_service_key": str(todo.get("product_service_key") or todo.get("product_key") or ("hxc" if action != "manual_handoff" else "manual")),
            "why_now": why, "evidence_refs": refs, "reply_hold": hold}
  for field in ('handoff_context','communication_context','matter_ref'):
    if field in todo:result[field]=todo[field]
  for key in ("last_user_reply_at", "last_outreach_at", "last_light_followup_at", "new_evidence_at", "callback_due_at", "source_snapshot_id"):
    result[key] = todo.get(key)
  return result


def potential_cooldown(item: dict, now: datetime | None = None) -> dict:
  """Use only dated reply/touch evidence; missing dates require human review."""
  current = now or datetime.now(timezone.utc)
  def stamp(key: str) -> datetime | None:
    raw = str(item.get(key) or "").strip()
    if not raw:
      return None
    try:
      parsed = datetime.fromisoformat(raw.replace("Z", "+00:00"))
      return parsed if parsed.tzinfo else parsed.replace(tzinfo=timezone.utc)
    except ValueError:
      return None
  reply = stamp("last_user_reply_at")
  outreach = stamp("last_outreach_at")
  followup = stamp("last_light_followup_at")
  new_evidence = stamp("new_evidence_at")
  callback = stamp("callback_due_at")
  last_touch = max([value for value in (reply, outreach, followup) if value], default=None)
  if (callback and callback <= current) or (new_evidence and (not last_touch or new_evidence > last_touch)):
    return {"eligible": True, "state": "new_evidence_or_promised_callback"}
  if reply and (not outreach or reply >= outreach) and current - reply < timedelta(days=30):
    return {"eligible": False, "state": "replied_cooling_30d", "until": (reply + timedelta(days=30)).isoformat()}
  if followup and current - followup < timedelta(days=30):
    return {"eligible": False, "state": "light_followup_cooling_30d", "until": (followup + timedelta(days=30)).isoformat()}
  if outreach and not followup and current - outreach < timedelta(days=7):
    return {"eligible": False, "state": "await_first_reply_7d", "until": (outreach + timedelta(days=7)).isoformat()}
  if outreach and not followup and (not reply or reply < outreach):
    return {"eligible": True, "state": "one_light_followup_allowed"}
  return {"eligible": True, "state": "history_unknown_human_review"}


def choose_actions(candidates: list[dict]) -> list[dict]:
  """Choose one action per user × service line; a third line needs coordination."""
  chosen: dict[tuple[str, str], dict] = {}
  for item in candidates:
    ref = str(item.get("safe_user_ref") or "")
    action = str(item.get("action") or "")
    if not SAFE_REF.fullmatch(ref) or action not in PRIORITY:
      continue
    service = _service_for(item)
    if not SERVICE_KEY.fullmatch(service):
      continue
    cooldown = potential_cooldown(item) if action == "potential_review" else {"eligible": True, "state": "not_applicable"}
    if not cooldown["eligible"]:
      continue
    proposal = {
      "safe_user_ref": ref,
      "product_service_key": service,
      "action": action,
      "priority": PRIORITY[action],
      "source": str(item.get("source") or "")[:80],
      "strategy_key": str(item.get("strategy_key") or "")[:160],
      "strategy_version": int(item.get("strategy_version") or 0),
      "branch": str(item.get("branch") or "")[:80],
      "why_now": str(item.get("why_now") or "")[:240],
      "source_snapshot_id": str(item.get("source_snapshot_id") or "")[:120],
      "decision_fingerprint": str(item.get("decision_fingerprint") or "")[:64],
      "trigger_evidence_refs": [str(ref)[:160] for ref in (item.get("evidence_refs") or [])[:3]],
      **({"reply_hold": item["reply_hold"]} if item.get("reply_hold") else {}),
      **({'handoff_context': __import__('hxc_communication_context').handoff(item['handoff_context'])}if 'handoff_context'in item else{}),
      **({'matter_ref':item['matter_ref']}if isinstance(item.get('matter_ref'),str)and SERVICE_KEY.fullmatch(item['matter_ref'])else{}),
      "manual_review_required": True,
      "automatic_send_allowed": False,
      "cooldown_state": cooldown["state"],
      "routine_scene": str(item.get("routine_scene") or "")[:80],
      "supplemental": bool(item.get("urgent_handoff") is True or (
        item.get("live_paid_verified") is True and item.get("identity_resolved") is True
        and item.get("entitlement_verified") is True and str(item.get("source_event_ref") or ""))),
    }
    key = (ref, service)
    prior = chosen.get(key)
    if prior is None or (-proposal["priority"], proposal["action"], proposal["source"]) < (-prior["priority"], prior["action"], prior["source"]):
      chosen[key] = proposal
  by_user: dict[str, list[dict]] = {}
  for proposal in chosen.values():
    by_user.setdefault(proposal["safe_user_ref"], []).append(proposal)
  selected = []
  for rows in by_user.values():
    manual = [row for row in rows if row["action"] == "manual_handoff"]
    available_lines=2
    if manual:
      from hxc_communication_context import blocks, handoff
      global_rows=[row for row in manual if handoff(row.get('handoff_context'))['handoff_scope']=='global']
      if global_rows:
        selected.append(sorted(global_rows,key=lambda row:(-row['priority'],row['product_service_key']))[0])
        continue
      selected.extend(sorted(manual,key=lambda row:row['product_service_key']))
      available_lines=max(0,2-len(manual))
      rows=[row for row in rows if row['action']!='manual_handoff' and not any(
          blocks(hold.get('handoff_context'),product_key=row['product_service_key'],matter_ref=row.get('matter_ref',''))for hold in manual)]
    ranked = sorted(rows, key=lambda row: (-row["priority"], row["product_service_key"]))
    selected.extend(ranked[:available_lines])
    if len(ranked) > available_lines:
      selected.append({**ranked[0], "action": "manual_handoff", "priority": PRIORITY["manual_handoff"],
                       "product_service_key": "manual_coordination", "source": "third_service_line_requires_coordination",
                       "strategy_key": "", "strategy_version": 0})
  return sorted(selected, key=lambda item: (-item["priority"], item["safe_user_ref"], item["product_service_key"]))
