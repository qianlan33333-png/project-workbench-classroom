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

## Linux与生产

已发布 `cloud-20261104-31-today-white-eba379b`，应用/前端/runtime源提交 `eba379b57f2ccfa6185f8cb6c102424865a3d20b`，tree `2c1ed1ea0f9b271a7f21cc6e7c1fb25d8222471c`。上一成功版本为 `cloud-20261104-30-today-clean-024e6c6`。没有推送GitHub。

首次Linux隔离验收因增量镜像配方的显式USER 0覆盖keep-id而无法访问测试目录，未切换生产。通过固定控制器恢复清理失败候选，移除该用户覆盖，应用代码包保持相同SHA256。修正后的镜像User与原镜像同为空，keep-id正常生效。

实际Linux镜像 `eb217999fe8983ee9b600ef634916b4ecef770119ad6e823b230af884ceee45e`，按运行中的原镜像增量安装固定PDF/OCR依赖。真实Podman Harness四轮在115.63秒内完成六种格式及微信/飞书附件入站回放，24条附件相关MCP事件；业务正文不变、会话身份稳定。回放使用一次性Store，未启动平台发送器，不等同本次新增平台入站/送达。原渠道适配器字节保持不变，发布后验证两渠道新的连接心跳。

通过原固定发布入口及同一把锁晋级。独立读回确认实际进程执行新二进制，安装manifest、公网HTML/JS/CSS与CLI下载摘要一致；1942个既有文件、21个Session/Harness映射和除RuntimeImage外的配置一致。服务均active，微信/飞书均connected且心跳晚于渠道重启。只有current/previous，临时发布状态、上传包和隔离数据已清退。

本次组合包精确依赖为runtime-image和attachment-runtime.Containerfile，当前源代码存档为source-white.tar；white-manifest.json列出源tree和安装文件摘要。不会把增量组合包中的历史参考材料当成本次构建来源。

本地收据：production-readback.json、linux-real-harness.log、production-deploy-fixed.log；首次环境失败记录为first-linux-harness-failed.log。生产保留white-production-acceptance.json及white-final-readback.json，不包含凭证。
