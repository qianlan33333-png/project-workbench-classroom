# 工具组件第一项验收记录（历史）

用户随后授权隔离Playwright并要求完整开发交付。以下记录为第一项历史状态，后续完整结果见 [完整验收](CONTEXT_MIGRATION_ACCEPTANCE.md)。

2026-11-05。基线58563bc，工作目录为独立分支codex/tool-components。此记录只覆盖通用接入与异步调用引用，不宣称独立数据插件、MySQL闭环或整期上线完成。

## 独立审查

独立review_components代理完成多轮只读/re。修复授权绕过、CAS竞态、目录敏感URL、调度阻塞、SSE双向请求、轮询饥饿及过期回收恢复问题后，最终结论：**No findings**。结论范围为当前代码，浏览器与生产验收独立进行。

## 本机最终检查

- go test ./...：通过；workbench包22.942秒，其他包通过。
- go vet ./...：通过。
- go test -race ./internal/workbench -run 'TestComponent|TestCommunication' -count=1：通过，3.154秒。
- node --test runtime/app-mcp.test.mjs：3项通过。
- npm run check --prefix web、npm run build --prefix web：通过。构建保留既有chunk体积提醒，未因此扩大打包改造。
- git diff --check：通过。
- Linux amd64应用与验收二进制交叉构建：通过。

覆盖：并发提交仅一次执行、相同操作键输入冲突、越项目/动作拒绝、模型身份参数拒绝、停止源Run拒绝、目录脱敏/不进原生MCP、丢响应查回、停用后的在途管理、迟到提交不覆盖取消、TLS和SSE、多行数据及相同请求编号的反向ping、异常轮询不饿死正常任务、清理前保护、已过期回收与旧墓碑恢复启动。

## 真实Harness

使用当前生产主机的既有受管Harness/模型配置，但创建隔离临时Store和独立runtime目录；组件执行器使用虚构固定输出，没有客户读写、发信或正式定时变更。首次测试8.31秒通过；最终候选复跑8.50秒通过。验证list_components→call_component实际调用及原Session调用记录，不以模型回复文字代替工具调用计数。

## 页面与生产

Work设置禁用了本机浏览器控制，尝试内置浏览器返回“Local browser use is disabled in Work settings”。未改用其他浏览器自动化绕过。已向用户请求启用后继续。

因此桌面/手机交互、视觉、生产切换及安装后读回均**未验收**。生产仍为cloud-20261105-33-session-communication-352c498；旧上下文中心保持运行。即使/re无问题，也必须完成页面验收后才上线。

最终生产只读核对：current/previous仍为上述会话通信版与上一白色工作台版，程序和渠道服务均active。本任务的远端隔离验收目录已删除，未切换生产。
