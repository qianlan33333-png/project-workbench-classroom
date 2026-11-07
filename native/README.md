# 原生工作台

此目录承载新实现，开发期间与旧入口隔离，尚未替换生产。架构与验收以 `docs/plans/2026-11-07-native-workbench-prd.md` 为准。

- `harness/`：官方 Harness 的 Cordis 扩展与配置。
- `worker/`：MySQL 资源工具、官方 MCP 服务及 Temporal Worker。
- `deploy/`：固定版本的部署组成，不承载客户资料或凭证。

会话归 Harness，执行归 Temporal，资源和业务事实归 MySQL。禁止引入旧运行时兼容层或专项活动生成器。
