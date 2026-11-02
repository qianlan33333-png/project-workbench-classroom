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

正式入口：https://dsh.qianlan333.cloud/ 。前端 `b9c1cc86dad2a83e98593a7c3079965de7cf88c6`，发布资源 `web-20261102-popup-b9c1cc8`。保留已安装日历/计划版后端 `cloud-20261102-19-planner-r2` / `f1a6ed0`，后端二进制和服务 PID 未变。应用源码 SHA 与前端 SHA 不同是本轮仅更新前端的明确边界。

- 完整 14 组真实线上检查通过，`live/report.json` 为 `passed:true`。两个文件菜单的桌面、短窗口、手机关闭及可达性；能力选择的真实 SSE 自动滚动、快速输入光标、草稿；项目改名 SSE 的最新提示；原会话菜单重排/换行/稳定锚点均通过。
- 使用真实 Harness 完成两轮回复并校验随机口令，接收 45 个本轮原生 SSE 事件，返回为未压缩 identity。另有发布前真实 Harness 预检查。console/page error 为空，渠道状态均 connected；没有向联系人发送验收消息。
- 公网 HTML/JS/CSS 在开始与最终独立读回时均匹配发布包；`final-read.json` 健康检查 ok。服务 PID、二进制、配置摘要、原有项目/会话 Harness 映射及定时任务记录与发布前一致（`deployment.json`）。六轮专用项目及本轮专家全部清理，最终剩余自身测试数据为零。
- 最终线上截图 `live/project-add.png`、`project-add-short.png`、`expert-add-1280.png`、`expert-add-390.png`、`capability-live-sse.png`、`capability-mobile.png`、`project-tip-live-sse.png` 已保存并检查；项目菜单、专家手机图与本地已验收截图成对查看。
- 前五轮中断均为验收夹具问题，分别涉及首帧定位等待、带子说明的标题定位、合成输入与原值相同被 React 忽略、手机转桌面后迟到的 resize、仅改 hash 没有进入专家页及删除缺少 baseDigest。没有据此改产品代码或重复发布。记录保留在 `live-attempt1` 至 `live-attempt5`；第五轮清理参数错误后已按准确 ID/摘要补清并独立读回，见 `live-attempt5/recovered-cleanup.json`。最终完整运行通过。

## 复跑

```sh
(cd web && npm run build)
node scripts/verify-popup-menus.mjs
node scripts/verify-session-menu.mjs
```

线上脚本 `scripts/verify-popup-menus-live.mjs` 必须使用新专用项目与匹配已部署包的 manifest；不能复用已清理的项目。发布包及源归档在 `/opt/project-workbench/frontend-releases/web-20261102-popup-b9c1cc8/`，旧前端入口在对应 `frontend-rollbacks` 目录。后续发布/回滚遵循项目当前发布规则，勿将历史收据当作当前活动版本。
