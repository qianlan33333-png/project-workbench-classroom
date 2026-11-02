---
name: project-workbench-release
description: 发布、回滚或清理项目专家工作台（个人工作台），始终只留正式版和上一成功版本，清退旧环境与无用构建资源。仅适用于本工作台，不用于 CRM 或同机其他项目。
---

# 工作台发布：只留两版

用户已确定：**一个正式版 + 一个上次成功的回滚版，其他旧版本、旧快照、无用构建资源和退休旧环境全部删除。** 替代以前的七天保留要求。常规轮转继承本项目已有发布授权，不重复问是否保留第三份。

设计依据和对抗性推演见 [极简 PRD](references/retention-prd.md)。曾核验生产为 `124.220.53.183`，SSH 用户 `ubuntu`、运行用户 `projectwb`；操作前重新确认主机和 current，密钥不写入仓库。

## 只维护这些东西

- 一把锁：`/opt/project-workbench/.release.lock`，现有全量、前端增量、镜像构建、回滚和清理入口共用，从准备到清理结束持有。接管前等待仍未使用这把锁的旧发布任务结束。
- 两个指针：`current` 指正式版，`previous` 指上次成功版。首次接入根据实际成功发布记录确定 previous，之后随发布更新，不按目录时间排序。首次安装可暂时只有 current。
- 每个版本目录保存自己的程序、前端和依赖，并有一行 `runtime-image` 写实际完整镜像 ID；配置确有变更才保存必要的版本配置，不复制账号凭证。
- 发版中才有一个临时 `.deploying` 文件，原子写入原 current、原 previous、candidate 三个路径。它用于中断恢复，完成后删除，不保存清理收据。

## 每次发布的固定顺序

1. 取得锁。若有 `.deploying`，按 PRD 先恢复上次操作。先清理两个保留版本之外的已确认旧包，再检查候选解包/镜像增量所需空间，另留 5 GiB 余量；不足就停止新发版，不能删 previous 腾空间。
2. 写好 `.deploying` 后准备一个全新 candidate，复用项目现有的业务静默、Harness、渠道和发布验收。已发布目录不再原位修改；前端补丁也成为一个新组合版本，可复用未变的程序和依赖。不加独立验证平台或清理测试流程。
3. 原子切换 current 并完成现有生产验收；失败恢复原 current/previous 和本次改动的配置，仅删除原两版之外的新候选。通过后原子将 previous 指向原 current 并落盘，随即解除发布失败回滚 trap；这一步完成才允许删除旧回滚版。
4. releases 下只留 current 与 previous。删除其余版本、过期回滚副本、旧前端资源/归档、结束的预览、上传解包文件和本项目构建缓存。两版所需共享文件仍保留。
5. 在实际运行用户的容器存储中，保留两版 `runtime-image` 指定的 ID，定点移除其他本项目镜像；父子层和在用保护交给引擎。不能全局 `prune -a`、强删镜像/volume，或手删 overlay/diff1。正在构建/使用或归属不明的项先停下核对，不扩大删除。
6. 全部清完后删除 `.deploying`。清理失败不回退已经成功的正式版，保留临时文件，下次持锁先续清；直接向用户说明剩余项，不另建清理日志、收据或状态系统。

## 仅有的保护边界

- 生产 DB、项目正文、上传、blobs、现用工具/凭证、渠道游标及业务发送回执保留原位。普通发版不备份整份数据，回滚只退程序和必要配置。业务发送回执与“清理收据”是不同内容，不能删除前者。
- 必要数据库迁移才做一致性备份，最多保留一份对应回滚边界的恢复点；不能用旧 DB 覆盖上线后的新业务写入。常规发布须兼容上一版程序，不兼容迁移单独处理，不硬套这条极简发布流程。
- 退休旧工作台的代码、数据、缓存、预览和服务一次性清退；先确认当前两版没有指向它们。公司官网、当前业务数据及仍被其他应用使用的系统组件不删。
- 发布资源不得留下第三个稳定版本。必要共享层属于两版的依赖，不按层数限制；无法证明无用的层不手删，异常存储另行修复。
- 主动回滚只交换两个指针，沿用同一锁和临时恢复信息，不重新打包。失败时不能删除本来就是 previous 的回滚目标；若原正式版已确认不可用，删故障版后允许暂留恢复成功的一版，不能保留坏版凑数。

## 已接入的入口

现有六个 shell 入口共用 `scripts/release-common.py`。生产同名固定入口在 `/opt/project-workbench/`；当前与回滚版内的 scripts 也转到该固定实现，不再运行 `/tmp` 里的历史副本。

- 全量安装：`sudo bash /opt/project-workbench/install-release.sh ARCHIVE RELEASE_NAME`，校验摘要、空间预算、构建镜像后轮转，成功删除上传包。
- 已准备候选：`sudo env PW_RELEASE=RELEASE_NAME PW_EXPECTED_PREVIOUS=当前绝对路径 bash /opt/project-workbench/deploy-today.sh`。
- 回滚：`sudo bash /opt/project-workbench/rollback-today.sh`，不传历史快照。
- 常规清理：`sudo python3 /opt/project-workbench/release-common.py cleanup`；清退旧环境沿用 `cleanup-legacy.sh`。

云端自行准备增量候选时，准备 shell 先 `exec 9>/opt/project-workbench/.release.lock`、`flock -n 9`，再 `export PW_RELEASE_LOCK_FD=9`；在同一 shell 内准备并调用现有发布入口，控制器复用此锁。临时上传/解包放 `/opt/project-workbench/incoming`，成功或恢复时清除。需要额外既有发布验收时传可执行的 `PW_ACCEPTANCE_SCRIPT`，它在提交 previous 前运行，失败恢复原两版。

若用 `cp -al` 复用当前版本，更新文件必须先 unlink 或原子替换，不能截断硬链接文件；前端 `web/dist` 用新构建整目录替换，不持续追加旧散列资源。

控制器仅修改配置中的 RuntimeImage，保留其他业务配置；其他配置变更、不兼容迁移和异常存储修复单独处理。必要迁移的单份恢复点放 `migration-backup`，不要放进会轮转删除的 `rollback-*`。首次安装沿用隔离原则，不自动启用渠道消费者。

2026-11-02 已在生产安装这些入口并清退旧资源，正式程序没有换版；已验证真实 Harness 和渠道状态。不创建清理收据、常驻服务或 timer。
