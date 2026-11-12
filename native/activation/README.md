# 独立激活码管理

管理页由 [SQLPage](https://github.com/sqlpage/SQLPage) 渲染。签发服务是独立 Go 进程，继续使用原激活表和签名协议。SQLPage 通过私有 Unix socket 提供页面。Go 进程验证管理身份、生成激活码和签发授权。

## 安装

1. 从 `native/worker` 运行 `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o activation-issuer ./cmd/activation-issuer`。从 [SQLPage v0.47.0](https://github.com/sqlpage/SQLPage/releases/tag/v0.47.0) 取 Linux 发行包。`sqlpage-linux.tgz` 的 SHA-256 为 `b566a5d609e2894cf7658c5a421828de3a3b0bc993f405713ee2d2513d27ae5c`。
2. 使用独立的系统账号 `workbench-activation`。将两个二进制和 `admin/` 安装到 `/opt/workbench-activation/`，创建空的 `/opt/workbench-activation/sqlpage/` 配置目录。安装 [systemd 单元](deploy/)。
3. 指向现有 MySQL 库。SQLPage 账号仅有 `wb_activation_codes` 的 `digest, expires_at, claimed_at` 列的 SELECT 权限。签发账号只在该表有 SELECT、INSERT 和 `installation, claimed_at` 的 UPDATE 权限。使用 HTTPS 前置代理保留原 Host。
4. 配置下列环境变量并启动两个独立单元。保留原发码域名及原 Ed25519 私钥，已安装客户端继续按原地址和公钥验证。

| 文件 | 变量 | 内容 |
| --- | --- | --- |
| `/etc/workbench-activation/issuer.env` | `WORKBENCH_ACTIVATION_ISSUER` | 原签发方 HTTPS origin，无末尾斜杠 |
| 同上 | `WORKBENCH_ACTIVATION_SIGNING_KEY_FILE` | 原 PKCS#8 Ed25519 私钥绝对路径 |
| 同上 | `WORKBENCH_ACTIVATION_DB_DSN` | 具有激活表读写权的 Go MySQL DSN |
| 同上 | `WORKBENCH_ACTIVATION_ADMIN_PASSWORD_SHA256` | 管理密码的 64 位十六进制 SHA-256。生成一个独立的 32 字节随机密码并存入现有凭据管理器。登录名是 `admin` |
| `/etc/workbench-activation/sqlpage.env` | `SQLPAGE_DATABASE_URL` | 激活库只读账号的 `mysql://` URL，特殊字符用百分号编码 |

仅将以下路径转发到本机 `127.0.0.1:7387`。以 Caddy 的路径匹配为例，在原工作台通配路由前加此规则。

```caddyfile
@activation path /workbench-activation/redeem /activation-codes /activation-codes/*
handle @activation {
    reverse_proxy 127.0.0.1:7387
}
```

切换前用隔离实例验原码和新码，验证匿名管理页返回 401、已认证跨站提交返回 403、同一实例重试取回原授权。切换后读回两个进程、管理页和原兑换路由，再从发码方的 Harness 配置移除 `WORKBENCH_ACTIVATION_SIGNING_KEY_FILE`，关闭旧的管理及兑换路由。若回滚，先恢复原发码方配置，再将这几个路径交回原上游。两个版本共用原表，回滚不需要恢复数据。

## 统计

「今日新增」按北京时间计数。「累计生成」包含全部历史码。「已兑换」按已记录的首次兑换计数，领取截止时间只影响尚未兑换的码。管理页按原码精确查询，表格保留记录编号和时间。生成响应丢失时，重试同一次表单提交会取回原结果。
