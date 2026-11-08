window.__ModuleLoader__.load({
  id: "@local/personal-workbench",
  factory(require) {
    const React = require("react"),
      h = React.createElement;
    const { useState, useEffect } = React;
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
      if (!r.ok) throw Error(v.error || "操作失败");
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
    function ReplyPage({openSession}){
      const [catalog,setCatalog]=useState(),[projects,setProjects]=useState([]),[project,setProject]=useState(replyProject),[page,setPage]=useState({items:[]}),[q,setQ]=useState(''),[paused,setPaused]=useState(false),[cursor,setCursor]=useState(''),[trail,setTrail]=useState([]),[edit,setEdit]=useState(),[transports,setTransports]=useState([]),[allowedResources,setAllowedResources]=useState([]),[error,setError]=useState(''),[busy,setBusy]=useState(false),[loading,setLoading]=useState(true),[refresh,setRefresh]=useState(0);
      useEffect(()=>{let current=true;Promise.all([replyProjects(),api('/transports')]).then(([v,t])=>{if(!current)return;setCatalog(v.catalog);setProjects(v.projects);setTransports(t);setProject(p=>p||v.projects[0]?.projectId||'');setLoading(false)}).catch(e=>{if(current){setError(e.message);setLoading(false)}});return()=>{current=false}},[refresh]);
      useEffect(()=>{if(!project)return;let current=true;setLoading(true);setError('');api('/message-routes?'+new URLSearchParams({projectId:project,q,paused:String(paused),after:cursor})).then(v=>{if(current){setPage(v);setLoading(false)}}).catch(e=>{if(current){setError(e.message);setLoading(false)}});return()=>{current=false}},[project,q,paused,cursor,refresh]);
      useEffect(()=>{if(!project)return;let current=true;setEdit(undefined);Promise.all([api('/reply-settings?projectId='+encodeURIComponent(project)),api('/reply-resources?projectId='+encodeURIComponent(project))]).then(([config,resources])=>{if(current){setEdit(config);setAllowedResources(resources)}}).catch(e=>{if(current)setError(e.message)});return()=>{current=false}},[project]);
      const reset=()=>{setCursor('');setTrail([])};
      const changeProject=id=>{replyProject=id;reset();setProject(id)};
      const change=(name,value)=>setEdit(v=>({...v,[name]:value}));
      const save=async e=>{e.preventDefault();setBusy(true);setError('');try{const v=await api('/reply-settings',edit);setEdit(v);setRefresh(v=>v+1)}catch(e){setError(e.message)}finally{setBusy(false)}};
      const create=async()=>{setBusy(true);try{const v=await api('/reply-project/create',{});changeProject(v.projectId);setRefresh(v=>v+1)}catch(e){setError(e.message)}finally{setBusy(false)}};
      const mode=async row=>{setBusy(true);try{await api('/message-routes',{id:row.id,revision:row.revision,autoReply:!row.autoReply});setRefresh(v=>v+1)}catch(e){setError(e.message)}finally{setBusy(false)}};
      const projectName=catalog?.projects.find(p=>p.id===project)?.name||'自动化应答';
      return h('div',{className:'wb-page'},h('style',null,css),h('div',{className:'wb-content'},
        h('div',{className:'wb-heading'},h('h1',null,projectName),h('div',{className:'wb-actions'},projects.length>1&&field('项目',h('select',{value:project,onChange:e=>changeProject(e.target.value)},...projects.map(c=>h('option',{key:c.projectId,value:c.projectId},catalog?.projects.find(p=>p.id===c.projectId)?.name||c.projectId)))),button('刷新',()=>setRefresh(v=>v+1),{disabled:busy}))),
        h('p',{className:'wb-muted'},'每位客户使用独立的原生对话。只加载当前页，点击后查看专家执行、上下文和回执。'),
        error&&h('p',{role:'alert',className:'wb-note'},error),
        !project&&!loading&&h('div',{className:'wb-section'},h('p',null,'还没有自动应答项目。创建后先配置渠道、专家和资料，再启用。'),button('创建自动化应答项目',create,{disabled:busy})),
        project&&h(React.Fragment,null,
          h('div',{className:'wb-actions'},field('搜索客户',h('input',{type:'search',value:q,placeholder:'客户名称或标识',onChange:e=>{reset();setQ(e.target.value)}})),h('label',{className:'wb-check'},h('input',{type:'checkbox',checked:paused,onChange:e=>{reset();setPaused(e.target.checked)}}),'只看已暂停')),
          loading?h('p',{role:'status'},'正在读取会话…'):h('div',{className:'wb-list'},page.items.length===0?h('p',{className:'wb-note'},q||paused?'没有匹配的会话。':'暂无客户会话。启用接入后，新消息会出现在这里。'):page.items.map(row=>h('div',{className:'wb-row',key:row.id},h('div',{className:'wb-row-main'},button(row.displayName||row.actorId,()=>{replyProject=project;(async()=>{setBusy(true);try{const workspace=catalog.projects.find(p=>p.id===project);const target=await api('/session/start',{workspaceId:workspace.workspaceId,sessionId:row.sessionId});await openSession(target.sessionId)}catch(e){setError(e.message)}finally{setBusy(false)}})()}),h('div',{className:'wb-muted',style:{overflowWrap:'anywhere'}},row.preview||'暂无文字消息'),h('time',{className:'wb-muted',dateTime:row.lastMessageAt},row.lastMessageAt?new Date(row.lastMessageAt).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai'}):'时间未知')),h('span',{className:'wb-tag'},row.autoReply?'自动回复':'已暂停'),button(row.autoReply?'暂停':'恢复',()=>mode(row),{disabled:busy})))),
          h('div',{className:'wb-actions',style:{marginTop:16}},button('上一页',()=>{const prev=[...trail];setCursor(prev.pop()||'');setTrail(prev)},{disabled:loading||trail.length===0}),h('span',{className:'wb-muted'},`第 ${trail.length+1} 页 · 每页最多50条`),button('下一页',()=>{setTrail(v=>[...v,cursor]);setCursor(page.next||'')},{disabled:loading||!page.next})),
          edit&&h('details',{className:'wb-section'},h('summary',null,'接入设置'),h('form',{className:'wb-form',onSubmit:save},
            field('渠道账号',h('select',{value:edit.connectionId||'',required:true,onChange:e=>change('connectionId',e.target.value)},h('option',{value:''},'选择已安装的第三方连接器'),...transports.filter(t=>t.enabled&&t.projects.includes(project)).map(t=>h('option',{key:t.id,value:t.id},t.name)))),
            h('p',{className:'wb-muted'},'连接凭证保存在宿主私有配置。这里只列出已允许本项目使用的渠道账号。'),
            field('默认专家Skill',h('input',{required:true,value:edit.expertSkill||'customer-reply',pattern:'[a-zA-Z0-9_-]{1,96}',onChange:e=>change('expertSkill',e.target.value)})),
            h('fieldset',null,h('legend',null,'允许客户查询的公共资料'),...allowedResources.flatMap(r=>r.currentVersion?r.sections.map(section=>{const checked=edit.knowledge?.some(ref=>ref.resourceId===r.id&&ref.section===section.id);return h('label',{className:'wb-check',key:r.id+'/'+section.id},h('input',{type:'checkbox',checked:!!checked,onChange:e=>change('knowledge',e.target.checked?[...(edit.knowledge||[]),{resourceId:r.id,version:r.currentVersion,section:section.id}]:(edit.knowledge||[]).filter(ref=>ref.resourceId!==r.id||ref.section!==section.id))}),r.name+' / '+section.name)}):[])),
            field('本用户资料板块（可选）',h('select',{value:edit.subject?edit.subject.resourceId+'/'+edit.subject.section:'',onChange:e=>{if(!e.target.value)return change('subject',undefined);const [resourceId,section]=e.target.value.split('/');change('subject',{resourceId,section,matchField:''})}},h('option',{value:''},'不读取个人资料'),...allowedResources.flatMap(r=>r.currentVersion?r.sections.map(section=>h('option',{key:r.id+'/'+section.id,value:r.id+'/'+section.id},r.name+' / '+section.name)):[]))),
            edit.subject&&field('身份匹配字段',h('input',{value:edit.subject.matchField||'',placeholder:'留空：记录ID；或填资料中的可信外部ID字段',onChange:e=>change('subject',{...edit.subject,matchField:e.target.value})})),
            h('label',{className:'wb-check'},h('input',{type:'checkbox',checked:!!edit.notify,onChange:e=>change('notify',e.target.checked)}),'转人工和异常提醒今日，沿用本人已配置的通知目标'),
            h('label',{className:'wb-check'},h('input',{type:'checkbox',checked:!!edit.enabled,onChange:e=>change('enabled',e.target.checked)}),'启用此项目的自动应答'),
            h('p',{className:'wb-muted'},'同一真实会话只能有一个自动发送者。先用独立测试账号验证；恢复只处理后续新消息。'),
            h('button',{type:'submit',disabled:busy},busy?'保存中…':'保存设置')
          ))
        )
      ));
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
      const channelSnapshots=new WeakMap();
      for(const slot of ['main.conversation','conversation.content','conversation.header','conversation.session'])ctx.slots.inject(slot,()=>{
        const base=ctx.slots.entries(slot).find(entry=>entry.component);if(!base)return()=>{};
        const Original=base.component;
        function ChannelConversation(props){
          if(!props.sessionId?.startsWith('object-')||!props.useSession)return h(Original,props);
          return h(Original,{...props,useSession:selector=>props.useSession(state=>{
            if(!state)return selector(state);
            let view=channelSnapshots.get(state);if(!view){view={...state,blank:false,awaitingFirstTurn:false};channelSnapshots.set(state,view)}
            return selector(view);
          })});
        }
        base.component=ChannelConversation;return()=>{if(base.component===ChannelConversation)base.component=Original};
      });
      ctx.slots.inject('conversation.session.header.utilities',()=>ctx.slots.register({name:'conversation.session.header.utilities',id:'channel-history',order:-20,inject:sessionId=>({sessionId})},({sessionId})=>sessionId.startsWith('object-')?h(ChannelHistory,{sessionId}):null));
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
