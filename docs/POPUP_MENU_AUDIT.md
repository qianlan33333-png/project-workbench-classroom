# 工作台弹出菜单排查记录 · 2026-11-02

目标：排查侧边栏以外的关闭、裁切及 SSE 更新问题，保留现有功能和样式。依据 Product Design audit/design-qa 流程，以当前编译网页、原生 EventSource 和真实点击收据判断。业务流程与方案见 `POPUP_MENU_PRD.md`。

## 分步结果

| 步骤 | 入口与动作 | 原问题 | 最终状态 / 证据 |
| --- | --- | --- | --- |
| 1 | 项目文件：添加、Esc、外部、选择创建、上传选择器 | 不关闭；短窗口超出底边 | 通过；`candidate/01-project-add.png`、`03-project-short.png`、`08-project-mobile.png` |
| 2 | 专家能力文件：添加、创建、历史只读 | 不关闭；短窗口超出底边 | 通过；`candidate/01-expert-add.png`、`03-expert-short.png`、`08-expert-mobile.png` |
| 3 | 项目 `/` 能力列表：8 标签、键盘/输入法、目录加载/失败/空结果 | 手机顶部裁切；连续输入被旧 RAF 改写光标 | 通过；`candidate/04-project-capabilities.png`、`04-fast-selection.png` |
| 4 | 个人助手 `/` 能力列表 | 共用组件；最新线上基线此用例未裁切 | 通过；`candidate/04-personal-capabilities.png` |
| 5 | 项目名称提示：Esc、导航滚动、窗口变化、项目改名 | 悬挂提示、旧名称 | 通过；`candidate/05-project-tip.png`、`06-project-tip-scrolled.png`、`07-project-tip-sse.png` |
| 6 | SSE：写入锁定、聊天自动滚动、会话重排和换行、无位移更新 | 锁定后旧菜单/建议交互与位置需要覆盖 | 通过；`candidate/09-capability-sse.png`；原 18 组会话菜单回归 |
| 7 | 原生 select 与内容 details，包括新增日历/计划/任务页 | 源码盘点；原生下拉、资料/执行/高级配置折叠 | 不改业务；原生下拉焦点/Esc 抽样通过，内容折叠为源码盘点 |

图像路径均相对 `artifacts/popup-audit/`。基线与修复截图在同一轮、同 viewport、1× 像素密度捕获并成对查看。短窗口为 1280×340，普通桌面 1280×720，移动 390×568。能力浮层从整个标签区上方改为靠实际输入框避让，允许覆盖标签区以保证选项可达。源图和实现中页面自身因浏览器聚焦而滚动的差异已记录，不声称整页像素一致。

## 验证

- 基线 `f1a6ed0` 的构建入口摘要与已安装版本一致；`baseline/report.json` 12 组失败、2 组通过，失败定位均为实际产品行为。
- 新增 23 组真实编译 App 浏览器测试、原 18 组对话菜单回归、4 项状态合并、6 项渠道回放均通过；Go test/vet、Web 类型与构建通过。
- `candidate/report.json`：`passed:true`，console/page errors、未知 API、写消息请求均为空。加载失败测试仅豁免明确注入的能力目录 503。
- 测试调试中修正过未等专家加载完成、未限定 listbox 的原生 option 定位、点击坐标落入菜单，以及异步 resize 的等待；这些夹具问题不计为产品缺陷。
- 本轮真实验收边界：隔离桌面 Chromium，手机为触摸视口模拟；未完成 Safari/Firefox、物理手机或完整辅助技术验收。不声称截图证明无障碍合规。

## 线上复验

待发布后补充独立收据；不以本地测试代替部署验证。
