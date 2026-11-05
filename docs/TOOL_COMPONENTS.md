# 工具组件接入：第一项交付

2026-11-05。依据一期PRD v1.1“标品底座＋独立插件”。本项交付通用组件接入、项目动作授权、Session调用与异步结果引用；独立数据插件和真实MySQL数据闭环属于下一项，尚未交付。

## 业务流程

```mermaid
flowchart LR
 A[设置：登记项目组件与允许动作] --> B[Session：人或专家调用]
 B --> C[服务端检查项目、动作和调用身份]
 C --> D[插件立即返回结果或任务引用]
 D --> E[原Session查看状态、结果与适用的取消]
 E --> F[插件自己维护实际任务与业务数据]
```

## 复用与范围

复用原插件配置API、Session/Harness身份、工具桥、SQLite和设置页。HTTPS组件在既有插件登记API中保存，但放在隔离的component_plugins扩展表，避免上一版程序回滚后把受管组件直接注入Harness。它们不进入原生MCP直连目录，凭据仅由宿主代转使用。

新增component_calls只保存调用、归属、幂等键、结果引用和展示摘要，不保存客户逐条任务，不要求工作台连接MySQL。状态刷新使用独立有界循环，不阻塞定时调度；异常记录有独立退避，不能饿死后续任务。

本项不含专家默认组件编辑器、持久化Session组合模板和确定性Schedule直提组件模式。专家已经可以通过现有工具桥调用；现有定时也可以沿Harness调用工具，但这些不冒充已经完成v1.1的固定参数直提模式。下一项接数据插件时再落实组合配置，不先造通用编排器。

## 用户入口

- 连接与工具→工具组件：接入/配置、所属项目、固定版本、允许动作、停用和移除。
- 项目Session的工具组件区域：选择动作并传JSON参数；展示后台状态/结果，支持刷新和插件声明的取消。
- 专家原会话：先list_components，后call_component；component_status/cancel_component只允许操作当前Session的任务。

普通即时工具无需后台协议。异步工具需同时声明statusAction和lookupAction；cancelAction可选。停用阻止新提交，已受理任务仍可核查/取消。归档/回收有在途任务时先要求收拢；删除触发器是最后兜底，不把会话提前推进到不可恢复清理状态。

## 插件协议

接入已有HTTPS MCP Streamable HTTP端点，支持JSON及SSE结果。宿主执行initialize、notifications/initialized、tools/call，处理会话ID和有界响应；反向ping可响应，其他未声明服务端请求返回不支持。当前不支持OAuth交互、订阅、采样或任意第三方插件本机安装。

登记结构示例（无真实连接或凭据）：

```json
{
  "name": "示例组件",
  "projectId": "owner-selected-project",
  "transport": "http",
  "url": "https://example.invalid/mcp",
  "enabled": false,
  "component": {
    "version": "1.0.0",
    "actions": [{"name": "start", "description": "开始处理"}],
    "statusAction": "status",
    "lookupAction": "lookup",
    "cancelAction": "cancel"
  }
}
```

宿主向执行动作补入invocation_key、project_id、session_id。插件必须接受这些字段，业务权限由安装凭据/租户授权验证，不能仅信任请求字段。

异步结果：

```json
{"job_ref":"opaque-job-reference","status":"running","summary":"已受理"}
```

终态结果：

```json
{"job_ref":"opaque-job-reference","status":"succeeded","summary":"完成","result":{"processed":12}}
```

结果放在MCP structuredContent，也兼容text内的JSON。status/lookup/cancel动作同样返回该结构。lookup根据invocation_key找回任务，不重新执行。提交结果不明显示unknown，宿主不自动重跑原动作。即时组件无异步协议时，响应不明须人工核对；不能声称有恢复保证。

同一Session的operationKey稳定且相同输入才去重；模型工具桥进一步绑定来源Run。插件还需按invocation_key实现自身幂等。跨数据库的业务恢复、发布水位以及发信授权由具体插件负责。

## 验证命令与边界

- go test ./...；go vet ./...；go test -race ./internal/workbench -run 'TestComponent|TestCommunication' -count=1。
- node --test runtime/app-mcp.test.mjs；npm run check --prefix web；npm run build --prefix web。
- TestComponentRealHarness使用PW_REAL_CONFIG和PW_COMPONENT_RUNTIME，在隔离Store中运行真实Harness，只执行样例组件，无客户数据和外发。
- PW_COMPONENT_PREVIEW启用TestComponentsPreview，仅用于本机页面验收，模拟组件后台不代替真实远端数据插件。

验收必须分别记录单元/协议、真实Harness、浏览器及生产读回。Work设置禁用本机浏览器时，不绕过限制改用其他自动化工具；不能把构建通过当页面验收通过。

## 发布边界

沿工作台现有current/previous和固定发布锁。新增表及触发器向后兼容；旧版看不到受管组件扩展表，也不会拿到其凭据。必要schema迁移前按现有发布规则做一致性恢复点。数据插件独立部署和MySQL初始化不在这次发布范围。
