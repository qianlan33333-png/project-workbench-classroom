# 执行与数据边界

## 不变式

- Project 默认助手 → Session → Harness ID 稳定对应。数据库触发器禁止改写对话的项目和 Harness ID；恢复失败会报错。
- 新消息先持久化、按来源／连接／外部事件 ID 去重，再确定归属和排队。旧消费者的已处理事件哈希也能阻止迁移后重放。
- 应用不拼接最近 N 条聊天。新输入、必要的来源引用和附件交给 Harness；它自行恢复、压缩、执行。
- 项目正文按需读取。入口归属器只接收目录元数据，禁用文件和执行工具；它不是独立常驻 Agent 服务。
- 渠道焦点独立，显式平台回复或短编号优先。最近成功发出的本渠道回复标记为辅助线索。定时结果不改变焦点。
- 每次业务执行只装载该项目目录、自己的运行状态、固定版本专家及启用的 MCP。专家包只读，项目笔记正常进入文件版本。

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
| 专家 | `/experts`、`/experts/import`、`/experts/git`、`/experts/:id/skill` |
| 对话 | `/projects/:id/sessions`、`/sessions/:id` |
| 消息与执行 | `/messages`、`/messages/:id/resolve`、`/inbox`、`/runs`、`/runs/:id/stop`、`/interactions/:id/answer` |
| 事件 | `/events`，`json=1` 支持批量历史读取 |
| 定时任务 | `/schedules`、`/schedules/:id`、`/schedules/:id/runs`、`/notification-targets` |
| 插件与传输 | `/plugins`、`/connections`、`/deliveries`、`/channel/*` |
| 浏览器 | `/browser/*`，仅登录用户且有活动任务时代理 noVNC / WebSocket |

发送 pending→sending 后才调用平台；异常重启将遗留 sending 置为 unknown，不能重新领取。飞书存平台消息回执；微信只把成功提交记为 accepted，不能等同于本人已收到或已读。
