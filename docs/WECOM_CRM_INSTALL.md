# 工作台与 CRM 配对安装

安装方从部署清单取得两端的 HTTPS base（含实际前缀）、已启用的工作台绑定和验证过的企业/托管员工身份。示例文件只使用占位值。

```json
[{"id":"support","label":"售后服务","workbenchBase":"https://workbench.example.com","crmBase":"https://crm.example.com","connectionId":"support-gateway","bindingId":"existing-binding-id","bindingVersion":1,"corpId":"example-corp","staffUserId":"example-staff","keyId":"support-install"}]
```

在私有目录生成成对文件。每个绑定的随机密钥保存在安装方的独立密钥目录，后续安装复用同名密钥。每次输出到全新目录。增加项目时将其加入同一清单，保留已有条目。

```sh
python3 scripts/render-auto-reply-install.py private/inventory.json private/bundle-1 \
  --keys-dir private/installation-keys --workbench-install-dir /etc/workbench/auto-reply
```

将 bundle 的 workbench 目录安装到指定工作台宿主路径，供服务的 WORKBENCH_AR_GRANTS_FILE 指向其中的 grants.json。将 crm/install.json 安装为 CRM 服务可读的私有文件，设置 AICRM_AUTO_REPLY_INSTALL_FILE。目录权限 0700，密钥与配置文件权限 0600，凭证留在对应服务端。已有会话需要补齐官方身份时，安装方可根据网关已验证的消息证据预置 contactMappings，将网关 contactId 对应到官方 externalUserId。

CRM 管理后台读出两个地址。打开 CRM 集成并保存后，侧边栏可查询受控会话或提交恢复。后台通过带签名的配置请求与回调探测确认地址变更。跨 origin 更换部署时在安装清单更新信任范围并重新配对。客户可见的开关状态以工作台的查询或恢复回执为准。
