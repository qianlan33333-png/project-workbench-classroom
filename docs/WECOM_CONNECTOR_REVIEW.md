# 七项修复与本地对抗复核

日期：2026-11-02；基线 `9bb06e5`。范围：本地开发，不部署、不真实联调。

| 原问题 | 修复与本地回归 |
|---|---|
| 客户容器没有浏览器端口但 Execute 强制查询 | `TestGatewayExecuteDoesNotRequireBrowserPort` 使用子进程 SDK 协议替身，完整走过初始化、提交、答复、结束；任何端口查询都会使替身失败 |
| 客户工具可以读取模型密钥 | 不复制模型凭证，仅挂载本轮桥；`TestGatewayRuntimeMountsOnlyGrantedKnowledgeAndNoModelSecrets` 检查客户挂载内容，`TestGatewayModelProxyKeepsCredentialOnHostAndScopesRequests` 验证真实认证仅到固定本机替身上游 |
| 客户可读取他人归档 | 默认空的 `knowledgePaths` 精确共享文件快照；同一项目的他人归档和内部文件不进入挂载，符号链接、隐藏路径和归档路径拒绝；业务 CLI 授权仍保留 |
| 慢入口路由遗漏 pending 超时 | 独立超时循环覆盖 inbox 与 runs；`TestGatewayPendingDeadlineIsIndependentOfSlowIngressRouter`、`TestGatewayPendingDeadlineDoesNotWaitForSlowNotifier` 验证慢路由/慢通知及被占用项目锁下仍停答 |
| 旧投递回执再次暂停已恢复会话 | `TestGatewayOldDeliveryCannotPauseResumedConversation` 覆盖 failed、unknown、轮询超时和重启恢复；原投递仍结算，新会话版本保持 auto |
| 停机后通知越窗重发 | 首次尝试时间持久化，外发前校验时效/次数；`TestGatewayNotificationWindowCheckedBeforeExternalEffect` 覆盖 pending/sending 两种重启状态，无重复外部动作 |
| 同租户停用连接不能替换 | `TestGatewayDisabledIngressCanBeReplacedWithoutRevivingOldWork` 验证新账号范围接入、双启用冲突、旧归属保持和旧消息重推去重 |

实现阶段对抗复核发现并处理了三个相邻边界：超时循环与后台维护分离；运行清单使用原子发布、
不跟随客户可写的符号链接，外部专家挂载不写入客户可控的 skills 路径；替换入口保留同租户的
消息去重范围，避免跨连接重放旧输入。

代理另验证任意目标/方法/模型拒绝、重定向不跟随且错误正文不泄漏凭证、请求取消向上游传播、
私有桥缺失时有界失败、SSE 字节流保留。对应 Go 的 `TestGatewayModelProxyRefusesRedirectAndCancelsUpstream`
及 Node 的 `gateway-model.test.mjs`。最终代理审查还修复了 JSON 字段大小写与重复键解析差异，
`TestGatewayModelProxyCanonicalizesDuplicateModelKeys` 及混淆字段输入验证转发对象与校验对象一致。这些替身测试不证明真实 Harness 容器安装已经通过。

本地最终验证：Go race 126 项通过、8 项真实环境跳过；Node 9 项通过；vet、Web check/build、
Linux amd64 后端构建通过。机器可读收据位于 `.var/connector-acceptance/`，私有生成物不进入 Git。

剩余验收边界：真实 Podman/固定 SDK 加载、真实模型服务兼容性、企微回调/收发和飞书通知仍需后续
独立验收。本轮不执行；已提交给网关的消息仍无法撤回，不能承诺消除所有人工同时回复的竞争。
