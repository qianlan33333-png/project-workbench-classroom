# 今天持续跟进：验收与生产发布

日期：2026-11-04。分支 `codex/today-followups`，基线 `ea51003`。流程与 PRD 先于开发保存并提交（`9d438ea`）。

## 2026-11-04 生产结果

已按“review → 修复 → 复核 → 真实 Harness/渠道 → 同包生产”执行。应用提交 `94f692ede3b54bcf9f9faae81978fe0678af1106`，tree `19c409361d00a50cd8cc51e65ee1c1a9ae6dc0e2`；文档记录提交可以晚于应用构建。没有推送 GitHub。

审查修复两项 P1：原轮尚未收拢时提前派发；旧程序回滚不认识只读跟进范围。复核没有剩余可操作发现；详见 `TODAY-FOLLOWUPS-REVIEW.md`。修复后整仓 Go race（workbench 61.675s）、vet、Web check/build、13项 runtime 和6项发布回滚行为检查均通过，UI 沿用已验收的18项 Product Design 旅程（本次修复没有改前端）。

真实验收分开记录：

- 隔离 Store + 实际 Linux 二进制、Podman、Harness、真实模型：三轮142.25秒，实际等待到期，覆盖持久问题、回答后原会话续办、到期只读核实、依据结案、等待释放锁、正文原样，共8次真实工具调用。
- 微信、飞书分别通过隔离 channel API + 真实模型，验证同一 Today 会话、持久问题/命令回答、重复消息去重、专用续办、只读正文及通知 outbox，共4轮。该部分入站是回放，未启动第二套平台消费者。
- 用户分别发送约定测试消息，生产原消费者真实收到；两条均进入原 `personal-assistant` 会话，模型均回复“收到”，微信取得 accepted、飞书取得 sent。每个入站只对应一个 Run 和一条 answer 投递。
- 新流程生成的最终跟进通知通过真实平台发送到原绑定本人私聊：微信 accepted，飞书 sent；采用实际 outbox 投递 ID 和确定回执，未自动重发。平台发送回执不等于已读证明。

首次真实 Harness 验收因运行用户继承 `/home/ubuntu` 无进入权限而在容器清理失败，未计为通过。指定正确工作目录、清理唯一隔离容器后，按同一二进制重新完整通过；没有为通过而修改产品或省略清理。

正式包为 `cloud-20261104-29-today-followups-94f692e`，上一成功版为 `cloud-20261103-28-calendar-sync`。候选归档 SHA256 `666d13924b370516099e86e9ef90a5ae1e8829641e94b315eaf4d98927cfa9f4`；实际运行二进制 SHA256 `3a5965b27a6f7448772567596ce44b620a40a59e0bbf8fd4dfd74270762d83f5`。继续复用已固定的运行镜像，本次 runtime JS 按目录只读挂载交付。

在既有发布锁内准备新候选，复用 `/opt/project-workbench/deploy-today.sh` 和固定控制器，验收 hook 成功后提交 previous。固定控制器已原子更新回滚保护；新增表迁移前只保留一份 SQLite 一致性恢复点，程序回滚不恢复旧业务数据库。

安装后再次独立读取实际进程 exe/cwd、候选 manifest、公开 HTML/JS/CSS 摘要和渠道重启后心跳。应用、渠道服务均 active，两个渠道 connected；原有1942个文件摘要、21个 Session/Harness 映射和业务配置保持一致。只保留 current/previous，`.deploying`、incoming、一次性预览数据/服务及上传辅助文件均已清理，发布锁已释放。

证据保存在当前包 `followup-acceptance/`、`followup-production-acceptance.json`、`followup-final-readback.json`，以及本机不进 Git 的 `artifacts/today-followups/production/`。日志和真实回执不写入源码。

## 实现结果

- 在现有个人空间内保存事项；固定 key 幂等创建、版本校验更新、稳定 Session/Harness 身份。
- 持久决策复用 personal_decisions；回答、暂缓、过期和上下文重置均更新事项。回答已提交但未派发时，由现有后台循环恢复，不依赖打开“今天”。
- 一次性复查复用 schedules，事务内关联 Run、去重并计数，每事项最多自动复查3次；自动轮仅提供工作台只读资料、日程缓存和本事项转换工具。
- 完成依据只有同一轮成功收拢后才使事项结案；失败和中断留为待核实。取消阻止未来/排队/迟到操作，运行锁直到进程清理后释放；未发送的自动通知撤下，已发送/未知不被重发或宣称撤回。
- 新增“持续跟进”折叠区域，问题只有一个操作入口；保留主对话、个人理解、每日建议、已有安排与草稿。来源渠道通知固定到原本人私聊，换绑后不跟随新账号。

## 验证

| 检查 | 结果 |
|---|---|
| `go test -race ./... -count=1` | 整仓通过；真实环境用例仍按既有环境开关跳过 |
| `go vet ./...` | 通过 |
| `npm --prefix web run check`、`npm --prefix web run build` | 通过；保留既有构建 chunk 大小提醒 |
| `npm --prefix runtime run check` | 13项通过，包括真实 stdio MCP 握手和三种工具目录 |
| `node scripts/verify-today-followups.mjs` | 18项通过，真实本地 Store/API + 确定性 Executor |
| `git diff --check` | 通过 |

后端行为覆盖：幂等创建/更新/继续、版本冲突、重复回答至结案、暂缓与过期、到期重复触发/重启、复查上限、自动轮工具限制、本人渠道身份、取消与到期竞态、执行清理锁、换绑通知、未发送通知撤销、脱离页面的回答恢复和上下文重置。

页面旅程：展开列表 → 单一决策入口 → 手机回答 → 同会话续办并以依据结案 → 注入旧版本确认保留草稿 → 正常继续核实 → 取消/保留选择 → 撤销定时 → 查看已结束历史。1280×900、1280×600、390×844、320×568 验证，无横向溢出，短屏输入框完整可见。一次故意提交旧版本产生预期 HTTP 409，已展示在条目内；没有未预期的客户端异常或项目写请求。

修复过程：已消费回答在完成时被误标过期；同一错误双重 live-region 提示；短屏列表挤出输入框；决策详情优先级。修复后重新验收。Product Design 图像对照及优先级记录见根目录 `design-qa.md`。

本地证据在 `artifacts/today-followups/`：`ui-report.json`、前后截图、比较图、`validation.json`、构建/Go/Node日志。该目录不进 Git，只含虚构资料与临时标识。

## 复跑隔离页面

先构建前端。将基线静态文件放在 `artifacts/today-followups/baseline-dist/` 后启动：

```sh
PW_FOLLOWUP_PREVIEW="$PWD/artifacts/today-followups" go test ./internal/workbench -run '^TestFollowupPreview$' -count=1 -timeout=2h
```

另一个终端运行 `node scripts/verify-today-followups.mjs`。脚本读取 `preview.json` 中的回环地址；每轮测试需要新夹具。写入该目录的 `stop` 文件可正常结束夹具，下一轮启动前移走该停止文件。预览使用真正的数据库和 HTTP API，但执行器是确定性测试替身，不调用模型或渠道。

## 本地阶段的验证边界（上线前记录）

最初本地开发阶段没有部署、推送 GitHub、调用真实 Provider、发送真实消息或修改生产资料；后续已按上方生产结果完成真实验收及上线。模拟渠道证明应用内身份与发送队列行为，不等于平台回调、投递或用户可见性验收。

自动复查第一版核对工作台已有项目资料和日程缓存；没有泛化到 CRM/网站等第三方实时读取。缺少相应只读入口时应保留待核实，不能声称已验证外部状态。

发布时后端、Web 和 runtime 必须同版本交付；实际 Harness/渠道和安装后读回已按上方结果完成。旧版本不了解跟进专用权限，回滚前必须通过现有发布流程停用复查并收拢跟进 Run，不应让旧执行器消费这些自动任务。本次发布前 review 已在既有固定发布入口加入自动兼容保护，主动回滚和失败恢复均调用同一保护；不另建发布通道。恢复支持跟进的程序后仍需人工核实并继续，不重放旧定时。
