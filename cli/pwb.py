#!/usr/bin/env python3
"""Personal Project Workbench CLI. Python 3.9+, standard library only."""
import argparse
import base64
import getpass
import json
import os
from pathlib import Path
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request
import uuid

VERSION = "0.3.0"
DEFAULT_URL = "https://dsh.qianlan333.cloud"


class CLIError(Exception):
    def __init__(self, message, status=0, **details):
        super().__init__(message)
        self.value = {"error": message, "status": status, **details}


class Parser(argparse.ArgumentParser):
    def error(self, message):
        raise CLIError(message, 2)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def output(value, pretty=False):
    print(json.dumps(value, ensure_ascii=False, indent=2 if pretty else None))


def read_input(file):
    return sys.stdin.buffer.read() if file == "-" else Path(file).expanduser().read_bytes()


def atomic_write(path, content):
    path = Path(path).expanduser()
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(prefix=".pwb-", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as f:
            f.write(content)
        os.replace(tmp, path)
        os.chmod(path, 0o600)
    finally:
        if os.path.exists(tmp):
            os.unlink(tmp)


class Client:
    def __init__(self, url, password, timeout=120):
        parts = urllib.parse.urlsplit(url)
        if parts.scheme not in ("http", "https") or not parts.netloc or parts.username or parts.query or parts.fragment:
            raise CLIError("--url 需要不含账号、查询参数和 fragment 的服务根网址", 2)
        self.url = url.rstrip("/")
        if self.url.endswith("/api/v1"):
            self.url = self.url[:-7]
        self.password = password
        self.timeout = timeout
        self.opener = urllib.request.build_opener(NoRedirect())

    def request(self, method, path, value=None, *, body=None, content_type=None, raw=False, destination=None):
        if not self.password:
            raise CLIError("请先 pwb login，或设置 PWB_PASSWORD", 401)
        if not path.startswith("/") or path.startswith("//") or "://" in path:
            raise CLIError("API 路径必须是 /projects 这样的相对路径", 2)
        if value is not None:
            body = json.dumps(value, ensure_ascii=False).encode("utf-8")
            content_type = "application/json"
        auth = base64.b64encode(("owner:" + self.password).encode("utf-8")).decode("ascii")
        headers = {"Authorization": "Basic " + auth, "User-Agent": "project-workbench-cli/" + VERSION}
        if content_type:
            headers["Content-Type"] = content_type
        path = urllib.parse.quote(path, safe="/%?=&:+,;@!$'()*[]~-")
        request = urllib.request.Request(self.url + "/api/v1" + path, data=body, headers=headers, method=method)
        try:
            with self.opener.open(request, timeout=self.timeout) as response:
                if destination:
                    target = Path(destination).expanduser()
                    target.parent.mkdir(parents=True, exist_ok=True)
                    fd, tmp = tempfile.mkstemp(prefix=".pwb-", dir=target.parent)
                    size = 0
                    try:
                        with os.fdopen(fd, "wb") as f:
                            while True:
                                chunk = response.read(1024 * 1024)
                                if not chunk:
                                    break
                                f.write(chunk)
                                size += len(chunk)
                        os.replace(tmp, target)
                    finally:
                        if os.path.exists(tmp):
                            os.unlink(tmp)
                    return {"path": str(target.resolve()), "bytes": size}
                data = response.read()
                if raw:
                    return data
                return json.loads(data) if data else {"ok": True}
        except urllib.error.HTTPError as e:
            try:
                message = json.loads(e.read()).get("error", "HTTP " + str(e.code))
            except (ValueError, UnicodeError):
                message = "HTTP " + str(e.code)
            raise CLIError(message, e.code, method=method, path=path) from None
        except (urllib.error.URLError, TimeoutError) as e:
            raise CLIError("连接失败：" + str(getattr(e, "reason", e)), method=method, path=path) from None

    def project_id(self, reference):
        # ID lookup first; exact names are resolved without silently picking a duplicate.
        try:
            return self.request("GET", "/projects/" + urllib.parse.quote(reference, safe=""))["id"]
        except CLIError as e:
            if e.value["status"] != 404:
                raise
        matches = [p for p in self.request("GET", "/projects") if p["name"] == reference]
        if not matches:
            raise CLIError("项目不存在：" + reference, 404)
        if len(matches) != 1:
            raise CLIError("同名项目不唯一，请使用 ID", 409, matches=[p["id"] for p in matches])
        return matches[0]["id"]

    def upload(self, endpoint, file, fields=None):
        boundary = "pwb-" + uuid.uuid4().hex
        chunks = []
        for key, value in (fields or {}).items():
            chunks.append((f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"\r\n\r\n{value}\r\n').encode())
        name = "stdin.bin" if file == "-" else Path(file).name
        name = name.replace('"', "_").replace("\r", "_").replace("\n", "_")
        chunks.extend([(f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{name}"\r\nContent-Type: application/octet-stream\r\n\r\n').encode(), read_input(file), f'\r\n--{boundary}--\r\n'.encode()])
        return self.request("POST", endpoint, body=b"".join(chunks), content_type="multipart/form-data; boundary=" + boundary)

    def multipart(self, method, endpoint, fields, files, destination=None):
        boundary = "pwb-" + uuid.uuid4().hex
        chunks = []
        def field_pair(value):
            key, separator, text = value.partition("=")
            if not separator or not key or not all(c.isascii() and (c.isalnum() or c in "_-") for c in key):
                raise CLIError("multipart 参数需要 FIELD=VALUE，字段名使用字母、数字、下划线或连字符", 2)
            return key, text
        for value in fields:
            key, text = field_pair(value)
            chunks.append((f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"\r\n\r\n{text}\r\n').encode())
        for value in files:
            key, file = field_pair(value)
            filename = "stdin.bin" if file == "-" else Path(file).name
            filename = filename.replace('"', "_").replace("\r", "_").replace("\n", "_")
            chunks.extend([(f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"; filename="{filename}"\r\nContent-Type: application/octet-stream\r\n\r\n').encode(), read_input(file), b"\r\n"])
        chunks.append(f'--{boundary}--\r\n'.encode())
        return self.request(method, endpoint, body=b"".join(chunks), content_type="multipart/form-data; boundary=" + boundary, destination=destination)


def parser():
    p = Parser(description="项目工作台 CLI：直接密码鉴权，默认 JSON 输出，无登录过期时间。")
    p.add_argument("--version", action="version", version=VERSION)
    p.add_argument("--url", help="服务根网址；也可设置 PWB_URL")
    p.add_argument("--password", help="工作台密码；也可设置 PWB_PASSWORD")
    p.add_argument("--config", default=os.environ.get("PWB_CONFIG", str(Path.home() / ".config/project-workbench/cli.json")))
    p.add_argument("--timeout", type=float, default=120)
    p.add_argument("--pretty", action="store_true", help="缩进 JSON")
    sub = p.add_subparsers(dest="command", required=True)
    login = sub.add_parser("login", help="验证密码并永久保存到本机配置")
    login.add_argument("--password-stdin", action="store_true")
    sub.add_parser("logout", help="清除本机保存的密码")
    sub.add_parser("status", help="验证当前密码并显示服务状态")
    sub.add_parser("schema", help="输出机器可读的完整命令参数说明")
    projects = sub.add_parser("projects", help="项目增删改查、归档、导入导出").add_subparsers(dest="action", required=True)
    projects.add_parser("list")
    for action in ("get", "delete", "archive", "restore"):
        projects.add_parser(action).add_argument("project", help="项目 ID 或精确名称")
    create = projects.add_parser("create")
    create.add_argument("--name", required=True)
    create.add_argument("--description", default="")
    create.add_argument("--alias", action="append", default=[])
    update = projects.add_parser("update")
    update.add_argument("project")
    update.add_argument("--name")
    update.add_argument("--description")
    update.add_argument("--alias", action="append", help="替换全部别名；可重复")
    update.add_argument("--clear-aliases", action="store_true")
    update.add_argument("--expert", action="append", help="替换启用的专家 ID；可重复")
    update.add_argument("--default-expert")
    update.add_argument("--plugin", action="append", help="替换启用的插件 ID；可重复")
    imp = projects.add_parser("import", help="导入 ZIP，已有文件不会覆盖")
    imp.add_argument("project")
    imp.add_argument("file")
    exp = projects.add_parser("export")
    exp.add_argument("project")
    exp.add_argument("--output", required=True)
    files = sub.add_parser("files", help="项目内文件读写、搜索和版本").add_subparsers(dest="action", required=True)
    files.add_parser("list").add_argument("project")
    for action in ("read", "write", "delete", "upload", "download", "history"):
        f = files.add_parser(action)
        f.add_argument("project")
        f.add_argument("path", help="项目内相对路径")
        if action == "read":
            f.add_argument("--raw", action="store_true", help="输出原始文件字节；默认返回 JSON 与 hash")
        if action == "write":
            src = f.add_mutually_exclusive_group(required=True)
            src.add_argument("--text")
            src.add_argument("--from", dest="input_file", help="UTF-8 文件，或 - 从 stdin 读取")
        if action == "upload":
            f.add_argument("file", help="本机文件或 -")
        if action in ("write", "delete", "upload"):
            f.add_argument("--base-hash", help="使用先前读取的 hash；空串表示仅新建。省略时先读取当前 hash")
        if action == "download":
            f.add_argument("--output", required=True)
    search = files.add_parser("search")
    search.add_argument("project")
    search.add_argument("query")
    files.add_parser("undo").add_argument("revision")
    api = sub.add_parser("api", help="直接调用任何现有 /api/v1 接口")
    api.add_argument("method", choices=["GET", "POST", "PUT", "PATCH", "DELETE"])
    api.add_argument("path", help="例如 /projects 或 /sessions/ID/history")
    data = api.add_mutually_exclusive_group()
    data.add_argument("--data", help="JSON 请求体")
    data.add_argument("--from", dest="input_file", help="JSON 文件，或 -")
    api.add_argument("--form", action="append", default=[], help="multipart 文本字段 FIELD=VALUE；可重复")
    api.add_argument("--file", action="append", default=[], help="multipart 文件 FIELD=PATH；可重复，PATH 可为 -")
    api.add_argument("--output", help="把响应原始字节写入文件")
    return p


def command_schema(p):
    options, commands = [], {}
    for a in p._actions:
        if isinstance(a, argparse._SubParsersAction):
            commands = {name: command_schema(child) for name, child in a.choices.items()}
        elif a.dest != "help":
            options.append({"name": a.dest, "flags": a.option_strings, "required": a.required, "choices": a.choices, "help": a.help})
    return {"description": p.description, "arguments": options, "commands": commands}


def run(argv=None):
    p = parser()
    args = p.parse_args(argv)
    if args.command == "schema":
        output({"version": VERSION, "authentication": "HTTP Basic owner:<password>; no expiry", "stdout": "JSON except files read --raw", "errors": "JSON on stderr, nonzero exit; never auto-retry writes", "cli": command_schema(p)}, args.pretty)
        return
    config_path = Path(args.config).expanduser()
    config = json.loads(config_path.read_text()) if config_path.exists() else {}
    url = args.url or os.environ.get("PWB_URL") or config.get("url") or DEFAULT_URL
    password = args.password if args.password is not None else os.environ.get("PWB_PASSWORD", config.get("password", ""))
    if args.command == "logout":
        config.pop("password", None)
        if config_path.exists():
            atomic_write(config_path, json.dumps(config).encode())
        output({"ok": True, "config": str(config_path)}, args.pretty)
        return
    if args.command == "login":
        if args.password_stdin:
            password = sys.stdin.readline().rstrip("\r\n")
        elif args.password is None and "PWB_PASSWORD" not in os.environ:
            password = getpass.getpass("工作台密码：")
    client = Client(url, password, args.timeout)
    if args.command in ("login", "status"):
        if not client.request("GET", "/auth").get("authenticated"):
            raise CLIError("密码不正确", 401)
        result = {"authenticated": True, "url": client.url, "expiresAt": None}
        if args.command == "login":
            atomic_write(config_path, json.dumps({"url": client.url, "password": password}, ensure_ascii=False).encode())
            result["config"] = str(config_path)
        else:
            result["server"] = client.request("GET", "/health")
    elif args.command == "api":
        data = None
        if args.data is not None:
            data = json.loads(args.data)
        elif args.input_file:
            data = json.loads(read_input(args.input_file))
        if args.form or args.file:
            if args.data is not None or args.input_file:
                raise CLIError("JSON 输入与 multipart --form/--file 不能混用", 2)
            if sum(value.partition("=")[2] == "-" for value in args.file) > 1:
                raise CLIError("stdin 只能作为一个文件输入", 2)
            result = client.multipart(args.method, args.path, args.form, args.file, args.output)
        else:
            result = client.request(args.method, args.path, data, destination=args.output)
    elif args.command == "projects":
        if args.action == "list":
            result = client.request("GET", "/projects")
        elif args.action == "create":
            result = client.request("POST", "/projects", {"name": args.name, "description": args.description, "aliases": args.alias})
        else:
            endpoint = "/projects/" + client.project_id(args.project)
            if args.action == "get":
                result = client.request("GET", endpoint)
            elif args.action == "delete":
                result = client.request("DELETE", endpoint)
            elif args.action in ("archive", "restore"):
                result = client.request("PATCH", endpoint, {"archived": args.action == "archive"})
            elif args.action == "import":
                result = client.upload(endpoint + "/import", args.file)
            elif args.action == "export":
                result = client.request("GET", endpoint + "/export", destination=args.output)
            else:
                fields = {}
                for attr, key in [("name", "name"), ("description", "description"), ("alias", "aliases"), ("expert", "expertIds"), ("default_expert", "defaultExpertId"), ("plugin", "pluginIds")]:
                    value = getattr(args, attr)
                    if value is not None:
                        fields[key] = value
                if args.clear_aliases:
                    fields["aliases"] = []
                if not fields:
                    raise CLIError("请指定需要修改的项目字段", 2)
                result = client.request("PATCH", endpoint, fields)
    elif args.command == "files":
        if args.action == "undo":
            result = client.request("POST", "/revisions/" + urllib.parse.quote(args.revision, safe="") + "/undo", {})
        else:
            endpoint = "/projects/" + client.project_id(args.project)
            if args.action == "list":
                result = client.request("GET", endpoint + "/files")
            elif args.action == "search":
                result = client.request("GET", endpoint + "/search?" + urllib.parse.urlencode({"q": args.query}))
            else:
                query = urllib.parse.urlencode({"path": args.path})
                file_endpoint = endpoint + "/file?" + query
                if args.action == "read":
                    result = client.request("GET", file_endpoint + ("&raw=1" if args.raw else ""), raw=args.raw)
                    if args.raw:
                        sys.stdout.buffer.write(result)
                        return
                elif args.action == "history":
                    result = client.request("GET", endpoint + "/revisions?" + query)
                elif args.action == "download":
                    result = client.request("GET", file_endpoint + "&raw=1", destination=args.output)
                else:
                    base_hash = args.base_hash
                    if base_hash is None:
                        try:
                            base_hash = client.request("GET", file_endpoint)["hash"]
                        except CLIError as e:
                            if e.value["status"] != 404 or args.action == "delete":
                                raise
                            base_hash = ""
                    if args.action == "delete":
                        result = client.request("DELETE", file_endpoint + "&" + urllib.parse.urlencode({"baseHash": base_hash}))
                    elif args.action == "upload":
                        result = client.upload(endpoint + "/upload", args.file, {"path": args.path, "baseHash": base_hash})
                    else:
                        text = args.text if args.text is not None else read_input(args.input_file).decode("utf-8")
                        result = client.request("PUT", endpoint + "/file", {"path": args.path, "content": text, "baseHash": base_hash})
    output(result, args.pretty)


def main():
    try:
        run()
    except CLIError as e:
        print(json.dumps(e.value, ensure_ascii=False), file=sys.stderr)
        return 2 if e.value["status"] == 2 else 1
    except (OSError, ValueError, UnicodeError) as e:
        print(json.dumps({"error": str(e), "status": 0}, ensure_ascii=False), file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        return 130
    return 0


if __name__ == "__main__":
    sys.exit(main())
