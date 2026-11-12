# 浅蓝的一号位工作台 · 激活与渠道链接

首次安装使用 Harness 打印的本人访问链接。分发实例的激活页接受激活码，成功后在本实例保存签名授权。工作台里从「更多 → 链接飞书/微信」配置本人渠道。

## 分发实例

在现有安装配置的 Host 环境变量中设置以下值。发码域名和公钥由分发方提供。

| 变量 | 值 |
| --- | --- |
| `WORKBENCH_ACTIVATION_REQUIRED` | `1` |
| `WORKBENCH_ACTIVATION_ISSUER` | 发码方 Harness 的 HTTPS origin |
| `WORKBENCH_ACTIVATION_PUBLIC_KEY_FILE` | Ed25519 SPKI PEM 公钥文件的绝对路径 |
| `WORKBENCH_ACTIVATION_DIR` | 应用进程可读写的私有持久目录，使用绝对路径 |

保持激活目录与实例配套备份。目录中的安装标识和签名授权用于重启后的本地验签。激活服务超时后，使用同一个码重试。已绑定实例可以在领取期限过后取回原授权。

## 发码方实例

发码功能由 `activation-issuer.js` 单独注册。它与实例激活模式互斥。在发码方 Host 环境配置 `WORKBENCH_ACTIVATION_ISSUER` 和 `WORKBENCH_ACTIVATION_SIGNING_KEY_FILE`。后者指向仅运行账号可读的 Ed25519 PKCS#8 PEM 私钥。HTTPS 入口应保留原始 Host，并把 `POST /workbench-activation/redeem` 交给该 Harness。分发实例只获得公钥。

本人用 Harness 的现有浏览器会话打开 `/activation-codes`，点「生成激活码」，再复制给使用者。新码可在七天内绑定一个实例。验码只收激活码和安装标识，数据库保存激活码摘要、领取时间及绑定实例。

## 渠道链接

Host、Worker 和渠道服务共用现有 `WORKBENCH_CONNECTOR_TOKEN`。如修改默认端口，在三者配置一致的 `WORKBENCH_CONNECTOR_PORT`。渠道服务把动态凭证和 CLI 配置保存在 `WORKBENCH_CHANNEL_LINK_DIR`，默认使用服务用户的 `~/.local/state/project-workbench/channels`。同一机器运行多个隔离实例时，各实例使用独立目录。

- 飞书支持扫码建机器人，也可以提交已有自建应用的 App ID 和 App Secret。已有应用需开启机器人能力，订阅长连接事件 `im.message.receive_v1`，具有私聊接收和机器人发消息权限，并完成应用发布。扫码建应用时会预填这些配置和 CLI 的用户权限。租户如果忽略预填或未批准权限，在开放平台启用缺项后回到浮窗。
- 飞书与微信均通过本人私聊发送界面上的测试口令。收到测试回复后点击「已收到回复」，渠道才绑定到该实例的 SuperAgent。已有手工部署的渠道可走「验证私聊」。
- 飞书 CLI 使用同一应用、同一本人身份。使用内置的官方 CLI 进行用户授权，已授权项可以先用。SuperAgent 的 `feishu_cli_read` 工具提供有限的只读操作。授权详情里的缺项来自平台读回结果。
- 微信扫码后按手机提示确认。如果微信显示配对码，在浮窗提交配对码。二维码过期可重新发起。
- 同一飞书应用或同一微信账号重新验证后沿用原连接，通知配置、接收游标和回执继续使用原连接标识。微信同时核对平台机器人 ID 和本人标识。历史配置只有本人标识时，扫码身份必须与 Worker 已保存的本人私聊一致，并用该私聊完成测试；新记录会保存平台机器人 ID。切换到新账号时，旧连接保留回执查询，已选旧通知地址会移除，可在设置中选择新地址。
- 飞书 CLI 读取会记录本人来源，模型改写的派发指令不能把该来源带进项目。省略改写指令时，仍可直接派发本人原始要求。

参考 [飞书 Channel SDK](https://github.com/larksuite/channel-sdk-node/blob/main/README.zh.md)、[打开机器人会话](https://open.larkoffice.com/document/develop-an-echo-bot/introduction) 和 [腾讯微信扫码实现](https://github.com/Tencent/openclaw-weixin/blob/main/src/auth/login-qr.ts)。
