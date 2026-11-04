# Product Design QA — Today 第3版

source visual truth: `/Users/qianlan/.codex/generated_images/01a1a482-6cdc-7562-8caf-f9a86f0207a9/exec-e18bc4b9-4bd0-4fd5-87fd-6c439e7ee1e6.png`

implementation screenshot: `artifacts/today-white/03-memory.png`

viewport: 1487×1058 CSS px；source与implementation均1487×1058像素，deviceScaleFactor=1，无密度缩放。state：个性化→我的记忆，3条虚构记忆，第二条展开纠正/忘记/查看依据，输入区有本地会议记录附件。

full-view comparison: `artifacts/today-white/comparison.png`，左参考、右实现，在同一比较输入中查看。

focused comparison: `artifacts/today-white/memory-detail-comparison.png`；另查看一级设置、主界面、390/320手机和1280短屏截图。所有内容来自隔离夹具，不含真实业务资料。

## Findings / comparison history

1. [P1，已修复] 旧绿色正文/导航和页头占位使纯白方向失真。取消旧padding，统一白/灰/黑；后续截图中正文rgb(23,23,23)，页面背景rgb(255,255,255)。
2. [P2，已修复] 初版记忆文字17px、面板位置居中偏低、输入框偏窄。改为20px记忆正文、850×630面板及选中图位置、1200px宽输入，调整行间距。查看最终完整/局部组合图，正文和选中行布局接近参考。
3. [P2，已修复] 手机提示覆盖底部入口，且关闭后的键盘焦点不稳定。提示移到上方，显式恢复焦点，Tab包含更多与依据操作；39项浏览器检查覆盖。

## Required fidelity surfaces

- 字体：系统PingFang SC/system sans；标题22/600，正文20/1.8，动作17，手机正文16；长记忆自然换行，不截断事实。保留现有导航密度，避免改动所有业务页面。
- 间距：选中图面板与内容主区域对齐，轻阴影、23px圆角；正文840px阅读宽，输入1200px；小屏设置全屏且内部滚动，原对话不卸载。
- 颜色：#fff背景、#171717主要文字/发送、#f4f4f4选中/气泡、浅灰边线；必要错误保持语义颜色。
- 资产：目标无照片/插图；沿用现有Lucide真实图标库和产品品牌。未用CSS画图或伪造资产。
- 文案：保留个性化/怎么合作/怎么陪我/我的记忆及纠正、忘记、依据；去掉文件名技术描述、日期、大标题、键盘提示、刷新。既有保存正文不改写。

## Intentional product constraints / P3

- 复用已有导航的80px窄栏、图标和项目入口；目标图的98px导航为概念比例，未另建一套应用导航。
- 背景对话为真实隔离API历史，包含正常资料状态提示和待发送草稿，与概念图示例历史不同。输入与我的记忆的交互状态一致。
- 记忆页保留折叠“更多”入口，供主动整理和全局依据检索；未铺开技术说明。

## Implementation checklist and verification

已核对主界面、一级设置、二级记忆、选中/纠正/遗忘/依据、规则加载/保存/409/未保存关闭、附件上传/失败重试/移除/拖入/图片粘贴/纯附件发送/重载草稿、中文IME、跟进决策依据失效与重新核对、已有安排及键盘焦点。

39项浏览器检查通过；390×844、320×568、1280×600、1440×900无横向溢出，输入与设置可用；client exceptions=0。原始收据：`artifacts/today-white/ui-report.json`。

final result: passed
