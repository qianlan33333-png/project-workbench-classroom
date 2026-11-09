# 客户身份与资料：两个 MCP 工具

CID 使用独立 MySQL 库，仅包含两张表 `wb_customers`、`wb_customer_identifiers`，以及同一 Worker `/mcp` 的 `customer_upsert`、`customer_get`。不依赖 Temporal 任务、聊天历史或模型判断，不改已有采集、消息路由和工作台账号。

## 来源配置

用 `native/worker/internal/core/customers.sql` 初始化空库，将仅有该库 SELECT、INSERT、UPDATE 权限的账号配置为 `WORKBENCH_CUSTOMER_MYSQL_DSN`。启动时核对表清单。复用 Worker 私有环境配置。`WORKBENCH_CUSTOMER_CLIENTS` 是下列 JSON 数组的字符串；凭据单独放入对应 `tokenEnv` 环境变量，每个至少 32 字符，不复用 Host 凭据。未配置时默认关闭客户入口；正常 Host 的工具列表不增加这两个来源工具。

```json
[
  {
    "tokenEnv": "WORKBENCH_CUSTOMER_CRM_TOKEN",
    "access": {
      "source": "crm",
      "scopes": {
        "union_id": "shared-wechat-platform",
        "crm_one_id": "crm-company",
        "wecom_external_userid": "wecom-company"
      }
    }
  },
  {
    "tokenEnv": "WORKBENCH_CUSTOMER_HXC_TOKEN",
    "access": {
      "source": "hxc",
      "scopes": {
        "union_id": "shared-wechat-platform",
        "hxc_user_id": "hxc-application"
      }
    }
  }
]
```

示例范围须替换为真实所属平台/企业/应用的固定标识。只有属于同一微信开放平台的 UnionID 才配置同一范围。配置的 `source` 不可重复；每个来源固定一份凭据和身份范围。配置在启动时加载，变更后沿已有服务流程重启，不新增配置后台。

来源凭据仅能访问 `/mcp`，只列出这两个工具，无法访问 Host 管理工具和 REST 接口。调用使用 `Authorization: Bearer <来源凭据>`，不需要 Session、project、source 或 scope 请求头。这些头不能改变来源身份。复用现有 loopback 监听；远程接入需要既有受控连接，本文不变更公网路由。

`scopes` 限定可绑定和读取的 ID 类型/范围。默认只读取自身来源的资料；如已授权 CRM 读取黄小璨资料，可在 CRM 的 `access` 加 `"readSources":["hxc"]`，身份映射仍按 `scopes` 过滤。`"readOnly":true` 禁止该凭据写入。CID 本身不授予读取权限。

## 写入和读取

完成标准 MCP 初始化后，通过 `tools/list` 发现 Schema，通过 `tools/call` 调用。CRM 写入示例：

```json
{
  "name": "customer_upsert",
  "arguments": {
    "identifiers": [
      {"kind":"union_id","value":"U1"},
      {"kind":"crm_one_id","value":"C1"},
      {"kind":"wecom_external_userid","value":"W1"}
    ],
    "data": {"nickname":"示例", "stage":"已报名"}
  }
}
```

成功 `structuredContent` 为 `{"cid":"cid_<UUID>","created":true}`，同时返回等价文本内容。重复提交返回同一 CID，`created:false`。黄小璨提交相同范围的 `union_id=U1` 和自己的 `hxc_user_id=H1`，获得同一 CID。调用方保存返回值。

读取参数：`{"name":"customer_get","arguments":{"cid":"实际返回的 CID"}}`。结果包含 `cid`、可见 `identifiers` 和按来源分组的 `data`，例如 `{"crm":{"nickname":"示例","stage":"已报名"}}`。

至少提交一个非 UnionID 的来源 ID；每次每种类型最多一个。缺 UnionID 时可先按来源 ID 建立客户，后续补齐；没有共同标识时不猜测跨端身份。如果两个独立 CID 已建成，后续出现交叉绑定则返回冲突，不自动合并。

本次提交字段覆盖本来源的同名顶层字段；未提交字段保留，嵌套对象和数组整体替换，`null` 原样保存。其他来源资料不变。请求资料及合并后的完整资料各限 64 KiB，HTTP 请求上限 128 KiB；订单、聊天等大批明细留在原数据表。任意 SQL、来源、范围和 CID 都不能作为写入参数。

身份值区分大小写，前后空白和控制字符拒绝；不按姓名或手机号推断。身份、资料和新 CID 在同一事务提交，超时后用原 ID 重试可查回原 CID。冲突返回 `isError:true` 以及 `{"error":{"code":"IDENTITY_CONFLICT"}}`，不包含冲突客户的私密资料。其他错误码为 `INVALID_INPUT`、`ACCESS_DENIED`、`NOT_FOUND`、`REQUEST_INTERRUPTED`、`INTERNAL_ERROR`；失败后不要换 ID 来规避冲突。

## 验证

在 `native/worker` 执行 `go test ./...` 和 `go vet ./...`。真实 MySQL 测试仅接受名称以 `workbench_native_acceptance` 开头的隔离库 DSN，测试数据全部合成：

```sh
go test ./internal/core -run '^TestMySQLCustomer' -v
go test ./internal/api -run '^TestMySQLCustomerMCPRoundTrip$' -v
go test ./internal/core -run '^TestMySQLCustomerConcurrent' -count=5 -v
```

运行前通过受控环境注入 `WORKBENCH_TEST_DSN`；未提供时真实数据库测试明确跳过。覆盖双向跨端接入、补齐 UnionID、重复写入、32 并发合并字段、12 并发身份冲突、零部分写入、读取范围、大小写、资料限额回滚和官方 MCP Schema/实际读写。SDK v1.8 便捷工具封装会在应用默认值时改写大整数，因此这两个工具使用同一官方 SDK 原始处理接口，加现有 JSON Schema 库校验，保持原 JSON 精度；包含 `9007199254740993` 往返验收。

正式接入沿工作台发布入口，CID 专库单独初始化。来源读取使用既有只读通道。
