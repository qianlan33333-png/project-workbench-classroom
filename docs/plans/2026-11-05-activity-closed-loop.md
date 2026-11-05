# Activity Closed Loop Implementation Plan

**Goal:** 一个运营项目、一个活动Session内完成新数仓取数、≤2000生成、抽检通知、本人批准和原发送回执。

**Architecture:** 工作台增加通用审核/通知和只读历史引用，业务状态与原发送适配在独立toolplugin。复用既有任务、投递和通信，不改旧Session/Harness归属，不改原中心。

**Tech Stack:** Go、SQLite、MySQL、React、既有MCP/微信渠道、原中心HTTP协议。

---

1. 锁定通用审核契约与持久化表（internal/workbench/component_reviews.go、model/store）；测试原子授权、通知去重、版本绑定与已停止来源。
2. 扩展既有component协议/组件工具/HTTP，展示冻结预览；worker在模型前处理真实本人“发”，复用QueueReply与Delivery回执。覆盖伪造/重复/引用/多批/取消竞态。
3. 独立插件实现warehouse batch（internal/toolplugin/activity.go、jobs/batch/warehouse），固定发布代、逐人读取、恢复和样本；真实MySQL 2000项验证。
4. 原中心适配器（internal/toolplugin/source_activity.go）prepare/export/import/review/handoff/status，保留原绑定与校验，严格区分手机待发与实发；原接口TLS fixture与未知结果测试。
5. 工作台组件结果预览与历史只读入口（web/src/tool-components.tsx及小型复用组件）；Product Design现有视觉参照、隔离Playwright关键交互与移动端。
6. 归并脚本：按稳定ID保留历史/配置，建立统一项目三类Session与一个组件连接，原timer参数与启用状态事务转移；模拟重跑/改名/自定义/中断验证。
7. 隔离服务安装与原中心只读探针，两条真实模型，真实Harness实际调用记录，实际本人微信抽检/回复验收；所需平台选择仍待用户回复。
8. 最终独立/re、修复复审、必要schema一致性边界、现有发布锁/current/previous部署及生产独立读回；更新IMPLEMENTATION、用户指南与准确验收范围。
