# 五个运营工具

本轮候选复用同一 MCP 服务、原 Harness 生成、Temporal、MySQL 和 CRM CLI。正式安装与真实业务验收状态见 [实现记录](../../docs/IMPLEMENTATION.md)，不能以本说明判断已上线。

```mermaid
flowchart LR
 A[data_refresh 采集清洗] --> B[strategy_select 原规则选单]
 B --> C[batch_generate 原方法生成]
 C --> D[crm_push 准备整批稿件和素材]
 D --> E[本人一次整批确认]
 E --> F[原 CRM CLI 提交与审核]
 F --> G[crm_receipts 原计划逐人回执]
```

| 工具 | 本轮调用合同 |
|---|---|
| `data_refresh` | `{operationKey,sources,destination}`。来源按原始资料、联系历史、策略、方法排列，均为已登记的 `operations-original` 资源；同轮复用一份真实采集与清洗结果。返回 `runId` 和四份 `inputRefs`。 |
| `strategy_select` | `{operationKey,data,strategies,history,destination,asOf,strategyKeys,limit}`。三个输入固定版本；`asOf` 使用本轮清洗结果的 `archive.generated_at`。`limit:0` 保留全部合格成员，排除原因从 `run_items` 读取。 |
| `batch_generate` | 沿用现有 `{operationKey,sources,destination,methodPointer,methodRefs}`。`methodPointer` 为 `/actions/0/generation/skill_key`，方法引用来自本轮采集；大批使用原 `selection` 分页，每段至多 2,000 人。 |
| `crm_push` | `{operationKey,review:{selection,drafts}}`。固定名单和全部生成段，返回已有整批待确认任务；准备期间可创建并读回 CRM 素材，不创建发送计划。 |
| `crm_receipts` | `{runId}` 查询原整批任务，或 `{planId}` 查询已绑定原计划。只查询、不补发；分别返回平台受理、待员工确认、实际发送、失败或未知。 |

长任务都使用已有 `run_get`、`run_items`、`run_steps` 和 `run_resume`。续办保留原任务与成功结果；来源、正文或身份变更不得混入已经确认的批次。没有合格成员就结束备稿，不生成占位稿。

固定定时入口为现有本人接口 `POST /operations/original-schedule` 的 `cycle` 配置，保存四份来源、三个目标资源和六份原方法引用；不使用通用 `program_run` 方案。先在同项目登记资源、原 CRM transport 和员工，再安装定时。原有新工作台分开的资料/策略任务由该入口暂停；原服务器的任务与配置保持只读。

常态服务与新人承接可配置在同一条日程。`cycle.additionalBatch` 指定第二条原策略及独立的名单、稿件资源。两包复用同轮清洗、策略、历史和方法快照，并按相同时间逐人判断；每包独立备稿与确认。迁入策略的旧包归入历史。原发送范围继续由 `WORKBENCH_OPERATIONS_BATCH_STRATEGY` 控制，第二条策略按同样方式由 `WORKBENCH_OPERATIONS_ADDITIONAL_BATCH_STRATEGY` 单独开放。

确认入口继续使用 `/operations/source-confirm`，确认版本固定名单、正文、素材及员工。连接器执行 CRM 原审核合同；企微必要的员工确认仍由平台处理。创建或审核前集中检查当前业务事实；网络结果未知只查原请求、原计划。

规则验收见 [42 条策略矩阵](OPERATIONS-ACCEPTANCE.md)。旧自动化程序按用户要求保持现状；当前读取其联系和执行历史，不声称跨系统原子互斥。真实客户发送必须在正式任务库留下计划和逐人回执，不能在隔离库发送后丢弃业务记录。

## 兼容的 CRM CLI 基本入口

```mermaid
flowchart LR
  A[登记 CRM 资料来源] --> B[CLI 导出原始事实]
  B --> C{选定集合通过清单和字段校验}
  C -->|通过| D[资源发布]
  E[登记名单与稿件] --> F{本人确认}
  F -->|确认| G[CLI 提交并审核 CRM 计划]
  G --> H[按原计划查询实际状态]
```

以下是同名工具保留的 CRM CLI 兼容调用方式，沿用原有资源同步、批次确认和回执查询。CLI 命令来自当前 CRM 目录，工具注册沿用 [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)。全量原始采集与策略选单使用上文五工具合同。

| 入口 | 输入与结果 |
|---|---|
| `data_refresh` | `{operationKey,destination}`。对已登记 CRM 来源执行 `customers archive-facts export`，按现有资源字段投影、清洗和发布，返回 `runId`。用 `run_get` 查状态、条数和发布结果。 |
| `crm_push` | 复用 `message_batch_prepare` 的 `operationKey`、`title` 和 `plan`。名单与正文取固定引用，本人确认后调用 CRM CLI 计划与审核操作。用 `message_batch_read` 读原批次与计划编号。 |
| `crm_receipts` | `{planId}` 或 `{runId,deliveryId}`。查询该工作台已经关联的原计划，返回当前逐人记录并触发原回执读回。用 `message_batch_read` 查看更新后的状态。 |

采集连接由本人登记为 `kind: crm-archive-cli`。`secretEnv` 使用 `WORKBENCH_SOURCE_` 前缀，引用宿主环境中的 JSON 配置 `{cli,origin,clientId,clientSecretEnv,requestFile}`。程序与请求文件使用绝对路径。`clientSecretEnv` 指向 `WORKBENCH_SOURCE_` 前缀的机器密钥环境变量。每次采集经 CRM 原机器授权接口申请短期只读令牌，再调用 CLI 导出。请求文件为原导出合同的 `{"hxc_account_ids":[...]}`，使用准确、已授权的账号标识。CRM 机器权限和来源 IP 由原服务校验。

资源沿用 `jsonSource.arrayField/keyPointer/fields`、`trimFields` 与 `recordSchema`。每个资源登记一个导出集合，例如 `customers`、`orders`、`messages` 或 `surveys`。同步验证该集合的原清单条数和 SHA-256，再按现有发布规则切换。允许使用既有 `program_schedule` 定时刷新登记资源。

### 全量采集补齐

```mermaid
flowchart LR
 A[专用只读机器授权] --> B[原 CLI 实时导出]
 B --> C[逐集合核对原清单]
 C --> D{原记录有单字段主键}
 D -->|有| E[沿用 keyPointer]
 D -->|无| F[复合键或完整内容及出现序号]
 E --> G[字段清洗和完整发布]
 F --> G
 G --> H[原 runId 查回数量和内容]
```

本次缺口是部分原始记录没有单字段主键。复用 [RFC 6901 JSON Pointer](https://www.rfc-editor.org/rfc/rfc6901.html) 定位字段，用 Go 标准 JSON 编码有序键值数组并取 SHA-256，避免字符串分隔符歧义和行序变化。`keyPointers` 的每一部分必须是非空字符串或 JSON 数字，缺失即拒绝整批发布。没有原始唯一键的数组可登记 `rowKey:"content-occurrence"`，使用完整原始行内容摘要和相同内容的出现序号保留所有行；行序改变不改变同一多重集的引用集合，内容变化会产生新引用。三种键方式互斥，不新增键表达式语言、身份匹配或客户主键。

范围为 CRM 原导出的八个记录数组：`customers` 使用 `/id`，`users` 使用 `/customer_id`，`orders` 使用 `/order_id`，`surveys` 使用 `/submission_id`，`answers` 使用 `/answer_id`；`identities` 使用 `[/kind,/scope,/normalized_value]`，`messages` 使用 `[/message_id,/customer_id,/participant_role]`。`activation_receipts` 原投影未提供回执主键，使用 `content-occurrence` 保留同一客户、渠道、时间下不同回调产生的重复投影。这些只是快照记录引用，不是 CID 或原回执主键。已有单字段资源合同不变。

验收依次完成机器授权、真实 CLI 导出、正式工具逐集合刷新、原任务重复调用及独立内容读回。对复合键验证字段分隔、类型、大小写、行序和缺键拒绝，隔离 MySQL 验证非法重复键不替换已发布资料，以及无主键回执的合法重复投影全部保留。原包还含名称映射、首次联系映射和 HXC 身份对账输出，这八个数组不代表包内所有字段均已作为资源发布。HXC 原始权益与使用记录不属于 CRM 档案导出，此验收不代替 HXC 来源或策略与业务发送验收。

推送沿用本人登记的资格资源及字段绑定，名单可由本人提供。资格检查绑定 CRM `customerId`、`staffId` 和同一业务动作的稳定 `businessSlot`。连接器沿用明确允许的客户范围、原请求标识、原计划审核和外部结果未知时只查回执的处理。平台受理、待员工确认、实际发送和失败分别记录。

发送通道配置 `kind: crm-cli`、`cli` 绝对路径和 `allowedCustomerIds`，以 `cliEnv` 绑定现有 CLI 本人会话的配置目录及凭据文件。工作台连接登记同一通道 ID 和 `kind`，使用已有 MCP 通道地址与项目授权。

## 本次来源切换的回滚

回滚前先用已有日程暂停入口暂停本地运营定时，等待正在清洗、生成或提交的原任务收拢；结果未知时继续保留原计划查询，不换编号提交。然后走固定 `rollback-today.sh` 退程序与单元。若退回尚无本地 `cycle` 的上一版，通过原 `operations/original-schedule` 接口恢复原 `hxc:onboarding` 的 09:00 配置，保持新本地定时暂停。不得回退 MySQL 或删除确认、发送回执；原服务器任务不变。来源私有配置与原素材模板是兼容追加项，不作为客户发送授权。
