# pwb：项目工作台独立 CLI

供 Codex、其他 Agent 和脚本直接操作个人工作台。Python 3.9+，仅标准库，无 npm、Go 或额外服务依赖。默认输出 JSON。

## 安装与密码

macOS / Linux：

```sh
mkdir -p ~/.local/bin
curl -fsS https://dsh.qianlan333.cloud/cli/pwb.py -o ~/.local/bin/pwb
chmod +x ~/.local/bin/pwb
export PATH="$HOME/.local/bin:$PATH"
pwb login
pwb status
```

`login` 提示输入现有工作台密码。Windows 可下载同一个文件，以 `python pwb.py` 调用。

每次请求通过 HTTP Basic `owner:<工作台密码>` 直接鉴权，没有 Token、Cookie 或刷新流程，不设过期时间。密码不变就一直有效；修改工作台密码后，使用新密码重新 `login`。网页登录是否过期不影响 CLI。

默认保存到 `~/.config/project-workbench/cli.json`，权限 0600。`pwb logout` 仅删除本机保存的密码。无交互调用可用 `PWB_PASSWORD`，或向 `pwb login --password-stdin` 的标准输入提供一行密码。

配置优先级：全局参数 > 环境变量 > 配置文件。环境变量为 `PWB_URL`、`PWB_PASSWORD`、`PWB_CONFIG`；默认地址为 `https://dsh.qianlan333.cloud`。全局参数放在子命令前：

```sh
pwb --url https://your-server.example --config ./client.json login
pwb --pretty projects list
pwb --timeout 300 projects export PROJECT_ID --output project.zip
```

## 给 Agent 的操作约定

先运行 `pwb schema` 获取完整参数说明，再用 `pwb projects list` 确定项目 ID。所有 PROJECT 参数接受 ID 或精确项目名；同名项目会报冲突并返回候选 ID，不自行选择。归档项目也在列表中，通过 `archived` 字段识别。

编辑流程：列文件 → 读取文件 → 基于内容修改 → 带读取结果的 `hash` 保存。生成文本建议写到临时文件，通过 `--from` 提交，避免 Shell 引号和命令替换改变内容。

```sh
pwb projects list
pwb projects get PROJECT_ID
pwb files list PROJECT_ID
pwb files read PROJECT_ID '品牌/定位.md'
pwb files search PROJECT_ID '品牌'
pwb files write PROJECT_ID '品牌/定位.md' --from ./edited.md --base-hash HASH_FROM_READ
```

`--base-hash ''` 表示只允许新建；传入读取时的 hash 可防止覆盖他人后续编辑。省略时，CLI 会在提交前读取当前 hash，保留服务器的并发校验。项目执行任务期间写入返回 423；应等待任务结束，或在用户要求停止时调用停止接口。

成功：JSON 在 stdout，退出码 0。失败：`{"error":"说明","status":409}` 在 stderr，非零退出码（参数错误为 2，其他为 1）。`files read --raw` 直接输出字节，`--output` 将二进制写入本机文件。CLI 不自动重试写操作；连接中断或超时应先查询结果。409 表示冲突，401 表示密码无效，404 表示目标不存在，423 表示项目忙。

## 项目和文件命令

```sh
# 项目：新建、修改、归档与恢复、删除
pwb projects create --name '项目 A' --description '业务资料' --alias 'A'
pwb projects update PROJECT_ID --description '新的说明'
pwb projects update PROJECT_ID --alias '新别名' --alias '另一个别名'
pwb projects update PROJECT_ID --clear-aliases
pwb projects archive PROJECT_ID
pwb projects restore PROJECT_ID
pwb projects delete PROJECT_ID

# 文件：新建、读取、修改、删除、版本与撤销
pwb files write PROJECT_ID 'notes.md' --text '初稿' --base-hash ''
pwb files read PROJECT_ID 'notes.md' --raw
pwb files write PROJECT_ID 'notes.md' --from ./notes.md
pwb files delete PROJECT_ID 'notes.md' --base-hash HASH_FROM_READ
pwb files history PROJECT_ID 'notes.md'
pwb files undo REVISION_ID

# 附件和整库 ZIP
pwb files upload PROJECT_ID '附件/图片.png' ./picture.png
pwb files download PROJECT_ID '附件/图片.png' --output ./picture.png
pwb projects export PROJECT_ID --output ./project.zip
pwb projects import PROJECT_ID ./project.zip
```

ZIP 导入保留 Obsidian 目录、附件、隐藏配置和双链；遇到已有路径报冲突，不覆盖。`write --from -` 和 `upload ... -` 可从 stdin 读取。

`projects update` 只改变指定字段，保留其他设置与默认助手 ID。`--expert`、`--plugin` 可重复，用于替换启用的能力包 ID 列表；`--default-expert` 设置默认专家。清空列表可用通用 PATCH 接口传 `[]`。

`projects delete` 是真实删除，无交互确认：删除项目目录、对话、执行记录、定时任务和待发送通知；活动执行或发送尚未结束时拒绝删除。保留最小消息摘要用于防止渠道迟到重放；共享文件版本内容库不在单项目删除时清理。归档则保留全部数据。文件删除可通过版本撤销，项目删除不能通过文件撤销恢复。

## 通用 API：会话、任务、专家与设置

`pwb api METHOD /path` 使用同一密码访问 `/api/v1` 下的所有者接口；渠道桥接专用接口仍使用内部凭证。JSON 请求体支持 `--data` 或 `--from FILE`，`--from -` 为 stdin。路径必须是当前工作台的相对路径。

```sh
# 查询项目会话、历史和运行记录
pwb api GET /projects/PROJECT_ID/sessions
pwb api GET /sessions/SESSION_ID/history
pwb api GET '/runs?sessionId=SESSION_ID'
pwb api GET /runs/RUN_ID
pwb api GET '/events?sessionId=SESSION_ID&json=1&after=0'

# 新专题、提交一次模型任务（查询/编辑文件不调用模型）
pwb api POST /projects/PROJECT_ID/sessions --data '{"title":"资料整理"}'
pwb api POST /messages --data '{"eventId":"MY_UNIQUE_REQUEST_ID","sessionId":"SESSION_ID","text":"阅读资料，整理一份摘要"}'
pwb api POST /runs/RUN_ID/stop --data '{}'

# 专家、定时任务、部分更新项目
pwb api GET /experts
pwb api GET '/schedules?projectId=PROJECT_ID'
pwb api PATCH /projects/PROJECT_ID --data '{"expertIds":[],"defaultExpertId":"","pluginIds":[]}'
```

同一任务重发必须保留同一个 `eventId` 和原请求体；更改内容使用新 ID。`POST /messages` 是提交入队，不能将提交成功视为执行完成；后续通过运行记录查询结果。CLI 提交的任务来源为网页入口，不向微信／飞书主动广播。

## 直接 HTTP 调用

不安装 CLI 时也可调用，`curl -u owner` 会提示输入密码：

```sh
curl -u owner https://dsh.qianlan333.cloud/api/v1/projects
```

同一密码具有个人工作台所有者权限，没有单独的 Agent 账号和权限配置。

## multipart：专家与二进制（CLI 0.3.0）

`pwb api` 支持可重复的 `--form KEY=VALUE` 与 `--file FIELD=PATH`；保留指定 HTTP 方法。它们与 JSON 的 `--data`／`--from` 互斥。最多一个文件可从 `-` 读取 stdin；返回文件用 `--output`。

```sh
pwb api GET /experts/EXPERT_ID
pwb api GET /experts/EXPERT_ID/versions
pwb api POST /experts/EXPERT_ID/changes \
  --form 'changes={"baseDigest":"DIGEST_FROM_READ","files":[{"op":"write","path":"参考/示意图.png","upload":"resource"}]}' \
  --file resource=./diagram.png
pwb api POST /experts/EXPERT_ID/changes \
  --data '{"baseDigest":"DIGEST_FROM_READ","metadata":{"name":"新媒体专家","description":"选题与内容方法"}}'
pwb api GET /experts/EXPERT_ID/export --output ./expert.zip
pwb api POST /experts/EXPERT_ID/copy --data '{}'
pwb api DELETE /experts/EXPERT_ID --data '{"baseDigest":"DIGEST_FROM_READ"}'
pwb api POST /projects/PROJECT_ID/directories --data '{"path":"导入资料"}'
pwb api POST /projects/PROJECT_ID/import --form targetDir=导入资料 --file file=./vault.zip
pwb api POST /projects/PROJECT_ID/upload --form path=附件/data.bin --form conflict=rename --file file=./data.bin
```

专家修改必须使用读取时的 digest；409 时重新读取并解决冲突，不直接重试覆盖。删除专家暂停显式绑定的定时任务，已提交任务继续使用固定版本。接口细节见仓库 `docs/ARCHITECTURE.md`。

对话生命周期和全局工具也可通过现有 `pwb api` 调用，无需换登录方式，例如 `pwb api GET /recycle-bin`、`pwb api GET /cli-tools`、`pwb api GET /tool-jobs`。写操作与请求体见 [接口说明](../docs/ARCHITECTURE.md#对话生命周期与全局工具)。`pwb` 的工作台密码鉴权不设到期时间；飞书和 CRM 登录仍遵循各自服务端授权期限，两者不同。
