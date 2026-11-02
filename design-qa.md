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

## 项目定时任务 · 2026-11-02

现网定时面板作为视觉基准，测试项目使用真实界面。对比 1280×720 的 before、first-pass 和 after 截图。第一轮发现四个操作放在一行时文字断开，最终调整为两列，手动执行和启用定时各自可见。已在浏览器完成模板创建、默认暂停、显式启用及暂停，验证无变化时手动执行显示跳过回执。证据位于 artifacts/scheduled-skill/。

final result: passed
