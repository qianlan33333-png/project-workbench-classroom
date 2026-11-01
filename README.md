# 项目工作台

个人云端工作台：项目保存资料，专家提供通用方法，对话承接持续工作。网页、本人微信、本人飞书及定时任务复用一个 DeepSeek Harness 执行内核。

后端使用 igo `v0.4.5`、SQLite WAL；前端使用 React、TypeScript、CodeMirror。Harness 固定提交为 `ce7073d51254c26cd63b22a60835b8f948deb447`。没有旧业务初始化、强制分类、Electron、自动组队或另一套 Agent 循环。

## 使用

1. 新建空项目，或在文件栏添加任意文件、文件夹／ZIP。项目列表不预置业务数据。
2. 在项目助手中提出任务；输入 `/` 搜索并选择本条消息使用的 CLI、Skill 或 MCP。需要另一个独立话题时手动另开对话；改变专家不会换掉历史。
3. 专家库可创建方法包，也支持目录、ZIP、完整 Git SHA 导入。点击专家可查看、编辑包内文件，管理项目使用情况、版本，或复制／导出／删除。专家按项目选择；CLI 和 MCP 在设置中全局管理，所有项目均可使用。
4. 微信、飞书使用项目全名切换；`项目列表`、`任务状态`、`回到项目助手` 可直接使用。`另开对话 招聘` 新建专题，`#S对话编号` 或 `#T任务编号` 引用已有工作。
5. `停止` 停止当前渠道焦点中的工作，`停止 #T任务编号` 精确停止。问题回答格式为 `回答 #Q问题编号 答案`。
6. 跨项目引用从网页消息框选择具体项目和路径。渠道也支持：`引用项目「青海」文件「品牌/定位.md」到项目「青山」：比较定位`。本次授权不会延续到下一次输入。
7. `/compact` 调用 Harness 原生压缩。重要结论应落入项目文件；可按需检索本项目其他对话的原始记录。
8. 对话菜单提供置顶、改名、归档和删除。归档先把核心事实与待办提炼到项目的“对话归档”Markdown，再移入回收站；删除直接移入回收站。设置中可在 15 天内找回，超时清除原始聊天，项目文件继续保留。

界面默认收起全局导航；项目内依次是项目侧栏、对话和按需打开的右侧详情。进入项目先显示对话，打开文件不切换会话。未保存的文档在关闭、替换或离开前提供保存／放弃／取消。导航偏好和详情宽度只保存在本机浏览器。

Agent 写入期间可查看、编辑本地草稿、补充下一条指令或停止，手动保存需等锁释放。停止会保留已发生的修改。文件历史提供前后内容和带当前版本校验的撤销。

浏览器随任务启动，可从对话顶部打开可视接管。任务结束后容器退出；当前项目的浏览器登录状态保留在自己的运行目录，其他项目不会共用。

## CLI 与 Agent 调用

标准企微网关连接器提供独立的 `wecom-gw-cli` 发行包及工作台签名回调适配，通过配置绑定账号、项目和专家；支持私聊应答、人工停答与可靠通知。配置模板、命令和验收边界见 [接入说明](docs/WECOM_CONNECTOR.md)，设计见 [PRD](docs/WECOM_CONNECTOR_PRD.md)。默认不启用，不改变现有本人微信／飞书入口。

独立的 `pwb` CLI 可通过现有工作台密码直接操作项目、文件、附件及其他 API 数据，不设鉴权过期时间。仅需 Python 3.9+；支持 JSON 输出、stdin、版本校验和 ZIP 导入导出。详见 [安装及 Agent 使用说明](cli/README.md)。构建和发行包自动包含 `/cli/pwb.py` 与 `/cli/README.md` 下载入口。

这是调用工作台 API 的客户端。Agent 执行环境中的全局 CLI 是另一项能力：设置提供飞书 CLI、CRM CLI 的安装与授权，也支持指定 Linux amd64 独立命令 tar.gz 发行包。登录状态保存在项目目录外，工具输出仍写入当前项目。MCP 复用 Harness 原生 stdio／HTTPS 协议；不要求逐项目安装绑定。

## 构建与测试

需要 Go 1.26、完整 Node 24.22（含头文件）、pnpm 11.28.5、Python 3.9+。所有依赖使用提交的锁文件；Linux 发行包针对 amd64。

```sh
./scripts/build-harness.sh
./scripts/build.sh
python3 scripts/package-release.py --node .toolchain/node-v24.22.0-linux-x64
```

`build-harness.sh` 校验固定提交，执行上游构建／检查，创建生产依赖部署包，封闭 pnpm `link:` 引用和必需的 workspace peer，并按上游锁中的 SHA512 补入 Linux 原生依赖。不会修改上游源码。发行包中不允许残留源码目录外链。

本轮发行包含飞书／CRM 两套经官方 SHA256 核对的 Linux CLI 原始包，安装时优先使用本地缓存，避免服务器 GitHub 下载不稳定。重新打包时可用 `--tool-archives` 指定目录，放入 `cli-lark-cli-1.0.98-linux-amd64.tar.gz` 和 `aicrm-cli-aicrm-cli_0.6.1_linux_amd64.tar.gz`；脚本验证固定摘要后写入 `tool-artifacts`。安装器将其复制到数据目录的 `tools/downloads`。未提供缓存的其他工具从配置的 HTTPS 发行地址下载。

```sh
go test -race ./...
go vet ./...
pnpm --dir web run build
pnpm --dir runtime run check
```

真实模型测试默认跳过。提供独立配置后可运行；测试使用临时项目目录，不导入用户业务资料。云端容器测试同时验证工具进程收拢、原生恢复、压缩、专家、浏览器和应用 MCP：

```sh
PW_REAL_CONFIG=/path/to/test-config.json go test -v ./internal/workbench -run TestReal -timeout 15m
```

浏览器验收脚本 `scripts/verify-workspace.mjs` 使用独立临时项目和专家，完成布局、五档宽度、草稿保护、文件和专家操作后自动清理。设置 `PW_UI_URL` 和 `PWB_PASSWORD`，使用自己的 Chromium（`PW_CHROME` 可指定路径）。报告及截图写入 `artifacts/workspace-ui/`。

## 部署与资料

- [部署、重启与回滚](docs/OPERATIONS.md)
- [接口与持久化](docs/ARCHITECTURE.md)
- [导入导出与能力边界](docs/DATA.md)
- [真实验收记录](docs/ACCEPTANCE.md)
- [实施状态](docs/IMPLEMENTATION.md)

正式发布状态以验收记录为准。配置、渠道凭证、项目文件、模型账户和运行日志均不进入 Git。
