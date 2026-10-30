# 项目工作台

个人云端工作台：项目保存资料，专家提供通用方法，对话承接持续工作。网页、本人微信、本人飞书及定时任务复用一个 DeepSeek Harness 执行内核。

后端使用 igo `v0.4.5`、SQLite WAL；前端使用 React、TypeScript、CodeMirror。Harness 固定提交为 `ce7073d51254c26cd63b22a60835b8f948deb447`。没有旧业务初始化、强制分类、Electron、自动组队或另一套 Agent 循环。

## 使用

1. 新建空项目，或在项目内导入文件夹／ZIP。项目列表不预置业务数据。
2. 在项目助手中提出任务。需要另一个独立话题时手动另开对话；改变专家不会换掉历史。
3. 专家库可创建方法包，也支持目录、ZIP、完整 Git SHA 导入。先在项目设置启用专家或 MCP，再使用。
4. 微信、飞书使用项目全名切换；`项目列表`、`任务状态`、`回到项目助手` 可直接使用。`另开对话 招聘` 新建专题，`#S对话编号` 或 `#T任务编号` 引用已有工作。
5. `停止` 停止当前渠道焦点中的工作，`停止 #T任务编号` 精确停止。问题回答格式为 `回答 #Q问题编号 答案`。
6. 跨项目引用从网页消息框选择具体项目和路径。渠道也支持：`引用项目「青海」文件「品牌/定位.md」到项目「青山」：比较定位`。本次授权不会延续到下一次输入。
7. `/compact` 调用 Harness 原生压缩。重要结论应落入项目文件；可按需检索本项目其他对话的原始记录。

Agent 写入期间可查看、编辑本地草稿、补充下一条指令或停止，手动保存需等锁释放。停止会保留已发生的修改。文件历史提供前后内容和带当前版本校验的撤销。

浏览器随任务启动，可从对话顶部打开可视接管。任务结束后容器退出；当前项目的浏览器登录状态保留在自己的运行目录，其他项目不会共用。

## 构建与测试

需要 Go 1.26、完整 Node 24.22（含头文件）、pnpm 11.28.5、Python 3.9+。所有依赖使用提交的锁文件；Linux 发行包针对 amd64。

```sh
./scripts/build-harness.sh
./scripts/build.sh
python3 scripts/package-release.py --node .toolchain/node-v24.22.0-linux-x64
```

`build-harness.sh` 校验固定提交，执行上游构建／检查，创建生产依赖部署包，封闭 pnpm `link:` 引用和必需的 workspace peer，并按上游锁中的 SHA512 补入 Linux 原生依赖。不会修改上游源码。发行包中不允许残留源码目录外链。

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

## 部署与资料

- [部署、重启与回滚](docs/OPERATIONS.md)
- [接口与持久化](docs/ARCHITECTURE.md)
- [导入导出与能力边界](docs/DATA.md)
- [真实验收记录](docs/ACCEPTANCE.md)
- [实施状态](docs/IMPLEMENTATION.md)

正式发布状态以验收记录为准。配置、渠道凭证、项目文件、模型账户和运行日志均不进入 Git。
