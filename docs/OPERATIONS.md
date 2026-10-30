# 部署、重启与回滚

## 固定运行环境

Ubuntu 24.04 / Linux amd64，Go 1.26.10，Node 24.22.0（完整发行包含头文件），pnpm 11.28.5。igo 0.4.5，Harness ce7073d51254c26cd63b22a60835b8f948deb447。运行镜像基础层固定摘要，系统工具由 Debian 签名仓库安装；需要严格复现所有系统包时应保留已验收镜像。

安装系统依赖：`apt-get install podman uidmap slirp4netns fuse-overlayfs dbus-user-session`。只有新工作台使用 projectwb 用户，不更改其他应用用户。`loginctl enable-linger projectwb` 保证重启后用户服务管理器可用。

**实际应用必须在 projectwb 的用户服务域运行。** 顶层 `project-workbench.service` 通过 `systemd-run --user` 启动 `project-workbench-runtime.service`，不是直接在系统服务的 User= 进程里启动容器。后者会因跨 cgroup 迁移权限失败；cgroupfs 虽可能启动，也不能假定资源限额有效。本机实测 memory.max=2411724800、pids.max=256。

## 构建完整发行包

参见 README 的构建命令。`package-release.py` 同时打包应用二进制、web/dist、runtime 及生产依赖、固定 Harness、Linux 原生组件、完整 Linux Node、服务模板、脚本和文档。Linux Node 24.22.0 放在 `.toolchain/node-v24.22.0-linux-x64`，或传 `--node` 指定目录；从 Node 官方发行页下载并校验官方 SHASUMS256。没有外部 pnpm workspace 链接。

```sh
python3 scripts/package-release.py --output .var/project-workbench-release.tar.gz
# 上传 tar.gz、同名 .sha256 及 install-release.sh 后，在服务器执行：
sudo APT_MIRROR=mirrors.tencentyun.com bash install-release.sh project-workbench-release.tar.gz cloud-YYYYMMDD-NN
```

安装脚本只安装新应用，不修改 Caddy、不停止其他应用。首次创建配置后再执行安装：

- `/etc/project-workbench/config.json`：从 deploy/config.example.json 配置。DataDir、WebDir、RuntimeImage、HarnessHome 均为真实绝对路径。
- `PasswordHash`：用发行包 `bin/workbench -hash-password` 从标准输入生成；在网页改密码后，以数据库内设置优先。
- `ChannelToken`：随机 32 字节 token；`channels.json` 的 token 必须一致。
- `/etc/project-workbench/model-home/.credentials.yaml`：Harness 的模型凭证，projectwb 可读、0600。保持该文件在项目目录外。
- `/etc/project-workbench/channels.json`：`{"url":"http://127.0.0.1:3082","token":"与 ChannelToken 一致"}`。url 是应用根地址，不含 `/api/v1/channel`。预览阶段可含 `/next`。
- `/etc/project-workbench/service.env`：安装器按用户实际 UID 生成 XDG_RUNTIME_DIR、DBUS_SESSION_BUS_ADDRESS，不手填其他用户 UID。
- 配置目录 projectwb 所有、0700；含凭证的文件 0600。不得把真实配置打进源码或发布包。

正式根入口：BasePath 为空，PublicURL 是站点根 URL；BrowserURL 为 `/api/v1/browser/vnc.html?autoconnect=true&resize=scale&path=api/v1/browser/websockify`。Caddy 只把工作台入口代理到 127.0.0.1:3082。公司官网 `/company/` 与 `/robots.txt` 保持独立路由。

首次启动需预热 `podman run --rm --userns=keep-id IMAGE dsh --version`。新镜像首次创建用户映射可能耗时约 80 秒；后续执行不重复该开销。容器 VNC 只发布在服务器 127.0.0.1:6081，由登录后的应用代理。

## 渠道与切换

新渠道桥使用官方飞书 SDK 和 iLink。新飞书绑定可在设置内生成一次性绑定码；私聊机器人发送 `绑定 码` 完成绑定。微信配置需要现有 iLink 登录 token、ownerId、baseUrl；第一版没有网页扫码登录流程。过期后更新凭证再启用，不重放未知状态的出站消息。

迁移本机旧账号：先停 `workbench-wechat`、`workbench-feishu` 和 `deepseek-harness`，检查旧 Harness 中没有遗留机器人子进程；再执行 `scripts/migrate-channels.mjs --inspect`、`--commit`。脚本核对旧账号 hash，飞书从已知私聊成员解析并核验唯一本人。只搬凭证、绑定、游标、去重，不导入业务资料和聊天，不发送旧 outbox。重复迁移会拒绝覆盖新的游标。

先验证新渠道真实收发，再切换正式网页入口。顶层新渠道单元与三个旧消费者单元冲突，避免重启后重新出现多消费者。切换前后检查完整进程树，不能只凭 service 名称推断旧机器人已停止。

## 日常检查与重启

独立 CLI 下载为站点 `/cli/pwb.py`，说明为 `/cli/README.md`。`scripts/stage-cli.sh` 将源码复制进 `web/dist/cli`，构建和发行打包均会执行它。CLI 使用当前工作台密码直接鉴权，不依赖登录表，无需定期刷新凭证；不要将个人 `~/.config/project-workbench/cli.json` 放入发行包。

```sh
sudo systemctl status project-workbench project-workbench-channels
sudo journalctl -u project-workbench -u project-workbench-channels --since '10 minutes ago'
curl -f http://127.0.0.1:3082/api/v1/health
sudo systemctl restart project-workbench
sudo systemctl start project-workbench-channels
```

应用重启会停止依赖它的渠道桥，应用恢复健康后显式重启渠道桥。启动先清理遗留容器、收拢文件变化、标记中断、使旧问题失效、释放写锁，再接收新的业务运行。SSE 支持 Last-Event-ID，服务退出时主动关闭事件连接。

渠道状态页面显示连接状态；微信“平台已接受”和飞书“平台已发送”不是已读凭证。`unknown` 必须核对平台，不自动重发。失败任务可以另发明确的继续指令，保留原失败记录，不能简单改回 queued 重放可能发生的外部动作。

## 备份与恢复

完整备份应在新应用及渠道桥停机后复制 `/var/lib/project-workbench`、`/etc/project-workbench`、对应发行目录，保留目录所有权。SQLite WAL 文件要与主文件同一时点复制；在线只用 SQLite backup API，不只复制主数据库。项目 ZIP 导出不包含完整对话及凭证；完整恢复还需要 runtime、blobs 和数据库。

恢复时停止两项新服务，恢复同一时点的数据与配置，将 current 原子指向对应发行目录，确认镜像存在、projectwb 所有权、模型凭证，再启动应用与渠道。不要把旧数据库与新 runtime 混搭。

## 本次回滚窗口

切换时间记录在 `/opt/project-workbench/cutover.json`。旧部署及快照至少保留 7 天。旧回滚资料在 `/opt/project-workbench/rollback-20261030`；新应用部署及用户资料在另一组目录。`scripts/cleanup-legacy.sh` 默认只检查，达到时间且稳定后才可显式清理旧部署。公司官网不在清理路径内。

如仅需回退网页，保留新应用及新渠道，使用已备份的 Caddy 配置把根入口改回 3081，并启用旧 yihaowei-workbench；新渠道仍连接新对话，不恢复旧消费者。

如需完全停用新系统：先停止新渠道和新应用，保存当前完整快照，再回退网页。**不要直接启动旧微信／飞书消费者**：它们的游标、去重表与待发送记录停留在切换时刻，可能重复执行。应先把新接收游标和已消费 ID 反向核对，隔离旧待发送消息，再单独启用；当前交付不会自动执行该反向业务迁移。无需回退数据时优先回滚新应用的发行目录，维持新数据库和会话映射。

相关机制见 [systemd 委托文档](https://systemd.io/CONTROL_GROUP_INTERFACE/) 和 [Podman 文档](https://docs.podman.io/en/stable/markdown/podman.1.html)。

## 工作台优化版本与回滚

当前发行：`/opt/project-workbench/releases/cloud-20261030-05-workspace`。上一版：`cloud-20261030-04-cli`。发布前完整快照保存在 `/opt/project-workbench/rollback-workspace-20261030/{data,config}`，`previous-release` 记录上一版路径，至少保留 7 天。快照包含当时数据；部分临时 CLI 验收记录可能在该时点存在，最终线上已清理。

本次是兼容的字段和文件版本扩展，不重新导入项目、不重建对话或渠道。需要紧急退回上一版程序时，等待全部排队／活动任务结束，再执行（只回退程序，保留当前数据、渠道游标及回执）：

```sh
sudo systemctl stop project-workbench-channels project-workbench
sudo ln -sfn /opt/project-workbench/releases/cloud-20261030-04-cli /opt/project-workbench/current.rollback
sudo mv -Tf /opt/project-workbench/current.rollback /opt/project-workbench/current
sudo systemctl start project-workbench
curl -f http://127.0.0.1:3082/api/v1/health
sudo systemctl start project-workbench-channels
```

旧界面不提供新增专家管理功能。不要直接恢复旧快照覆盖上线后的文件或回退平台游标；完整数据恢复前先保存当前状态并核对后续消息和发送回执，按前文“备份与恢复”处理。源码、验收脚本和配置说明随新发行一并保留。

## 消息反馈修复版本

当前运行版本为 `cloud-20261030-06-message-feedback`，上一版为 `cloud-20261030-05-workspace`；发布前完整数据与配置快照在 `/opt/project-workbench/rollback-message-feedback-20261030`。回滚只需在无活动任务时按前文步骤将 current 指回 05，并显式启动渠道桥；不要回退数据库和消息游标。新版仅增加回执读取与关联字段，没有变更执行队列或模型配置。


### 实时事件流不能缓冲

本机 Caddy 2.6.2 的默认 `encode zstd gzip` 曾将 SSE 编码，实测新增事件未及时送到浏览器，甚至后台完成后界面仍显示执行中。只在工作台事件流请求上排除压缩（JSON 事件历史及其他页面仍可压缩）：

```caddyfile
@workbench_compressible {
    not {
        path /api/v1/events /next/api/v1/events
        header Accept *text/event-stream*
    }
}
encode @workbench_compressible zstd gzip
```

完整独立站点示例见 `deploy/Caddyfile.example`；不要用示例覆盖现有公司官网路由。编辑后运行 `caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile`，通过后 reload。实际修改前的 Caddyfile 保存在 `/opt/project-workbench/rollback-message-feedback-20261030/Caddyfile`。程序回滚时可继续保留这项事件流修复；只有确需恢复代理配置时才恢复该文件并再次 validate/reload。对照脚本通过新增临时文件产生一个真实事件，检查浏览器是否及时收到，不能只看连接为 200。

配置依据：[Caddy encode](https://caddyserver.com/docs/caddyfile/directives/encode) 与 [请求匹配器](https://caddyserver.com/docs/caddyfile/matchers)。当前新版文档的增量流行为不能代表服务器已经安装的 2.6.2，必须以实际版本和浏览器测量为准。
