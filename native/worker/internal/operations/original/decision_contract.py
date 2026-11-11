import hashlib,json

def short_hash(value) -> str:
  return hashlib.sha256(json.dumps(value, ensure_ascii=False, sort_keys=True).encode("utf-8")).hexdigest()[:16]

def _action_id(week: str, ref: str, service: str) -> str:
  return "agent_user_action:" + hashlib.sha256(f"{week}:{ref}:{service}".encode("utf-8")).hexdigest()[:24]
