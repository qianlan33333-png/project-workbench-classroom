# 已有 CRM 侧边栏接入

自动应答仍是普通项目、原生 Session 和专家 Skill。AI/人工状态只保存在客户路由的 `autoReply/revision`；通知失败不能恢复 AI。不同发送通道显式配置 `modeConnectionId` 后，统一读取该账号下的客户模式，找不到唯一可信映射时停发。

在现有渠道私有 JSON 项增加 `crm`（不进入项目提示词）：

```json
{
  "crm": {
    "enabled": true,
    "hookId": "existing-installed-hook",
    "keyId": "existing-pair-key-id",
    "staffUserId": "trusted-staff-id",
    "bindingVersion": 1,
    "callbackOrigin": "https://crm.example.com",
    "callbackUrl": "https://crm.example.com/api/v1/integrations/auto-reply/events",
    "secretFile": "/etc/project-workbench/auto-reply/pair.secret",
    "contactMappings": {"trusted-contact-id": "verified-external-user-id"}
  }
}
```

只使用已验证的同租户、同账号身份映射，不按姓名猜测。密钥为宿主专用文件（绝对路径、普通文件、无组及其他人权限），或 `WORKBENCH_CRM_*` 环境变量。`hookId` 可保留 CRM 已安装的入口别名，不改变当前连接、客户或 Session ID；重复别名拒绝启动。

公开入口为 `/api/v1/gateway/hooks/{hookId}`，也接受 `/hooks/{connectionId}`。由反向代理仅把这些路径转到现有连接器端口；原始 JSON 按旧 v1 协议签名（`X-AR-Key/Time/Sig`），状态查询、显式恢复、callback configure/probe 均沿用旧协议。恢复校验原 Session、绑定版本与状态版本，同请求重放返回原收据；恢复只接新消息，不补发旧输入或草稿。

状态和事件同事务保存。既有 Temporal 通知计划每分钟扫描状态事件，以事件编号记录已接受的 Workflow，不以自增序号冒充提交水位。每次最多处理 1,000 个事件，通知失败不重跑专家。状态通知的重复提交使用相同 `eventId`，由 CRM 幂等处理；客户发送仍只查询原回执，不盲目重发。

新版只增加生成列及索引，不改旧表语义；迁移前保存一份本机 MySQL 恢复点。回滚只退程序，保留上线后的客户状态、渠道游标和回执。

推进型专家可复用本 Session 的追问与渠道工具。要求“信息齐全才登记”时，必须在通用登记工具的 JSON Schema 再校验；不能只靠提示词承诺，也不应在宿主硬编码年龄、收入等业务字段。


## 应答方法与语料恢复后的回滚

通用外部工具部署使用 `WORKBENCH_TOOL_SERVERS`。退回不支持此启动映射的旧版本（本次为原生08）时，先在同一发布锁内恢复上一份 `expertSkill/knowledge` 接入配置，再调用固定回滚入口；会话、资料及回执不回退。只退程序会让新专家找不到外部工具。当前一份必要路由恢复配置保存在宿主私有 `operations/native-answer-previous/routing.json`，不含账号密钥；程序发布验收及提交前保持旧路由，既有控制器可安全退回旧程序；程序提交成功后才原子启用新专家配置，配置验收失败时恢复旧路由。操作收据和旧版配置的具体路径见本轮实现记录，禁止把旧数据库覆盖回去。
