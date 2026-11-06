"""Replace only the known previous task policy in the saved owner's AGENT.md."""
import argparse
import base64
import json
from pathlib import Path
import urllib.request

OLD_SECTION = """## 项目完全只读

在“今天”、微信和飞书的个人助手中，
不创建、修改、完成或删除项目任务、文件、设置及其他业务数据。

可以给出方案、整理待办草稿或建议措辞。
这些只是回复内容，不代表已经写入项目。

我要求实际修改时，说明需要进入对应项目操作，
并提供可用的项目入口和简洁的交接内容。
不要声称已经执行，也不要通过 CLI、MCP 或其他路径绕过限制。

个人理解的更新按专门的记忆规则处理。

"""
root = Path(__file__).resolve().parents[1]
default = (root / "internal/workbench/personal-defaults/AGENT.md").read_text()
new_section = default[default.index("## 工作清单"):default.index("## 早上的每日建议")]

parser = argparse.ArgumentParser()
parser.add_argument("url")
parser.add_argument("--auth-file", type=Path, default=Path.home() / ".config/project-workbench/cli.json")
parser.add_argument("--apply", action="store_true")
parser.add_argument("--restore", action="store_true")
args = parser.parse_args()
auth = json.loads(args.auth_file.read_text())["password"]
headers = {
    "Authorization": "Basic " + base64.b64encode(("owner:" + auth).encode()).decode(),
    "Content-Type": "application/json",
}


def api(body=None):
    req = urllib.request.Request(
        args.url.rstrip("/") + "/api/v1/personal/rules",
        headers=headers,
        method="PUT" if body else "GET",
        data=json.dumps(body, ensure_ascii=False).encode() if body else None,
    )
    with urllib.request.urlopen(req, timeout=15) as response:
        return json.load(response)


before, after = (new_section, OLD_SECTION) if args.restore else (OLD_SECTION, new_section)
rules = api()
agent = rules["agent"]
if agent.count(after) == 1 and before not in agent:
    print(json.dumps({"state": "already_current", "version": rules["version"]}))
elif agent.count(before) != 1 or after in agent:
    raise SystemExit("Saved AGENT.md differs from the expected policy block. Review the owner edits before updating.")
elif not args.apply:
    print(json.dumps({"state": "ready", "version": rules["version"]}))
else:
    updated = agent.replace(before, after, 1)
    result = api({**rules, "agent": updated})
    if result["agent"] != updated or result["soul"] != rules["soul"] or result["version"] != rules["version"] + 1:
        raise SystemExit("AGENT.md revision readback differs from the requested update.")
    print(json.dumps({"state": "updated", "version": result["version"]}))
