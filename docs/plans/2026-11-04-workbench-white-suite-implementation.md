# 工作台全页方案3 Implementation Plan

> **For Claude:** 逐项实现和复核；使用Code与Product Design，不委派代理，不重复追加用户已完成的选稿确认。

**Goal:** 一次覆盖全局导航、项目/文件/专家/任务/设置及日历、站内信和所有下钻页面，落地最近一次展示的第3张图“单栏专注，按需侧栏”。

**Architecture:** 保留现有React、Go API、SQLite、消息/文件/任务/渠道状态与权限，改造展示层。复用当前组件的业务回调，新增统一PageHeader/Overlay/EmptyState和中性tokens；项目目录默认按需右侧打开，文件正文继续使用现有Inspector；任务与设置使用统一详情面板。隔离Store/API夹具覆盖完整旅程，真实业务资料不进入预览。

**Tech Stack:** React19、TypeScript、现有Lucide标准图标和系统中文字体、Go opt-in UI fixture、已获授权的本地Playwright。

## 选中目标及尺寸

精确来源：exec-cb9e2c8b-453f-4d28-988b-1e576f091662.png，实际1536×1024，六页图板；选择依据为display-order.json的displayedOption=3。单页实施参考1440×1024；图板中每个子帧按同一比例提取对照，不把图板文字当实际控件。无待生成插图或照片，所有可见资产均为既有品牌及标准图标。

全局侧栏190–200px，紧凑56px页头；正文800–880px，输入最大1200px。项目资料/会话目录为约340px按需面板，文档Inspector360–640px；手机全屏。列表约64–78px一项；标题24–26px、正文15–16px。白/黑/灰token，必要错误/未知状态保留语义。

## Task 1 — 公共壳与弹层

Files: new web/src/surface-ui.tsx, suite.css; modify workspace.tsx, main.tsx, style.css, planner.css。

1. 记录选稿并提交规格；旧应用不另起脚手架。
2. 创建PageHeader、Overlay与EmptyState；Overlay可用于居中表单或侧边详情，提供Escape、焦点限制/归还、后台滚动和隐藏状态处理。
3. 全局去重复面包屑、品牌口号；默认可读侧栏，通知保留未读与异常。中性颜色回到原样式中的实际声明，不只加一层绿色覆盖。
4. 检查导航/深链接/弹层未保存保护和320px布局。

## Task 2 — 项目和文件

Files: main.tsx, workspace.tsx, capability-composer.tsx, project-files.tsx。

1. 项目列表转紧凑项目项，说明仅一行纯文本展示；原字段不改，归档和最近工作保留。
2. 去常驻项目第二栏；项目名/当前会话/对话记录在一个紧凑头，资料/项目事项打开原目录组件；原会话数据不卸载。
3. 文件仍通过openFile/documentGuard进入原Inspector，保留锁、保存冲突、下载/版本/恢复。仅有一个主要详情层，手机文件可返回对话。
4. 项目输入区采用白色宽输入、黑圆发送；去键盘说明/范围脚注和常驻完成标识，保留实际执行/取消/错误及工具选择；不改变权限。
5. 验证会话切换、草稿、文件读写/409/取消关闭、工具选择/引用和项目任务入口。

## Task 3 — 专家、任务、设置

Files: main.tsx, expert-detail.tsx, today.tsx, lifecycle-ui.tsx, planner-tasks.tsx。

1. 专家列表与选中详情组成稳定内容区；导入目录/ZIP/Git折为真实菜单；详情标题/文件/使用范围/版本统一，保存和原guard保持。
2. TasksPanel改为紧凑未完成列表+详情，已结束折叠；只读arrangements分支仍只读。编辑与新增沿用原API，关页前保护改动。
3. 设置常用连接/MCP/CLI/浏览器居前，选中对象在侧边看详情；账号、运行信息/回收站/发送回执按需展开。所有原启停/授权和未知状态语义保持，不虚构渠道外部写能力。
4. 验证专家编辑/版本/项目绑定、导入菜单、待办编辑/准备对话、AI规则、渠道与工具范围；关闭面板不静默丢稿。

## Task 4 — 关联与公共页面

Files: planner.tsx, planner-tasks.tsx, main.tsx (Login/Modal), suite.css。

1. 日历/站内信复用PageHeader与详情壳；同步明确写“同步飞书日历”，保留缓存/Provider同步差异。
2. 统一登录、空/加载/失败、确认与回收站，去掉重复营销文案，保留操作意义所需说明。
3. 检查关联跳转、筛选、刷新/后退、手机弹层/滚动/焦点和错误恢复。

## Task 5 — 真实隔离预览与Product Design QA

Files: new internal/workbench/suite_preview_test.go, scripts/verify-workbench-suite.mjs; update design-qa.md, docs/IMPLEMENTATION.md。

1. 先保存失败的浏览器场景：当前仍常驻二级栏、绿色代码块/页签、页面标题重复、任务长说明、设置运行参数常驻。
2. 夹具使用实际Store/API和虚构项目/文件/专家/任务/日程/连接；不启用渠道发送或真实账号。用实际端点验证动作与冲突。
3. 捕获1440/1280/1024/768/390/320与短屏，每个入口和下钻至少一张截图；组装六页板与选中参考同尺寸一起打开，另查可读局部。
4. review修复后复核；P0/P1/P2清零后design-qa.md写passed，完整记录未展示状态和P3差异。
5. 运行Web check/build、Go test/vet及既有相关行为旅程，变化所及范围再运行竞争检测。只有Go预览测试新增，不改后端业务。

## Task 6 — 发布与交付

沿用当前任务已授权的review→修复→复核→真实Harness/渠道适用验收→同包发布与独立读回。此次应用业务执行代码不变，保持其已验收字节，真实前端入口在隔离实际API验证，发布继续通过既有固定工作台入口与单一锁。实际范围发生变化时准确记录，不把截图当真实执行收据。最终提供运行中本地预览与正式状态。
