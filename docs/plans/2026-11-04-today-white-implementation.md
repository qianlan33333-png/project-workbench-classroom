# Today White Personalization Implementation Plan

> **For Claude:** 按本计划逐项实现并核对；复用Code、Product Design与现有执行/验证流程，不委派代理，不追加确认门禁。

**Goal:** 落地用户选择的第3版纯白Today、宽输入与本地附件、统一个性化和我的记忆二级页。

**Architecture:** 保留React页面、SQLite、原Session/Harness和消息幂等。个人网页附件复用uploads暂存及既有附件归集，增加绑定个人Session的owner入口；文件内容通过固定受限解析器提供给read_message_attachment，不获得任意CLI或项目写能力。个性化复用原规则与理解API，保留版本、草稿、遗忘语义。

**Tech Stack:** Go、React/TypeScript、现有Lucide/system字体；Python标准库解析文本和OpenXML，Linux固定pdftotext/tesseract，macOS固定PDFKit/Vision用于开发验收。

---

## Task 1: 个人附件接入与归属边界

Files: `internal/workbench/uploads.go`, `messages.go`, `today_http.go`; new `personal_attachments_test.go`。

- 先写行为回归：owner上传只绑定有效个人Session；网页消息仅可附该Session的暂存附件；渠道附件不能冒充网页附件；业务项目不能使用网页个人附件；重复投递仍一个Run；同一附件归集重入内容一致时可复用、被改写则拒绝。
- Run: `go test ./internal/workbench -run 'TestPersonalAttachment' -count=1`，先确认缺口，再实现并通过。
- 新owner POST `/personal/attachments`接收sessionId+file，限制8项消息附件/128MiB单文件；只返回引用和名称/大小，不改项目正文、不执行文件。
- 删除原“一律拒绝网页个人附件”分支，替换为精确来源、Session及引用校验；旧项目文件引用仍禁止。

## Task 2: 实际内容读取

Files: new `internal/workbench/attachment_reader.go`, `runtime/attachment-reader.py`, `runtime/attachment-reader-macos.swift`; modify `app_bridge.go`, `harness.go`, `deploy/Containerfile`。

- 只按当前Run内附件序号解析。通过os.Root读取并复制精确快照，不把任意路径或用户命令交给外部程序。
- 固定解析器返回文本、类型、必要OCR/不完整提取说明；输出最多2MiB、Office展开/XML/单元格/PDF页数均限量，超限明确错误。
- Podman读取容器无网络、无凭证/项目挂载，只有该快照与只读解析器；限CPU/内存/进程/时间。停止与最终Harness Cleanup一起清理读取容器，释放锁前确保已退出。
- 文本/Markdown/CSV/JSON及DOCX/XLSX/PPTX读取；PDF提取文字，图片OCR。无法提取/扫描PDF/格式异常不得假称已读。
- 通过原MCP工具`read_message_attachment`提供，自动复查工具白名单不扩张。
- 用虚构TXT/DOCX/XLSX/PDF/PNG夹具验证原内容、ZIP/XML限额、越界、取消清理；实际Harness另做附件内容到模型验收。

## Task 3: 个性化与我的记忆

Files: new `web/src/personalization.tsx` (统一dialog及焦点管理); modify `personal-rules.tsx`, `personal-understanding.tsx`。

- 一级页只有怎么合作、怎么陪我、我的记忆入口、保存。字段映射原agent/soul，正文不自动缩写。
- 两级共用设置层；记忆页返回个性化，关闭回到发起按钮；Tab/Escape焦点限制，编辑未保存保护。
- 原纠正/忘记/依据API保留，文案简化；正常阅读隐藏版本/技术说明，来源按需打开；旧上下文重置不改。
- 用真实隔离API覆盖保存、关闭未保存、409保留草稿、二级返回、纠正/忘记/空列表与请求竞态。

## Task 4: Today与输入区

Files: new `web/src/personal-attachments.tsx`; modify `today.tsx`, `capability-composer.tsx`, `main.tsx`, `followups.tsx`, `style.css`。

- Today不渲染大标题/日期/手动刷新/键盘提示；原全局导航在Today压缩，保留可访问的手机导航/通知入口。
- 宽输入框1200px，正文840px；底部附件、个性化、已有安排，黑色发送按钮；上传/选择/拖入/粘贴、移除/失败重试，有附件可单独发送。
- 本地草稿保存已上传引用，失败发送沿用原消息提交与重试标识；未完成/失败上传时阻止误发送，返回设置后保留输入状态。
- 已有安排默认关；跟进移至输入区附近的紧凑入口，空状态与历史仍可访问。
- 纯白/浅灰/黑色token，去绿色用户气泡与常驻完成标签。仅改变该旅程和基础导航颜色，不重排其他业务页面。

## Task 5: Product Design视觉与交互验收

Files: extend opt-in preview fixture; new `scripts/verify-today-white.mjs`; `design-qa.md`。

- 使用原始第3张图，按1487×1058 CSS视口捕获相同“我的记忆”状态；组合到同一图片后看字体、间距、颜色、文案与组件。
- 追加主界面/一级设置/附件状态，以及1440/1280/390/320/短屏；验证输入/发送、弹层滚动、键盘焦点、中文IME、草稿、错误重试。
- 仅虚构内容，Playwright复用用户已授权的本地浏览器方式。修复P0/P1/P2后复验，报告必须passed。

## Task 6: review、验证与交付

- 源码review实际消费者、修复后复核，明确不是独立代理review。
- `go test -race ./... -count=1`、`go vet ./...`、Web check/build、runtime checks、固定附件解析器真实夹具检查、`git diff --check`。
- 完整记录真实模型附件读取与尚未验证格式/平台；不把本地替身计为真实Harness。更新IMPLEMENTATION和专项验收记录。
- 先完成可审查实现及本地可运行预览；本次不自行推GitHub。生产切换沿用现有工作台入口及已授权范围，不能用新脚本另建发布通道。
