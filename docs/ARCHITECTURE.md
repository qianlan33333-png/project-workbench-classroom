# 执行与数据边界

## 标准企微网关连接器（可选）

公开签名入口 `/api/v1/gateway/hooks/:id` 在原始字节验签、租户/账号范围检查和事务持久化后确认。
管理入口 `/api/v1/gateway/{connections,bindings,sessions}` 使用现有 owner 鉴权，旧 channel token
不能读取连接器凭证。连接器状态仍存 connections/connection_secrets，绑定、会话模式和事件哈希
分别存 gateway_bindings/gateway_sessions/gateway_events；业务任务和外发继续用 inbox/runs/deliveries。
Worker 仅增加外发泵，复用现有队列和项目锁。202 记 accepted，由异步结果或读取确认最终状态。
外部客户无法通过普通 messages 入口伪造来源，无法进入本人渠道的控制命令和入口归属器。
模式版本与绑定版本随输入固定；人工、换绑、停用连接后，旧结果无法获得新的发送权限。
外部会话强制 Podman、项目只读、原生目录独立；只装载明确授予的业务工具和当前客户历史。
项目资料通过绑定 `knowledgePaths` 的文件快照共享，不挂载项目正文目录或客户归档。
客户容器仅持模型占位凭证，通过本轮私有桥请求固定的 DeepSeek Messages 接口；真实模型密钥留在宿主。
技术实现和真实 Provider 验收分开，详见 [连接器说明](WECOM_CONNECTOR.md)。

## 不变式

- Project 默认助手 → Session → Harness ID 稳定对应。数据库触发器禁止改写对话的项目和 Harness ID；恢复失败会报错。
- 新消息先持久化、按来源／连接／外部事件 ID 去重，再确定归属和排队。旧消费者的已处理事件哈希也能阻止迁移后重放。
- 应用不拼接最近 N 条聊天。新输入、必要的来源引用和附件交给 Harness；它自行恢复、压缩、执行。
- 项目正文按需读取。入口归属器只接收目录元数据，禁用文件和执行工具；它不是独立常驻 Agent 服务。
- 渠道焦点独立，显式平台回复或短编号优先。最近成功发出的本渠道回复标记为辅助线索。定时结果不改变焦点。
- 每次业务执行装载该项目目录、自己的运行状态、固定版本专家及全局启用的 CLI／MCP。专家和 CLI 发行包只读，CLI 登录状态在项目外独立持久化；项目笔记正常进入文件版本。

## 进程

`project-workbench.service` 是唯一 igo 业务服务，包含路由、串行执行队列、十秒调度检查和 SSE。`project-workbench-channels.service` 只维护飞书官方 SDK WebSocket 和微信 iLink 长轮询；不运行第二套 Agent。

业务任务最多一个，归属请求单独运行轻量 Harness。rootless Podman 容器业务内存上限 2300MB、入口 600MB，进程数上限 256。当前项目和运行状态挂载读写，专家挂载只读；结束时强制清理容器，随后才记录文件变化并释放项目锁。

运行状态包括 queued、running、waiting、completed、failed、cancelled、interrupted。stopping 仅用于进程或版本收拢尚未完成，保留写锁并显示原因。

私有 Unix socket 将本次 Run 绑定到应用 MCP。Agent 可搜索本项目历史、读取本项目 Run、列出或管理定时任务，以及读取用户本次明确列出的外部资料。工具参数不能扩大项目范围。自动任务不能自行创建更多定时任务。同一运行重复创建相同任务会返回同一任务。

## 持久化

SQLite 保存 projects、sessions、experts、inbox、runs、events、interactions、project_locks、runtime_sessions、harness_events、revisions、baselines、connections、deliveries、schedules 和 schedule_occurrences。JSON 记录提供可演进字段，关系键与唯一索引保证归属和幂等。

四个身份映射分别存在：默认助手、Harness ID、entry_focus、入站／出站消息关联。账号 ID、外部聊天 ID、平台消息 ID、Session ID、Run ID 分别保存，不互相替代。

`projects/<id>/files` 是正文；`experts/<id>/<digest>` 是不可变方法包；`blobs` 是文件内容版本；`runtime/projects/<id>` 保存原生会话和浏览器状态。模型账户来自独立配置目录。

SSE 使用持久化的全局递增 seq，支持 Last-Event-ID 和 after 参数。前端刷新按对话重读事件，执行记录可分页；这些分页不参与模型上下文构造。

## API

所有接口在配置 BasePath 下的 `/api/v1`。网页使用 HttpOnly 会话 Cookie；渠道传输使用独立 Bearer，Agent 无法得到渠道凭证。外部 CLI 使用 HTTP Basic `owner:<工作台密码>`，每次直接校验当前密码，不创建登录记录、不设到期时间；显式的无效 Authorization 不会回退到浏览器 Cookie。

`PATCH /projects/:id` 只更新提供的可编辑字段，不改变默认助手与身份；`DELETE /projects/:id` 在同一事务内清除项目业务记录并持久化文件清理意图，启动时恢复尚未完成的清理。运行中的写入和发送会阻止删除；排队执行随项目删除。保留原消息 ID 与摘要阻止迟到消息重放，共享内容寻址版本库不在单项目删除时回收。项目文件读写继续使用原有写锁、CAS 和修订机制。

| 能力 | 路径 |
|---|---|
| 账号 | `/auth`、`/login`、`/logout`、`/settings/password` |
| 项目与资料 | `/projects`、`/projects/:id/files`、`/file`、`/upload`、`/import`、`/export`、`/search`、`/revisions` |
| 专家 | `/experts`、`/experts/import`、`/experts/git`、`/experts/:id/skill`、`/experts/:id`、`/versions`、`/files`、`/file`、`/changes`、`/copy`、`/export` |
| 对话 | `/projects/:id/sessions`、`/sessions/:id` |
| 消息与执行 | `/messages`、`/messages/:id/resolve`、`/inbox`、`/runs`、`/runs/:id/stop`、`/interactions/:id/answer` |
| 事件 | `/events`，`json=1` 支持批量历史读取 |
| 定时任务 | `/schedules`、`/schedules/:id`、`/schedules/:id/runs`、`/notification-targets` |
| 插件与传输 | `/plugins`、`/connections`、`/deliveries`、`/channel/*` |
| 浏览器 | `/browser/*`，仅登录用户且有活动任务时代理 noVNC / WebSocket |

发送 pending→sending 后才调用平台；异常重启将遗留 sending 置为 unknown，不能重新领取。飞书存平台消息回执；微信只把成功提交记为 accepted，不能等同于本人已收到或已读。

## 文件和专家管理接口

- `POST /projects/:id/directories`：JSON `{ "path": "资料/参考" }`，沿用项目写锁。
- `POST /projects/:id/upload`：multipart `file`、完整相对 `path`、`baseHash`、`conflict`（空／skip／rename／replace）。目标目录包含在 path；返回修订和 skipped。
- `POST /projects/:id/import`：multipart `file` ZIP 和可选 `targetDir`，始终不覆盖。
- `GET /projects/:id/file?path=...&view=1`：返回 hash、binary、tooLarge、editable；超过 2MiB 时不把正文塞入编辑器。`raw=1` 获取原字节，原 CLI 读取行为兼容。
- `GET /experts/:id`：expert 和 usage（projects、sessions、schedules）。`GET /versions` 返回版本元数据；`GET /files`、`GET /file?path=...` 和 `GET /export` 可带 digest 读取指定版本，即使专家已删除仍可按已知 digest 读取文件。
- `POST /experts/:id/changes`：JSON `{baseDigest, metadata?, files?}`。metadata 支持 name（展示名）、description；files 每项使用 op=write/mkdir/rename/delete、path，rename 使用 to，write 使用 content 或 upload 字段名。multipart 将该 JSON 放在 changes 字段，二进制放在对应 upload 字段。校验或 CAS 失败不会发布部分内容。
- `POST /experts/:id/copy`：克隆当前版本并生成新的内部标识。`DELETE /experts/:id` JSON `{baseDigest}`：删除目录登记并事务性清理关联；不可变版本仍保留。
- 现有专家 ZIP／Git 更新同样必须提供 baseDigest；新导入不需要。已排队和运行任务使用提交时 ExpertVersions，运行器不再依赖最新专家目录记录。

导航收起、文档、详情类型、尺寸和编辑草稿属于界面状态，不参与 Session/Harness ID。布局断点只改变 CSS；对话节点不会因详情开合重新挂载。专家详情通过 `#expert=ID` 恢复，专题仍通过 `#project=ID&session=ID` 定位。

## 网页发送回执与即时回显

`POST /messages` 返回已持久化的 Message，不承诺已开始运行。`GET /messages/by-event/:eventId` 使用同一所有者鉴权，仅查询网页来源的对应事件，404 表示尚无该消息；不能读取同名渠道事件。`message.received` 事件包含 id、eventId、source、sessionId，新 Run 带原始 eventId 用于界面去重，原 Session/Harness 身份不变。

网页在本标签页 sessionStorage 暂存未对齐的输入，以 eventId 显示“发送中／已接收／待确认／未能提交”；真正执行状态来自 Run。传输结果未知时先只读核对，不自动重发。用户显式核对重试时，只有 404 才提交同一 ID 和原请求。客户端消息和 Run 以消息 ID／事件 ID 对齐，不用正文猜测身份。旧回执不能修改当前输入框。

SSE 直接合并运行状态。全局 HTTP 刷新记下请求发起时的事件序号，拒绝覆盖此后收到的运行事件；迟到的旧记录也不能让完成的任务退回排队。待发送状态属于 UI，不改变服务端执行优先级。

## 无标题新对话与自动命名

`POST /api/v1/projects/:id/sessions` 可传空对象：立即创建稳定的应用 Session/Harness ID，`draft=true`、`title=""`、`titleSource=pending`。列表与入口语义目录不包含未发送的草稿，但 `GET /sessions/:id` 可以恢复明确链接的空会话。已有显式标题、默认项目助手及定时会话保持原有行为。

首条网页消息的接收事务同时写 inbox 和激活 Session：清除 draft，将原始消息开头最多 8 个词／24 个 Unicode 字符作为 fallback 标题；附件消息使用“附件讨论”。去重重试不会再次创建对话或覆盖首条标题。前端先通过待发送消息即时显示同一个侧栏条目，再以服务器回执收敛；不会等待路由、排队或模型。

首轮在同一 Harness 中启用固定版本自带 `session-title-first-prompt-llm`，使用原生辅助请求生成简短标题；它不往主对话追加命名消息、不建立第二段历史，不改变任务排序。以 `session/title` 的 provider 事件更新应用元数据，忽略 Harness 根据包装提示词生成的 fallback。标题生成失败／超时保留首句标题，聊天任务照常进行。

`PUT /sessions/:id` 仅修改明确传入的 `title` 或 `expertId`。手动标题非空且不超过 80 字，设为 manual；标题写入通过 SQLite 事务检查来源，迟到的 AI 结果不能覆盖人工命名。版本更新时间单调递增，客户端通过 `session.updated` 事件立即合并，慢快照不能覆盖较新的标题。改名和专家选择都不改变应用 Session/Harness ID。

## 对话生命周期与全局工具

置顶只修改 Session 元数据和排序，人工改名规则不变。归档使用现有串行队列的 `Run.kind=archive`，不向原 Harness 追加总结请求。归档冻结原对话；每个摘要阶段使用禁用工具的临时 Harness，分块覆盖全部应用问答，区分用户事实、真实结果与建议。先按原写锁保存并读回核验 Markdown，才进入回收站；失败保留原聊天，允许重试。维护运行不进入正常聊天和渠道回复。

原始记录保留 15 天，普通 API、历史检索和 SSE 均排除回收站内容；原生会话目录移出业务容器挂载范围，文件移动清单支持中断恢复。删除默认助手会建立全新默认助手并更新渠道焦点；找回原助手作为专题保留原 ID／Harness，不改变新的默认助手，不重放运行或投递。业务文件和归档 MD 不随聊天删除。

到期或手动彻底删除会移除应用原始消息、执行详情、交互、原生会话与调用配置，保留不可逆入站摘要以阻止迟到重放。SQLite 启用 secure_delete；部署的固定路径备份清理脚本同时清除受管回滚快照内的对应原件并 VACUUM。独立清除清单不包含正文；恢复旧数据时启动检查重新应用清单，必要时建立新的默认助手，避免复活已清除历史。清理未完成时保持隔离，不报告成功。

| 操作 | `/api/v1` 接口 |
|---|---|
| 置顶 | `PUT /sessions/:id/pin`，`{pinned:true}` |
| 提炼归档／停止归档 | `POST /sessions/:id/archive`、`POST /sessions/:id/archive/stop` |
| 移入回收站 | `DELETE /sessions/:id` |
| 回收站／找回／彻底清除 | `GET /recycle-bin`、`POST /recycle-bin/:id/restore`、`DELETE /recycle-bin/:id` |
| 全局 CLI 配置 | `GET/POST /cli-tools`、`DELETE /cli-tools/:id` |
| 安装与授权任务 | `POST /cli-tools/:id/install`、`/configure`、`/auth`、`/check`；`GET /tool-jobs` |
| 全局 MCP | 继续使用 `GET/POST /plugins`、`DELETE /plugins/:id`，`enabled` 全局生效 |

CLI 包安装复用异步工具操作记录，等待其他业务执行空闲；用户通过对话主动安装时，只跳过发起操作的当前运行，避免相互等待。固定版本目录与原子命令入口保留旧二进制。安装器只提取指定的独立命令，支持 Linux amd64 tar.gz，不做 npm／pip／源码构建市场。账号授权不等同于永久登录，按各服务的到期和续期协议处理。飞书、CRM 的配置与凭证各自独立。CLI 安装、配置、授权和检查也可通过现有应用 MCP 的 `manage_cli` 在对话中发起；自动定时任务不能修改全局工具。

MCP 直接使用固定 Harness 的客户端插件，不增加代理网关。每个全局 MCP 的启动及工具发现限时 5 秒；失败后卸载本轮连接并向 Agent 标明不可用，其余工作继续。工具调用与业务输出继续属于原项目、Session、Run。`project-workbench-root` 容器标签绑定当前数据目录，隔离预览与正式实例的遗留容器清理范围。

初始两套 CLI 固定官方 SHA256，发行包同时缓存对应原始 tar.gz。下载源不可用时仍可完成本地提取安装；缓存按摘要寻址，读取后再次核对实际内容。未缓存工具保持 HTTPS 下载路径，并给出有限超时与简短错误原因，不把带签名的完整重定向 URL 当作错误提示。

## 输入框能力选择

`GET /projects/:id/capabilities` 仅列出能力元数据：全局已安装且启用的 CLI、全局启用的 MCP、当前项目已加入的专家入口 Skill。正文不参与菜单。网页 `/` 搜索并选择后，`POST /messages` 可附加 `capabilities:[{kind:"cli"|"skill"|"mcp",id:"..."}]`，每条最多 8 项。

新消息入库前和归属入队时均校验能力可用性与项目范围；运行记录保存服务端解析的名称和命令，不信任客户端展示文案。Skill 继续使用运行提交时的不可变版本。执行提示明确本条输入选择的能力；不可用时报告原因，不静默换用其他服务。渠道不能从附件或消息内容注入此结构。

能力选择只作用于这条消息，不修改 Session 的专家、项目、Harness ID，也不通过重建会话实现。前端按对话保存选择草稿；收件失败可连同原输入恢复，重试仍使用原 eventId。已有输入在能力随后删除时仍去重返回原执行。原生 `/compact` 等文本指令在没有匹配菜单项时按原文发送，URL 中的斜杠不触发菜单。

专家下拉框只列一个通用助手和各已启用专家，以当前有效值显示；选择项目默认的同一能力时继续继承项目默认设置，没有重复的“项目默认／通用助手”选项。CLI 设置隐藏已完成操作流水，保留授权状态、当前进行中的操作和必要授权链接，后台任务记录仍用于恢复和核对。
