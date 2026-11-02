# 标准企微连接器

协议版本 v1；CLI 版本 0.2.0。一个包可供不同业务复用。依赖网关的同一套公开协议，
不直接连接设备或运营商。CLI 可独立使用；自动应答还需要安装本版本工作台后端。
首期不修改前端，安装与项目、专家、知识管理继续使用工作台已有能力。

升级带 CRM 恢复功能的工作台后端时，同时安装 0.2.0 CLI。恢复命令现在先取得当前会话及绑定版本，再提交恢复。旧版 0.1.0 的无版本恢复请求会被拒绝。

## 安装和配置

从 `wecom-gw-cli-0.2.0-<系统>-<架构>.tar.gz` 解压可执行文件，校验同目录 sha256。
工作台的现有 CLI 安装器可登记 HTTPS 发行包和摘要，入口命令为 `wecom-gw-cli`。
本仓未配置远程发布源，因此本地生成的发行包不声称已经发布到 GitHub。

复制 `examples/profile.json`。密钥通过已有网关后台创建，写到本机 `api-key` 文件；
工作台密码写到 `workbench-password`。这两个文件设置 `chmod 600`，目录设置 `chmod 700`。
也可使用 `apiKeyEnv` / `passwordEnv` 引用环境变量；同一项只能配置一种引用。
相对路径以 profile 所在目录为基准。不要把密钥放在命令参数、项目文件或 Git 中。

```sh
wecom-gw-cli config init --config gateway.json --data examples/profile.json
wecom-gw-cli accounts list --config gateway.json
wecom-gw-cli doctor check --config gateway.json
wecom-gw-cli webhook show --config gateway.json
```

`doctor check` 仅读取账号可达性与账号状态，不代表模型、回调或收发验收已经通过。
所有命令输出 JSON；退出码 0 表示命令成功，1 表示失败，错误写入 stderr。
发送返回的 httpStatus=202 仅代表网关接收；只有查询/回调的状态才说明后续结果。
`sent` 也不代表客户已读。

## 连接、项目和专家

1. 先准备本版本工作台的公开 HTTPS 回调地址：
   `<workbenchUrl>/api/v1/gateway/hooks/support-gateway`。有 BasePath 时把它包含在 workbenchUrl 中。
2. 用下面命令设置回调；只读检查若发现已有其他地址，会拒绝覆盖。已有 secret 但本机
   文件缺失时也会拒绝，不自动轮换。新 secret 保存到 profile 指定的私有文件，不打印。
3. 登记宿主连接，再用不同绑定配置项目与专家。连接中的凭证保存在宿主数据库私有目录，
   不传给模型和旧渠道桥。工作台地址应为 HTTPS（仅本机回环测试允许 HTTP）。

```sh
wecom-gw-cli webhook apply --config gateway.json --url https://workbench.example.com/api/v1/gateway/hooks/support-gateway
wecom-gw-cli connection apply --config gateway.json --data examples/connection.json
wecom-gw-cli binding apply --config gateway.json --data examples/binding.json
wecom-gw-cli binding list --config gateway.json
```

订阅默认全部事件，以便接收人工发送、发送结果与状态变化。网关只有一个租户级回调入口，
同一租户多个托管账号由该入口分别绑定项目。`tenantId` 可显式配置；省略时仅在通过验签、
账号白名单校验的第一个事件中锁定，之后不可切换。相同网关存在未锁定租户时，不允许创建
第二个含糊入口。回调开始早于连接登记时会失败重试，不会被提前确认后丢失。
账号尚未绑定项目或绑定被停用时同样返回失败，事件去重写入一起回滚；完成配置后可接收原事件重试。

绑定必须引用现有业务项目及已加入项目的专家。通知始终使用现有飞书 CLI 的 bot 身份，
bot 应已加入 notifyChatId 对应群，并具有发送权限。首期没有认领、升级提醒和飞书管理机器人。
`contactIds` 非空时仅服务这些客户，适合灰度验收；删除此字段才开放整个已授权账号的私聊。

`toolIds` 和 `pluginIds` 默认空，只向客户会话显式授予所需业务 CLI/MCP。
宿主飞书和企微发送工具不能作为客户 CLI 授予。第三方业务工具自身的业务权限由其账号和
专家配置负责。外部客户执行强制使用 Podman，项目资料只读、原生会话目录独立，不继承
全局工具状态或其他客户运行目录。不得把发送凭证存入所选业务项目的知识文件中。

`knowledgePaths` 是明确共享给客户的项目文件列表，默认空，不会自动共享整个项目。
例如 `"knowledgePaths": ["知识库/常见问题.md", "知识库/服务规则.md"]`。每次运行创建只读快照，
最多 32 个文件、每个不超过 1 MiB；拒绝符号链接、隐藏路径及 `对话归档/`。
专家包仍独立挂载；内部资料和其他客户归档不进入客户容器。

客户会话使用宿主的 `Provider=deepseek-official` 和 `Model`，真实模型密钥不复制到容器。
密钥从宿主 `HarnessHome/.credentials.yaml` 的 `refs.DEEPSEEK_API_KEY` 读取，或从宿主环境读取。
`GatewayModelAPIKeyEnv` 可指定其他凭证引用；`GatewayModelBaseURL` 可指定受信的 HTTPS
DeepSeek Messages 地址，默认 `https://api.deepseek.com/anthropic`，不继承通用 HarnessPatch。
这些是工作台宿主配置项，不写入连接 profile 或客户项目。客户容器只持占位值，模型请求经
本轮私有 Unix socket 桥转发，由宿主注入真实密钥；仅允许固定模型的 Messages 请求，
拒绝重定向及任意目标。此路径首期不支持账号登录 Provider 或其他模型协议。

修改绑定须提供已有绑定 `id`，版本递增并使旧结果失效；已有 Session 项目/Harness ID 不改。
同配置重复提交不产生新版本。停用连接会暂停已有客户会话，再启用也不会重放旧回复。
调整连接账号范围时先停用旧连接，再用新 ID 登记替代连接并建立绑定；同租户仅允许
一个启用入口，替代连接启用期间旧连接不能重新启用。按已有步骤显式更新网关回调 URL。

## 专家结果协议

专家仍负责业务判断和信息调取，最终返回一个 JSON 对象，不用 Markdown 包裹：

```json
{"action":"reply","text":"客户可见的回复"}
```

```json
{"action":"handoff","reason":"需要人工处理的原因","summary":"必要的客户上下文"}
```

```json
{"action":"ignore"}
```

目标客户由宿主固定，结果不接受 `to` 等额外字段。等待资源或应答耗时不切换人工；旧绑定的 timeoutSeconds 不再生效。执行失败或格式错误仍转人工。
图片/文件转人工；历史、群、系统及 unsupported 消息不触发模型。
迟到的新消息若早于当前会话已接收消息，则记录并转人工核对，避免乱序内容被静默遗漏。

## 人工处理和核对

```sh
wecom-gw-cli session list --config gateway.json
wecom-gw-cli session pause --config gateway.json --id SESSION_ID
wecom-gw-cli session resume --config gateway.json --id SESSION_ID
wecom-gw-cli events list --config gateway.json --limit 100 --cursor CURSOR
wecom-gw-cli events redeliver --config gateway.json --id EVENT_ID
```

转人工时会话模式与通知事务性保存；飞书失败不会恢复 AI。通知最多以同一幂等键尝试
5 次，从首次尝试起限制在飞书一小时去重时间窗内，并预留单次请求超时。
次数与时间窗在外发前检查；重启后的 pending/sending 也不会越窗重发。
未确认记录保留给负责人核对，不承诺通知已读或有人认领。
人工在企微直接回复也会暂停对应会话。恢复仅处理之后的新输入，旧结果不能重新获得发送权限。
最近一次人工回复作为明确标注的来源引用交给后续专家，不拼装其他客户历史。

企微提交发生传输错误后，仅在从首次提交计算的安全重试窗口内复用原正文与幂等键（最多 5 次），平台明确 `unknown` 后不再重发。
状态回调与按消息 ID 查询对账；未知状态保留，迟到的确定回执可澄清。
已交给设备的消息不能撤回，人工与已经提交的 AI 消息仍可能撞车。
暂停客户会话不会调用网关的账号 pause 接口。

## 运行和交付

工作台扩展原有 SQLite、inbox/runs/deliveries 和 Worker，增加绑定、客户模式及事件去重元数据；
项目写锁被占用时，消息保存在队列中；人工介入或权限变化会使原任务失效。旧版本回执仅更新原投递，不改变人工明确恢复后的会话模式。不同客户的等待及已受理回执的轮询均不能抢占较早就绪的新投递。

```sh
go test -race ./...
go vet ./...
python3 scripts/package-wecom-connector.py
```

产物和指纹位于 `.var/connector-release/`。真实网关、真实 Harness、真实飞书分别验收，
离线协议和故障替身测试不等同于生产部署或真实业务验收。
