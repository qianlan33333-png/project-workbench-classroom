# 已安装证据组件合同

1. qa_search({question,queries,limit:18})。question 始终保留宿主本轮完整客户原文及标点；queries 可结合前文补足指代、拆分问题。保存 search_receipt，不能复用其他问题的收据。
2. qa_read({ids:[来源ID],records:false}) 读取来源上下文。来源引用具体消息编号时可 records:true 回查。保存 context_receipt。相邻回复、摘要和相似度不是答案证明。
3. 按客户真正的问题选择 requested_scope，按答案选择 answer_kind。stable_fact/general_method 可由适用的明确原文支持；historical_fact 必须保留历史限定。current_policy 必须使用已登记有效期和适用范围的维护文档，不能用旧个案证明当前通用规则。缺少可信实时工具时，live_personal/external_action 转人工。
4. 把完整答案中的业务事实写入 claims，每条给出真实来源和连续原文引用。先核对完整草稿中的事实、隐含承诺、个人经历和提问前提，再调用 qa_check。自然承接不需冒充知识引用；“你上次说过”必须来自本 Session 的用户消息或可信档案。不能用客户自己的假设证明业务事实。

输入样式（使用实际值替换说明文字）：

```json
{"request":{"question":"本轮完整原文","request_id":"本轮稳定编号","attachments":[],"search_receipts":["本轮检索收据"],"context_receipts":["原文读取收据"],"coverage":[{"text":"完整问题的连续片段","status":"covered","requested_scope":"stable_fact","answer_kind":"stable_fact","claims":[{"text":"准备输出的事实句，含全部适用条件。","sources":[{"id":"真实来源ID","quote":"工具返回原文的连续引用"}]}]}],"draft":"准备输出的事实句，含全部适用条件。\n你目前是在尝试这件事吗？"}}
```

coverage 按顺序覆盖完整问题。claims.text 是草稿中业务事实的原样句子。draft 是可选的完整自然回复，必须逐字保留已核验的事实句及条件，不加 Markdown。程序检查来源、版本、引用和已核验句子是否保留，不能证明额外文字的语义正确；同一位回答专家负责检查所有补充表达，不增加另一个审稿 Agent。

- answer/ack：发送返回的 reply，保持原样；想修改先重新检查。
- needs_retrieval/needs_interpretation：按原因补资料或修正格式。收据与 question 不一致时替换错误收据，不能改写 question 迁就收据。修正最多两次，每个意图检索扩展最多三轮。
- 确实无依据：coverage 标记 unknown/ambiguous/needs_live_state，交原 gate 处理，再按 human 转人工。明确人工请求直接 handoff，不必检索。
- 技术失败：有限重试仍失败后转人工，记录具体原因，不把检索错误当成“知识库没有答案”。

不要输出他人姓名、联系方式、订单、私聊全文、内部路径或来源编号。不要从其他客户退款个案推导普遍退款承诺。公开知识缺失不等于必须中断所有非业务交流。维护、采集和 CRM 写入由既有后台任务负责，本专家不启动它们。
