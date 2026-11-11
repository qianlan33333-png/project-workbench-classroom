"""Original deterministic functions; sources are bound by the adapter."""
from __future__ import annotations
import re
import unicodedata

_PLACEHOLDERS = {"用户", "客户名", "未知用户", "待识别用户", "称呼待核", "昵称待核", "问卷提交用户"}

_EMOJI = re.compile(r"[\U0001F000-\U0001FAFF\u2600-\u27BF\u200d\ufe0e\ufe0f\u20e3]")

_MOJIBAKE = re.compile(r"(?:ð[\x80-\xbf\x91-\x9f]|Ã[\x80-\xbf]|Â[\x80-\xbf]|�)")

_ID = re.compile(r"(?:\+?\d[\d\s-]{7,}|(?:CRM)?用户[-_]?[0-9a-f]{4,}|[0-9a-f-]{16,})$", re.I)

def _clean(raw: str) -> tuple[str, str]:
    value = unicodedata.normalize("NFKC", str(raw or "")).strip()
    if not value or value in _PLACEHOLDERS or _ID.fullmatch(value):
        return "", "invalid"
    broken = bool(_MOJIBAKE.search(value))
    value = _MOJIBAKE.split(value, maxsplit=1)[0] if broken else value
    value = _EMOJI.sub("", value)
    value = re.sub(r"[★☆♡♥✨·•~～丨|]+", " ", value)
    value = re.sub(r"\s+", " ", value).strip(" .,_-—/\\，。!！?？")
    if not value or value in _PLACEHOLDERS or _ID.fullmatch(value):
        return "", "invalid"
    if re.search(r"[\u4e00-\u9fff]", value) and re.search(r"[A-Za-z]", value):
        return value[:24], "mixed_review"
    if len(value) > 10 or len(value.split()) > 1:
        return value[:24], "long_review"
    if re.search(r"(.)\1{3,}", value):
        return value[:24], "repetition_review"
    if broken:
        return value[:24], "encoding_review"
    if not re.fullmatch(r"[A-Za-z0-9\u4e00-\u9fff_-]+", value):
        return value[:24], "symbols_review"
    return value, "clean"

def suggest_salutation(*, wecom: str = "", hxc: str = "", manual: str = "") -> dict:
    """Return a safe send name and an independently reviewable suggestion."""
    if manual:
        value = unicodedata.normalize("NFKC", str(manual)).strip()
        # An operator may confirm a short mixed-script nickname such as 小C.
        # Source nicknames still go through the stricter automatic gate.
        if (value not in _PLACEHOLDERS and len(value) <= 12
            and re.fullmatch(r"(?:[A-Za-z0-9\u4e00-\u9fff_-]+|[小老] [A-Za-z])", value)
            and not _ID.fullmatch(value)):
            return {"send_name": value, "suggestion": value, "state": "operator_confirmed",
                    "source": "workbench_override", "ask_later": False}
    from nickname_rules import decide
    had_source = False
    for source, raw in [("hxc", hxc), ("wecom", wecom)]:
        had_source = had_source or bool(str(raw or "").strip())
        if not str(raw or "").strip():
            continue
        normalized = unicodedata.normalize("NFKC", str(raw)).strip()
        if _ID.fullmatch(normalized) or normalized in _PLACEHOLDERS:
            continue
        value, rule = decide(str(raw))
        if value not in {"你好", "宝子"}:
            return {"send_name": value, "suggestion": value, "state": "automatic_resolved",
                    "source": source, "rule": rule, "ask_later": False}
    return {"send_name": "你好" if had_source else "宝子", "suggestion": "",
            "state": "neutral_greeting" if had_source else "fallback_salutation",
            "source": "approved_fallback", "ask_later": False}

COPY_SALUTATION_VERSION = "copy-salutation.v5.1-stable"

_COPY_SCENES = {"weekly_review", "content_service", "activation_recall",
                "new_user_onboarding", "campaign", "personal_followup"}

def copy_salutation_contract(data: dict) -> dict:
    """Reuse the resolved decision: no lookup, alias inference or model call."""
    profile = data.get("profile_context") or {}
    decision = (profile.get("salutation") or data.get("salutation_decision")
                or (data.get("judgment_card") or {}).get("salutation_decision") or {})
    name = str(decision.get("send_name") or (data.get("display_name") if not decision else "") or "").strip()
    state = str(decision.get("state") or "")
    cleaned, clean_state = ("", "stable") if state in {"automatic_resolved", "operator_confirmed"} else _clean(name)
    usable = bool(name and name != "你好" and name not in _PLACEHOLDERS
                  and ((state == "operator_confirmed" and len(name) <= 12
                        and re.fullmatch(r"(?:[A-Za-z0-9\u4e00-\u9fff_-]+|[小老] [A-Za-z])", name)
                        and not _ID.fullmatch(name))
                       or (state == "automatic_resolved" and len(name) <= 15
                           and re.fullmatch(r"[A-Za-z0-9\u4e00-\u9fff_-]+", name)
                           and not _ID.fullmatch(name) and not re.search(r"\d{6,}", name))
                       or (clean_state == "clean" and cleaned == name
                           and state in {"", "cleaned_candidate", "cleaned_wecom_preferred", "fallback_salutation"})))
    required = usable and str(data.get("scene") or "") in _COPY_SCENES
    return {"version": COPY_SALUTATION_VERSION, "required": bool(required),
            "send_name": name if usable else "", "state": state or "unknown",
            "rule": "人工称呼优先；自动称呼随档案刷新保存，源昵称或规则变化才更新，生成话术直接复用。空昵称用宝子，复杂昵称按既定规则提取，无法提取用你好。简称确认可选，不要求固定句式或问号数量，不覆盖已有人工稿和已审稿。"}

def resolved_send_name(data):
    decision=(data.get('profile_context')or{}).get('salutation')or data.get('salutation_decision')or{}
    if decision.get('send_name')=='你好':return '你好'
    return copy_salutation_contract(data).get('send_name') or '宝子'
