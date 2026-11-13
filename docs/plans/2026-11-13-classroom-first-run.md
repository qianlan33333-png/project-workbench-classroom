# 课堂版首次使用

```mermaid
flowchart LR
  A[解压并启动课堂工作台] --> B{本机已激活}
  B -->|否| C[输入发码方给的激活码]
  C -->|校验成功| D[进入工作台]
  C -->|无效或签发服务不可用| C
  B -->|是| D
  D --> E[打开应答项目并开始练习]
  E --> F[配置模型，可选加载课堂示例]
  F --> G[打招呼、问语料、在同一会话追问]
  G -->|本轮未完成| F
  G -->|修改专家或语料| H[开新练习]
```

参考 [Ollama 本地运行方式](https://github.com/ollama/ollama/blob/main/docs/api/introduction.mdx)、[Temporal CLI 桌面平台](https://github.com/temporalio/cli/blob/main/README.md) 和 [MySQL 官方压缩发行包](https://dev.mysql.com/doc/refman/8.4/en/windows-choosing-package.html)。课堂下载包用本机进程运行现有组件，再打开原生网页。Mac 和 Windows 分别打包。

空项目可以用内置的「通用客服专家」打招呼。「加载课堂示例」把可编辑专家和虚构语料放到当前项目，允许直接验证检索、追问和修改。已有配置保留，学员决定何时切换专家或语料。设置面板复用现有 UI，使用原生浅色和深色主题变量。

课堂启动器固定启用现有激活门，随包只提供验签公钥。开源自部署和正式运行仍使用各自的现有配置。模型密钥由学员填写。执行失败按练习失败显示并允许同会话继续，明确请求转人工仍保留原行为。
