# 自动应答跨端合同 v1（冻结）
两个接收器均为 HTTPS POST。W 为现有 /api/v1/gateway/hooks/{connectionId}（可含工作台 BasePath），网关原生验签、载荷与订阅保持兼容。C 为 CRM /api/v1/integrations/auto-reply/events（包含实际应用前缀）。以下 CRM 协议只处理 application/json，最大 16KiB。供同一绑定使用的两端密钥由宿主注入，绑定及两个允许的目标 origin 由部署提供，禁止携带凭据重定向或发往未受信 origin。

签名头 X-AR-Key 标识不透明的当前绑定密钥 ID；X-AR-Time 为十进制 Unix 秒，与当前时间相差最多 300 秒；X-AR-Sig 为小写 hex HMAC-SHA256(secret, UTF8("ar1\n"+direction+"\n"+keyId+"\n"+timestamp+"\n")+rawBody)，direction 为 crm-to-wb 或 wb-to-crm。按原始字节验签，混合网关与 CRM 认证头拒绝。

POST W 读状态 {"v":1,"op":"status","staffUserId":"staff-a","externalUserId":"external-a","actor":"operator-a"}。staffUserId 为该关系的企微托管员工 userid，actor 来自可信 CRM 员工会话，服务端核对操作权限；绑定范围由 key 与入口固定。成功 200 {"mode":"human","version":4,"bindingVersion":1,"sessionId":"s1"}；mode 可为 auto/human。禁用绑定、无唯一匹配和未知关系均不得建客或改状态。

POST W 恢复在同一请求字段上把 op 换为 resume，增加 sessionId、version、bindingVersion 及 requestId（UUID）。事务中验证当前关系和版本、保存操作人及幂等结果。相同请求 ID 同一载荷重放返回原结果，不重复变更；不同载荷冲突。200 为写入完成，超时后查询；恢复本身不生成 Run。旧通知/跨项目或多个匹配拒绝。

POST C 状态变化 {"v":1,"op":"state.changed","eventId":"UUID","staffUserId":"staff-a","externalUserId":"external-a","sessionId":"s1","bindingVersion":1,"version":4,"mode":"human"}。CRM 在已启用绑定下作展示失效/版本刷新，侧边栏打开及操作后读取 W 的权威状态，200 {"ok":true}；未启用时验证来源后忽略并确认，零客户正文。

地址配对：安装预置 W、C、keyId、secret 引用和受信 origin；CRM 管理员编辑 W/C 后，CRM 由后端向候选 W 发 {"v":1,"op":"configure","callbackUrl":"https://crm.example/...","requestId":"UUID"}。W 先向候选 C 签名发送 {"v":1,"op":"probe","nonce":"随机挑战"}，C 验签后 200 {"nonce":"原值"}（未启用也可探测）；W 验证后持久更新绑定回推目标，200 {"ok":true,"callbackUrl":"已生效地址"}。配置写限绑定、受信 origin、服务后端管理员路径；初始 secret/key/origin 的信任关系不能通过此操作改写。失败显示待生效，可用同一请求对账；切环境不得复用身份凭证。

共同错误形状 {"error":"code"}；400 invalid_request、401 invalid_signature、403 forbidden、404 not_bound、409 stale_version / ambiguous / request_conflict、503 unavailable。CRM 自身集成关闭时其侧边栏恢复 API 返回 403，禁止调用 W；不影响工作台项目。共享夹具为本目录 auto-reply-contract-fixture.json，其中所有身份与密钥均为合成测试值。
