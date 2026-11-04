# Today 第3版验收

## 本地实现与review

用户选择最近一次展示的第3张Product Design图。流程/参考/PRD见plans/2026-11-04-today-white-personalization-design.md，实施计划见同日today-white-implementation.md。源码review修复与复核见TODAY-WHITE-REVIEW.md；视觉QA见根design-qa.md。

- 39项真实浏览器交互检查；本地隔离Store/API、固定文件解析器，模型替身，零客户端异常。
- Go整仓竞争检测与vet、Web类型/构建、13项runtime检查通过。
- 六项Python检查含真实PDF、图片OCR、DOCX、XLSX、PPTX、文本及限量/异常结构/子程序输出与期限。
- Go行为检查覆盖本人鉴权、个人会话归属、跨来源拒绝、消息幂等、归集重入与被改写拒绝、分页/越界、取消收拢子进程与暂存。
- 独立实际Harness验收：一次性Store和虚构文件，两个真实模型轮次读取六种格式，再回放微信/飞书各一轮附件；新校验串仅在文件里，提示不提供答案。核对真实MCP事件、同一原生会话和未变业务文件。没有渠道监听器或真实外发，不能把此项称为平台入站/送达。

## 附件能力边界

支持本地文件选择/多选、拖入、图片粘贴、上传状态/失败重试/移除、已上传草稿恢复及纯附件发送。上传最多8件，每件128MiB；读取单文件32MiB，提取正文2MiB，PDF/PPTX50页，XLSX50000单元格。Office不执行宏，不重算公式。PDF提取文字，扫描PDF/无文字返回明确说明；图片OCR保留误识别与非完整图像理解说明。无法读取不宣称已读。

## 收据

本地未入Git的运行收据在artifacts/today-white/：ui-report.json、go-race.log、go-vet.log、web-check.log、web-build.log、runtime-check.log、parser.log、attachments-go.log、real-harness.log和Product Design组合图。

生产部署与Linux安装环境读回将在完成后补记；这些本地收据不能替代生产验收。
