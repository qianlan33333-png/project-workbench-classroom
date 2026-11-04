# Today Followups Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 在今天提供可持续跟进、等待持久决策、到期只读复查的完整闭环。

**Architecture:** 为现有个人空间增加事项与 Run 关联。复用 decisions、单次 schedules、同一 Session/Harness、执行队列和事件/发送状态，不增加服务。以事务和版本校验处理重复提交、取消与到期竞态。

**Tech Stack:** Go、SQLite、现有 DeepSeek Harness、React/TypeScript、现有 CSS/Lucide。

---

## Task 1：文档和视觉基线

Files: `docs/TODAY-FOLLOWUPS-PRD.md`、本计划、`docs/TODAY-FOLLOWUPS-DESIGN.md`。
先保存流程、参考和 PRD；Product Design 用现有页面捕获视觉基线，记录新增组件状态及保持部分。独立 worktree，不改共享目录。

## Task 2：持久事项和决策

Create: `internal/workbench/followups.go`、`followups_test.go`。
Modify: `today_model.go`、`personal.go`、`today_http.go`。
先测创建幂等、CAS、待决定、重复回答、终态取消和跨范围拒绝。实现事项/执行关联及决策复用，回答通过既有 inbox 入队。运行 `go test ./internal/workbench -run 'TestFollowup|TestDecision' -count=1`。

## Task 3：一次性复查与执行生命周期

Modify: `schedules.go`、`worker.go`、`harness.go`、`runtime/app-mcp.mjs`。
用现有 Schedule 保存未来时间；既有入队事务关联事项与 Run，复查次数上限3。实现自动轮只读工具、结束/失败/恢复收拢、取消停止和迟到工具结果拒绝。行为测试使用假 Executor/本地 SQLite，覆盖重启、重复到期、取消和越权。

## Task 4：今天界面

Create: `web/src/followups.tsx`。
Modify: `web/src/today.tsx`、现有样式。
Product Design 按当前视觉目标增量实现折叠列表与详情、DecisionCard 单一入口、继续/取消、加载/空/冲突反馈。保留对话草稿及已有安排。通过真实本地 API 或隔离夹具验证桌面和手机，不使用生产资料。

## Task 5：验证与交付

运行 `go test ./...`、`go vet ./...`、`npm --prefix web run check`、`npm --prefix web run build`、`npm --prefix runtime run check`、`git diff --check`。用 @Browser 验证核心交互并保存 Product Design 对照和 `design-qa.md`。更新 `docs/IMPLEMENTATION.md`，明确真实模型/渠道和生产未验证项。检查最终差异、修复发现，再交付本地分支与文档/验证证据。
