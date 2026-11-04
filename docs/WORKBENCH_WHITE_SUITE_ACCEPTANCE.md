# 全工作台方案 3 上线验收

2026-11-04 18:59（北京时间）完成正式切换，独立公开资源读回通过。

## 版本和范围

- 正式：`cloud-20261104-32-suite-white-78a6002`。
- 上次成功版：`cloud-20261104-31-today-white-eba379b`；只保留这两个版本。
- 前端提交：`78a6002f3e18508906d00ab9a35acf44b1cb9f89`，tree `d541cd4cea430fc5312847f7844601db54012644`。
- 后端与运行代码继续使用 `eba379b57f2ccfa6185f8cb6c102424865a3d20b`。后端 SHA256 `7df6d2405a2cdfff106789a40a5df9cc1db5a2fc50667f4058e50db7de55f9a0`；运行镜像 `eb217999fe8983ee9b600ef634916b4ecef770119ad6e823b230af884ceee45e`。没有业务 API、数据库迁移或运行/外发权限变更。

项目列表、项目对话/资料/文件/事项/设置、专家概览/文件/编辑/范围/版本、待办/AI 规则、连接/MCP/CLI/账号/高级、日历/日程、站内信/失败详情与登录已统一。项目主视图只保留对话，资料与事项按需打开；文档 Inspector 保持修订、冲突和锁定语义。Today 原有个性化、二级记忆和持续跟进逻辑保留。

## Review 和本地验收

- 业务流程、参考与 PRD：`plans/2026-11-04-workbench-white-suite-design.md`；选中图板为最近一次第3张“单栏专注，按需侧栏”。
- Product Design 同图及局部对照：根目录 `design-qa.md` 为 passed。修复导航旧样式、输入宽度、面板遮挡、平板密度、概览重复和短屏设置折叠位置。
- 真实隔离 Store/API、虚构资料与确定性执行器：83项主旅程、12项补充检查通过。覆盖 1440/1280/1024/768/390/320 和 390×420 短屏。
- Review 额外修复：文档和目录互斥、未保存取消/保存/放弃、保存后的误报提醒、浏览器后退单次确认与草稿保留、导出链接键盘操作和删除任务后详情残留。
- Go `test ./...`、`vet ./...`、Web check/build、Runtime 13项检查通过。未将预览中的固定回复作为真实模型证据。

复核脚本：`scripts/verify-workbench-suite.mjs`、`scripts/verify-workbench-suite-review.mjs`；opt-in 预览：`PW_SUITE_PREVIEW=$PWD/artifacts/workbench-white-suite go test ./internal/workbench -run '^TestWorkbenchSuitePreview$' -count=1 -timeout=3h`。预览不使用真实账号、业务资料、平台发送器或模型凭证。

## 真实执行与渠道

正式同一运行镜像在临时 Store 中完成真实 Harness/MCP 验收（28.49秒）：TXT、DOCX、XLSX、PPTX、PDF 和图片 OCR 的实际内容读取，两轮附件消息，以及微信/飞书两种来源的真实模型入站回放；原生会话连续，测试业务正文24字节保持不变。该验收不启用平台消费者或平台发送器。

真实平台链路复用用户本人此前发出的「今天联测1104，仅回复收到」：只读核对微信与飞书各一条入站、一个完成运行和一条有平台回执的回复，没有重复运行/回复。微信状态为 accepted（平台已接受），飞书为 sent；平台接受不被写成接收人可见。本次没有新增真实平台外发。切换后两条启用连接均为 connected，心跳晚于本次渠道服务启动。

## 打包修复及生产读回

首次候选的网页包漏带原有 `/cli/pwb.py` 与 `/cli/README.md`；正式验收失败后固定控制器自动恢复原 current/previous，服务继续 active。随后将 CLI 复制接入常规 Web build，补上解包前的必需文件检查，重新构建、复核和完成切换。两份 CLI 下载与原正式文件/仓库源文件的 SHA256 一致。前端 JS/CSS 在此修复前后字节不变。

发布沿用固定 `/opt/project-workbench/deploy-today.sh` 和同一把锁，未原位覆盖已发布资源。生产验收与独立读回确认：

- 公开 index、JS、CSS 和两份 CLI 下载逐文件 SHA256 等于准确发布包，health=200。
- 107份包内文件以及复用后端/关键运行文件与组合 manifest 一致；实际进程使用当前版本的二进制。
- 1942份原有项目文件逐一保持原 SHA256，21组原生 Session/Harness 映射保持不变；业务配置保持不变。
- 工作台及渠道服务 active；当前/previous 正确，仅保留两版，发版中临时信息已删除。

本地证据：`artifacts/workbench-white-suite/{browser-receipt,review-receipt,production-readback,independent-public-readback,cli-http-receipt}.json`、`linux-real-harness.log`、`production-deploy.log` 和首次失败日志。生产组合版本内保存 `suite-manifest.json`、`suite-linux-harness.log`、`suite-production-acceptance.json` 与 `suite-final-readback.json`。这些是业务/发布验收，不是新增清理收据平台。
