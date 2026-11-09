# CRM 原始档案采集验收

2026-11-09，正式工作台 `native-20261109-05`，上一成功版本 `native-20261109-04`。代码来源 `878d1a1df820aaec98656b396a0b4a1a563cd390`，tree `e2ab25a82167a4f85e8a007085941e6bba9d6a72`。运行 Worker SHA-256 `d60ddde167302e04053c854caa13ae92b16fc74bf0986c727514575bccd88e40`，制品归档 SHA-256 `eed73633903bc121f576cb4d5b7cdf6c1e73b832b370077f02dd23ee3ec7b0da`。

本次完成的是 CRM 原档案八个记录数组的独立拉取、字段清洗、原清单验证、正式发布与项目读回。没有从原上下文中心复制成品画像来冒充采集，也没有新建 CRM 客户发送计划。

## 首轮逐项验收

| 原数组 | 发布条数 | 原任务编号 |
|---|---:|---|
| customers | 24,315 | `3b103800-830a-5020-9b24-d1d982f7fb03` |
| users | 24,315 | `6c63870e-69c2-541a-b713-22a69a16df4d` |
| identities | 48,096 | `fc746c19-9457-5679-bd00-c5c24df3f718` |
| orders | 7,012 | `9914e204-3fda-5f22-8282-15d5957bbd70` |
| messages | 84,173 | `1a7a85be-c69a-5fb4-afc5-3b8bdb6c96ef` |
| surveys | 2,433 | `f6db8f48-821f-5235-b192-583a36acec12` |
| answers | 13,598 | `5a4b11cd-955c-58b5-bfa3-0dd09c365f1d` |
| activation_receipts | 507 | `fc88f5e6-7d5b-5cf2-926e-c3b6efe1afc2` |

合计 **204,449 条记录**，不是独立客户人数。客户主表和 users 是同一客户集合的两种原投影。

每项均由正式项目会话调用标准 MCP `data_refresh`，沿原 Temporal/MySQL 任务执行。程序核对源清单数量与 SHA-256 后发布；独立读取正式快照和实际存储行数一致。每项抽读三条，核对字段白名单、清洗字段和记录键，随后重复同一 `operationKey`，仍返回原已完成任务。没有重导入成功步骤。

身份记录使用原类型、范围和值的复合键；消息按消息、客户与参与角色区分。激活回执原导出没有回执主键，使用完整原始内容摘要和出现序号保留重复投影。其 ID 仅作快照记录引用，不代表客户 CID 或平台回执 ID。

## 授权、入口和使用

专用机器客户端 `workbench-crm-archive-ro-20261109` 仅有 `read` 与 `archive.facts.read`，限制原企业和工作台出口，未设置客户端到期。短期访问令牌按原接口合同自动换取；原宽权限客户端配置保持不变。本人 CLI 凭据继续在工作台受控位置保留，未写入 Git。

来源登记为 `crm-archive-cli`。资源为 `crm-archive-customers`、`crm-archive-users`、`crm-archive-identities`、`crm-archive-orders`、`crm-archive-messages`、`crm-archive-surveys`、`crm-archive-answers`、`crm-archive-activation-receipts`，归属私域运营项目，均使用 `facts` 段。

在该项目会话中刷新单项：

```json
{"operationKey":"crm-customers-20261109-02","destination":"crm-archive-customers"}
```

`operationKey` 只用字母、数字、下划线或连字符，换一轮刷新才换编号；续办、查询或响应丢失时保留原编号。`run_get` 查询执行状态，资料快照或发布事件给出采集条数，`resource_read` 使用固定版本读回。生成任务的逐条计数不用于替代采集快照数量。

已配置既有 Temporal 定时 `program-6c64dc61-a1d2-5372-a6b0-63af8f39e029`，北京时间每天 00:35、06:35、12:35、18:35。按表中顺序刷新八个来源，重叠跳过，正常成功通知静默，故障沿原规则。目的地为空，不进行生成和发送。可从原工作台定时入口暂停，不新增定时管理模块。

11:46:14 北京时间，使用官方 Temporal SDK 手动触发同一个正式定时，原任务 `01a1be69-e993-7af1-abbc-2cbbc97b6a1a` 完成，`run_steps` 八个子任务均 Completed 且 published。独立快照读回合计 **204,450 条**；消息数组增至 84,174，其余数量与首轮相同。该任务及子任务创建发送记录为零。此证据证明正式定时入口与顺序刷新通过，不等同自然到点执行；首次自然到点为本日 12:35，本轮未等待该时点。资料刷新没有生成目的地，父任务 `published` 不用于判断八个子来源是否发布，应看 `run_steps`。

每项当前都会通过原 CLI 独立导出后读取登记数组，一轮八次导出。各数组分别验证自己的清单，不声称共享同一份上游数据库快照。按照首份约 77.8 MB 原包计算，四轮每日约 2.5 GB 未压缩导出内容；这不是网络计费字节的测量。

## 检查与范围

Go test/vet、Node 41 项通过。真实 Linux CLI、隔离 TLS/MySQL 通过原短令牌及中断续办、复合键精确匹配、重复回执保留、坏批次保持前版四项行为验证。代码、授权和发布激活脚本经独立复审至 No findings。固定发布控制器通过真实 Harness、认证项目目录及原资源/定时保持检查，四服务 active，仅保留正式与上一成功版本。

八个数组以外的名称映射、首次联系映射和 HXC 身份对账输出尚未作为本次独立资源发布。HXC 原始权益/使用数据不属于本次 CRM 档案验收。策略选单仍按用户要求暂缓。本人计划 391 的发送成功不能代替策略、名单、稿件与真实业务发送验收。

CID 仅整理 [方案与 PRD](plans/2026-11-09-cid-identity-prd.md)，本轮没有新增 CID 表、接口或前端。
