# 自动化应答课堂练习

学员自己部署时默认无需激活码。启动后使用终端打印的 **Harness 本人访问链接**打开工作台。真实试答需要自己的 DeepSeek API 密钥，可在工作台的「设置 → 模型」填写。

安装需要 Node.js 24、Go 1.26、MySQL 8.4 和 Temporal CLI。下面以本机课堂练习为例，命令在仓库根目录运行。

1. 在本机 MySQL 创建空库 `workbench_classroom`，给工作台账号该库的建表和读写权限。安装依赖并构建。

   ```sh
   mkdir -p .var/classroom/dsh
   (cd native && npm ci --include=dev --no-audit --no-fund && npm run build:vault-editor)
   (cd native/worker && go build -o ../../.var/classroom/workbench-worker ./cmd/worker)
   ```

2. 在一个终端启动本机 Temporal，保持该终端运行。

   ```sh
   temporal server start-dev --ip 127.0.0.1 --port 7233 --namespace workbench \
     --db-filename "$PWD/.var/classroom/temporal.db" --headless
   ```

3. 在另一个终端设置工作台环境变量，将示例 DSN 换成自己的 MySQL 连接。保持该终端运行。

   ```sh
   unset WORKBENCH_ACTIVATION_REQUIRED WORKBENCH_ACTIVATION_SIGNING_KEY_FILE
   export WORKBENCH_MYSQL_DSN='workbench:YOUR_PASSWORD@tcp(127.0.0.1:3306)/workbench_classroom?parseTime=true'
   export WORKBENCH_HOST_TOKEN="$(openssl rand -hex 32)"
   export WORKBENCH_REPLY_PRACTICE=1 WORKBENCH_RESOURCE_ACL_EDIT=1
   export WORKBENCH_LISTEN=127.0.0.1:7381 WORKBENCH_WORKER_URL=http://127.0.0.1:7381
   export WORKBENCH_BRIDGE_PORT=7382 HARNESS_BRIDGE_URL=http://127.0.0.1:7382
   export TEMPORAL_ADDRESS=127.0.0.1:7233 TEMPORAL_NAMESPACE=workbench
   export WORKBENCH_PUBLIC_HOST=127.0.0.1 WORKBENCH_TOOL_SERVERS='[]'
   export DSH_HOME="$PWD/.var/classroom/dsh" DSH_BUNDLED_SKILL_DIR="$PWD/native/skills"
   export WORKBENCH_PERSONAL_WORKSPACE="$PWD/.var/classroom/personal"
   export WORKBENCH_VAULT_ROOT="$PWD/.var/classroom/vaults"
   test -d "$DSH_HOME/node_modules" || ln -s "$PWD/native/node_modules" "$DSH_HOME/node_modules"

   .var/classroom/workbench-worker &
   wb_worker_pid=$!
   trap 'kill "$wb_worker_pid" 2>/dev/null || true' EXIT
   node native/node_modules/@deepseek-ai/dsh/lib/bin.js web \
     --patch "$PWD/native/harness/cordis.patch.yml" --no-open --host 127.0.0.1 --port 3082
   ```

4. 打开本人访问链接，创建「自动化应答」项目。进入项目「设置 → 开始练习」，选择专家和语料。可在「知识库」建语料库并授予项目读取权限。底部聊天框接收模拟客户消息，回复只在当前练习会话中显示。修改专家或更新语料后点「新练习」复测。

如果由分发方集中发码，在 Host 设置 `WORKBENCH_ACTIVATION_REQUIRED=1`，并配置签发服务地址、验签公钥和持久化激活目录。这样的实例首次使用需要分发方签发的激活码。部署在服务器或本机都遵循同一开关。详见[分发激活配置](ONBOARDING.md)。
