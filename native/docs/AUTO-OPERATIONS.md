# 自动化运营

「项目 → 新建项目 → 专项项目 · 自动化运营」使用原生侧栏单行入口。内测策略为「新人前 7 天承接」。用户档案来自单独的授权资料，策略名单来自当天的原任务，历史通过原任务 ID 回查。档案设置和策略设置各自选择时间与专家，具体业务判断从现有专家目录加载。专家编辑发布后，下次执行读取新版本，已经开始的任务继续使用原版本。

## 配置来源

`components/operations-source/collect.py` 通过标准服务器已有的档案、HXC 用户与素材 GET 接口获取批准的 `safe_user_ref`。原服务器的定点核对可以使用 `--operator-env`，该参数只接受标准服务器本机的既有认证。持续刷新使用 `server.mjs` 提供的 `operations_refresh` 标准 MCP 工具。MCP 连接登记到工作台的现有来源目录。

运行来源适配时提供 `WORKBENCH_OPERATIONS_PROJECT` 与 `WORKBENCH_OPERATIONS_TOKEN`。直连接法另外提供 `WORKBENCH_OPERATIONS_REFS`（一行一个已批准 ID，最多 50 人）、`WORKBENCH_OPERATIONS_STATE`（私有目录）、`WORKBENCH_OPERATIONS_ARCHIVE_ORIGIN` 和 `WORKBENCH_OPERATIONS_ARCHIVE_AUTH`。使用安装包自身的官方 MCP SDK。服务监听本机 7396，来源连接注册 `operations_refresh`。重试只读取原操作编号对应的冻结结果，并复查其中的每一位用户仍在当前允许名单。来源快照变化、映射冲突或权限拒绝时保留上次成功发布。

跨机接法使用 `WORKBENCH_OPERATIONS_SSH_CONFIG`，固定目标 `workbench-operations-source` 并核对服务器主机密钥。标准机独立系统账号的专用公钥限制为工作台出口地址、禁止转发和终端，只能执行 `ssh-entry.sh` 的 `read <operationKey>`。它只获准通过 `collect-approved.sh` 读取根用户维护的固定名单和本机原接口。原操作员凭证留在标准机原配置里。工作台使用 `deploy/project-workbench-operations-source.service` 和独立的 MCP 令牌，仅允许本专项的项目和内部 Session 调用。

先通过 Worker 本机的 owner 接口登记有界资源，字段板块只开放 `nickname`、`profile_fields`、`evidence`、`source_watermarks`。由项目身份执行首次刷新，完成后用 `POST /operations/setup` 绑定项目、输入资源和板块。启动前可用真实只读导出的 JSONL 做隔离验收。需要持续更新时使用上述 MCP 来源连接。原系统的用户和素材接口要求 operator 角色，原 agent-read 白名单仅包含其专用代理路径，跨主机部署需要有明确授权的来源身份。

知识库在专项设置中选择已授权且含正文 `text` 字段的已发布板块。每次执行在工作台原资源读取门禁下固定版本，取有界正文片段给专家。HXC 返回的素材候选仅提供标题、摘要与来源版本。CRM 订单事实可以从原档案已经核验的订单摘要读取。

## 原中心工作包与全量档案

同一项目来源可以显式启用 `readSource`。`operations_read` 走现有 SSH 机器身份的 `directory` 固定命令。工作包读取原 `work-packages`，名单读取原 `test_candidates` 分支并核对逐人去重人数。这个分支在原系统生成、审批和发送动作之前返回。档案用原 `archive/users` 的总量、搜索与游标，单人详情再读取同一档案的发布记录、联系方式、人工修订、会话提炼、关系中心和产品判断。只有可读且身份一致的补充来源用于显示。

全量档案与计划核对是查询。工作台试点名单、待发人数和原回执仍使用各自的固定记录和授权。初次核对记录见 [原计划与全量档案](../../docs/plans/2026-11-10-operations-source-parity.md)。

原工作包的执行接线使用同一来源连接与独立的 `workbench-operations-execute` SSH 主机别名。该别名使用专用密钥，固定入口为 `ssh-execution-entry.sh`，只允许 `package` 命令。原只读身份仍走 `ssh-entry.sh`。执行身份也只从工作台 Worker 的内部项目任务使用。生成、审核的输入由工作包和原任务固定，模型及浏览器均不能指定原接口地址。

生成复用原中心的准备、逐条导出和导入。工作台 Harness 按原专家输入生成，原中心继续验证稿件合同、资料水位和重复触达。现有 Temporal 项目写入锁串行化生成，正文和逐人结果保存在原中心。打开用户时单独读取完整话术、本轮判断和随附卡片。保存最终稿复用原审稿接口，绑定原包摘要、条目与修订号，成功后读回原版本。此版工作包开放生成和审稿，CRM 交接入口关闭。历史可按原业务周查回原执行和逐人回执。

## 发送接线

使用既有 `crm-cli` 通道。对明确授权的测试收件人登记独立资格资源，记录 ID 与原档案 ID 一致。注册现有 `RecordCheck`，从资格板块绑定 `/customerId`、`/staffId`、`/businessSlot`。`POST /operations/setup` 接收 `eligibility`、`check`、`staffId`。专项设置只列当前项目允许使用的真实 CRM 通道。

点击「确认并发送」准备固定的合格记录清单，使用既有本人决定。收件人和账号来自资格资料，正文来自同一任务的已发布稿件。计划已保存但尚未启动时沿用原发送任务编号。原任务结束后，只对确认未提交的记录重新核对当前资格和账号，并要求新的本人决定。旧计划的未提交记录先锁定，已经提交、明确失败或结果未知的记录继续保留原计划与回执。跨日可以从历史进入同一份名单续办。

## 验收

验证原接口读回、项目授权、档案和策略自然触发、原方法与素材版本、结果和历史一致，再在明确授权的测试范围验证本人确认、企微手机确认及原 CRM 逐人回执。接线不齐时页面呈现当前资料和真实状态。
