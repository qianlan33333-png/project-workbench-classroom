# 自动化应答课堂练习

课堂版首次使用需要激活码。下载包开箱启用激活，按下面的源码步骤启动也会启用激活。使用终端打印的 **Harness 本人访问链接**打开激活页。激活后在「设置 → 模型」填写自己的 DeepSeek API 密钥。

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
   unset WORKBENCH_ACTIVATION_SIGNING_KEY_FILE
   export WORKBENCH_ACTIVATION_REQUIRED=1
   export WORKBENCH_ACTIVATION_ISSUER=https://dsh.qianlan333.cloud
   export WORKBENCH_ACTIVATION_PUBLIC_KEY_FILE="$PWD/native/classroom/activation-public.pem"
   export WORKBENCH_ACTIVATION_DIR="$PWD/.var/classroom/activation"
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

4. 打开本人访问链接，输入激活码，再创建「自动化应答」项目。进入项目「设置 → 开始练习」，选择专家和语料。可在「知识库」建语料库并授予项目读取权限。底部聊天框接收模拟客户消息，回复只在当前练习会话中显示。修改专家或更新语料后点「新练习」复测。

课堂使用现有签发服务。部署在服务器或本机都遵循同一激活开关。替换签发方时须一起更换地址和验签公钥。详见[分发激活配置](ONBOARDING.md)。

## 课堂下载包

课堂 ZIP 已编译工作台和启动器，并附带 Node、Harness、MySQL、Temporal。完整解压后，macOS 双击 `Start.command`，Windows 双击 `Start.cmd`。保持启动窗口运行。打开的是同一个本机工作台网页。macOS 和 Windows 使用各自的包。

课堂启动器会先显示激活页。输入发码方的激活码后再进入工作台配置自己的 DeepSeek API 密钥。数据、会话和激活状态保存在本机用户配置目录 `WorkbenchClassroom`。首次启动初始化本机数据库和 Temporal；Windows 如系统缺少 Microsoft Visual C++ x64 运行库，先安装官方运行库。

新建自动化应答项目后点「开始练习」，可先用通用客服专家输入「你好」。如果要练习检索和追问，在「应答设置」点「加载课堂示例」并保存。可输入「蓝松方案多久可以取消？」，再输入「那改期窗口呢？」。示例是可编辑的虚构资料。重复加载保留已经编辑的内容。
