window.__ModuleLoader__.load({
  id: "@local/personal-workbench",
  factory(require) {
    const React = require("react"),
      h = React.createElement;
    const { useState, useEffect, useRef, useSyncExternalStore } = React;
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
      const [kind,setKind]=useState("database"),[documents,setDocuments]=useState(),[record,setRecord]=useState();
      const documentRequest=useRef(0),previewRequest=useRef(0);
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
      const openPreview=async(r,section=r.sections[0]?.id,after="")=>{
        const n=++previewRequest.current;setError("");setDocuments(undefined);documentRequest.current++;
        try{const page=await api(query("/resource-page",{resourceId:r.id,version:r.currentVersion,section,after}));if(n===previewRequest.current)setPreview({resource:r,section,page})}
        catch(e){if(n===previewRequest.current)setError(e.message)}
      };
      const openCorpus=async(r,uri)=>{
        const projectId=projectFilter||r.ownerProject;
        const n=++documentRequest.current;setError("");setPreview(undefined);previewRequest.current++;
        try{
          const v=projectId?await api(query("/resource-documents",{projectId,resourceId:r.id,...(uri?{uri}:{})})):{external:false};
          if(n!==documentRequest.current)return;
          if(v.external){setDocuments({resource:r,...v});return}
          await openPreview(r);
        }catch(e){if(n===documentRequest.current)setError(e.message)}
      };
      const resetView=()=>{documentRequest.current++;previewRequest.current++;setDocuments(undefined);setPreview(undefined);setRecord(undefined)};
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
        (r) => ((connections.find(c=>c.id===r.connectionId)?.kind==="directory" ||
          Boolean(catalog?.documentResourceIds?.includes(r.id)) || r.sections.some(s=>s.fields?.includes("markdown"))) === (kind==="corpus")) && (
          !projectFilter ||
          r.ownerProject === projectFilter ||
          r.maintainerProject === projectFilter ||
          grants.some(
            (g) => g.resourceId === r.id && g.projectId === projectFilter,
          )),
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
            h("h1", null, "知识库"),
            h("select",{"aria-label":"按项目查看",value:projectFilter,onChange:e=>{resetView();resourceProject=e.target.value;setProjectFilter(resourceProject)}},h("option",{value:""},"全部项目"),...(catalog?.projects??[]).map(p=>h("option",{key:p.id,value:p.id},p.name))),
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
          h("div",{className:"wb-tabs","aria-label":"资料类型"},
            button("语料",()=>{resetView();setKind("corpus")},{"aria-pressed":kind==="corpus"}),
            button("数据库",()=>{resetView();setKind("database")},{"aria-pressed":kind==="database"})),
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
          documents&&h("section",{className:"wb-section"},
            h("div",{className:"wb-heading"},h("h2",null,documents.resource.name),button("关闭预览",resetView)),
            documents.content!==undefined?h(React.Fragment,null,button("返回语料目录",()=>setDocuments({...documents,content:undefined})),markdown(documents.content)):
              h("div",{className:"wb-list"},...(documents.documents||[]).map(d=>h("div",{className:"wb-row",key:d.uri},h("span",{className:"wb-row-main"},d.title||d.name),button("阅读",()=>openCorpus(documents.resource,d.uri)))))),
          preview&&h("section",{className:"wb-section"},h("div",{className:"wb-heading"},h("h2",null,preview.resource.name),button("关闭预览",resetView)),
            h("div",{className:"wb-actions"},field("资料板块",h("select",{value:preview.section,onChange:e=>openPreview(preview.resource,e.target.value)},...preview.resource.sections.map(s=>h("option",{key:s.id,value:s.id},s.name||s.id)))),
              h("a",{className:"wb-inline",href:"/workbench-api/resource-export?"+new URLSearchParams({resourceId:preview.resource.id,version:preview.page.ref.version,section:preview.section}),download:true},"下载此板块")),
            h("p",{className:"wb-muted"},`共 ${preview.page.count} 条 · ${time(preview.page.publishedAt)} 发布${preview.page.stale?" · 来源已更新，此结果需要重新生成":""}`),
            h(ResourceRecords,{rows:preview.page.records,fields:preview.resource.sections.find(s=>s.id===preview.section)?.fields||[],corpus:kind==="corpus",open:setRecord}),
            preview.page.next&&button("再看 10 条",()=>openPreview(preview.resource,preview.section,preview.page.next))),
          record&&h(UI.Modal,{open:true,title:"记录详情",closeLabel:"关闭",onClose:()=>setRecord(undefined)},
            h("div",{className:"wb-page",style:{maxHeight:"65vh",padding:16}},h("div",{className:"wb-detail-grid"},...Object.entries(record.fields).map(([k,v])=>
              h("div",{key:k},h("strong",null,k),h("pre",null,typeof v==="string"?v:JSON.stringify(v,null,2))))))),
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
                r.currentVersion&&button("查看资料",()=>kind==="corpus"?openCorpus(r):openPreview(r)),
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

    const UI = require("@deepseek-ai/dsh-client-ui-primitives");
    const workbenchCSS = `
      .wb-project-shortcut{width:100%;display:flex;align-items:center;gap:8px;padding:8px 10px;border:0;border-radius:8px;background:transparent;color:inherit;font:inherit;text-align:left;cursor:pointer}
      .wb-project-shortcut:hover,.wb-project-shortcut[aria-current=page]{background:var(--dsw-alias-interactive-bg-hover)}
      .wb-project-shortcut>span:first-of-type{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
      .wb-project-shortcuts{padding:0 8px 8px}
      .wb-project-tree{display:flex;flex-direction:column;flex:1;min-height:0;overflow:auto}
      .wb-project-tree>div:first-child{flex:0 0 auto}
      .wb-page .wb-primary{background:var(--dsw-alias-label-primary);color:var(--dsw-alias-bg-base);border-color:transparent}
      .wb-work-area{display:flex;height:100%;min-height:0;min-width:0;position:relative;background:var(--dsw-alias-bg-base)}
      .wb-native-conversation{flex:1;min-width:0;height:100%;overflow:hidden}
      .wb-blank-actions{position:absolute;top:8px;right:48px;z-index:2}
      .wb-companion{width:clamp(260px,27vw,390px);min-height:0;border-left:1px solid var(--dsw-alias-border-l3);display:flex;flex-direction:column;background:var(--dsw-alias-bg-base)}
      .wb-companion .wb-content{padding:14px 16px 24px}
      .wb-companion .wb-section{padding:12px;margin:10px 0}
      .wb-companion-head{display:flex;align-items:center;justify-content:space-between;gap:8px;padding:12px 16px;border-bottom:1px solid var(--dsw-alias-border-l3);flex-wrap:wrap}
      .wb-companion-head strong{font-size:14px}
      .wb-companion-scroll{flex:1;min-height:0;overflow:auto}
      .wb-rules-editor{padding:14px 16px}
      .wb-rules-editor textarea{min-height:40vh;font-family:ui-monospace,monospace}
      .wb-tabs{display:flex;gap:8px;margin:0 0 16px;flex-wrap:wrap}
      .wb-tabs [aria-pressed=true]{background:var(--dsw-alias-interactive-bg-hover);font-weight:600}
      .wb-resource-table{overflow:auto;margin:12px 0}
      .wb-resource-table table{border-collapse:collapse;width:100%;font-size:13px}
      .wb-resource-table th,.wb-resource-table td{padding:8px 10px;border-bottom:1px solid var(--dsw-alias-border-l3);max-width:260px;text-align:left;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
      .wb-detail-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:12px}
      .wb-detail-grid pre{font:inherit;white-space:pre-wrap;overflow-wrap:anywhere;margin:4px 0 0}
      @media(max-width:760px){.wb-companion{position:absolute;z-index:6;inset:0 0 0 auto;width:min(100%,390px);box-shadow:-8px 0 24px #0001}.wb-project-shortcuts{padding-inline:4px}}
    `;
    const markdown = text => h(UI.MarkdownText,{text,omitHtml:true,labels:{
      code:{copyLabel:"复制",copiedLabel:"已复制",toolbarLabels:{codeLabel:"代码",wrapLabel:"换行",unwrapLabel:"不换行"}},
      footnotes:"引用",
    }});
    const alert = message => message && h("p",{role:"alert",className:"wb-note"},message);
    const query = (path, parameters) => path + "?" + new URLSearchParams(parameters);
    const typeLabel = type => type === "ordinary" ? "普通" : "专项";
    function ResourceRecords({rows,fields,corpus,open}) {
      if(!rows.length)return h("p",{className:"wb-muted"},"暂无记录");
      if(corpus)return h("div",{className:"wb-list"},...rows.map(row=>{
        const title=[row.fields.title,row.fields.name,row.id].find(v=>(typeof v==="string"&&v.trim())||typeof v==="number");
        return h("details",{className:"wb-section",key:row.id},h("summary",null,title),
          markdown(String(row.fields.markdown??row.fields.content??row.fields.text??Object.values(row.fields).join("\n\n"))));
      }));
      const columns=(fields.length?fields:Object.keys(rows[0].fields)).slice(0,8);
      const cell=value=>value==null?"—":typeof value==="object"?JSON.stringify(value):String(value);
      return h("div",{className:"wb-resource-table"},h("table",null,
        h("thead",null,h("tr",null,...columns.map(k=>h("th",{key:k,scope:"col"},k)),h("th",{scope:"col"},"详情"))),
        h("tbody",null,...rows.map(row=>h("tr",{key:row.id},
          ...columns.map(k=>h("td",{key:k,title:cell(row.fields[k])},cell(row.fields[k]))),
          h("td",null,button("查看",()=>open(row),{"aria-label":"查看记录 "+row.id})))))));
    }
    function ReadableResources({project,close,manage}) {
      const [data,setData]=useState(),[error,setError]=useState(""),[notice,setNotice]=useState(""),[busy,setBusy]=useState("");
      const editing=useRef(false);
      const load=async()=>{const [resources,grants]=await Promise.all([api("/resources"),api("/grants")]);setData({resources,grants})};
      useEffect(()=>{load().catch(e=>setError(e.message))},[project.id]);
      const save=async(resourceId,sections)=>{
        if(editing.current)return;
        editing.current=true;setBusy(resourceId);setError("");setNotice("");
        try{
          await api("/grants",{resourceId,projectId:project.id,sections});
          setData(v=>({...v,grants:[...v.grants.filter(g=>g.projectId!==project.id||g.resourceId!==resourceId),{projectId:project.id,resourceId,sections}]}));
          setNotice("读取范围已保存");
        }catch(e){setError(e.message)}finally{setBusy("");editing.current=false}
      };
      return h(UI.Modal,{open:true,title:project.name+" · 可读资料",onClose:close,closeLabel:"关闭"},
        h("section",{className:"wb-page",style:{maxHeight:"68vh",padding:"12px 20px"}},
          h("p",{className:"wb-muted"},"选择这个项目的对话可以读取的资料板块。"),alert(error),notice&&h("p",{role:"status"},notice),
          !data&&!error&&h("p",{role:"status"},"正在读取资料…"),
          data&&h("div",{className:"wb-list"},...data.resources.map(r=>{
            const own=r.ownerProject===project.id,maintains=r.maintainerProject===project.id;
            const raw=data.grants.filter(g=>g.projectId===project.id&&g.resourceId===r.id).flatMap(g=>g.sections);
            const selected=raw.includes("*")?r.sections.map(s=>s.id):raw;
            return h("div",{className:"wb-row",key:r.id},h("div",{className:"wb-row-main"},
              h("strong",null,r.name),own||maintains?h("p",{className:"wb-muted"},own?"本项目资料":"由本项目维护 · 可读"):r.sections.map(s=>
                h("label",{className:"wb-check",key:s.id},h("input",{type:"checkbox",checked:selected.includes(s.id),disabled:!!busy,onChange:e=>
                  save(r.id,e.target.checked?[...selected,s.id]:selected.filter(id=>id!==s.id))}),s.name||s.id))));
          }),!data.resources.length&&h("p",{className:"wb-muted"},"暂无可管理资料")),
          h("div",{className:"wb-actions"},button("在知识库管理",manage),button("关闭",close))));
    }
    const onEvent = (event, fn) => { window.addEventListener(event,fn); return () => window.removeEventListener(event,fn); };
    const notifyProjects = () => window.dispatchEvent(new Event("wb-project-list"));
    function useProjectList() {
      const [state,setState]=useState({});
      useEffect(()=>{
        let request=0,alive=true;
        const read=()=>{const n=++request;api("/project-list").then(data=>{
          if(alive&&n===request){rememberPersonal(data.personalSessionId);setState({data})}
        }).catch(error=>{if(alive&&n===request)setState(previous=>({...previous,error:error.message}))});};
        read();
        const detach=onEvent("wb-project-list",read);
        return()=>{alive=false;detach()};
      },[]);
      return state;
    }
    function NewProject({onClose,onCreated}) {
      const [name,setName]=useState(""),[type,setType]=useState("ordinary"),[busy,setBusy]=useState(false),[error,setError]=useState(""),[created,setCreated]=useState();
      const pending=useRef(false);
      return h(UI.Modal,{open:true,title:"新建项目",closeLabel:"关闭",onClose:()=>{if(!pending.current)onClose()}},
        h("div",{className:"wb-page"},h("form",{className:"wb-form",onSubmit:async event=>{
          event.preventDefault();if(pending.current)return;
          pending.current=true;setBusy(true);setError("");
          try{
            const p=created||await api("/project/create",{name,type});
            setCreated(p);notifyProjects();
            await onCreated(p);onClose();
          }catch(e){setError(e.message)}
          finally{pending.current=false;setBusy(false)}
        }},
        h("fieldset",{disabled:busy||!!created,className:"wb-form"},
          h("legend",null,"项目类型"),
          ...[["ordinary","普通项目","在项目内创建多个对话。"],["auto-reply","专项项目 · 自动化应答","使用现有的客户会话与应答设置。"],["auto-operations","专项项目 · 自动化运营","核对名单、用户档案和运营设置。"]].map(([id,label,hint])=>
            h("label",{key:id,className:"wb-check"},h("input",{type:"radio",name:"project-type",checked:type===id,onChange:()=>setType(id)}),h("span",null,label,h("small",{className:"wb-muted",style:{display:"block"}},hint)))),
          field("项目名称",h("input",{required:true,maxLength:80,value:name,onChange:e=>setName(e.target.value),placeholder:type==="ordinary"?"例如：产品研究":"例如：自动化应答"}))),
        h("p",{className:"wb-muted"},"项目类型在创建时确定。"),
        alert(error),
        h("div",{className:"wb-actions"},button("取消",onClose,{disabled:busy}),
          h("button",{type:"submit",className:"wb-primary",disabled:busy||!name.trim()},busy?"正在打开…":created?"打开已创建项目":"创建项目")))));
    }
    function ProjectsPage({openProject}) {
      const {data,error}=useProjectList();
      const [create,setCreate]=useState(false),[search,setSearch]=useState(""),[openError,setOpenError]=useState("");
      const rows=(data?.projects||[]).filter(p=>p.name.toLocaleLowerCase().includes(search.trim().toLocaleLowerCase()));
      return h("section",{className:"wb-page"},h("div",{className:"wb-content"},
        h("div",{className:"wb-heading"},h("h1",null,"项目"),button("新建项目",()=>setCreate(true),{className:"wb-primary"})),
        field("查找项目",h("input",{type:"search",value:search,onChange:e=>setSearch(e.target.value),placeholder:"搜索项目名称"})),
        alert(error||openError),
        !data&&!error&&h("p",{role:"status"},"正在读取项目…"),
        data&&h("div",{className:"wb-list",style:{marginTop:16}},...rows.map(p=>
          h("div",{className:"wb-row",key:p.id},h("div",{className:"wb-row-main"},h("strong",null,p.name)),
            h("span",{className:"wb-tag"},typeLabel(p.type)),button("打开",()=>{setOpenError("");openProject(p).catch(e=>setOpenError(e.message))},{"aria-label":"打开 "+p.name}))),
          !rows.length&&h("p",{className:"wb-muted"},search?"没有匹配的项目":"还没有项目")),
        create&&h(NewProject,{onClose:()=>setCreate(false),onCreated:openProject})));
    }
    const ruleDrafts=new Map();
    function TodayRules() {
      const [files,setFiles]=useState(),[name,setName]=useState("SOUL.md"),[preview,setPreview]=useState(false),[error,setError]=useState(""),[notice,setNotice]=useState(""),[busy,setBusy]=useState(false);
      const load=()=>api("/today-rules").then(rows=>{
        setError("");
        setFiles(Object.fromEntries(rows.map(f=>{
          const previous=ruleDrafts.get(f.name);
          if(!previous)return [f.name,{...f,draft:f.content}];
          return [f.name,{...previous,latest:previous.revision!==f.revision?f:undefined}];
        })));
      }).catch(e=>setError(e.message));
      useEffect(()=>{load()},[]);
      const file=files?.[name],dirty=file&&file.content!==file.draft;
      const download=()=>{
        const url=URL.createObjectURL(new Blob([file.draft],{type:"text/markdown;charset=utf-8"}));
        const a=document.createElement("a");a.href=url;a.download=name;a.click();setTimeout(()=>URL.revokeObjectURL(url),0);
      };
      const save=async e=>{
        e.preventDefault();if(busy||!dirty||file.latest)return;setBusy(true);setError("");setNotice("");
        try{
          const saved=await api("/today-rules",{name,content:file.draft,revision:file.revision});
          ruleDrafts.delete(name);
          setFiles(v=>({...v,[name]:{...saved,draft:saved.content}}));setNotice("已保存，下一轮生效");
        }catch(e){setError(e.message)}finally{setBusy(false)}
      };
      return h("section",{className:"wb-rules-editor wb-page","aria-label":"今日 Markdown 配置"},
        h("h2",null,"今日配置"),h("p",{className:"wb-muted"},"设置和今日 Agent 的合作方式。"),
        alert(error),notice&&h("p",{role:"status"},notice),
        !file&&!error&&h("p",{role:"status"},"正在读取配置…"),
        h("div",{className:"wb-tabs"},...["SOUL.md","AGENT.md"].map(n=>button(n,()=>{setName(n);setPreview(false);setNotice("")},{key:n,"aria-pressed":name===n,disabled:busy}))),
        file&&h("form",{className:"wb-form",onSubmit:save},
          file.latest&&h("details",{className:"wb-section"},h("summary",null,"原文件已更新，草稿已保留"),
            markdown(file.latest.content||"当前文件为空"),
            button("保留草稿，按最新版本继续编辑",()=>{
              const merged={...file.latest,draft:file.draft};ruleDrafts.set(name,merged);setFiles(v=>({...v,[name]:merged}));
            })),
          h("div",{className:"wb-actions"},button(preview?"编辑":"预览",()=>setPreview(v=>!v)),button("下载",download),dirty&&h("span",{className:"wb-muted"},"有未保存的修改")),
          preview?h("div",{className:"wb-section"},markdown(file.draft||"暂无内容")):
            field(name,h("textarea",{value:file.draft,disabled:busy,onChange:e=>{
              const updated={...file,draft:e.target.value};
              if(updated.draft===updated.content)ruleDrafts.delete(name);else ruleDrafts.set(name,updated);
              setFiles(v=>({...v,[name]:updated}));
            }})),
          h("button",{type:"submit",className:"wb-primary",disabled:busy||!dirty||!!file.latest},busy?"正在保存…":"保存")));
    }
    function TodayConversation({children,openSession}) {
      const [pane,setPane]=useState(()=>window.innerWidth>760?"follow":"");
      const [rulesOpened,setRulesOpened]=useState(false);
      const showRules=()=>{setRulesOpened(true);setPane("rules")};
      return h("div",{className:"wb-work-area"},
        h("div",{className:"wb-native-conversation"},children),
        !pane&&h("div",{className:"wb-companion-head",style:{position:"absolute",top:0,right:0}},
          button("关注",()=>setPane("follow"),{className:"wb-inline"}),button("配置",showRules,{className:"wb-inline"})),
        h("aside",{className:"wb-companion",style:pane?undefined:{display:"none"},"aria-label":"今日辅助栏"},
          h("div",{className:"wb-companion-head"},h("strong",null,pane==="rules"?"配置":"关注中的事"),
            h("div",{className:"wb-actions"},button(pane==="rules"?"关注":"配置",pane==="rules"?()=>setPane("follow"):showRules,{className:"wb-inline"}),
              button("收起",()=>setPane(""),{className:"wb-inline","aria-label":"收起今日辅助栏"}))),
          h("div",{className:"wb-companion-scroll",hidden:pane!=="follow"},
            h(TodayPage,{openSession,embedded:true})),
          rulesOpened&&h("div",{className:"wb-companion-scroll",hidden:pane!=="rules"},h(TodayRules))));
    }
    function ExpertsPage() {
      const [rows,setRows]=useState(),[selected,setSelected]=useState(),[search,setSearch]=useState(""),[error,setError]=useState("");
      const [draft,setDraft]=useState(),[saving,setSaving]=useState(false),[editError,setEditError]=useState("");
      const request=useRef(0);
      useEffect(()=>{let live=true;api("/experts").then(v=>{if(live)setRows(v)}).catch(e=>{if(live)setError(e.message)});return()=>{live=false;request.current++}},[]);
      const show=async name=>{
        const n=++request.current;setError("");setEditError("");setDraft(undefined);setSelected({name,loading:true});
        try{const result=await api(query("/experts",{name}));if(n===request.current)setSelected(result)}
        catch(e){if(n===request.current){setSelected(undefined);setError(e.message)}}
      };
      const save=async e=>{
        e.preventDefault();if(saving)return;
        setSaving(true);setEditError("");
        try{setSelected(await api("/experts",{name:selected.name,content:draft,revision:selected.revision}));setDraft(undefined)}
        catch(e){setEditError(e.message)}finally{setSaving(false)}
      };
      return h("section",{className:"wb-page"},h("div",{className:"wb-content"},h("div",{className:"wb-heading"},h("h1",null,"专家")),
        h("p",{className:"wb-muted"},"专家是可在对话中调用的方法。"),
        field("搜索专家",h("input",{type:"search",value:search,onChange:e=>setSearch(e.target.value)})),
        alert(error),!rows&&!error&&h("p",{role:"status"},"正在读取专家…"),
        h("div",{className:"wb-list",style:{marginTop:16}},...(rows||[]).filter(r=>(r.name+" "+r.description).toLowerCase().includes(search.trim().toLowerCase())).map(r=>
          h("div",{key:r.name,className:"wb-row"},h("div",{className:"wb-row-main"},h("strong",null,r.name),h("p",{className:"wb-muted"},r.description)),button("查看方法",()=>show(r.name),{"aria-label":"查看 "+r.name+" 的方法"})))),
        selected&&h(UI.Modal,{open:true,title:selected.name,closeLabel:"关闭",onClose:()=>{
          if(saving||draft!==undefined&&draft!==selected.content&&!window.confirm("放弃未保存的方法修改？"))return;
          request.current++;setSelected(undefined);setDraft(undefined)
        }},
          h("article",{className:"wb-page",style:{maxHeight:"65vh",padding:"12px 20px"}},selected.loading?h("p",{role:"status"},"正在读取方法…"):
            draft!==undefined?h("form",{className:"wb-form",onSubmit:save},alert(editError),field("方法正文",h("textarea",{value:draft,rows:14,disabled:saving,onChange:e=>setDraft(e.target.value)})),
              h("div",{className:"wb-actions"},button("取消编辑",()=>setDraft(undefined),{disabled:saving}),h("button",{type:"submit",className:"wb-primary",disabled:saving||!draft.trim()},"保存方法"))):
            h(React.Fragment,null,selected.revision?button("编辑方法",()=>setDraft(selected.content)):h("p",{className:"wb-muted"},"已安装方法 · 只读"),
              markdown(selected.content),...(selected.references||[]).map(r=>h("details",{key:r.name},h("summary",null,r.name),markdown(r.content))))))));
    }
    function MCPPage({openNativePlugins}) {
      const [rows,setRows]=useState(),[error,setError]=useState(""),[selected,setSelected]=useState(),[results,setResults]=useState({}),[busy,setBusy]=useState(false);
      const load=()=>{setError("");api("/mcp-catalog").then(setRows).catch(e=>setError(e.message))};
      useEffect(()=>{load()},[]);
      const test=async name=>{
        setBusy(true);setResults(v=>({...v,[name]:undefined}));
        try{const r=await api("/mcp-test",{name});setResults(v=>({...v,[name]:r}))}
        catch(e){setResults(v=>({...v,[name]:{error:e.message}}))}finally{setBusy(false)}
      };
      const result=selected&&results[selected.name];
      return h("section",{className:"wb-page"},h("div",{className:"wb-content"},
        h("div",{className:"wb-heading"},h("h1",null,"MCP 插件"),h("div",{className:"wb-actions"},
          button("Harness 插件",openNativePlugins),button("刷新",load))),
        h("p",{className:"wb-muted"},"查看已登记的标准工具连接。配置状态与连接检测分别显示。"),
        alert(error),!rows&&!error&&h("p",{role:"status"},"正在读取插件…"),
        h("div",{className:"wb-list"},...(rows||[]).map(r=>
          h("div",{key:r.name,className:"wb-row"},h("div",{className:"wb-row-main"},h("strong",null,r.label),h("div",{className:"wb-muted"},"Streamable HTTP")),
            h("span",{className:"wb-tag"},results[r.name]?.tools?"已连接":results[r.name]?.error?"测试未通过":r.configured?"已配置 · 未测试":"缺少凭证"),
            button("连接配置",()=>setSelected(r),{"aria-label":r.label+"连接配置"})))),
        selected&&h(UI.Modal,{open:true,title:selected.label+" · 连接配置",closeLabel:"关闭",onClose:()=>setSelected(undefined)},
          h("div",{className:"wb-page",style:{maxHeight:"65vh",padding:"16px 20px"}},
            h("dl",null,h("dt",null,"服务"),h("dd",null,selected.endpoint||"工作台内置服务"),h("dt",null,"凭证"),h("dd",null,selected.configured?"已配置":"未配置")),
            h("p",{className:"wb-muted"},"连接由宿主管理。测试只检查工具目录。"),
            button(busy?"正在测试…":"测试连接",()=>test(selected.name),{disabled:busy||!selected.configured}),
            alert(result?.error),
            result?.tools&&h("div",{className:"wb-list"},h("h2",null,"工具预览 · "+result.tools.length),...result.tools.map(tool=>h("div",{className:"wb-row",key:tool.name},
              h("div",{className:"wb-row-main"},h("strong",null,tool.name),h("p",{className:"wb-muted"},tool.description)))))))));
    }
    const time = value => value ? new Date(value).toLocaleString("zh-CN",{timeZone:"Asia/Shanghai",month:"2-digit",day:"2-digit",hour:"2-digit",minute:"2-digit"}) : "尚未记录";
    const stateText = r => r.stats?.failed ? `已保留 ${r.stats.succeeded} 条 · ${r.stats.failed} 条失败` : ({Running:"执行中",Completed:r.published?"结果已发布":"执行已结束",Failed:"执行失败",Canceled:"已停止",Terminated:"已终止",TimedOut:"已超时",Unknown:"状态待核实"}[r.execution]||r.execution);
    const receiptText = r => {const s=r.deliveries?.states;if(!s)return "";return `渠道回执：待提交 ${s.prepared||0} · 已受理 ${s.accepted||0} · 待手机确认 ${s.pending_confirmation||0} · 已发送 ${s.sent||0} · 已送达 ${s.delivered||0} · 失败 ${s.failed||0} · 待核实 ${(s.unknown||0)+(s.submitting||0)}`};
    const scheduleHours=s=>s.hours.map(hour=>String(hour).padStart(2,"0")+":"+String(s.minute).padStart(2,"0")).join("、");
    function ScheduleRows({rows, openSession, act, busy, openPanel}) {
      const [edit,setEdit]=useState(null);
      const results={Running:"执行中",Completed:"执行完成",Failed:"执行失败",Canceled:"已停止",TimedOut:"已超时",Terminated:"已终止"};
      return rows.map(s=>h("div",{key:s.id,className:"wb-row"},
        h("div",{className:"wb-row-main"},h("strong",null,s.title),
          h("div",{className:"wb-muted"},scheduleHours(s)+" · 北京时间"),
          h("div",null,s.state!=="ready"?"定时状态待核实":s.paused?"已暂停":"下次 "+time(s.next?.[0])),
          s.last&&h("div",{className:"wb-muted"},"上次 "+time(s.last)+" · "+(results[s.result]||"结果待核实")),
          s.strategy&&h("details",null,h("summary",null,"执行信息"),
            h("p",null,"原策略 "+s.strategy+(s.versions?.length?" · 原包冻结版本 "+s.versions.join("、"):"")),
            h("p",null,s.experts?.length?"原专家方法 "+s.experts.join("、"):"等待原工作包回读实际专家"),
            s.source&&h("p",{className:"wb-muted"},"MCP 连接 "+s.source+" · 原包读取与稿件执行"),
            openPanel&&h("div",{className:"wb-actions"},
              button("专家",()=>openPanel("wb-experts")),button("MCP 插件",()=>openPanel("wb-mcp"))),
            edit?.id===s.id?h("form",{className:"wb-form",onSubmit:e=>{e.preventDefault();act(async()=>{
              await api("/operations/original-schedule?projectId="+encodeURIComponent(s.projectId),{strategy:s.strategy,time:edit.time});
              setEdit(null);
            })}},field("自动运行时间",h("input",{type:"time",required:true,value:edit.time,onChange:e=>setEdit({...edit,time:e.target.value})})),
              h("div",{className:"wb-actions"},h("button",{type:"submit",disabled:busy},"保存"),button("取消",()=>setEdit(null))))
              :button("调整时间",()=>setEdit({id:s.id,time:scheduleHours(s)}),{disabled:busy}))),
        !s.strategy&&button("查看对话",()=>openSession(s.sessionId)),
        button(s.paused?"恢复":"暂停",()=>act(()=>api("/schedules/pause",{id:s.id,paused:!s.paused})),{disabled:busy||s.state!=="ready"})));
    }
    function SchedulesPage({openSession,openPanel}) {
      const [rows,setRows]=useState(null),[error,setError]=useState(""),[busy,setBusy]=useState(false);
      const load=()=>api("/schedules").then(setRows);
      useEffect(()=>{let live=true;const refresh=()=>load().catch(e=>{if(live)setError(e.message)});refresh();
        const timer=setInterval(refresh,15000);return()=>{live=false;clearInterval(timer)}},[]);
      const act=async fn=>{setBusy(true);setError("");try{await fn();await load()}catch(e){setError(e.message)}finally{setBusy(false)}};
      return h("section",{className:"wb-page"},h("style",null,css),h("div",{className:"wb-content"},
        h("div",{className:"wb-heading"},h("h1",null,"定时任务")),alert(error),
        !rows?h("p",{role:"status"},"正在读取定时任务…"):
          rows.length?h(ScheduleRows,{rows,openSession,act,busy,openPanel}):h("p",{className:"wb-muted"},"暂无定时任务。")));
    }
    function TodayPage({ openSession, embedded }) {
      const [data,setData]=useState(),[error,setError]=useState(""),[notice,setNotice]=useState(""),[busy,setBusy]=useState(false),[edit,setEdit]=useState(),[answers,setAnswers]=useState({}),[settings,setSettings]=useState(),[notifications,setNotifications]=useState();
      const load=async()=>{const [runs,decisions,memory,config,notify,targets,schedules]=await Promise.all([api("/runs"),api("/decisions"),api("/memory"),api("/personal/settings"),api("/notifications"),api("/notification-targets"),api("/schedules")]);setError("");setData({runs,decisions,memory,config,targets,schedules});setSettings(v=>v||config);setNotifications(v=>v||{...notify,sessionId:config.sessionId,targets:notify.targets||[]})};
      useEffect(()=>{let live=true;const refresh=()=>load().catch(e=>{if(live)setError(e.message)});refresh();const id=setInterval(refresh,15000);return()=>{live=false;clearInterval(id)}},[]);
      const act=async fn=>{setBusy(true);setError("");setNotice("");try{await fn();await load();setNotice("已保存")}catch(e){setError(e.message)}finally{setBusy(false)}};
      return h("section",{className:"wb-page"},h("style",null,css),h("div",{className:"wb-content"},
        !embedded&&h("div",{className:"wb-heading"},h("h1",null,"今日")),
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
          h(ScheduleRows,{rows:data.schedules,openSession,act,busy})),
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
    let personalSessionId="";
    const subscribePersonal=listener=>onEvent("wb-personal-session",listener);
    const personalSnapshot=()=>personalSessionId;
    function rememberPersonal(id){
      if(id===personalSessionId)return;
      personalSessionId=id;
      window.dispatchEvent(new Event("wb-personal-session"));
    }
    let ctxReplyBack=()=>{};
    let replyWorkspaceIds=new Set();
    const sessionViews=new WeakMap(),workspaceViews=new WeakMap();
    const visibleSession=id=>id!==personalSessionId&&!String(id).startsWith('object-');
    function filteredSessions(state){
      const previous=sessionViews.get(state);if(previous?.personal===personalSessionId)return previous.value;
      const byId=Object.fromEntries(Object.entries(state.byId||{}).filter(([id])=>visibleSession(id)));
      const value={...state,ids:(state.ids||[]).filter(visibleSession),byId};sessionViews.set(state,{personal:personalSessionId,value});return value;
    }
    function filteredWorkspaces(state){
      const previous=workspaceViews.get(state);if(previous?.generation===replyWorkspaceIds)return previous.value;
      const value={...state,items:(state.items||[]).filter(w=>!replyWorkspaceIds.has(w.workspaceId)).map(w=>({...w,sessionIds:(w.sessionIds||[]).filter(visibleSession)})),archivedSessionIds:(state.archivedSessionIds||[]).filter(visibleSession),pinnedSessionIds:(state.pinnedSessionIds||[]).filter(visibleSession)};
      workspaceViews.set(state,{generation:replyWorkspaceIds,value});return value;
    }
    async function replyProjects(){
      const [projects,catalog]=await Promise.all([api('/reply-projects'),api('/catalog')]);
      return{projects:projects.filter(c=>catalog.projects.some(p=>p.id===c.projectId)),catalog};
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
    function ReplyPage({openSession,sessionId='',selectedProject=''}){
      const UI=require('@deepseek-ai/dsh-client-ui-primitives'),{Switch,Modal,MarkdownText,IconSettingsOutlineRegular,IconCloseOutlineRegular,IconPaperPlaneOutlineRegular,IconChevronLeftOutlineRegular,IconAgentPresetOutlineRegular}=UI;
      const [catalog,setCatalog]=useState(),[projects,setProjects]=useState([]),[project,setProject]=useState(replyProject),[page,setPage]=useState({items:[]}),[q,setQ]=useState(''),[cursor,setCursor]=useState(''),[trail,setTrail]=useState([]),[route,setRoute]=useState(),[timeline,setTimeline]=useState({items:[]}),[edit,setEdit]=useState(),[activeSettings,setActiveSettings]=useState(),[accounts,setAccounts]=useState([]),[experts,setExperts]=useState([]),[resources,setResources]=useState([]),[settings,setSettings]=useState(false),[modal,setModal]=useState(),[error,setError]=useState(''),[notice,setNotice]=useState(''),[busy,setBusy]=useState(false),[loading,setLoading]=useState(true),[tick,setTick]=useState(0),[draft,setDraft]=useState(''),[intent,setIntent]=useState();
      const stream=useRef(null),stick=useRef(true),sending=useRef(false),timelineRef=useRef([]),refreshOffset=useRef(0);
      useEffect(()=>{timelineRef.current=timeline.items},[timeline.items]);
      const reset=()=>{setCursor('');setTrail([])};
      const reloadAccounts=async id=>{try{setAccounts(await api('/reply-accounts?projectId='+encodeURIComponent(id||project)))}catch(e){setError(e.message)}};
      useEffect(()=>{let current=true;replyProjects().then(async v=>{if(!current)return;setCatalog(v.catalog);setProjects(v.projects);let id=selectedProject||replyProject||v.projects[0]?.projectId||'';if(sessionId){const r=await api('/reply-session?sessionId='+encodeURIComponent(sessionId));if(!current)return;setRoute(r);id=r.principal.projectId}setProject(id);replyProject=id;setLoading(false)}).catch(e=>{if(current){setError(e.message);setLoading(false)}});return()=>{current=false}},[sessionId,selectedProject]);
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
    let operationsProject = "";
    const operationsCSS = `
      .op-page{padding:0 clamp(18px,3vw,32px) 28px;box-sizing:border-box}
      .op-header{min-height:66px;display:flex;align-items:center;justify-content:space-between;gap:12px;border-bottom:1px solid var(--dsw-alias-border-l3)}
      .op-header h1{margin:0;font-size:20px;font-weight:600}
      .op-header button.op-link{border-color:transparent}
      .op-tabs{display:flex;gap:16px;border-bottom:1px solid var(--dsw-alias-border-l3);margin-bottom:12px}
      .op-tabs button{border:0;border-bottom:2px solid transparent;border-radius:0;padding:14px 10px}
      .op-tabs button[aria-selected=true]{border-color:var(--dsw-alias-brand-primary,#2860e9);color:var(--dsw-alias-brand-primary,#2860e9)}
      .op-strategy{display:grid;grid-template-columns:minmax(0,1fr) auto auto;gap:clamp(8px,3vw,40px);align-items:center;padding:16px 8px;border-bottom:1px solid var(--dsw-alias-border-l3)}
      .op-title{font-size:22px;font-weight:600;margin:18px 0}
      .op-detail{display:grid;grid-template-columns:1.1fr 1fr;align-items:start;border-top:1px solid var(--dsw-alias-border-l3)}
      .op-table{width:100%;border-collapse:collapse;table-layout:fixed;text-align:left}
      .op-table th,.op-table td{padding:12px 8px;overflow-wrap:anywhere;border-bottom:1px solid var(--dsw-alias-border-l3);vertical-align:top}
      .op-table th:first-child{width:23%}.op-table th:last-child{width:21%}
      .op-reason{display:-webkit-box;-webkit-box-orient:vertical;-webkit-line-clamp:2;overflow:hidden}
      .op-table button{border:0;background:transparent;padding:0;text-align:left}
      .op-table tr[aria-selected=true]{background:var(--dsw-alias-bg-hover,rgba(63,106,233,.10))}
      .op-message{padding:16px 18px;border-left:1px solid var(--dsw-alias-border-l3);min-width:0}
      .op-message h2{margin:0 0 14px;font-size:18px}
      .op-message p{white-space:pre-wrap;overflow-wrap:anywhere}
      .op-message section{border-top:1px solid var(--dsw-alias-border-l3);padding:12px 0}
      .op-message section p{margin:5px 0}
      .op-review summary{cursor:pointer;font-weight:600}
      .op-review summary:focus-visible{outline-offset:4px}
      .op-judgments{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px;margin:12px 0}
      .op-judgments article,.op-card{padding:12px;border:1px solid var(--dsw-alias-border-l3);border-radius:8px;min-width:0}
      .op-card{margin-top:10px}.op-card code{overflow-wrap:anywhere}.op-card img{display:block;max-width:100%;height:auto;margin:8px 0}
      .op-editor textarea{box-sizing:border-box;width:100%;min-height:160px;resize:vertical;font:inherit;padding:10px;border:1px solid var(--dsw-alias-border-l3);border-radius:8px;background:transparent;color:inherit}
      .op-editor .wb-actions{margin-top:8px}
      .op-profiles{display:grid}.op-profiles button{text-align:left;border:0;border-radius:0;border-bottom:1px solid var(--dsw-alias-border-l3);padding:13px 8px}
      .op-fields{max-width:760px}.op-fields div{display:grid;grid-template-columns:minmax(120px,28%) minmax(0,1fr);gap:10px;padding:14px 0;border-bottom:1px solid var(--dsw-alias-border-l3)}
      .op-fields dd{margin:0;white-space:pre-wrap;overflow-wrap:anywhere}
      .op-groups{display:grid;gap:20px}.op-groups h2{margin:0 0 8px}.op-field-source{display:block;margin-top:5px;color:var(--dsw-alias-text-l2);font-size:12px}.op-search{display:flex;gap:8px;margin:12px 0}.op-search input{flex:1;min-width:0}.op-packages h2{margin:16px 0 4px}.op-counts{display:flex;gap:12px;flex-wrap:wrap}
      .op-footer{padding-top:16px}.op-modal-content{padding:8px 0 0}.op-modal-content .wb-actions{justify-content:flex-end}
      @media(max-width:650px){.op-detail,.op-judgments{grid-template-columns:1fr}.op-message{border-left:0;padding:16px 0}.op-table th,.op-table td{padding:10px 5px}.op-fields div{grid-template-columns:minmax(90px,35%) minmax(0,1fr)}.op-header{align-items:flex-start;padding:12px 0}.op-strategy{padding-left:0;padding-right:0}.op-page .wb-actions{gap:5px}}
    `;

    function shanghaiWeek() {
      const day=new Date(Date.now()+8*3600*1000);
      day.setUTCHours(0,0,0,0);
      day.setUTCDate(day.getUTCDate()+4-(day.getUTCDay()||7));
      const year=day.getUTCFullYear();
      const week=Math.ceil(((day-new Date(Date.UTC(year,0,1)))/86400000+1)/7);
      return year+"-W"+String(week).padStart(2,"0");
    }

    function OperationsPage({projectId, openSchedules}) {
      const [view,setView]=useState(null),[tab,setTab]=useState("work"),[page,setPage]=useState("home");
      const [runId,setRunId]=useState(""),[selected,setSelected]=useState(""),[profile,setProfile]=useState(null);
      const [profiles,setProfiles]=useState([]),[profileNext,setProfileNext]=useState(""),[profileState,setProfileState]=useState("");
      const [profileMeta,setProfileMeta]=useState({}),[searchText,setSearchText]=useState(""),[query,setQuery]=useState("");
      const [today,setToday]=useState(null),[todayError,setTodayError]=useState("");
      const [sourcePlan,setSourcePlan]=useState(null),[profileBack,setProfileBack]=useState("home");
      const [sourceCopy,setSourceCopy]=useState(null),[sourceConfirm,setSourceConfirm]=useState(null),[sourceSubmission,setSourceSubmission]=useState(""),[excluded,setExcluded]=useState([]),[sourceItem,setSourceItem]=useState(null);
      const [copyEdit,setCopyEdit]=useState(null),[copyMessage,setCopyMessage]=useState("");
      const [history,setHistory]=useState([]),[historyMore,setHistoryMore]=useState(false);
      const [sourceHistory,setSourceHistory]=useState(null),[sourceHistoryWeek,setSourceHistoryWeek]=useState("");
      const [settings,setSettings]=useState(null),[experts,setExperts]=useState([]),[accounts,setAccounts]=useState([]);
      const [error,setError]=useState(""),[busy,setBusy]=useState(false),[review,setReview]=useState("");
      const [reload,setReload]=useState(0);
      const path=(name,params={})=>"/operations/"+name+"?"+new URLSearchParams({projectId,...params});
      const refresh=()=>setReload(n=>n+1);
      const readSource=!!view?.config.readSource;
      useEffect(()=>{
        let active=true,timer;
        const read=()=>api(path("view",runId||review?{runId:runId||review}:{})).then(data=>{
          if(!active)return;
          setView(data);
          if(page==="history"){setHistory(data.history);setHistoryMore(data.history.length===20)}
        }).catch(e=>{if(active)setError(e.message)}).finally(()=>{
          if(active&&page!=="history")timer=setTimeout(read,4000);
        });
        read();
        return()=>{active=false;clearTimeout(timer)};
      },[projectId,runId,review,reload,page]);
      useEffect(()=>{
        if(!readSource||tab!=="work"||page!=="home")return;
        let active=true;
        const load=()=>api(path("source-today")).then(data=>{if(active){setToday(data);setTodayError("")}})
          .catch(e=>{if(active)setTodayError(e.message)});
        load();
        const timer=setInterval(load,30000);
        return()=>{active=false;clearInterval(timer)};
      },[projectId,readSource,tab,page,reload]);
      useEffect(()=>{
        if(tab!=="profiles"||page!=="home")return;
        let active=true;
        setProfileState("loading");setProfileNext("");
        api(path("profiles",{q:query})).then(data=>{
          if(active){setProfiles(data.records);setProfileNext(data.next);setProfileState(data.state);setProfileMeta(data)}
        }).catch(e=>{if(active)setError(e.message)});
        return()=>{active=false};
      },[projectId,tab,reload,page,query]);
      const act=async fn=>{
        setBusy(true);setError("");
        try{await fn()}catch(e){setError(e.message)}finally{setBusy(false)}
      };
      const home=()=>{setView(null);setPage("home");setRunId("");setSelected("");setProfile(null);setCopyEdit(null);setCopyMessage("");setError("")};
      const openRun=id=>{if(id)setView(null);setRunId(id||"");setSelected("");setPage("detail")};
      const openProfile=id=>act(async()=>{
        const data=await api(path("profiles",{id,...(page==="home"&&profileMeta.snapshot?{snapshot:profileMeta.snapshot}:{})}));
        if(data.state==="source_changed"){refresh();throw Error("档案版本已更新，请从新列表重新打开。")}
        setProfileBack(page);setProfile(data);setPage("profile");
      });
      const loadSourceCopy=(plan,run="",item="")=>plan.daily
        ? api(path("source-today",{daily:plan.daily,item}))
        : api(path("source",{view:"package",package:plan.package_id,week:plan.week,run,item}));
      const loadSourceHistory=week=>act(async()=>{
        setSourceHistoryWeek(week);
        setSourceHistory(await api(path("source",{view:"package_history",week})));
      });
      const openPlan=plan=>{
        setSourcePlan(plan);setSourceCopy(null);setSourceItem(null);setSelected("");setCopyEdit(null);setCopyMessage("");
        setExcluded([]);setSourceConfirm(null);setSourceSubmission("");setPage("source-plan");
        act(async()=>setSourceCopy(await loadSourceCopy(plan,plan.run_id)));
      };
      const refreshSourceCopy=()=>act(async()=>{
        const copy=await loadSourceCopy(sourcePlan,sourcePlan.run_id);
        setSourceCopy(copy);setSourceItem(null);setCopyMessage("");
        refresh();
      });
      const confirmSource=()=>act(async()=>{
        const result=await api(path("source-confirm"),sourceConfirm);
        setSourceConfirm(null);
        setSourceSubmission(result.runId);
        setCopyMessage("正在查回本次确认结果。");
      });
      useEffect(()=>{
        if(page!=="source-plan"||!sourceSubmission)return;
        let live=true,timer;
        const poll=async()=>{
          try{
            const result=await api(path("source-package-status",{id:sourceSubmission}));
            if(!live)return;
            if(result.state==="Completed"){
              const data=await loadSourceCopy(sourcePlan,sourcePlan.run_id);
              if(!live)return;
              setSourceCopy(data);
              setSourceItem(null);
              setCopyMessage(result.result==="source_changed"?"名单或稿件有变化，请重新核对。":result.result==="needs_check"?"原中心交接结果待核实，请查看原包回执。":"原中心已记录本次确认，可在原包查看交接。");
              setSourceSubmission("");
              return;
            }
            if(["Failed","Canceled","Terminated","TimedOut"].includes(result.state)){
              setCopyMessage("确认结果待核实，请刷新原稿与回执。");
              return;
            }
          }catch(e){if(live)setError(e.message)}
          if(live)timer=setTimeout(poll,3500);
        };
        poll();
        return()=>{live=false;clearTimeout(timer)};
      },[page,sourcePlan,sourceSubmission]);
      const showSettings=kind=>act(async()=>{
        if(kind==="project"){
          const list=await api("/transports");
          setAccounts(list.filter(account=>account.kind==="crm-cli"&&account.enabled&&account.projects?.includes(projectId)));
          setSettings({kind,knowledge:view.config.knowledge,transport:view.config.transport,staffId:view.config.staffId});
        }else{
          setExperts(await api(path("experts")));
          setSettings({kind,...view.config[kind]});
        }
      });
      const saveSettings=e=>{
        e.preventDefault();
        act(async()=>{
          const {kind,knowledge,transport,staffId,expert,time}=settings;
          await api(path(kind==="project"?"settings":"schedule"),kind==="project"?{knowledge,transport,staffId}:{kind,expert,time});
          setSettings(null);refresh();
        });
      };
      const requestedRun=runId||review;
      const run=requestedRun&&view?.run.id!==requestedRun?null:view?.run;
      const row=run?.rows.find(item=>item.id===selected)||run?.rows[0];
      const stateText={unconfigured:"等待接入已授权的真实资料",awaiting:"等待当天的定时运行",running:"正在生成",unavailable:"本轮资料或生成结果暂不可用"};
      const startReview=()=>act(async()=>{
        await api(path("send"),{runId:run.id});
        setReview(run.id);refresh();
      });
      const confirm=()=>act(async()=>{
        const {id,revision,digest}=run.decision;
        await api("/decisions/answer",{id,revision,digest,answer:"发 "+run.dispatchId.slice(0,8)});
        setReview("");refresh();
      });
      const sectionTitle=page==="profile"?(profile?.nickname||"用户档案"):page==="history"?"历史":page==="source-plan"?sourcePlan?.label:"自动化运营";
      const sourceLabels={observed_active:"来源事实",user_confirmed:"用户明确自述",observed_candidate:"待核线索",conflict:"来源冲突",unknown:"暂无证据"};
      const profileFields=fields=>h("dl",{className:"op-fields"},...fields.map((field,i)=>{
        const provenance=[field.source,field.observedAt,sourceLabels[field.state],field.sourceRef].filter(Boolean).join(" · ");
        return h("div",{key:field.label+":"+i},h("dt",{className:"wb-muted"},field.label),h("dd",null,field.value||"待确认",
          field.value&&provenance&&h("small",{className:"op-field-source"},provenance)));
      }));
      const modal=settings&&h(UI.Modal,{open:true,title:{project:"专项设置",profile:"档案设置",strategy:"策略设置"}[settings.kind],closeLabel:"关闭",onClose:()=>{if(!busy)setSettings(null)}},h("form",{className:"wb-page wb-form op-modal-content",onSubmit:saveSettings},
        settings.kind==="project"
          ? h(React.Fragment,null,
            field("关联知识库",h("select",{value:settings.knowledge.resourceId+"/"+settings.knowledge.section,onChange:e=>{
              const item=view.knowledge?.find(item=>item.resourceId+"/"+item.section===e.target.value);
              setSettings({...settings,knowledge:{resourceId:item?.resourceId||"",section:item?.section||"",version:""}});
            }},h("option",{value:"/"},"请选择"),...(view.knowledge||[]).map(item=>h("option",{key:item.resourceId+"/"+item.section,value:item.resourceId+"/"+item.section},item.label)))),
            field("运营企微账号",h("select",{value:settings.transport,onChange:e=>setSettings({...settings,transport:e.target.value})},
              h("option",{value:""},"请选择"),...accounts.map(account=>h("option",{key:account.id,value:account.id},account.name)))),
            settings.transport&&!settings.staffId&&h("p",{className:"wb-muted"},"此账号等待后台登记员工发送身份。"))
          : h(React.Fragment,null,
            field(settings.kind==="profile"?"清洗时间":"自动运行时间",h("input",{type:"time",required:true,value:settings.time,onChange:e=>setSettings({...settings,time:e.target.value})})),
            field("专家",h("select",{required:true,value:settings.expert,onChange:e=>setSettings({...settings,expert:e.target.value})},h("option",{value:""},"请选择"),...experts.map(expert=>h("option",{key:expert.name,value:expert.name},({"operations-onboarding":"新人承接专家","operations-profile":"用户档案专家"}[expert.name]||expert.name))))),
            view.next?.[settings.kind]&&h("p",{className:"wb-muted"},"下次运行 "+new Date(view.next[settings.kind]).toLocaleString("zh-CN",{timeZone:"Asia/Shanghai"}))),
        alert(error),h("div",{className:"wb-actions"},button("取消",()=>setSettings(null),{disabled:busy}),h("button",{type:"submit",className:"wb-primary",disabled:busy},"保存"))));
      const dispatchClosed=["Completed","Failed","Canceled","Terminated","TimedOut"].includes(run?.dispatchState);
      const confirmModal=review&&h(UI.Modal,{open:true,title:"确认并发送",closeLabel:"关闭",onClose:()=>{if(!busy)setReview("")}},h("section",{className:"wb-page wb-form op-modal-content"},
        run?.decision?h(React.Fragment,null,h("p",null,"发送人数 "+run.reviewCount+" 人"),h("p",null,"运营企微账号 "+run.account),h("p",null,"稿件版本 "+run.version)):
          h("p",{role:"status"},dispatchClosed?"该发送任务已结束。请在名单查看逐人状态。":run?.dispatchId?"正在校验发送资格":"正在准备确认"),
        alert(error),h("div",{className:"wb-actions"},button("返回",()=>setReview(""),{disabled:busy}),button("确认发送",confirm,{className:"wb-primary",disabled:busy||!run?.decision}))));
      const sourceRows=sourceCopy?.rows||[];
      const draftByID=new Map(sourceRows.map(item=>[item.id,item]));
      const displayMembers=sourceRows;
      const sendKeys=sourceRows.filter(item=>item.status==="待核对"&&!excluded.includes(item.key)).map(item=>item.key);
      const activeMember=displayMembers.find(item=>item.id===selected)||displayMembers[0];
      const itemKey=activeMember&&draftByID.get(activeMember.id)?.key;
      const draft=sourceItem?.run===sourceCopy?.run&&sourceItem?.digest===sourceCopy?.digest&&sourceItem?.rows[0]?.key===itemKey?sourceItem?.rows[0]:null;
      useEffect(()=>{
        if(page!=="source-plan"||!sourceCopy?.run||!itemKey)return;
        let active=true;
        loadSourceCopy(sourcePlan,sourceCopy.run,itemKey).then(data=>{
          if(!active)return;
          if(data.digest!==sourceCopy.digest){
            setError("原工作包已更新，请刷新稿件。");
            return;
          }
          setSourceItem(data);
        }).catch(e=>{if(active)setError(e.message)});
        return()=>{active=false};
      },[page,sourcePlan,sourceCopy?.run,sourceCopy?.digest,itemKey,reload]);
      const selectSourceMember=id=>{
        if(copyEdit||id===activeMember?.id)return;
        setSelected(id);setCopyMessage("");setError("");
      };
      const saveCopy=e=>{
        e.preventDefault();
        act(async()=>{
          const {item,revision,digest,text}=copyEdit;
          const input={package:sourcePlan.package_id,week:sourcePlan.week,run:sourceCopy.run,item,revision,digest,text,daily:sourcePlan.daily};
          const result=await api(path("source-package-edit"),input);
          if(result.state!=="saved"){
            setCopyEdit({...copyEdit,stale:true});
            throw Error("原稿已更新，当前输入已保留。请核对新版再修改。");
          }
          const [copy,person]=await Promise.all([loadSourceCopy(sourcePlan,input.run),loadSourceCopy(sourcePlan,input.run,item)]);
          if(copy.digest!==person.digest||person.rows[0]?.message!==text.trim()||person.rows[0]?.revision!==result.revision)
            throw Error("保存结果待核对，请刷新稿件。");
          setSourceCopy(copy);setSourceItem(person);setCopyEdit(null);setCopyMessage("最终稿已保存");
        });
      };
      const cardPreview=card=>h("article",{className:"op-card",key:card.index},
        h("strong",null,card.title||"随附卡片"),card.purpose&&h("p",null,card.purpose),
        h("p",{className:"wb-muted"},card.verified?"原入口已核验":"原入口待核验"),
        card.page&&h("p",null,"小程序页面 ",h("code",null,card.page)),
        card.note&&h("p",null,card.note),
        card.image&&h("img",{loading:"lazy",alt:card.title||"原报告图片",src:"/workbench-api"+path("source-card",{
          package:sourcePlan.package_id,week:sourcePlan.week,run:sourceCopy.run,item:draft.key,digest:sourceCopy.digest,card:card.index,
        })}));
      return h("section",{className:"wb-page op-page"},
        h("header",{className:"op-header"},h("div",{className:"wb-actions"},page!=="home"&&button("返回",page==="profile"&&profileBack==="source-plan"?()=>setPage("source-plan"):page==="source-plan"&&sourcePlan?.historical?()=>setPage("history"):home,{className:"op-link",disabled:!!copyEdit}),h("h1",null,sectionTitle)),
          view&&h("div",{className:"wb-actions"},
            page==="home"&&button("历史",()=>{setPage("history");if(readSource)loadSourceHistory(shanghaiWeek())},{className:"op-link"}),
            page==="home"&&button("设置",()=>showSettings("project")),
            page==="source-plan"&&sourceCopy&&h(React.Fragment,null,
              sourceCopy.run&&button("刷新稿件与回执",refreshSourceCopy,{className:"op-link",disabled:busy||!!copyEdit}),
              sourcePlan.daily&&sourceCopy.sendEnabled&&!sourceCopy.reviewed&&!sourceCopy.handoff&&!sourceCopy.formal&&
                button("确认并发送",()=>setSourceConfirm({daily:sourcePlan.daily,digest:sourceCopy.digest,keys:sendKeys,account:sourceCopy.sendAccount,transport:sourceCopy.sendTransport}),
                  {className:"wb-primary",disabled:busy||!!copyEdit||!!sourceSubmission||!sendKeys.length})),
            page==="detail"&&button("设置",()=>showSettings("strategy")),
            page==="detail"&&button(run?.retryReview&&run.pending?"重新核对并发送":"确认并发送",startReview,{className:"wb-primary",
              disabled:busy||!run||(!run.pending&&!run.decision&&!run.resume&&run.dispatchState!=="Running")}))),
        !settings&&!review&&alert(error),
        !view?h("p",{role:"status"},"正在读取自动化运营…"):null,
        view&&page==="home"&&h(React.Fragment,null,
          h("div",{className:"op-tabs",role:"tablist","aria-label":"自动化运营"},
            button("待我处理",()=>setTab("work"),{role:"tab","aria-selected":tab==="work"}),
            button("用户档案",()=>setTab("profiles"),{role:"tab","aria-selected":tab==="profiles"})),
          tab==="work"?(readSource?h("div",{className:"op-packages"},
            todayError?h("p",{role:"status"},"待核对稿件暂不可读。 ",button("重试",refresh)):
            !today?h("p",{role:"status"},"正在读取待核对稿件…"):
            h(React.Fragment,null,
              today.packages.length===0&&h("p",{className:"wb-muted"},"暂无待核对稿件。"),
              ...today.packages.map(plan=>h("div",{className:"op-strategy",key:plan.daily},
                h("strong",null,plan.label),h("span",{className:"wb-muted"},"待核对 "+plan.pending+" 人"),
                button("核对名单",()=>openPlan(plan),{"aria-label":"核对名单 · "+plan.label}))),
              today.attention&&h("p",{role:"status"},"有工作包需要核对运行状态。 ",button("查看定时任务",openSchedules,{className:"op-link"})),
              !today.attention&&today.running&&h("p",{role:"status"},"定时任务正在准备稿件。"))):
            h("div",{className:"op-strategy"},h("strong",null,run.title),
              h("span",{className:"wb-muted"},run.state==="ready"?"待发 "+run.pending+" 人":stateText[run.state]),
              button("核对名单",()=>openRun("")))):
            h(React.Fragment,null,
              h("div",{className:"op-header"},h("h2",{className:"op-title"},"用户档案"),button("档案设置",()=>showSettings("profile"))),
              readSource&&h("form",{className:"op-search",onSubmit:e=>{e.preventDefault();setQuery(searchText.trim());refresh()}},
                h("input",{type:"search",value:searchText,onChange:e=>setSearchText(e.target.value),placeholder:"搜索昵称或档案资料","aria-label":"搜索用户档案",maxLength:120}),
                h("button",{type:"submit"},"搜索")),
              profileState==="ready"&&Number.isInteger(profileMeta.total)&&h("p",{className:"wb-muted"},(query?"搜索结果 ":"共 ")+profileMeta.total.toLocaleString("zh-CN")+" 人"),
              profileState==="unconfigured"&&h("p",{className:"wb-muted"},"等待接入已授权的真实资料"),
              profileState==="loading"&&h("p",{role:"status"},"正在读取档案…"),
              profileState==="source_changed"&&button("档案已更新，重新读取",refresh),
              profileState==="ready"&&h("div",{className:"op-profiles"},...profiles.map(item=>button(item.nickname||"未记录称呼",()=>openProfile(item.id),{key:item.id,disabled:busy}))),
              profileNext&&h("div",{className:"op-footer"},button("加载更多",()=>act(async()=>{
                const data=await api(path("profiles",{after:profileNext,q:query,...(profileMeta.snapshot?{snapshot:profileMeta.snapshot}:{})}));
                if(data.state==="source_changed"){refresh();return}
                setProfiles(items=>[...items,...data.records]);setProfileNext(data.next);
              }),{disabled:busy})))),
        page==="profile"&&profile&&(profile.groups?h("div",{className:"op-groups"},
          profile.snapshot&&h("p",{className:"wb-muted"},"档案来源版本 "+profile.snapshot),
          !!profile.missingSources?.length&&h("p",{role:"status"},"部分实时补充资料暂不可读，以下保留可读来源。"),
          ...profile.groups.map(group=>h("section",{key:group.title},h("h2",{className:"op-title"},group.title),profileFields(group.fields)))):profileFields(profile.fields)),
        page==="source-plan"&&sourcePlan&&h(React.Fragment,null,
          sourcePlan.daily&&sourceCopy&&!sourceCopy.sendEnabled&&h("p",{className:"wb-muted"},"当前开放核对与改稿，CRM 交接未启用。"),
          sourceCopy?.formal&&h("p",{role:"status"},sourceCopy.formal==="running"?"原中心正在处理本人确认。":"原中心审核失败，请核对原包。"),
          sourceCopy?.plan&&h("p",{className:"wb-muted"},"原 CRM 计划 "+sourceCopy.plan),
          sourceCopy?h(React.Fragment,null,
              displayMembers.length?h("div",{className:"op-detail"},
                h("table",{className:"op-table"},h("thead",null,h("tr",null,...["客户昵称","入选依据","状态"].map(label=>h("th",{key:label,scope:"col"},label)))),
                  h("tbody",null,...displayMembers.map(item=>h("tr",{key:item.id,"aria-selected":item.id===activeMember?.id,onClick:()=>selectSourceMember(item.id)},
                    h("td",null,button(item.nickname||"未记录称呼",()=>selectSourceMember(item.id),{disabled:!!copyEdit})),h("td",null,h("div",{className:"op-reason"},item.reason||"原运行名单")),
                    h("td",null,item.status,sourcePlan.daily&&sourceCopy.sendEnabled&&item.status==="待核对"&&h("label",{className:"wb-muted"},h("input",{type:"checkbox",checked:!excluded.includes(item.key),"aria-label":"纳入发送 · "+item.nickname,onClick:e=>e.stopPropagation(),onChange:e=>setExcluded(e.target.checked?excluded.filter(key=>key!==item.key):[...excluded,item.key])})," 纳入")))))),
                activeMember&&h("article",{className:"op-message",key:activeMember.id},
                  h("div",{className:"op-header"},h("h2",null,activeMember.nickname||"未记录称呼"),
                    !sourcePlan.historical&&draft?.editable&&!copyEdit&&button("修改正文",()=>{
                      setCopyMessage("");
                      setCopyEdit({item:draft.key,revision:draft.revision,digest:sourceCopy.digest,text:draft.message});
                    },{disabled:busy})),
                  h("section",null,h("strong",null,draft?.edited?"已保存最终稿":"话术正文"),
                    copyEdit?h("form",{className:"op-editor",onSubmit:saveCopy},
                      h("textarea",{"aria-label":"最终稿正文",value:copyEdit.text,maxLength:5000,required:true,autoFocus:true,disabled:busy,
                        onChange:e=>setCopyEdit({...copyEdit,text:e.target.value})}),
                      h("div",{className:"wb-actions"},button("取消修改",()=>{setCopyEdit(null);setError("")},{disabled:busy}),
                        h("button",{type:"submit",className:"wb-primary",disabled:busy||copyEdit.stale||copyEdit.text.trim().length<2},"保存最终稿"))):
                      h("p",null,draft?.message||(itemKey&&!draft?"正在读取原稿…":"暂无可核对稿件"))),
                  copyMessage&&h("p",{role:"status"},copyMessage),
                  draft?.judgment&&h("section",null,h("details",{className:"op-review"},h("summary",null,"查看本轮判断与核对说明"),
                    h("div",{className:"op-judgments"},...draft.judgment.sections.map(section=>h("article",{key:section.title},h("strong",null,section.title),
                      ...section.lines.map((line,i)=>h("p",{key:i},line)),
                      section.title==="我们知道他什么"&&draft.judgment.facts.map((fact,i)=>h("p",{key:"fact-"+i},fact.text,
                        h("small",{className:"op-field-source"},[fact.time,fact.status].filter(Boolean).join(" · "))))))))),
                  !!draft?.attachments.length&&h("section",null,h("details",{className:"op-review"},
                    h("summary",null,"预览随附卡片 · "+draft.attachments.length+" 张"),...draft.attachments.map(cardPreview))),
                  draft?.progress&&h("section",null,h("strong",null,sourceCopy.handoff||draft.status==="已发送"?"CRM 回执":"状态明细"),h("p",null,draft.progress)),
                  h("div",{className:"wb-actions"},button("查看用户档案",()=>openProfile(activeMember.id),{disabled:busy||!!copyEdit})))):h("p",null,"本工作包当前无可处理名单"),
              null):busy?h("p",{role:"status"},"正在读取原工作包…"):button("重新核对",()=>openPlan(sourcePlan))),
        sourceConfirm&&h(UI.Modal,{open:true,title:"确认并发送",closeLabel:"关闭",onClose:()=>{if(!busy)setSourceConfirm(null)}},
          h("section",{className:"wb-page wb-form op-modal-content"},
            h("p",null,"将 "+sourceConfirm.keys.length+" 人的已核对稿件交给原中心。"),
            h("p",{className:"wb-muted"},"运营企微账号 "+sourceConfirm.account),
            alert(error),h("div",{className:"wb-actions"},button("返回",()=>setSourceConfirm(null),{disabled:busy}),
              button("确认发送",confirmSource,{className:"wb-primary",disabled:busy})))),
        page==="history"&&view&&h(React.Fragment,null,
          readSource&&h("section",null,h("h2",{className:"op-title"},"原工作包"),
            h("form",{className:"op-search",onSubmit:e=>{e.preventDefault();loadSourceHistory(sourceHistoryWeek)}},
              h("input",{type:"week",required:true,value:sourceHistoryWeek,onChange:e=>setSourceHistoryWeek(e.target.value),"aria-label":"历史周"}),
              h("button",{type:"submit",disabled:busy},"查询")),
            sourceHistory?.runs.length===0&&h("p",{className:"wb-muted"},"本周暂无原工作包记录"),
            ...(sourceHistory?.runs||[]).map(item=>h("div",{className:"op-strategy",key:item.run_id},
              h("strong",null,item.label),h("span",{className:"wb-muted"},item.createdAt?new Date(item.createdAt).toLocaleString("zh-CN",{timeZone:"Asia/Shanghai"}):item.week),
              button("查看原包",()=>openPlan({...item,historical:true}),{disabled:busy})))),
          !readSource&&h("h2",{className:"op-title"},run.title),
          !readSource&&history.length===0&&h("p",{className:"wb-muted"},"暂无历史运行"),
          ...(!readSource?history:[]).map(item=>h("div",{className:"op-strategy",key:item.id},h("span",null,new Date(item.date).toLocaleString("zh-CN",{timeZone:"Asia/Shanghai"})),null,button("查看明细",()=>openRun(item.id)))),
          !readSource&&historyMore&&button("加载更多",()=>act(async()=>{
            const data=await api(path("view",{after:history.at(-1).id}));setHistory(items=>[...items,...data.history]);setHistoryMore(data.history.length===20);
          }))),
        page==="detail"&&run&&h(React.Fragment,null,
          !readSource&&h("h2",{className:"op-title"},run.title),
          run.state!=="ready"||run.rows.length===0?h("p",{role:"status",className:"wb-muted"},stateText[run.state]||"本轮无入选用户"):
            h("div",{className:"op-detail"},
              h("table",{className:"op-table"},h("thead",null,h("tr",null,...["客户昵称","入选依据","状态"].map(label=>h("th",{key:label,scope:"col"},label)))),
                h("tbody",null,...run.rows.map(item=>h("tr",{key:item.id,"aria-selected":item.id===row?.id,onClick:()=>setSelected(item.id)},
                  h("td",null,button(item.nickname||"未记录称呼",()=>setSelected(item.id))),h("td",null,h("div",{className:"op-reason"},item.reason||"原运行名单")),h("td",null,item.status))))),
              row&&h("article",{className:"op-message",key:row.id},h("h2",null,row.nickname||"未记录称呼"),
                h("section",null,h("strong",null,"已生成稿件"),h("p",null,row.message)),
                h("section",null,h("strong",null,"入选依据"),h("p",null,row.reason)),
                row.detail&&h("section",null,h("strong",null,row.receipt?"CRM 回执":"状态明细"),h("p",null,row.detail),row.receipt&&h("p",null,row.receipt))))),
        modal,confirmModal);
    }

    function installWorkbenchViews(ctx,openProject,ReadableButton){
      const openSession=id=>ctx.uiWorkspace.openSession(id);
      const openCustomer=async id=>{await ctx.sessions.refresh();await ctx.sessions.refreshProjections(id);openSession(id)};
      function ReplyProjectPanel(){
        const [id,setId]=useState(replyProject);
        useEffect(()=>onEvent("wb-open-reply",()=>setId(replyProject)),[]);
        return h(ReplyPage,{key:id,selectedProject:id,openSession:openCustomer});
      }
      ctx.slots.inject('main',()=>ctx.slots.register({name:'main',key:'wb-replies'},ReplyProjectPanel));
      function OperationsPanel(){
        const [id,setId]=useState(operationsProject);
        useEffect(()=>onEvent("wb-open-operations",()=>setId(operationsProject)),[]);
        return id?h(OperationsPage,{key:id,projectId:id,openSchedules:()=>ctx.layout.selectPanel("wb-schedules")}):null;
      }
      ctx.slots.inject('main',()=>ctx.slots.register({name:'main',key:'wb-operations'},OperationsPanel));
      ctx.slots.inject("sidebar",()=>{
        const base=ctx.slots.entries("sidebar").find(entry=>entry.component);
        if(!base)return()=>{};
        const Original=base.component,cache=new WeakMap();
        function WorkbenchSidebar(props){
          return h(Original,{...props,
            usePanels:select=>props.usePanels(entries=>{
              if(!cache.has(entries))cache.set(entries,entries.filter(p=>!["plugins","schedules"].includes(p.id)));
              return select(cache.get(entries));
            }),
            startSession:workspaceId=>workspaceId?props.startSession(workspaceId):ctx.layout.selectPanel("wb-projects"),
          });
        }
        base.component=WorkbenchSidebar;
        return()=>{if(base.component===WorkbenchSidebar)base.component=Original};
      });
      // Replace only the native list's data projection; reuse its component,
      // controls, shortcuts, store, locale and ordinary project behaviour.
      ctx.slots.inject('sidebar.workspaces',()=>{
        const base=ctx.slots.entries('sidebar.workspaces').find(entry=>entry.component);
        if(!base)return()=>{};
        const Original=base.component;
        function NativeList(props){
          const {data,error}=useProjectList();
          const [create,setCreate]=useState(false);
          const activePanel=props.usePanelInfo(info=>info.activePanelId);
          if(!data)return error?h("div",{className:"wb-project-shortcuts"},alert(error),button("重试",notifyProjects)):null;
          const special=data.projects.filter(p=>p.type!=="ordinary");
          const hidden=new Set([data.personalWorkspaceId,...special.map(p=>p.workspaceId)]);
          if(hidden.size!==replyWorkspaceIds.size||[...hidden].some(id=>!replyWorkspaceIds.has(id)))replyWorkspaceIds=hidden;
          return h("section",{className:"wb-project-tree","aria-label":"项目与会话"},
            h(Original,{...props,requestAddWorkspace:()=>setCreate(true),
              useSessions:selector=>props.useSessions(state=>selector(filteredSessions(state))),
              useWorkspaces:selector=>props.useWorkspaces(state=>selector(filteredWorkspaces(state))),
              searchSessions:async(...args)=>{const result=await props.searchSessions(...args);return{...result,items:result.items.filter(item=>visibleSession(item.id||item.sessionId))}}}),
            h("div",{className:"wb-project-shortcuts"},...special.map(p=>
              button(h(React.Fragment,null,h(UI.IconFolderCloseRegular||IconDatabaseOutlineRegular,{size:16}),
                h("span",null,props.wide===false?"":p.name),props.wide!==false&&h("span",{className:"wb-tag"},"专项")),
              ()=>openProject(p),{key:p.id,className:"wb-project-shortcut","aria-label":p.name,title:p.name,"aria-current":(activePanel==="wb-replies"&&p.id===replyProject||activePanel==="wb-operations"&&p.id===operationsProject)?"page":undefined,"data-project-type":p.type}))),
            create&&h(NewProject,{onClose:()=>setCreate(false),onCreated:openProject}));
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
          const personalId=useSyncExternalStore(subscribePersonal,personalSnapshot);
          const blank=props.useSessions(s=>Boolean(s.byId[props.sessionId]?.blank));
          if(props.sessionId?.startsWith('object-'))return h(ReplyPage,{key:props.sessionId,sessionId:props.sessionId,openSession:openCustomer});
          const conversation=h(Original,props);
          if(personalId&&props.sessionId===personalId)return h(TodayConversation,{openSession},conversation);
          return h("div",{className:"wb-work-area"},h("div",{className:"wb-native-conversation"},conversation),
            blank&&props.sessionId&&h("div",{className:"wb-blank-actions"},h(ReadableButton,{key:props.sessionId,sessionId:props.sessionId})));
        }
        base.component=CustomerConversation;return()=>{if(base.component===CustomerConversation)base.component=Original};
      });
    }
    return {
      inject: ["slots", "layout", "uiWorkspace", "sessions", "workspaces"],
      apply(ctx) {
        ctx.effect(()=>{const style=document.createElement('style');style.textContent=css+workbenchCSS+operationsCSS;document.head.appendChild(style);return()=>style.remove()},'workbench shared native extension styles');
        ctx.effect(()=>{
          const key=()=>JSON.stringify(ctx.workspaces.list.getSnapshot().items.map(w=>[w.workspaceId,w.title]));
          let previous=key();
          return ctx.workspaces.list.subscribe(()=>{
            const current=key();
            if(current!==previous){previous=current;notifyProjects()}
          });
        },"workbench workspace catalogue changes");
        const creatingSession=new Map();
        const openProject=async p=>{
          if(p.type==="auto-operations"){
            operationsProject=p.id;
            ctx.layout.selectPanel("wb-operations");
            window.dispatchEvent(new Event("wb-open-operations"));
          }else if(p.type==="auto-reply"){
            replyProject=p.id;
            ctx.layout.selectPanel("wb-replies");
            window.dispatchEvent(new Event("wb-open-reply"));
          }else{
            const navigation=ctx.layout.beginNavigation();
            const native=ctx.workspaces.list.getSnapshot().items.find(w=>w.workspaceId===p.workspaceId);
            const sessions=ctx.sessions.list.getSnapshot();
            const archived=ctx.workspaces.list.getSnapshot().archivedSessionIds||[];
            const ids=(native?.sessionIds||p.sessionIds).filter(id=>visibleSession(id)&&!archived.includes(id));
            const recent=ids.find(id=>sessions.byId?.[id])||ids[0];
            if(recent)ctx.uiWorkspace.openSession(recent);
            else if(native)ctx.uiWorkspace.startSession(p.workspaceId,{clearPreviousDraft:false});
            else{
              let pending=creatingSession.get(p.workspaceId);
              if(!pending){
                pending=ctx.sessions.create({workspaceId:p.workspaceId}).finally(()=>creatingSession.delete(p.workspaceId));
                creatingSession.set(p.workspaceId,pending);
              }
              const id=await pending;
              if(!navigation.aborted)ctx.uiWorkspace.openSession(id);
            }
          }
        };
        installWorkbenchViews(ctx,openProject,ReadableButton);
        function TodayEntry(){
          const [error,setError]=useState("");
          const [retry,setRetry]=useState(0);
          useEffect(()=>{let alive=true;setError("");api("/project-list").then(c=>{
            if(!alive)return;rememberPersonal(c.personalSessionId);ctx.uiWorkspace.openSession(c.personalSessionId);
          }).catch(e=>{if(alive)setError(e.message)});return()=>{alive=false}},[retry]);
          return h("section",{className:"wb-page"},h("div",{className:"wb-content"},alert(error),
            error?button("重试",()=>setRetry(n=>n+1)):h("p",{role:"status"},"正在打开今日对话…")));
        }
        for (const [id, label, mark, Component, extra] of [
          ["wb-today","今日",IconClockOutlineRegular,TodayEntry,{}],
          ["wb-projects","项目",UI.IconFolderCloseRegular||IconDatabaseOutlineRegular,ProjectsPage,{openProject}],
          ["wb-resources","知识库",IconDatabaseOutlineRegular,ResourcePage,{}],
          ["wb-experts","专家",UI.IconAgentPresetOutlineRegular||IconDatabaseOutlineRegular,ExpertsPage,{}],
          ["wb-mcp","MCP 插件",UI.IconPluginPinwheelOutlineRegular||IconDatabaseOutlineRegular,MCPPage,{openNativePlugins:()=>ctx.layout.selectPanel("plugins")}],
          ["wb-schedules","定时任务",IconClockOutlineRegular,SchedulesPage,{openPanel:id=>ctx.layout.selectPanel(id),openSession:id=>ctx.uiWorkspace.openSession(id)}],
        ]) {
          ctx.slots.inject("sidebar.panellist", () =>
            ctx.slots.register(
              {
                name: "sidebar.panellist",
                id,
                order: -30+["wb-today","wb-projects","wb-resources","wb-experts","wb-mcp","wb-schedules"].indexOf(id),
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
        function ReadableButton({sessionId}){
          const [project,setProject]=useState(),[error,setError]=useState(""),[busy,setBusy]=useState(false);
          if(!visibleSession(sessionId))return null;
          const open=async()=>{
            setBusy(true);setError("");
            try{
              const c=await api("/project-list");
              const w=ctx.workspaces.list.getSnapshot().items.find(v=>v.sessionIds.includes(sessionId));
              const p=c.projects.find(v=>v.workspaceId===w?.workspaceId||v.sessionIds.includes(sessionId));
              if(!p||p.type!=="ordinary")throw Error("未找到当前普通项目");
              setProject(p);
            }catch(e){setError(e.message)}finally{setBusy(false)}
          };
          return h(React.Fragment,null,button("可读资料",open,{className:"wb-inline",disabled:busy}),
            error&&h(UI.Modal,{open:true,title:"可读资料",closeLabel:"关闭",onClose:()=>setError("")},alert(error)),
            project&&h(ReadableResources,{project,close:()=>setProject(undefined),manage:()=>{
              resourceProject=project.id;setProject(undefined);ctx.layout.selectPanel("wb-resources");
            }}));
        }
        ctx.slots.inject("conversation.session.header.utilities", () =>
          ctx.slots.register(
            {
              name: "conversation.session.header.utilities",
              id: "workbench-resources",
              order: 30,
              inject: sessionId=>({sessionId}),
            },
            props=>h(ReadableButton,{key:props.sessionId,...props}),
          ),
        );
      },
    };
  },
});
