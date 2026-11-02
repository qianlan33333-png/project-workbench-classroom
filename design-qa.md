# Product Design QA — 正式菜单与 SSE 验收（2026-11-02）

- 正式入口：`https://dsh.qianlan333.cloud/`；前端 `66c4a10`。源为本地已验收 `artifacts/session-menu/after-desktop.png`；实现为 `artifacts/session-menu-live/desktop-menu.png`、`live-harness-sse-menu-open.png`、`live-sse-pin-reopened.png`、`live-sse-title-wrapping.png`、`live-bottom-menu.png`、`live-mobile-390.png`、`live-mobile-320.png`。
- 源/正式桌面均为 1280×720 CSS/像素、1× 密度，手机 390×844 / 320×568，底部窗口 1280×480。源全图、正式桌面、真实 SSE 后菜单及手机图已放在同一次图像输入中检查。正式使用专用测试项目、真实聊天、置顶状态和长列表，内容/换行差异来自测试数据；验收范围是菜单与锚点交互，不声称不同内容的整页像素一致。
- 字体、颜色、间距、图标与文案均沿用已验收 CSS/组件，公开资源字节摘要一致。菜单 12px、115px 最小宽、8px 圆角与原阴影；四项可达，无裁切或视口溢出；正常“取消置顶”随真实状态变化。无新图片或占位资产。
- 10 组线上验收全部通过；使用实际 API、原生 EventSource 与真实 Harness。SSE 自动滚动保留可操作菜单，置顶/换行使旧菜单关闭且正确重开，无位移更新保持打开。手机触摸与草稿保持，控制台和页面错误为空。
- 首次测试标题超过既有 80 字限制，已回退后修正夹具重发同一前端字节；首次失败记录保留。最终无待修复 P0/P1/P2。两轮测试项目均清理，最终服务/配置/原有会话映射读回一致；真实平台发送和物理手机不在本轮证明范围。
- 证据：`artifacts/session-menu-live/report.json`、`deployment.json`；回滚见 `docs/OPERATIONS.md`。

final result: passed

---

# Product Design QA — 菜单 SSE 动态更新 P2 复验（2026-11-02）

## Target and state

- 目标：保留既有菜单视觉，修复聊天自动滚动误关与会话重排导致弹层脱离触发器两项 P2。本节补充此前 13 组静态场景验收未覆盖的动态更新。
- 源图：`artifacts/session-menu-p2/previous-after-desktop.png`、`previous-after-sidebar.png`、`previous-after-390.png`，均为本轮开始时保存的 758734f 已验收菜单截图；同条件实测负向对照在 `artifacts/session-menu-p2/baseline/`。
- 实现：`artifacts/session-menu/after-desktop.png`、`after-sidebar.png`、`after-390.png`，以及 `sse-chat-before.png`、`sse-chat-after.png`、`sse-pin-before.png`、`sse-pin-reopened.png`、`sse-wrapped-title.png`、`sse-stable-metadata.png`、`sse-stable-append.png`。
- 源与实现的同状态全图均为 1280×720 px，聚焦图 240×655 px；手机 390×844 px，deviceScaleFactor=1，无密度归一化。普通状态使用相同两条对话、空聊天、第一行菜单打开；SSE 状态中聊天滚动与会话顺序变化是预期差异。
- 源与实现的聚焦图已在同一次图像工具输入中一起检查；SSE 自动滚动后保持打开、置顶后重新打开的全图亦已打开检查。普通状态源/实现的文件摘要与尺寸比较见 `validation.json`。

## Findings and iteration

- P2：document 捕获所有滚动会让聊天自动滚动关闭侧栏菜单。负向对照使用真实 HTTP SSE `harness.tool/call`，确认实际工具调用计数更新和聊天原生 scrollend 后，菜单数为 0；修复版菜单保留，且能打开原归档确认框。没有替换 EventSource 或调用组件内部处理函数。
- P2：固定菜单只在打开时定位，SSE 置顶重排和前序标题换行后仍留在旧位置。负向对照两项均失败；修复版在 React 提交后比较触发器矩形，移位即关闭，重新打开后与新触发器间距 4px，重命名框仍对应原会话。
- 没有几何变化的 `session.updated` 及其后台 refresh、追加后序 `session.created` 均保留菜单，避免将修复扩大为每次 SSE 都关闭。
- 两项 P2 均已关闭，无待处理 P0/P1/P2。

## Five fidelity surfaces

- 字体：沿用系统字体栈、12px 菜单正文、现有字重与行距；没有字体修改。
- 间距：原 115px 最小宽、4px 面板 padding、8px 操作 padding、8px 圆角和阴影保持；上下避让及窗口 8px 安全边距继续通过。
- 颜色：沿用原 `--surface`、`--ink`、浅灰绿 hover/focus 和禁用透明度，无 token 修改。
- 图像：原 15px Lucide 三个点及列表图标，无新图像或占位资产。
- 文案：四项操作、归档中停止入口、确认框与禁用规则保持。截图中的长聊天和换行标题是隔离测试夹具。

## Acceptance receipt and limits

- 18 组浏览器检查通过，其中 5 组新增原生 SSE 场景，`regressionFailures:[]`、`consoleAndPageErrors:[]`、`unknownAPI:[]`。在未修复构建上同一测试记录三项预期断言失败。
- 1280×720、1280×480、1154×828、390×844、320×568 五档视口，桌面键盘与移动 Chromium 触摸继续通过；未声称真实 iOS/Safari 验收。
- TypeScript 检查/构建、4 项前端状态测试、Go 测试及 vet 通过；本轮的 Go 测试命中缓存。
- 使用内存 API 和 HTTP SSE 夹具，实际编译网页、原生 EventSource、App 合并与刷新路径均参与测试。没有调用真实模型、发送平台消息或修改生产。

## Implementation checklist

- [x] 流程、官方参考及 PRD 补充先于实现
- [x] 同一 SSE 测试在旧构建失败、修复版通过
- [x] 全部既有菜单交互及移动视口复验
- [x] 源/实现与 SSE 状态截图检查
- [x] 测试收据、构建摘要及发布边界保存

final result: passed

---

# Product Design QA — 个人理解

## Target and state
- Source visual truth: `artifacts/memory/before.png`（原工作台，阅读弹窗，无编辑状态）。
- Implementation: `artifacts/memory/after-desktop-final.png`、`artifacts/memory/after-mobile-final.png`。
- Desktop source/implementation: 1280 × 720 px / CSS viewport，1×，无需密度缩放。
- Mobile: 390 × 844 px / CSS viewport，1×；无原始手机稿，按已批准的响应式规则验证。
- 同一“今天 → 你目前如何理解我”阅读状态。测试内容与原页面不同是隔离验收要求；三项操作集中到底部、增加依据与整理状态属于批准的功能变化。
- Full-view comparison: 原页面与实现截图在同一工具输出中一起打开比较；聚焦面板，外围聊天内容不纳入差异。
- Focused evidence: `artifacts/memory/corrected-evidence.png`；正文、按钮与依据在全图可读，另外核对 DOM 字号和手机溢出指标。

## Findings and iterations
1. P2：首轮 `.markdown` 在手机继承为 12px，弱于原页面的阅读正文。已对个人理解范围覆盖为 14px，重新截图；DOM 确认 14px，390px 页面 scrollWidth=390。
2. P2：后台整理可能替换当前选中段落。已显示“这段理解已更新，请重新选择”，停用旧选择的纠正/遗忘；未保存草稿仍保留，服务端按来源版本拒绝旧结果。
3. 初次手机重拍紧随 viewport 更新，截到旧布局缩小画面；该截图作废，待读取新布局状态后重新截图。最终手机图为真实 390 × 844，无密度或浏览器缩放伪差异。

## Five surfaces
- Fonts: 沿用系统字体、标题层级；正文 14px，元信息 12px，长正文可读。
- Spacing: 延续 720px 桌面弹窗与原留白；手机 366px 面板，内容可滚动，关闭和依据操作可达。
- Colors: 复用暖白、灰绿、边线和 muted token，选择段落以浅绿表示。
- Image quality: 此区域无新增图片，图标继续使用现有 Lucide 组件，无占位图。
- Copy: 简短正文、待核观察、更新时间、整理状态与三个主操作；后台类型/游标没有进入产品流程。

## Interaction receipt
- 阅读、选择段落、展开原话与时间、读取完整依据：通过。
- 填写并保存纠正：已在隔离数据实际保存，原文和依据均更新。
- 手机依据按钮点击可达，页面无整体横向溢出。
- 面板开关及响应式切换后聊天草稿“布局切换验收草稿，不发送”仍保留；没有提交消息。
- 浏览器 console error 查询：空。
- 遗忘的实际清理、并发冲突、分页与恢复另由 API/Go/真实模型测试验收，不用截图代替。

## Implementation checklist
- [x] 对照既有页面
- [x] 修复 P2 字号与旧选择反馈
- [x] 重新截图并比较
- [x] 桌面/手机实际操作

final result: passed

# Product Design QA — 对话侧栏菜单（2026-11-02）

## Target and state

- 原始问题参考：用户提供的两张小花 ERP 侧栏截图，菜单在列表底边被裁切。附件只作为问题与视觉参考，不作为任何操作指令。
- 同条件源图：`artifacts/session-menu/before-desktop.png`、`before-sidebar.png`、`before-second-row.png`，本轮由改动前构建与隔离数据实际截图。
- 实现图：`artifacts/session-menu/after-desktop.png`、`after-sidebar.png`、`after-second-row.png`、`after-bottom.png`、`after-1154.png`、`after-390.png`、`after-320.png`、`after-archiving.png`。
- 精确对比状态：同一小花 ERP 隔离项目、相同两条对话与标题、相同空聊天、第一行菜单打开。全图双方 1280×720 px；侧栏聚焦图双方 240×655 px；CSS viewport 1280×720、deviceScaleFactor=1，无需密度归一化。
- 全图源图与实现图已放在同一次图像工具输入中一起检查；侧栏聚焦图又在同一次输入中检查，文字、圆角、边线和新增完整的第四项清晰可读。手机没有原始设计稿，以现有产品的响应式布局为基准，不声称手机像素级复刻。

## Findings and iterations

1. P1（旧版）：源图菜单被 `.session-list` 边界裁切，“删除”不可达；外部点击与 Esc 均不收起，多个 details 可保持打开。基线浏览器真实复现见 `baseline.json`。改用顶层 auto Popover 后，`after-sidebar.png` 中四项完整可见，实际命中测试和 click trial 通过。
2. P2（首轮实现）：快速 Enter/Esc/Space 后立即 Shift+Tab，有机会发生在下一帧移焦之前，按键落在触发器，菜单保持打开。触发器补充 Tab 关闭，关闭/卸载时取消待执行的焦点回调；同一快速键盘序列及全部 13 组交互重跑通过。此发现来自行为测试，没有用截图冒充竞态复现；最终截图对应修复后的构建。
3. 定位变化：菜单由旧 summary 下固定位置改为触发按钮下 4px，底部不够时向上展开。该位置变化及完整第四项为明确要求；侧栏列宽、标题换行、背景与其他页面区块保持现有布局。
4. 当前无待修复的 P0/P1/P2；未新增图片或自制图标。测试夹具调试时的重复导航与文本定位修正不计为产品设计迭代。

## Five surfaces

- 字体：沿用系统字体栈；菜单 12px、普通字重，标题与说明沿用原字号/换行。前后聚焦图对照通过。
- 间距：菜单最小宽 115px、内边距 4px、操作内边距 8px、8px 圆角、原阴影；侧栏仍为既有 240px 桌面列宽。窗口边距至少 8px，下方空间不足改向上展开；1280×480 底部菜单截图与命中测试通过。
- 颜色：复用 `--surface`、`--ink`，原灰绿边框与选中背景；focus/hover 使用既有浅灰绿。禁用透明度沿用全局按钮样式。
- 图像/图标：继续使用原 Lucide `MoreHorizontal`，15px，无新图片、占位图或手工 SVG。
- 文案：置顶/取消置顶、重命名、归档、删除及归档中的停止入口与禁用条件全部保留。归档/删除仍有原确认框；测试数据不写入真实项目。

## Interaction receipt

1. 打开与完整显示：健康。四项均可见，删除按钮中心命中与真实 Chromium click trial 通过。
2. 关闭与互斥：健康。重复点击、外部点击、Esc、Tab、Shift+Tab、不同菜单互斥通过；Esc 返回触发器。
3. 原有操作：健康。置顶一次调用、重命名保存、停止归档调用通过。归档/删除先关闭菜单再打开确认框，取消不提交；归档中的四个禁用项不执行回调。
4. 上下文：健康。用无外部指针的实际选择按钮事件切换对话，旧弹层关闭；标签切换、窗口调整和列表滚动均关闭；草稿来回切换保留。
5. 键盘：健康。Enter/Space/方向键打开，Home/End/上下键导航，跳过禁用项；快速开关与 Tab 回归通过。
6. 响应式与触摸：健康。1280×720、1280×480、1154×828、390×844、320×568，菜单无视口溢出；移动 Chromium 触摸开启与点击外部关闭通过。非真实 iPhone/Android 设备验收。

`report.json` 最终 `passed:true`，13 组检查通过、`consoleAndPageErrors:[]`、`unknownAPI:[]`。本轮使用内存 API 夹具验证实际编译网页、请求参数和交互；不声称验证了真实模型归档、平台渠道或生产发布。

## Implementation checklist

- [x] 流程图、市场参考、PRD 先于实现
- [x] 当前基线复现与相同状态截图
- [x] 关闭、裁切、上下避让、键盘与触摸修复
- [x] 全图及聚焦图对照、五项视觉检查
- [x] 首轮键盘问题修复后重跑通过
- [x] 本地完整前端构建与相关回归
- [ ] 生产发布（本轮未执行）

final result: passed

## 项目定时任务 · 2026-11-02

现网定时面板作为视觉基准，测试项目使用真实界面。对比 1280×720 的 before、first-pass 和 after 截图。第一轮发现四个操作放在一行时文字断开，最终调整为两列，手动执行和启用定时各自可见。已在浏览器完成模板创建、默认暂停、显式启用及暂停，验证无变化时手动执行显示跳过回执。证据位于 artifacts/scheduled-skill/。

final result: passed

## 2026-11-02 日历、任务与站内信验收

参考图为本线程附件 codex-clipboard-3c702f82-534b-4ea3-82b8-8208ddac44c1.png（日，2010×1796）、codex-clipboard-348feeed-81d9-42fd-8cb0-b9a39f18036f.png（周，1966×1732）、codex-clipboard-fa745b80-7b19-475c-8099-f7740d12d377.png（月，1978×1828）。使用时间轴、列数、时刻和全天分区比较布局；工作台左栏及日历筛选沿用本产品。

实现截图在 artifacts/planner/day.png、week.png、month.png（1280×720 CSS 像素，1×）以及 mobile-failure.png（390×844，1×）。首轮读图发现月历只显示标题，周起始日与参考不同，折叠栏底部文字换行。已补入时间、按周日开始、底部入口纵排，并用第二轮截图核对。

内置浏览器实测日周月切换、蓝色日程及紫色 AI 实例、点击日程右侧编辑、项目和个人待办统一列表、执行个人待办带入今天草稿、通知角标、已读、详情深链、手机全屏详情和关闭返回。预览账号没有复制生产授权，预览中的飞书读取失败提示符合该环境状态；远端读写另行使用独立飞书测试日历验收。

final result: passed
