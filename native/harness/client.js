window.__ModuleLoader__.load({
  id: "@local/personal-workbench",
  factory(require) {
    const React = require("react"),
      h = React.createElement;
    const { useState, useEffect, useRef } = React;
    const {IconClockOutlineRegular, IconDatabaseOutlineRegular}=require("@deepseek-ai/dsh-client-ui-primitives");
    let resourceProject="";
    const css = `.wb-page{height:100%;overflow:auto;background:var(--dsw-alias-bg-base);color:var(--dsw-alias-label-primary);font-size:14px;line-height:1.6}.wb-content{max-width:960px;margin:auto;padding:28px clamp(20px,4vw,48px) 48px}.wb-heading{display:flex;align-items:center;justify-content:space-between;gap:16px;flex-wrap:wrap;margin-bottom:24px}.wb-heading h1{font-size:20px;font-weight:500;margin:0}.wb-page button,.wb-inline{font:inherit;color:inherit;background:var(--dsw-alias-bg-base);border:1px solid var(--dsw-alias-border-l3);border-radius:8px;padding:6px 12px;cursor:pointer}.wb-short-label{display:none}@media(max-width:600px){.wb-full-label{display:none}.wb-short-label{display:inline}.wb-inline{font-size:12px;padding:4px 6px}}.wb-page button:disabled{opacity:.5;cursor:wait}.wb-page textarea{font:inherit;width:100%;box-sizing:border-box;min-height:88px;color:inherit;background:var(--dsw-alias-bg-base);border:1px solid var(--dsw-alias-border-l3);border-radius:6px;padding:10px}.wb-page input,.wb-page select{font:inherit;color:inherit;background:var(--dsw-alias-bg-base);border:1px solid var(--dsw-alias-border-l3);border-radius:6px;padding:7px 10px;min-width:0;max-width:100%;box-sizing:border-box}.wb-muted{color:var(--dsw-alias-label-secondary)}.wb-list{border-top:1px solid var(--dsw-alias-border-l3)}.wb-row{display:flex;gap:16px;align-items:center;padding:18px 0;border-bottom:1px solid var(--dsw-alias-border-l3);flex-wrap:wrap}.wb-row-main{flex:1;min-width:180px}.wb-row strong{font-weight:500}.wb-tag{font-size:12px;padding:2px 8px;border:1px solid var(--dsw-alias-border-l3);border-radius:6px}.wb-form{padding:20px 0;display:grid;gap:16px}.wb-field{display:grid;gap:6px;min-width:0}.wb-columns{display:grid;grid-template-columns:1fr 1fr;gap:16px}.wb-actions{display:flex;gap:8px;flex-wrap:wrap;align-items:flex-start}.wb-form>h2{font-size:16px;font-weight:500;margin:0}.wb-note{padding:12px 0;overflow-wrap:anywhere}.wb-check{display:flex;gap:8px;align-items:center;padding:6px 0}.wb-section{border:1px solid var(--dsw-alias-border-l3);border-radius:10px;padding:16px;margin-top:12px}.wb-section h2{font-size:16px;font-weight:500;margin:0 0 8px}@media(max-width:600px){.wb-columns{grid-template-columns:1fr}.wb-heading{align-items:flex-start}.wb-row-main{min-width:140px}}`;
    async function api(path, data) {
      const r = await fetch("/workbench-api" + path, {
        method: data === undefined ? "GET" : "POST",
        headers: { "content-type": "application/json" },
        body: data === undefined ? undefined : JSON.stringify(data),
      });
      const text = await r.text();
      let v;try{v=text?JSON.parse(text):{}}catch{v={error:text}}
      if (!r.ok) throw Object.assign(Error(v.error || "操作失败"),{status:r.status});
      return v;
    }
    const responsiveLabel=(full,short)=>h(React.Fragment,null,h('span',{className:'wb-full-label'},full),h('span',{className:'wb-short-label','aria-hidden':true},short));
    const field = (label, node) =>
      h("label", { className: "wb-field" }, h("span", null, label), React.cloneElement(node,{"aria-label":node.props["aria-label"]||label}));
    const button = (label, onClick, props = {}) =>
      h("button", { type: "button", onClick, ...props }, label);
    const emptyResource = () => ({
      id: "r_" + crypto.randomUUID().replaceAll("-", "").slice(0, 16),
      name: "",
      ownerProject: "",
      connectionId: "",
      relation: "",
      keyField: "id",
      revision: 0,
      sections: [{ id: "main", name: "资料", fields: [] }],
    });
    function ResourcePage() {
      const [projectFilter,setProjectFilter]=useState(resourceProject);
      useEffect(()=>{const listener=()=>setProjectFilter(resourceProject);window.addEventListener("wb-resource-project",listener);return()=>window.removeEventListener("wb-resource-project",listener)},[]);
      const [catalog, setCatalog] = useState(),
        [connections, setConnections] = useState([]),
        [grants, setGrants] = useState([]),
        [error, setError] = useState(""),
        [notice, setNotice] = useState(""),
        [busy, setBusy] = useState(false),
        [edit, setEdit] = useState(),
        [connection, setConnection] = useState(),
        [sharing, setSharing] = useState(),
        [selected, setSelected] = useState({}),[preview,setPreview]=useState();
      const load = async () => {
        try {
          const [c, n, g] = await Promise.all([
            api("/catalog"),
            api("/connections"),
            api("/grants"),
          ]);
          setError("");
          setCatalog(c);
          setConnections(n);
          setGrants(g);
        } catch (e) {
          setError(e.message);
        }
      };
      useEffect(() => {
        load();
      }, []);
      const act = async (fn) => {
        setBusy(true);
        setError("");
        setNotice("");
        try {
          await fn();
          await load();
          setNotice("已保存");
        } catch (e) {
          setError(e.message);
        } finally {
          setBusy(false);
        }
      };
      const openPreview=async(r,section=r.sections[0]?.id,after="")=>{setError("");try{const query=new URLSearchParams({resourceId:r.id,version:r.currentVersion,section,after});const page=await api("/resource-page?"+query);setPreview({resource:r,section,page})}catch(e){setError(e.message)}};
      const change = (key, value) => setEdit({ ...edit, [key]: value });
      const startShare = (r) => {
        const map = {};
        for (const p of catalog.projects) {
          const names = grants
            .filter((g) => g.resourceId === r.id && g.projectId === p.id)
            .flatMap((g) => g.sections);
          map[p.id] = r.sections
            .filter((s) => names.includes("*") || names.includes(s.id))
            .map((s) => s.id);
        }
        setSelected(map);
        setSharing(r);
      };
      const rows = (catalog?.resources ?? []).filter(
        (r) =>
          !projectFilter ||
          r.ownerProject === projectFilter ||
          r.maintainerProject === projectFilter ||
          grants.some(
            (g) => g.resourceId === r.id && g.projectId === projectFilter,
          ),
      );
      return h(
        "section",
        { className: "wb-page" },
        h("style", null, css),
        h(
          "div",
          { className: "wb-content" },
          h(
            "div",
            { className: "wb-heading" },
            h("h1", null, projectFilter ? "引用资源" : "通用资源库"),
            h("select",{"aria-label":"按项目查看",value:projectFilter,onChange:e=>{resourceProject=e.target.value;setProjectFilter(resourceProject)}},h("option",{value:""},"全部项目"),...(catalog?.projects??[]).map(p=>h("option",{key:p.id,value:p.id},p.name))),
            h(
              "div",
              { className: "wb-actions" },
              button("连接配置", () =>
                setConnection({
                  id: "c_" + crypto.randomUUID().slice(0, 8),
                  name: "",
                  kind: "mysql",
                  secretEnv: "WORKBENCH_SOURCE_",
                  location: "",
                }),
              ),
              button("注册资源", () => setEdit(emptyResource()), {disabled:!catalog||busy}),
            ),
          ),
          h(
            "p",
            { className: "wb-muted" },
            "资源注册一次，按项目勾选引用。共享资料只读；“今日”按需读取全部业务资料。",
          ),
          error && h("p", { role: "alert", className: "wb-note" }, error),
          notice && h("p", { role: "status", className: "wb-note" }, notice),
          !catalog && h("p", { role: "status" }, "正在读取资源…"),
          connection &&
            h(
              "form",
              {
                className: "wb-form",
                onSubmit: (e) => {
                  e.preventDefault();
                  act(async () => {
                    await api("/connections", connection);
                    setConnection(undefined);
                  });
                },
              },
              h("h2", null, "连接配置"),
              field(
                "名称",
                h("input", {
                  required: true,
                  value: connection.name,
                  onChange: (e) =>
                    setConnection({ ...connection, name: e.target.value }),
                }),
              ),
              field(
                "类型",
                h(
                  "select",
                  {
                    value: connection.kind,
                    onChange: (e) =>
                      setConnection({ ...connection, kind: e.target.value }),
                  },
                  h("option", { value: "mysql" }, "MySQL"),
                  h("option", { value: "http-json" }, "HTTPS 数据快照（JSON）"),
                  h("option", { value: "jsonl" }, "知识文件（JSONL）"),
                  h("option", { value: "directory" }, "知识目录（Markdown / TXT）"),
                ),
              ),
              connection.kind === "mysql"
                ? field(
                    "服务器凭证引用",
                    h("input", {
                      required: true,
                      value: connection.secretEnv,
                      onChange: (e) =>
                        setConnection({
                          ...connection,
                          secretEnv: e.target.value,
                        }),
                    }),
                  )
                : field(
                    connection.kind === "http-json" ? "快照地址（HTTPS）" : "知识文件目录",
                    h("input", {
                      required: true,
                      value: connection.location,
                      onChange: (e) =>
                        setConnection({
                          ...connection,
                          location: e.target.value,
                        }),
                    }),
                  ),
              connection.kind === "http-json" && field("服务器凭证引用（可选）",h("input",{value:connection.secretEnv||"",onChange:e=>setConnection({...connection,secretEnv:e.target.value})})),
              h(
                "p",
                { className: "wb-muted" },
                "凭证保存在服务器中。这里只填写引用名称，专家不会取得密码。",
              ),
              h(
                "div",
                { className: "wb-actions" },
                h("button", { disabled: busy, type: "submit" }, "保存连接"),
                button("取消", () => setConnection(undefined)),
              ),
            ),
          edit &&
            h(
              "form",
              {
                className: "wb-form",
                onSubmit: (e) => {
                  e.preventDefault();
                  act(async () => {
                    const {schemaText,checksText,sourceFieldsText,...resource}=edit;
                    await api("/resources", {
                      ...resource,
                      jsonSource:connections.find(c=>c.id===edit.connectionId)?.kind==="http-json" ? {...edit.jsonSource,fields:sourceFieldsText===undefined?edit.jsonSource?.fields:JSON.parse(sourceFieldsText||"{}")} : undefined,
                      recordSchema:schemaText===undefined?edit.recordSchema:JSON.parse(schemaText||"{}"),
                      checks:checksText===undefined?edit.checks:JSON.parse(checksText||"[]"),
                      sections: edit.sections.map(({selectText,...s}) => ({
                        ...s,
                        fields: s.fields.map((f) => f.trim()).filter(Boolean),
                        select:selectText===undefined?s.select:JSON.parse(selectText||"{}"),
                      })),
                    });
                    setEdit(undefined);
                  });
                },
              },
              h("h2", null, edit.revision ? "编辑资源" : "注册资源"),
              h(
                "div",
                { className: "wb-columns" },
                field(
                  "资源名称",
                  h("input", {
                    required: true,
                    value: edit.name,
                    onChange: (e) => change("name", e.target.value),
                  }),
                ),
                field(
                  "所属项目",
                  h(
                    "select",
                    {
                      value: edit.ownerProject,
                      onChange: (e) => change("ownerProject", e.target.value),
                    },
                    h("option", { value: "" }, "独立共享资源"),
                    ...(catalog?.projects ?? []).map((p) =>
                      h("option", { key: p.id, value: p.id }, p.name),
                    ),
                  ),
                ),
              ),
              h(
                "div",
                { className: "wb-columns" },
                field(
                  "来源连接",
                  h(
                    "select",
                    {
                      value: edit.connectionId,
                      onChange: (e) => change("connectionId", e.target.value),
                    },
                    h("option", { value: "" }, "生成产物（无需来源连接）"),
                    ...connections.map((c) =>
                      h("option", { key: c.id, value: c.id }, c.name),
                    ),
                  ),
                ),
                connections.find(c=>c.id===edit.connectionId)?.kind!=="http-json" && field(
                  "表 / 视图 / 文件名称",
                  h("input", {
                    value: edit.relation,
                    onChange: (e) => change("relation", e.target.value),
                  }),
                ),
              ),
              connections.find(c=>c.id===edit.connectionId)?.kind!=="http-json" && field(
                "记录唯一编号字段",
                h("input", {
                  value: edit.keyField,
                  onChange: (e) => change("keyField", e.target.value),
                }),
              ),
              connections.find(c=>c.id===edit.connectionId)?.kind==="http-json" && h("section",{className:"wb-section"},h("h2",null,"快照记录映射"),
                field("记录数组字段（顶层数组则留空）",h("input",{value:edit.jsonSource?.arrayField||"",onChange:e=>change("jsonSource",{...edit.jsonSource,arrayField:e.target.value})})),
                field("唯一编号路径（JSON Pointer）",h("input",{value:edit.jsonSource?.keyPointer||"",placeholder:"/id",onChange:e=>change("jsonSource",{...edit.jsonSource,keyPointer:e.target.value})})),
                field("保存字段与来源路径",h("textarea",{value:edit.sourceFieldsText??JSON.stringify(edit.jsonSource?.fields||{},null,2),onChange:e=>change("sourceFieldsText",e.target.value)})),
                h("p",{className:"wb-muted"},"例如 {\"name\":\"/display/name\"}。资料板块从这些保存字段中选择；空路径表示保留整条原文。")),
              field("维护项目",h("select",{value:edit.maintainerProject||"",onChange:e=>change("maintainerProject",e.target.value)},h("option",{value:""},"跟随所属项目 / 本人维护"),...(catalog?.projects??[]).map(p=>h("option",{key:p.id,value:p.id},p.name)))),
              h("details",null,h("summary",null,"使用条件（可选）"),h("p",{className:"wb-muted"},"登记可供专家选择的固定校验条件。发送时，资格和收件对象从此资料中核对；专家不能在调用时放宽条件。"),field("条件定义",h("textarea",{rows:6,value:edit.checksText??JSON.stringify(edit.checks||[],null,2),onChange:e=>change("checksText",e.target.value)}))),
              h("details",null,h("summary",null,"发布校验（可选）"),field("最少记录数",h("input",{type:"number",min:0,value:edit.minRecords||0,onChange:e=>change("minRecords",Number(e.target.value))})),field("至少保留上一版数量的百分比",h("input",{type:"number",min:0,max:100,value:edit.minRetainedPercent||0,onChange:e=>change("minRetainedPercent",Number(e.target.value))})),field("来源发布版本字段",h("input",{value:edit.watermarkField||"",onChange:e=>change("watermarkField",e.target.value)})),field("记录结构（JSON Schema）",h("textarea",{value:edit.schemaText??JSON.stringify(edit.recordSchema||{},null,2),onChange:e=>change("schemaText",e.target.value)}))),
              ...edit.sections.map((s, i) =>
                h(
                  "div",
                  { className: "wb-section", key: s.id },
                  h(
                    "div",
                    { className: "wb-columns" },
                    field(
                      "板块名称",
                      h("input", {
                        required: true,
                        value: s.name,
                        onChange: (e) =>
                          change(
                            "sections",
                            edit.sections.map((v, j) =>
                              j === i ? { ...v, name: e.target.value } : v,
                            ),
                          ),
                      }),
                    ),
                    field(
                      "包含字段（逗号分隔）",
                      h("input", {
                        required: true,
                        value: s.fields.join(", "),
                        onChange: (e) =>
                          change(
                            "sections",
                            edit.sections.map((v, j) =>
                              j === i
                                ? {
                                    ...v,
                                    fields: e.target.value
                                      .split(",")
                                      .map((t) => t.trim()),
                                  }
                                : v,
                            ),
                          ),
                      }),
                    ),
                  ),
                  h("details",null,h("summary",null,"资料切片（可选）"),field("输出字段与 JSON Pointer",h("textarea",{value:s.selectText??JSON.stringify(s.select||{},null,2),onChange:e=>change("sections",edit.sections.map((v,j)=>j===i?{...v,selectText:e.target.value}:v))})),h("p",{className:"wb-muted"},"只从本板块包含的字段选择子项。留空时保留全部已选字段；缺少的资料返回空值。")),
                  edit.sections.length > 1 &&
                    button("移除板块", () =>
                      change(
                        "sections",
                        edit.sections.filter((_, j) => j !== i),
                      ),
                    ),
                ),
              ),
              h(
                "div",
                { className: "wb-actions" },
                button("增加资料板块", () =>
                  change("sections", [
                    ...edit.sections,
                    {
                      id: "s_" + crypto.randomUUID().slice(0, 8),
                      name: "",
                      fields: [],
                    },
                  ]),
                ),
                h("button", { type: "submit", disabled: busy }, "保存资源"),
                button("取消", () => setEdit(undefined)),
              ),
            ),
          sharing &&
            h(
              "form",
              {
                className: "wb-form",
                onSubmit: (e) => {
                  e.preventDefault();
                  act(async () => {
                    for (const p of catalog.projects.filter(p=>p.id!==sharing.ownerProject&&p.id!==sharing.maintainerProject))
                      await api("/grants", {
                        resourceId: sharing.id,
                        projectId: p.id,
                        sections: selected[p.id] || [],
                      });
                    setSharing(undefined);
                  });
                },
              },
              h("h2", null, "允许哪些项目读取「" + sharing.name + "」"),
              h("p", {className:"wb-muted"}, "所属项目和维护项目始终可使用该资源；这里仅设置其他项目的只读范围。调整维护方请编辑资源。"),
              ...catalog.projects
                .filter((p) => p.id !== sharing.ownerProject && p.id !== sharing.maintainerProject)
                .map((p) =>
                  h(
                    "fieldset",
                    { key: p.id, className: "wb-section" },
                    h("legend", null, p.name),
                    ...sharing.sections.map((s) =>
                      h(
                        "label",
                        { key: s.id, className: "wb-check" },
                        h("input", {
                          type: "checkbox",
                          checked: (selected[p.id] || []).includes(s.id),
                          onChange: (e) =>
                            setSelected({
                              ...selected,
                              [p.id]: e.target.checked
                                ? [...(selected[p.id] || []), s.id]
                                : (selected[p.id] || []).filter(
                                    (id) => id !== s.id,
                                  ),
                            }),
                        }),
                        s.name,
                      ),
                    ),
                  ),
                ),
              h(
                "div",
                { className: "wb-actions" },
                h("button", { type: "submit", disabled: busy }, "保存引用范围"),
                button("取消", () => setSharing(undefined)),
              ),
            ),
          preview&&h("section",{className:"wb-section"},h("div",{className:"wb-heading"},h("h2",null,preview.resource.name),button("关闭预览",()=>setPreview(undefined))),
            h("div",{className:"wb-actions"},field("资料板块",h("select",{value:preview.section,onChange:e=>openPreview(preview.resource,e.target.value)},...preview.resource.sections.map(s=>h("option",{key:s.id,value:s.id},s.name||s.id)))),
              h("a",{className:"wb-inline",href:"/workbench-api/resource-export?"+new URLSearchParams({resourceId:preview.resource.id,version:preview.page.ref.version,section:preview.section}),download:true},"下载此板块")),
            h("p",{className:"wb-muted"},`共 ${preview.page.count} 条 · ${time(preview.page.publishedAt)} 发布${preview.page.stale?" · 来源已更新，此结果需要重新生成":""}`),
            ...preview.page.records.map(record=>h("details",{key:record.id},h("summary",null,(["string","number"].includes(typeof record.fields.name) ? record.fields.name : record.id)),h("pre",{style:{whiteSpace:"pre-wrap",overflowWrap:"anywhere",fontSize:13}},JSON.stringify(record.fields,null,2)))),
            preview.page.next&&button("再看 10 条",()=>openPreview(preview.resource,preview.section,preview.page.next))),
          h(
            "div",
            { className: "wb-list" },
            ...rows.map((r) =>
              h(
                "div",
                { className: "wb-row", key: r.id },
                h(
                  "div",
                  { className: "wb-row-main" },
                  h("strong", null, r.name),
                  h(
                    "div",
                    { className: "wb-muted" },
                    r.sections.map((s) => s.name).join(" · "),
                  ),
                ),
                h(
                  "span",
                  { className: "wb-tag" },
                  r.currentVersion ? "已发布" : "尚未发布",
                ),
                r.currentVersion&&button("查看资料",()=>openPreview(r)),
                button("设置引用", () => startShare(r)),
                button("编辑", () => setEdit(r)),
              ),
            ),
            catalog &&
              rows.length === 0 &&
              h(
                "p",
                { className: "wb-muted" },
                "还没有资源。先配置连接，再注册需要使用的资料。",
              ),
          ),
        ),
      );
    }
    const time = value => value ? new Date(value).toLocaleString("zh-CN",{timeZone:"Asia/Shanghai",month:"2-digit",day:"2-digit",hour:"2-digit",minute:"2-digit"}) : "尚未记录";
    const stateText = r => r.stats?.failed ? `已保留 ${r.stats.succeeded} 条 · ${r.stats.failed} 条失败` : ({Running:"执行中",Completed:r.published?"结果已发布":"执行已结束",Failed:"执行失败",Canceled:"已停止",Terminated:"已终止",TimedOut:"已超时",Unknown:"状态待核实"}[r.execution]||r.execution);
    const receiptText = r => {const s=r.deliveries?.states;if(!s)return "";return `渠道回执：待提交 ${s.prepared||0} · 已受理 ${s.accepted||0} · 待手机确认 ${s.pending_confirmation||0} · 已发送 ${s.sent||0} · 已送达 ${s.delivered||0} · 失败 ${s.failed||0} · 待核实 ${(s.unknown||0)+(s.submitting||0)}`};
    function TodayPage({ openToday, openSession }) {
      const [data,setData]=useState(),[error,setError]=useState(""),[notice,setNotice]=useState(""),[busy,setBusy]=useState(false),[edit,setEdit]=useState(),[answers,setAnswers]=useState({}),[settings,setSettings]=useState(),[notifications,setNotifications]=useState();
      const load=async()=>{const [runs,decisions,memory,config,notify,targets,schedules]=await Promise.all([api("/runs"),api("/decisions"),api("/memory"),api("/personal/settings"),api("/notifications"),api("/notification-targets"),api("/schedules")]);setError("");setData({runs,decisions,memory,config,targets,schedules});setSettings(v=>v||config);setNotifications(v=>v||{...notify,sessionId:config.sessionId,targets:notify.targets||[]})};
      useEffect(()=>{let live=true;const refresh=()=>load().catch(e=>{if(live)setError(e.message)});refresh();const id=setInterval(refresh,15000);return()=>{live=false;clearInterval(id)}},[]);
      const act=async fn=>{setBusy(true);setError("");setNotice("");try{await fn();await load();setNotice("已保存")}catch(e){setError(e.message)}finally{setBusy(false)}};
      return h("section",{className:"wb-page"},h("style",null,css),h("div",{className:"wb-content"},
        h("div",{className:"wb-heading"},h("h1",null,"今日"),button("进入今日对话",()=>openToday().catch(e=>setError(e.message)))),
        h("p",{className:"wb-muted"},"查看项目进展、安排工作，继续跟进等待中的事情。"),
        error&&h("p",{role:"alert"},error),notice&&h("p",{role:"status"},notice),!data&&h("p",{role:"status"},"正在核对任务与待决定事项…"),
        data&&h("div",{className:"wb-section"},h("h2",null,`等待你决定 · ${data.decisions.length}`),
          data.decisions.length===0?h("p",{className:"wb-muted"},"暂无待决定事项。"):
          data.decisions.map(d=>h("form",{key:d.id,className:"wb-form",onSubmit:e=>{e.preventDefault();act(async()=>{await api("/decisions/answer",{...d,answer:answers[d.id]});setAnswers(v=>({...v,[d.id]:""}))})}},
            h("p",{className:"wb-note",style:{whiteSpace:"pre-wrap"}},d.question),field("你的决定",h("textarea",{required:true,value:answers[d.id]||"",onChange:e=>setAnswers({...answers,[d.id]:e.target.value})})),h("div",{className:"wb-actions"},h("button",{type:"submit",disabled:busy},"确认并继续原事项"))))),
        data&&h("div",{className:"wb-section"},h("h2",null,"近期工作"),h("p",{className:"wb-muted"},"执行状态直接核对后台任务；发送与送达以渠道回执为准。"),data.runs.length===0&&h("p",null,"暂无任务。"),
          data.runs.map(r=>h("div",{key:r.id,className:"wb-row"},h("div",{className:"wb-row-main"},h("strong",null,r.title),h("div",{className:"wb-muted"},`${time(r.createdAt)} 开始${r.closedAt?" · "+time(r.closedAt)+" 结束":""}`),h("div",null,stateText(r)),r.stats?.total>0&&h("div",{className:"wb-muted"},`${r.stats.total} 条 · 成功 ${r.stats.succeeded} · 失败 ${r.stats.failed} · 待处理 ${r.stats.pending}`),r.deliveries&&h("div",{className:"wb-note"},receiptText(r))),button("查看对话",()=>openSession(r.sessionId))))),
        data&&h("details",{className:"wb-section"},h("summary",null,`个人规则与记忆 · ${data.memory.length}`),
          h("p",{className:"wb-muted"},"可以纠正或忘记；推断与本人确认分别保存。"),button("添加个人规则",()=>setEdit({id:"memory_"+crypto.randomUUID(),revision:0,text:"",source:"本人在今日设置中填写",confirmed:true,forgotten:false})),
          data.memory.map(m=>h("div",{key:m.id,className:"wb-row"},h("div",{className:"wb-row-main"},h("p",null,m.text),h("div",{className:"wb-muted"},`${m.confirmed?"本人确认":"待确认"} · ${m.source}`)),button("纠正",()=>setEdit({...m})),button("忘记",()=>act(()=>api("/memory",{...m,forgotten:true})),{disabled:busy}))),
          edit&&h("form",{className:"wb-form",onSubmit:e=>{e.preventDefault();act(async()=>{await api("/memory",{...edit,confirmed:true});setEdit(undefined)})}},field("记忆内容",h("textarea",{required:true,value:edit.text,onChange:e=>setEdit({...edit,text:e.target.value})})),field("来源",h("input",{required:true,value:edit.source,onChange:e=>setEdit({...edit,source:e.target.value})})),h("div",{className:"wb-actions"},h("button",{type:"submit",disabled:busy},"确认保存"),button("取消",()=>setEdit(undefined))))),
        data&&h("details",{className:"wb-section"},h("summary",null,`定时工作 · ${data.schedules.length}`),
          h("p",{className:"wb-muted"},"在项目对话中说明资料、方法和执行时间即可配置；每次结果回到原对话，上一轮未结束时跳过重叠执行。"),
          data.schedules.map(s=>h("div",{key:s.id,className:"wb-row"},h("div",{className:"wb-row-main"},h("strong",null,s.title),h("div",{className:"wb-muted"},s.hours.map(hour=>String(hour).padStart(2,"0")+":"+String(s.minute).padStart(2,"0")).join("、")+" · 北京时间"),h("div",null,s.state!=="ready"?"定时状态待核实":s.paused?"已暂停":`下次 ${time(s.next?.[0])}`)),button("查看对话",()=>openSession(s.sessionId)),button(s.paused?"恢复":"暂停",()=>act(()=>api("/schedules/pause",{id:s.id,paused:!s.paused})),{disabled:busy||s.state!=="ready"})))),
        notifications&&data&&h("details",{className:"wb-section"},h("summary",null,"本人消息通知"),
 h("p",{className:"wb-muted"},"只通知有实质结果、异常和需要你决定的事项；不会因此重新执行原任务。"),
 data.targets.length===0?h("p",null,"尚未连接本人微信或飞书。完成绑定后，可在这里选择通知渠道。"):
 h("form",{className:"wb-form",onSubmit:e=>{e.preventDefault();act(()=>api("/notifications",notifications))}},
 h("label",{className:"wb-check"},h("input",{type:"checkbox",checked:notifications.enabled,onChange:e=>setNotifications({...notifications,enabled:e.target.checked})}),"开启本人通知"),
 ...data.targets.map(t=>h("label",{key:t.connectionId,className:"wb-check"},h("input",{type:"checkbox",checked:notifications.targets.some(n=>n.connectionId===t.connectionId),onChange:e=>setNotifications({...notifications,targets:e.target.checked?[...notifications.targets,{connectionId:t.connectionId,target:t.target}]:notifications.targets.filter(n=>n.connectionId!==t.connectionId)})}),t.name)),
 h("button",{type:"submit",disabled:busy},"保存通知设置"))),
 settings&&h("details",{className:"wb-section"},h("summary",null,"每日安排时间"),h("form",{className:"wb-form",onSubmit:e=>{e.preventDefault();act(()=>api("/personal/settings",settings))}},
          field("北京时间",h("input",{type:"time",required:true,value:String(settings.hour).padStart(2,"0")+":"+String(settings.minute).padStart(2,"0"),onChange:e=>{const [hour,minute]=e.target.value.split(":").map(Number);setSettings({...settings,hour,minute})}})),
          h("label",{className:"wb-check"},h("input",{type:"checkbox",checked:settings.paused,onChange:e=>setSettings({...settings,paused:e.target.checked})}),"暂停每日汇总"),h("button",{type:"submit",disabled:busy},"保存安排")))
      ));
    }

    let replyProject="";
    let ctxReplyBack=()=>{};
    let replyWorkspaceIds=new Set();
    const sessionViews=new WeakMap(),workspaceViews=new WeakMap();
    const visibleSession=id=>!String(id).startsWith('object-');
    function filteredSessions(state){
      if(sessionViews.has(state))return sessionViews.get(state);
      const byId=Object.fromEntries(Object.entries(state.byId||{}).filter(([id])=>visibleSession(id)));
      const v={...state,ids:(state.ids||[]).filter(visibleSession),byId};sessionViews.set(state,v);return v;
    }
    function filteredWorkspaces(state){
      const previous=workspaceViews.get(state);if(previous?.generation===replyWorkspaceIds)return previous.value;
      const value={...state,items:(state.items||[]).filter(w=>!replyWorkspaceIds.has(w.workspaceId)).map(w=>({...w,sessionIds:(w.sessionIds||[]).filter(visibleSession)})),archivedSessionIds:(state.archivedSessionIds||[]).filter(visibleSession),pinnedSessionIds:(state.pinnedSessionIds||[]).filter(visibleSession)};
      workspaceViews.set(state,{generation:replyWorkspaceIds,value});return value;
    }
    async function replyProjects(){
      const [projects,catalog]=await Promise.all([api('/reply-projects'),api('/catalog')]);
      replyWorkspaceIds=new Set(catalog.projects.filter(p=>projects.some(c=>c.projectId===p.id)).map(p=>p.workspaceId));
      window.dispatchEvent(new Event('wb-reply-projects'));return{projects:projects.filter(c=>catalog.projects.some(p=>p.id===c.projectId)),catalog};
    }
    const replyLabels={received:'正在处理',human:'人工回复',paused:'人工接管',handoff:'已转人工',ignored:'已忽略',reply_queued:'待发送',reply_submitting:'正在发送',prepared:'待发送',submitting:'发送结果待核实',accepted:'网关已受理',pending_confirmation:'待手机确认',sent:'已发送',delivered:'已送达',failed:'发送失败',unknown:'结果待核实'};
    const replyCSS=`.ar-root{height:100%;min-height:0;display:flex;flex-direction:column;color:var(--dsw-alias-label-primary);background:var(--dsw-alias-bg-base);font:14px/1.6 system-ui,-apple-system,"PingFang SC",sans-serif}.ar-top{min-height:64px;display:flex;align-items:center;justify-content:space-between;padding:0 24px;border-bottom:1px solid var(--dsw-alias-border-l3)}.ar-top h1{font-size:20px;margin:0;font-weight:600}.ar-layout{position:relative;display:grid;grid-template-columns:300px minmax(0,1fr);min-height:0;flex:1}.ar-layout[data-settings=true]{grid-template-columns:300px minmax(0,1fr)450px}.ar-list{border-right:1px solid var(--dsw-alias-border-l3);min-height:0;display:flex;flex-direction:column}.ar-search{margin:18px 14px 12px;position:relative}.ar-search input{width:100%;padding:11px 12px;border:1px solid var(--dsw-alias-border-l3);border-radius:8px;background:transparent;color:inherit;box-sizing:border-box;font:inherit}.ar-contacts{overflow:auto;flex:1;padding:0 8px}.ar-contact{width:100%;display:flex;text-align:left;gap:12px;align-items:center;padding:16px 12px;background:transparent;border:0;border-bottom:1px solid var(--dsw-alias-border-l3);font:inherit;color:inherit;cursor:pointer}.ar-contact[aria-current=true]{background:var(--dsw-alias-primary-soft,#edf3ff);border-radius:8px}.ar-avatar{display:grid;place-items:center;flex:none;width:38px;height:38px;border-radius:50%;background:var(--dsw-alias-primary-soft,#e9effa);color:var(--dsw-alias-label-primary);font-size:17px}.ar-contact-main{min-width:0;flex:1}.ar-contact-main strong{font-weight:550}.ar-contact-line{display:flex;gap:6px;justify-content:space-between}.ar-contact-preview{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--dsw-alias-label-secondary);font-size:13px}.ar-time{color:var(--dsw-alias-label-secondary);font-size:12px;white-space:nowrap}.ar-btn,.ar-link,.ar-icon{font:inherit;cursor:pointer;color:inherit;background:transparent;border:1px solid var(--dsw-alias-border-l3);border-radius:8px;padding:7px 12px;display:inline-flex;gap:6px;align-items:center;justify-content:center}.ar-link{color:var(--dsw-alias-primary,#1769ff);border:0;padding:2px;white-space:nowrap}.ar-icon{padding:7px;border:0}.ar-btn:disabled,.ar-link:disabled,.ar-icon:disabled{opacity:.45;cursor:default}.ar-chat{min-width:0;min-height:0;display:flex;flex-direction:column}.ar-chat-head{padding:18px 24px;display:flex;gap:12px;align-items:center;border-bottom:1px solid var(--dsw-alias-border-l3)}.ar-chat-head strong{font-size:17px}.ar-chat-title{flex:1;min-width:0}.ar-stream{padding:20px 24px;overflow:auto;flex:1;min-height:0;scrollbar-gutter:stable}.ar-day{text-align:center;color:var(--dsw-alias-label-secondary);font-size:12px;margin:10px 0 25px}.ar-message{display:flex;gap:12px;margin-bottom:25px;align-items:flex-start}.ar-message.out{flex-direction:row-reverse}.ar-message.out .ar-message-main{align-items:flex-end}.ar-message-main{display:flex;flex-direction:column;gap:6px;min-width:0;max-width:min(82%,720px);font-size:15px}.ar-bubble{padding:12px 16px;border-radius:14px;background:var(--dsw-alias-bg-l2,#f3f4f6);white-space:pre-wrap;overflow-wrap:anywhere;line-height:1.7}.ar-message.out .ar-bubble{background:var(--dsw-alias-primary-soft,#e7efff)}.ar-evidence{white-space:normal;border-top:1px solid var(--dsw-alias-border-l3);margin-top:10px;padding-top:6px;font-size:12px}.ar-evidence>button{padding:0;border:0;background:transparent;color:inherit;font:inherit;cursor:pointer}.ar-evidence ol{padding-left:20px;max-width:100%;margin:10px 0}.ar-evidence pre{font-size:12px;white-space:pre-wrap;overflow-wrap:anywhere;max-height:240px;overflow:auto}.ar-composer{margin:14px 22px 22px;border:1px solid var(--dsw-alias-border-l3);border-radius:12px;padding:12px 14px;flex:none}.ar-composer textarea{width:100%;box-sizing:border-box;resize:vertical;min-height:52px;max-height:180px;border:0;outline:none;background:transparent;color:inherit;font:inherit}.ar-compose-bar{display:flex;justify-content:space-between;align-items:center;min-height:35px}.ar-send{width:36px;height:36px;display:grid;place-items:center;border:0;border-radius:50%;color:#fff;background:var(--dsw-alias-primary,#1468ff);cursor:pointer}.ar-send:disabled{opacity:.45;cursor:default}.ar-settings{border-left:1px solid var(--dsw-alias-border-l3);min-width:0;overflow:auto;display:flex;flex-direction:column;background:var(--dsw-alias-bg-base);padding:0 24px 24px}.ar-settings-head{display:flex;justify-content:space-between;align-items:center;padding:20px 0}.ar-settings-head h2{font-size:18px;margin:0}.ar-settings form{display:flex;flex-direction:column;flex:1;gap:0}.ar-setting{padding:22px 0;border-bottom:1px solid var(--dsw-alias-border-l3)}.ar-setting label>span,.ar-setting-title{font-weight:550;display:block;margin-bottom:13px}.ar-select-row{display:flex;align-items:center;gap:12px}.ar-select-row select{min-width:0;flex:1;width:100%;color:inherit;background:transparent;font:inherit;border:1px solid var(--dsw-alias-border-l3);padding:10px;border-radius:8px}.ar-account-info{display:flex;align-items:center;justify-content:space-between;gap:8px;color:var(--dsw-alias-label-secondary);font-size:12px;margin-top:8px;overflow-wrap:anywhere}.ar-toggle{display:flex;align-items:center;justify-content:space-between;gap:15px;padding:22px 0;border-bottom:1px solid var(--dsw-alias-border-l3)}.ar-save{margin-top:auto;padding-top:36px}.ar-primary{width:100%;border:0;color:#fff;background:var(--dsw-alias-primary,#1468ff);padding:12px;border-radius:8px;font:inherit;cursor:pointer}.ar-error{margin:0;padding:10px 20px;color:var(--dsw-alias-label-primary);background:var(--dsw-alias-bg-l2,#f5f5f5);overflow-wrap:anywhere}.ar-placeholder{display:grid;place-content:center;text-align:center;color:var(--dsw-alias-label-secondary);padding:24px;flex:1}.ar-pager{display:flex;justify-content:space-between;align-items:center;padding:12px;gap:6px;font-size:12px}.ar-modal{width:min(900px,100%);max-width:900px;max-height:100%}.ar-modal-content{min-height:0;flex:1}.ar-modal-content>div:last-child{min-height:0;overflow:auto}.ar-modal-content>div:first-child{flex:none}.ar-library{display:grid;gap:15px}.ar-library-row{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:12px 0;border-bottom:1px solid var(--dsw-alias-border-l3)}.ar-method{line-height:1.8;overflow-wrap:anywhere}.ar-back{display:none}.ar-native{height:65vh;min-height:200px}.ar-toast{font-size:12px;color:var(--dsw-alias-label-secondary)}.ar-switch{transform:scale(1.25);transform-origin:right center}.ar-switch[aria-checked=true]{background:var(--dsw-alias-state-business-primary,#1468ff)}@media(max-width:1400px){.ar-layout[data-settings=true]{grid-template-columns:230px minmax(0,1fr)}.ar-settings{position:absolute;inset:0 0 0 auto;width:min(360px,100%);box-sizing:border-box;z-index:5;box-shadow:-8px 0 28px #0001}.ar-layout{grid-template-columns:230px minmax(0,1fr)}}@media(max-width:650px){.ar-top{padding:0 14px;min-height:54px}.ar-layout,.ar-layout[data-settings=true]{display:flex;flex:1}.ar-list{width:100%}.ar-layout[data-selected=true]>.ar-list{display:none}.ar-chat{width:100%;flex:1}.ar-layout[data-selected=false]>.ar-chat{display:none}.ar-back{display:inline-flex}.ar-chat-head{padding:12px}.ar-stream{padding:12px}.ar-message{gap:7px}.ar-message-main{max-width:86%}.ar-avatar{width:30px;height:30px;font-size:14px}.ar-composer{margin:10px 12px 16px}.ar-settings{width:100%;padding:0 20px 20px}.ar-bubble{padding:10px 13px}.ar-chat-head strong{font-size:16px}}`;
    const timelineOrder=(a,b)=>a.at.replace(/(?:\.(\d+))?Z$/,(_,f='')=>'.'+f.padEnd(6,'0')+'Z').localeCompare(b.at.replace(/(?:\.(\d+))?Z$/,(_,f='')=>'.'+f.padEnd(6,'0')+'Z'))||a.key.localeCompare(b.key);
    function ReplyEvidence({sessionId,runId}){
      const [open,setOpen]=useState(false),[data,setData]=useState(),[error,setError]=useState('');
      useEffect(()=>{if(!open)return;let current=true;const load=async()=>{try{const value=await api('/reply-evidence?'+new URLSearchParams({sessionId,runId}));if(current){setData(value);setError('')}}catch(e){if(current)setError(e.message)}};load();const timer=setInterval(load,5000);return()=>{current=false;clearInterval(timer)}},[open,sessionId,runId]);
      const toggle=()=>setOpen(v=>!v);
      const label=name=>name==='skill'?'加载专家方法':/context$/.test(name)?'读取客户上下文':/search$/.test(name)?'检索资料':/read$|excerpt$/.test(name)?'读取来源原文':/check$|validate$/.test(name)?'核对回答依据':/reply$/.test(name)?'提交回复':/receipt$/.test(name)?'核对发送回执':name.split('__').pop();
      return h('div',{className:'ar-evidence'},button(open?'收起处理明细':'查看依据与处理明细',toggle,{'aria-expanded':open}),open&&h('div',null,error&&h('p',{role:'alert'},error),!data&&!error&&h('p',null,'正在读取本轮记录…'),data&&h('ol',null,...data.calls.map(c=>h('li',{key:c.id},h('strong',null,label(c.name)),c.failed&&' · 未完成',h('details',null,h('summary',null,'查看记录'),h('pre',null,c.output||c.input))))),data?.calls.length===0&&h('p',null,'此轮暂无可读取的工具记录。')));
    }
    function ReplyPage({openSession,sessionId=''}){
      const UI=require('@deepseek-ai/dsh-client-ui-primitives'),{Switch,Modal,MarkdownText,IconSettingsOutlineRegular,IconCloseOutlineRegular,IconPaperPlaneOutlineRegular,IconChevronLeftOutlineRegular,IconAgentPresetOutlineRegular}=UI;
      const [catalog,setCatalog]=useState(),[projects,setProjects]=useState([]),[project,setProject]=useState(replyProject),[page,setPage]=useState({items:[]}),[q,setQ]=useState(''),[cursor,setCursor]=useState(''),[trail,setTrail]=useState([]),[route,setRoute]=useState(),[timeline,setTimeline]=useState({items:[]}),[edit,setEdit]=useState(),[activeSettings,setActiveSettings]=useState(),[accounts,setAccounts]=useState([]),[experts,setExperts]=useState([]),[resources,setResources]=useState([]),[settings,setSettings]=useState(false),[modal,setModal]=useState(),[error,setError]=useState(''),[notice,setNotice]=useState(''),[busy,setBusy]=useState(false),[loading,setLoading]=useState(true),[tick,setTick]=useState(0),[draft,setDraft]=useState(''),[intent,setIntent]=useState();
      const stream=useRef(null),stick=useRef(true),sending=useRef(false),timelineRef=useRef([]),refreshOffset=useRef(0);
      useEffect(()=>{timelineRef.current=timeline.items},[timeline.items]);
      const reset=()=>{setCursor('');setTrail([])};
      const reloadAccounts=async id=>{try{setAccounts(await api('/reply-accounts?projectId='+encodeURIComponent(id||project)))}catch(e){setError(e.message)}};
      useEffect(()=>{let current=true;replyProjects().then(async v=>{if(!current)return;setCatalog(v.catalog);setProjects(v.projects);let id=replyProject||v.projects[0]?.projectId||'';if(sessionId){const r=await api('/reply-session?sessionId='+encodeURIComponent(sessionId));if(!current)return;setRoute(r);id=r.principal.projectId}setProject(id);replyProject=id;setLoading(false)}).catch(e=>{if(current){setError(e.message);setLoading(false)}});return()=>{current=false}},[sessionId]);
      useEffect(()=>{if(!project)return;let current=true;api('/message-routes?'+new URLSearchParams({projectId:project,q,after:cursor})).then(v=>{if(current){setPage(v);setLoading(false)}}).catch(e=>{if(current)setError(e.message)});return()=>{current=false}},[project,q,cursor,tick]);
      useEffect(()=>{if(!project)return;let current=true;Promise.all([api('/reply-settings?projectId='+project),api('/reply-resources?projectId='+project),api('/reply-experts?projectId='+project)]).then(([s,r,e])=>{if(current){setEdit(s);setActiveSettings(s);setResources(r);setExperts(e)}}).catch(e=>{if(current)setError(e.message)});reloadAccounts(project);return()=>{current=false}},[project]);
      useEffect(()=>{if(!sessionId)return;let current=true;setTimeline({items:[]});setDraft('');setIntent(undefined);stick.current=true;const load=async()=>{try{const [r,t]=await Promise.all([api('/reply-session?sessionId='+sessionId),api('/reply-timeline?sessionId='+sessionId)]);const saved=await api('/reply-settings?projectId='+r.principal.projectId);const candidates=timelineRef.current.filter(x=>!['delivered','failed','ignored','handoff','paused','human','reply_sent','reply_delivered','reply_failed'].includes(x.state));const start=candidates.length?refreshOffset.current%candidates.length:0;const keys=[...candidates.slice(start),...candidates.slice(0,start)].slice(0,100).map(x=>x.key);refreshOffset.current+=100;const states=keys.length?await api('/reply-states?'+new URLSearchParams({sessionId,keys:JSON.stringify(keys)})):{};if(current){setRoute(r);setActiveSettings(saved);setTimeline(v=>{if(!v.items.length)return t;const keys=new Set(v.items.map(x=>x.key));if(t.items.length&&!t.items.some(x=>keys.has(x.key)))return t;return{items:[...new Map([...v.items,...t.items].map(x=>[x.key,x])).values()].map(x=>states[x.key]?{...x,state:states[x.key]}:x).sort(timelineOrder),before:v.before}})}}catch(e){if(current)setError(e.message)}};load();const timer=setInterval(()=>{load();setTick(v=>v+1)},5000);return()=>{current=false;clearInterval(timer)}},[sessionId]);
      useEffect(()=>{if(stick.current&&stream.current)stream.current.scrollTop=stream.current.scrollHeight},[timeline.items.length,sessionId]);
      const change=(key,value)=>setEdit(v=>({...v,[key]:value}));
      const act=async fn=>{setBusy(true);setError('');setNotice('');try{await fn();setTick(v=>v+1)}catch(e){setError(e.message)}finally{setBusy(false)}};
      const save=e=>{e.preventDefault();act(async()=>{const saved=await api('/reply-settings',edit);setEdit(saved);setActiveSettings(saved);setNotice('设置已保存')})};
      const mode=()=>act(async()=>{await api('/message-routes',{id:route.id,revision:route.revision,autoReply:!route.autoReply});setRoute(await api('/reply-session?sessionId='+sessionId))});
      const send=async()=>{if(sending.current)return;sending.current=true;try{await act(async()=>{const request=intent||{sessionId,key:crypto.randomUUID(),text:draft,revision:route.revision};setIntent(request);try{await api('/reply-manual',request)}catch(e){if([400,403,404,409].includes(e.status)){setIntent(undefined);setRoute(await api('/reply-session?sessionId='+sessionId))}throw e}setDraft('');setIntent(undefined);setRoute(await api('/reply-session?sessionId='+sessionId));const t=await api('/reply-timeline?sessionId='+sessionId);stick.current=true;setTimeline(t);setNotice('消息已提交，正在等待渠道回执')})}finally{sending.current=false}};
      const older=()=>act(async()=>{const t=await api('/reply-timeline?'+new URLSearchParams({sessionId,before:timeline.before}));stick.current=false;setTimeline(v=>({items:[...new Map([...t.items,...v.items].map(x=>[x.key,x])).values()],before:t.before}))});
      const openExpert=()=>act(async()=>{const v=await api('/reply-experts?'+new URLSearchParams({projectId:project,name:edit.expertSkill}));setModal({kind:'expert',title:'回答专家',...v})});
      const showResources=()=>setModal({kind:'resources',title:'回答语料',resourceIds:selectedIds});
      const previewResource=chosen=>act(async()=>{const latest=await api('/reply-resources?projectId='+project);setResources(latest);const r=latest.find(v=>v.id===chosen.id);if(!r)throw Error('该资料已不可读取');const selected=edit.knowledge.find(v=>v.resourceId===r.id);const ref={resourceId:r.id,version:r.currentVersion,section:selected?.section||r.sections[0]?.id};const documents=await api('/resource-documents?'+new URLSearchParams({projectId:project,resourceId:r.id}));if(documents.external){setModal({kind:'documents',title:r.name,resource:r,...documents});return}const v=await api('/resource-page?'+new URLSearchParams(ref));setModal({kind:'resource',title:r.name,resource:r,page:v})});
      const readDocument=d=>act(async()=>{const v=await api('/resource-documents?'+new URLSearchParams({projectId:project,resourceId:modal.resource.id,uri:d.uri}));setModal({...modal,...v,documentTitle:d.title||d.name})});
      const currentAccount=accounts.find(a=>a.connectionId===route?.connectionId),settingsAccount=accounts.find(a=>a.connectionId===edit?.connectionId);
      const expertName=e=>(e.description||e.name).split(/[。；]/)[0].slice(0,26);
      const selectedIds=[...new Set((edit?.knowledge||[]).map(r=>r.resourceId))];
      const timeShort=t=>new Date(t).toLocaleTimeString('zh-CN',{timeZone:'Asia/Shanghai',hour:'2-digit',minute:'2-digit'});
      const day=t=>new Date(t).toLocaleDateString('zh-CN',{timeZone:'Asia/Shanghai',year:'numeric',month:'2-digit',day:'2-digit'});
      const projectName=catalog?.projects.find(v=>v.id===project)?.name||'自动化应答';
      const md = text => h(MarkdownText,{text,omitHtml:true,labels:{code:{copyLabel:'复制',copiedLabel:'已复制',toolbarLabels:{codeLabel:'代码',wrapLabel:'换行',unwrapLabel:'不换行'}},footnotes:'引用'}});
      const sidebar = h('aside',{className:'ar-list','aria-label':'客户会话'},
        h('div',{className:'ar-search'},h('input',{type:'search','aria-label':'搜索客户',placeholder:'搜索客户名称',value:q,onChange:e=>{reset();setQ(e.target.value)}})),
        h('div',{className:'ar-contacts'},
          loading&&h('p',{className:'ar-placeholder'},'正在读取会话…'),
          !loading&&!page.items.length&&h('p',{className:'ar-placeholder'},q?'没有匹配的客户':'暂无客户会话'),
          ...page.items.map(row=>h('button',{className:'ar-contact',key:row.id,'aria-current':row.sessionId===sessionId,onClick:()=>act(async()=>{replyProject=project;const w=catalog.projects.find(v=>v.id===project);await api('/session/start',{workspaceId:w.workspaceId,sessionId:row.sessionId});await openSession(row.sessionId)})},
            h('span',{className:'ar-avatar','aria-hidden':true},(row.displayName||'客').slice(0,1)),
            h('span',{className:'ar-contact-main'},
              h('span',{className:'ar-contact-line'},h('strong',null,row.displayName||row.actorId),h('time',{className:'ar-time'},row.lastMessageAt&&timeShort(row.lastMessageAt))),
              h('span',{className:'ar-contact-preview'},row.preview||'暂无文字消息'),
              !row.autoReply&&h('span',{className:'ar-time'},'人工接管'))))),
        h('div',{className:'ar-pager'},button('上一页',()=>{const a=[...trail];setCursor(a.pop()||'');setTrail(a)},{className:'ar-link',disabled:!trail.length}),h('span',{className:'ar-time'},`第${trail.length+1}页`),button('下一页',()=>{setTrail(v=>[...v,cursor]);setCursor(page.next)},{className:'ar-link',disabled:!page.next})));
      const message = (row,i) => h(React.Fragment,{key:row.key},
        (i===0||day(row.at)!==day(timeline.items[i-1].at))&&h('div',{className:'ar-day'},day(row.at)),
        h('div',{className:'ar-message'+(row.kind!=='客户'?' out':'')},
          h('span',{className:'ar-avatar','aria-hidden':true},row.kind==='客户'?(route?.displayName||'客').slice(0,1):row.kind==='人工'?'人':h(IconAgentPresetOutlineRegular,{size:23})),
          h('div',{className:'ar-message-main'},
            h('div',{className:'ar-bubble'},row.text,(row.kind==='自动回复'||row.kind==='客户'&&['handoff','ignored'].includes(row.state))&&h(ReplyEvidence,{sessionId,runId:row.runId})),
            h('div',{className:'ar-time'},`${timeShort(row.at)} · ${row.kind==='客户'?({received:'客户 · 正在处理',handoff:'客户 · 已转人工',paused:'客户 · 人工接管中',ignored:'客户 · 无需自动回复'}[row.state]||'客户'):row.kind==='人工'?'人工 · ':''}${row.kind!=='客户'?(replyLabels[row.state]||row.state):''}`))));
      const conversation = h('main',{className:'ar-chat'},!sessionId?
        h('div',{className:'ar-placeholder'},'选择一位客户，查看聊天与回复'):
        h(React.Fragment,null,
          h('header',{className:'ar-chat-head'},
            button('返回',()=>ctxReplyBack(),{className:'ar-btn ar-back'}),
            h('span',{className:'ar-avatar','aria-hidden':true},(route?.displayName||'客').slice(0,1)),
            h('div',{className:'ar-chat-title'},h('strong',null,route?.displayName||'客户'),h('div',{className:'ar-time'},`${currentAccount?.name||'企微会话'} · ${!activeSettings?'正在核实状态':activeSettings.enabled===false?'自动应答已关闭':!route?.autoReply?'人工接管':!currentAccount||currentAccount.status==='unknown'?'渠道状态待核实':currentAccount.status!=='online'||!currentAccount.sendEnabled?'渠道暂停':'AI接管'}`)),
            button(route?.autoReply?'人工接管':'恢复AI接管',mode,{className:'ar-btn',disabled:busy||!route})),
          h('div',{className:'ar-stream',ref:stream,onScroll:()=>{const e=stream.current;stick.current=e.scrollHeight-e.scrollTop-e.clientHeight<80}},
            timeline.before&&button('加载更早的聊天',older,{className:'ar-link',disabled:busy}),
            !timeline.items.length&&h('p',{className:'ar-placeholder'},'暂无可显示的渠道消息'),
            ...timeline.items.map(message)),
          h('form',{className:'ar-composer',onSubmit:e=>{e.preventDefault();if(draft.trim()&&!busy)send()}},
            h('textarea',{'aria-label':'发送消息给客户',placeholder:'发送消息给客户…',value:draft,disabled:busy||!!intent,onChange:e=>setDraft(e.target.value),maxLength:16000}),
            h('div',{className:'ar-compose-bar'},h('span',{className:'ar-toast',role:'status'},notice),
              h('button',{className:'ar-send',type:'submit','aria-label':intent?'核对并继续本条发送':'发送消息给客户',disabled:busy||!draft.trim()||!route||currentAccount?.status!=='online'||!currentAccount?.sendEnabled},h(IconPaperPlaneOutlineRegular,{size:21}))))));
      const toggleSetting=(label,key,checked)=>h('div',{className:'ar-toggle'},h('span',null,label),h(Switch,{label,className:'ar-switch',checked,onChange:value=>change(key,value),disabled:busy}));
      const settingsPanel=settings&&h('aside',{className:'ar-settings','aria-label':'应答设置'},
        h('div',{className:'ar-settings-head'},h('h2',null,'应答设置'),button(h(IconCloseOutlineRegular,{size:18}),()=>setSettings(false),{className:'ar-icon','aria-label':'关闭设置'})),
        edit&&h('form',{onSubmit:save},
          h('div',{className:'ar-setting'},h('span',{className:'ar-setting-title'},'企微账号'),
            h('div',{className:'ar-select-row'},h('select',{'aria-label':'企微账号',value:edit.connectionId||'',disabled:edit.revision>0,onChange:e=>change('connectionId',e.target.value)},...accounts.map(a=>h('option',{key:a.connectionId,value:a.connectionId},`${a.name||'账号暂不可读'} · ${{online:'在线',offline:'离线',deleted:'已删除',unknown:'状态待核实'}[a.status]||a.status}`)))),
            h('div',{className:'ar-account-info'},h('span',null,settingsAccount?.accountId?'账号：'+settingsAccount.accountId:settingsAccount?.error||'正在读取网关账号'),button('刷新账号',()=>reloadAccounts(),{className:'ar-link'}))),
          h('div',{className:'ar-setting'},h('span',{className:'ar-setting-title'},'回答专家'),
            h('div',{className:'ar-select-row'},h('select',{'aria-label':'回答专家',value:edit.expertSkill,onChange:e=>change('expertSkill',e.target.value)},...experts.map(e=>h('option',{key:e.name,value:e.name},expertName(e)))),button('查看方法',openExpert,{className:'ar-link',disabled:busy}))),
          h('div',{className:'ar-setting'},h('span',{className:'ar-setting-title'},'回答语料'),
            h('div',{className:'ar-select-row'},h('select',{'aria-label':'回答语料',value:selectedIds[0]||'',onChange:e=>{const r=resources.find(r=>r.id===e.target.value);change('knowledge',r?(edit.knowledge||[]).some(v=>v.resourceId===r.id)?edit.knowledge.filter(v=>v.resourceId===r.id):[{resourceId:r.id,version:r.currentVersion,section:r.sections[0].id}]:[])}},h('option',{value:''},'不使用公共语料'),...resources.filter(r=>r.currentVersion&&r.id!==edit.subject?.resourceId).map(r=>h('option',{key:r.id,value:r.id},r.name))),button('查看资料',showResources,{className:'ar-link',disabled:!selectedIds.length}))),
          toggleSetting('读取客户档案','readProfile',edit.readProfile!==false),
          toggleSetting('转人工时提醒我','notify',!!edit.notify),
          toggleSetting('自动应答','enabled',!!edit.enabled),
          h('div',{className:'ar-save'},h('button',{type:'submit',className:'ar-primary',disabled:busy},busy?'保存中…':'保存设置'),notice&&h('p',{className:'ar-toast',role:'status'},notice))));
      let modalContent;
      if(modal?.kind==='expert')modalContent=h('article',{className:'ar-method'},h('p',null,modal.description),md(modal.content),...(modal.references||[]).map(r=>h('details',{key:r.name},h('summary',null,r.name),md(r.content))));
      if(modal?.kind==='resources')modalContent=h('div',{className:'ar-library'},h('p',null,'选择可用于回答的资料板块。'),...resources.filter(r=>(modal.resourceIds||selectedIds).includes(r.id)).map(r=>h('section',{key:r.id},h('div',{className:'ar-library-row'},h('strong',null,r.name),button('阅读资料',()=>previewResource(r),{className:'ar-link'})),...r.sections.map(section=>h('label',{className:'wb-check',key:section.id},h('input',{type:'checkbox',checked:edit.knowledge.some(ref=>ref.resourceId===r.id&&ref.section===section.id),onChange:e=>{const rest=edit.knowledge.filter(ref=>ref.resourceId!==r.id||ref.section!==section.id);if(e.target.checked&&rest.length>=8){setError('最多选择8个回答资料板块');return}change('knowledge',e.target.checked?[...rest,{resourceId:r.id,version:r.currentVersion,section:section.id}]:rest)}}),section.name||section.id)))));
      if(modal?.kind==='documents')modalContent=h('div',{className:'ar-library'},modal.content!==undefined?
        h(React.Fragment,null,button('返回资料目录',()=>setModal({...modal,content:undefined}),{className:'ar-link'}),h('h3',null,modal.documentTitle),md(modal.content)):
        modal.documents.map(d=>h('div',{className:'ar-library-row',key:d.uri},h('strong',null,d.title||d.name),button('阅读',()=>readDocument(d),{className:'ar-link'}))));
      if(modal?.kind==='resource')modalContent=h('div',null,...modal.page.records.map(r=>h('article',{key:r.id,className:'ar-method'},...Object.entries(r.fields).map(([k,v])=>h('div',{key:k},h('strong',null,k),h('pre',{style:{whiteSpace:'pre-wrap',overflowWrap:'anywhere'}},typeof v==='string'?v:JSON.stringify(v,null,2)))))));
      return h('section',{className:'ar-root'},
        h('style',null,replyCSS),
        h('header',{className:'ar-top'},h('h1',null,projectName),button(h(React.Fragment,null,h(IconSettingsOutlineRegular,{size:17}),'设置'),()=>setSettings(v=>!v),{className:'ar-btn','aria-expanded':settings})),
        error&&h('p',{className:'ar-error',role:'alert'},error),
        h('div',{className:'ar-layout','data-settings':settings,'data-selected':!!sessionId},sidebar,conversation,settingsPanel),
        h(Modal,{open:!!modal,onClose:()=>setModal(undefined),title:modal?.title||'查看资料',closeLabel:'关闭',className:'ar-modal',contentClassName:'ar-modal-content'},modalContent));

    }
    function ChannelHistory({sessionId}){
      const [open,setOpen]=useState(false),[rows,setRows]=useState([]),[error,setError]=useState('');
      const labels={received:'待处理',human:'人工已回复',paused:'已暂停',handoff:'已转人工',ignored:'已忽略',reply_queued:'回复待提交',reply_submitting:'正在提交',prepared:'待提交',submitting:'正在提交',accepted:'网关已受理',sent:'已发送',delivered:'已送达',failed:'未发送／失败',unknown:'结果未知，未重发'};
      const load=async()=>{setError('');try{setRows(await api('/reply-channel-history?sessionId='+encodeURIComponent(sessionId)))}catch(e){setError(e.message)}};
      return h('div',{style:{position:'relative'}},button(responsiveLabel('渠道记录','记录'),()=>{setOpen(v=>!v);if(!open)load()},{className:'wb-inline','aria-label':'渠道记录','aria-expanded':open}),open&&h('section',{className:'wb-page',style:{position:'fixed',zIndex:20,top:90,right:14,width:'min(600px, calc(100vw - 28px))',maxHeight:'65vh',overflow:'auto',padding:16,boxSizing:'border-box',border:'1px solid var(--dsw-alias-border-l3)',borderRadius:10,boxShadow:'0 6px 24px #0002'}},h('div',{className:'wb-actions'},h('strong',null,'本客户的渠道记录'),button('刷新',load),button('关闭',()=>setOpen(false))),h('p',{className:'wb-muted'},'最近50条入站与50条回复；包括暂停期间的原消息。专家执行仍在原生对话中查看。'),error&&h('p',{role:'alert'},error),rows.length===0?h('p',null,'暂无渠道记录'):rows.map(row=>h('div',{className:'wb-note',key:row.kind+row.id},h('div',{className:'wb-muted'},new Date(row.at).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai'})+' · '+row.kind+' · '+(labels[row.state]||labels[row.state.replace(/^reply_/,'')]||row.state)),h('div',{style:{whiteSpace:'pre-wrap',overflowWrap:'anywhere'}},row.text)))));
    }
    function installReplyViews(ctx){
      const {IconChatBubbleOutlineRegular,IconMessageOutlineRegular}=require('@deepseek-ai/dsh-client-ui-primitives');
      ctx.slots.inject('sidebar.panellist',()=>ctx.slots.register({name:'sidebar.panellist',id:'wb-replies',order:-5,label:()=>'自动化应答'},IconChatBubbleOutlineRegular||IconMessageOutlineRegular||IconDatabaseOutlineRegular));
      ctx.slots.inject('main',()=>ctx.slots.register({name:'main',key:'wb-replies',inject:()=>({openSession:async id=>{await ctx.sessions.refresh();await ctx.sessions.refreshProjections(id);ctx.uiWorkspace.openSession(id);}})},ReplyPage));
      // Replace only the native list's data projection; reuse its component,
      // controls, shortcuts, store, locale and ordinary project behaviour.
      ctx.slots.inject('sidebar.workspaces',()=>{
        const base=ctx.slots.entries('sidebar.workspaces').find(entry=>entry.component);
        if(!base)return()=>{};
        const Original=base.component;
        function NativeList(props){
          const [,update]=useState(0);useEffect(()=>{const changed=()=>update(v=>v+1);window.addEventListener('wb-reply-projects',changed);return()=>window.removeEventListener('wb-reply-projects',changed)},[]);
          return h(Original,{...props,useSessions:selector=>props.useSessions(state=>selector(filteredSessions(state))),useWorkspaces:selector=>props.useWorkspaces(state=>selector(filteredWorkspaces(state))),searchSessions:async(...args)=>{const result=await props.searchSessions(...args);return{...result,items:result.items.filter(item=>visibleSession(item.id||item.sessionId))}}});
        }
        // Preserve the shipped entry's child-slot authority and composed props.
        // Restore the public component entry on this plugin's disposal.
        base.component=NativeList;
        return()=>{if(base.component===NativeList)base.component=Original};
      });
      ctx.slots.inject('conversation.session.header.utilities',()=>ctx.slots.register({name:'conversation.session.header.utilities',id:'reply-return',order:-30,inject:sessionId=>({sessionId})},({sessionId})=>sessionId.startsWith('object-')?button(responsiveLabel('返回自动应答','返回'),()=>ctx.layout.selectPanel('wb-replies'),{className:'wb-inline','aria-label':'返回自动应答'}):null));
      ctxReplyBack=()=>ctx.layout.selectPanel('wb-replies');
      ctx.slots.inject('main.conversation',()=>{
        const base=ctx.slots.entries('main.conversation').find(entry=>entry.component);if(!base)return()=>{};
        const Original=base.component;
        function CustomerConversation(props){
          if(!props.sessionId?.startsWith('object-'))return h(Original,props);
          return h(ReplyPage,{key:props.sessionId,sessionId:props.sessionId,openSession:async id=>{await ctx.sessions.refresh();await ctx.sessions.refreshProjections(id);ctx.uiWorkspace.openSession(id)}});
        }
        base.component=CustomerConversation;return()=>{if(base.component===CustomerConversation)base.component=Original};
      });
      replyProjects().catch(()=>{});
    }
    return {
      inject: ["slots", "layout", "uiWorkspace", "sessions"],
      apply(ctx) {
        ctx.effect(()=>{const style=document.createElement('style');style.textContent=css;document.head.appendChild(style);return()=>style.remove()},'workbench shared native extension styles');
        try{installReplyViews(ctx)}catch(error){console.error("Auto reply view failed:",error);throw error}
        const openToday = async () => {
          const c = await api("/catalog");
          if (c.personalSessionId)
            ctx.uiWorkspace.openSession(c.personalSessionId);
          else ctx.uiWorkspace.startSession(c.personalWorkspaceId);
        };
        for (const [id, label, mark, Component, extra] of [
          ["wb-today", "今日", IconClockOutlineRegular, TodayPage, { openToday,openSession:id=>ctx.uiWorkspace.openSession(id) }],
          ["wb-resources", "通用资源库", IconDatabaseOutlineRegular, ResourcePage, {}],
        ]) {
          ctx.slots.inject("sidebar.panellist", () =>
            ctx.slots.register(
              {
                name: "sidebar.panellist",
                id,
                order: id === "wb-today" ? -20 : -10,
                label: () => label,
              },
              mark,
            ),
          );
          ctx.slots.inject("main", () =>
            ctx.slots.register(
              { name: "main", key: id, inject: () => extra },
              Component,
            ),
          );
        }
        ctx.slots.inject("conversation.session.header.utilities", () =>
          ctx.slots.register(
            {
              name: "conversation.session.header.utilities",
              id: "workbench-resources",
              order: 30,
              inject: sessionId=>({sessionId}),
            },
            ({sessionId}) =>
              h(
                "button",
                {
                  className: "wb-inline",
                  type: "button",
                  onClick: async () => {const c=await api("/catalog");resourceProject=c.projects.find(p=>p.sessionIds?.includes(sessionId))?.id||"";ctx.layout.selectPanel("wb-resources");window.dispatchEvent(new Event("wb-resource-project"));},
                },
                "引用资源",
              ),
          ),
        );
      },
    };
  },
});
