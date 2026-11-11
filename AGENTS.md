# 项目专家工作台

独立个人应用：官方 DeepSeek Harness + 标准 MCP + Temporal + MySQL。新实现仅在 `native/`；会话和项目文件使用 Harness 原生存储，业务数据与执行状态不使用 SQLite。

## 后续开发默认规则

- 设计、开发、精简或审查本工作台前，读取并使用 `/Users/qianlan/.codex/skills/workbench-harness-development/SKILL.md`。它是自动化应答与运营共同的极简开发入口，按需读取其参考，不在各 worktree 复制规则正文。
- 自动化运营的本次定稿见 `docs/plans/2026-11-11-minimal-automation-operations-design.md`；设计目标与实际上线状态分别核验。用户当前要求优先，具体发布继续使用下述既有发布 skill。

## 不变式
- 一个应用对话稳定对应一个 Harness 会话；对话 project_id 不可改。
- 渠道只传新消息和明确区分的引用线索，不重拼历史。
- 外部消息先持久化去重；归属不明时不读取或写入项目正文。
- 一个项目一个写入任务；停止必须收拢整个运行进程，再释放锁。
- 专家是方法包，项目笔记不能自动写进全局包。
- 外部发送状态不明不得盲目重试；凭证、真实资料和日志不进 Git。
- 不修改其他服务器项目；正式切换必须通过真实 Harness 和渠道验收。

## 验证
- Go: 在 `native/worker` 执行 `go test ./...`、`go vet ./...`；真实 MySQL/Temporal 测试只使用隔离库。
- Host/连接器：`node --test native/harness/*.test.js native/connectors/*.test.mjs`。
- 前端沿用 Harness 原生界面及 Cordis 扩展；涉及界面调整先用 Product Design。用隔离 Playwright 验证桌面/手机及真实会话，不另建一套聊天前端。
- 对入口路由、重复投递、文件修订、崩溃恢复提供行为测试。
- 每个实现阶段更新 docs/IMPLEMENTATION.md，明确真实验证与未验证项。
- 数据库、知识文件及衍生产物均经资源允许列表；宿主绑定项目身份。通用工具协议和 Skill 规则见 `native/docs/EXTENSIONS.md`。
- 定时/并发/恢复使用官方 Temporal SDK；不引入专项活动生成器、自制编排语言或权限平台。

## 发布、回滚与空间治理

- 本项目发布、回滚或生产磁盘清理前，先读 `skills/project-workbench-release/SKILL.md`，由它统一约束现有发布入口。该 skill 只服务本工作台，不适用 CRM 或同机公司官网。
- 2026-11-02 用户选择的稳态为一个正式版本加一个上次成功的回滚版本；其他旧发布、过期快照、无用构建资源及退休旧环境必须清理。复用既有发布验收，不开发独立清理验证模块，不记录清理收据。
- 该策略替代旧文档中的七天保留规则。已接入现有发布、安装、回滚及清理脚本；它们共用 `scripts/release-common.py`、一把锁、current/previous 和发版中临时恢复信息。生产固定入口在 `/opt/project-workbench/`，不要再临时复制旧发布脚本绕行。
