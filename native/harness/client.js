window.__ModuleLoader__.load({
  id: "@local/personal-workbench",
  factory(require) {
    const React = require("react"),
      h = React.createElement;
    const { useState, useEffect, useRef, useSyncExternalStore } = React;
    const {IconClockOutlineRegular, IconDatabaseOutlineRegular, IconFolderOpenOutlineRegular}=require("@deepseek-ai/dsh-client-ui-primitives");
    let resourceProject="";
    let requestedResourcePermissions=new URLSearchParams(window.location.search).get("resourcePermissions");
    let requestedBusinessPolicy=new URLSearchParams(window.location.search).get("businessPolicy");
    const css = `.wb-page{height:100%;overflow:auto;background:var(--dsw-alias-bg-base);color:var(--dsw-alias-label-primary);font-size:14px;line-height:1.6}.wb-content{max-width:960px;margin:auto;padding:28px clamp(20px,4vw,48px) 48px}.wb-heading{display:flex;align-items:center;justify-content:space-between;gap:16px;flex-wrap:wrap;margin-bottom:24px}.wb-heading h1{font-size:20px;font-weight:500;margin:0}.wb-page button,.wb-inline{font:inherit;color:inherit;background:var(--dsw-alias-bg-base);border:1px solid var(--dsw-alias-border-l3);border-radius:8px;padding:6px 12px;cursor:pointer}.wb-short-label{display:none}@media(max-width:600px){.wb-full-label{display:none}.wb-short-label{display:inline}.wb-inline{font-size:12px;padding:4px 6px}}.wb-page button:disabled{opacity:.5;cursor:wait}.wb-page textarea{font:inherit;width:100%;box-sizing:border-box;min-height:88px;color:inherit;background:var(--dsw-alias-bg-base);border:1px solid var(--dsw-alias-border-l3);border-radius:6px;padding:10px}.wb-page input,.wb-page select{font:inherit;color:inherit;background:var(--dsw-alias-bg-base);border:1px solid var(--dsw-alias-border-l3);border-radius:6px;padding:7px 10px;min-width:0;max-width:100%;box-sizing:border-box}.wb-muted{color:var(--dsw-alias-label-secondary)}.wb-list{border-top:1px solid var(--dsw-alias-border-l3)}.wb-row{display:flex;gap:16px;align-items:center;padding:18px 0;border-bottom:1px solid var(--dsw-alias-border-l3);flex-wrap:wrap}.wb-row-main{flex:1;min-width:180px}.wb-row strong{font-weight:500}.wb-tag{font-size:12px;padding:2px 8px;border:1px solid var(--dsw-alias-border-l3);border-radius:6px}.wb-form{padding:20px 0;display:grid;gap:16px}.wb-field{display:grid;gap:6px;min-width:0}.wb-columns{display:grid;grid-template-columns:1fr 1fr;gap:16px}.wb-actions{display:flex;gap:8px;flex-wrap:wrap;align-items:flex-start}.wb-form>h2{font-size:16px;font-weight:500;margin:0}.wb-note{padding:12px 0;overflow-wrap:anywhere}.wb-check{display:flex;gap:8px;align-items:center;padding:6px 0}.wb-section{border:1px solid var(--dsw-alias-border-l3);border-radius:10px;padding:16px;margin-top:12px}.wb-section h2{font-size:16px;font-weight:500;margin:0 0 8px}@media(max-width:600px){.wb-columns{grid-template-columns:1fr}.wb-heading{align-items:flex-start}.wb-row-main{min-width:140px}}`;
    async function api(path, data, signal) {
      const r = await fetch("/workbench-api" + path, {
        method: data === undefined ? "GET" : "POST",
        headers: { "content-type": "application/json" },
        body: data === undefined ? undefined : JSON.stringify(data),
        signal,
      });
      const text = await r.text();
      let v;try{v=text?JSON.parse(text):{}}catch{v={error:text}}
      if (!r.ok) throw Object.assign(Error(v.error || "操作失败"),{status:r.status,invalidPaths:v.invalidPaths});
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
    const businessPolicyRelation = "wb_business_policies";
    const businessTopics = { product: "商品和课程", replay: "直播回放", survey: "问卷", activity: "活动" };
    const beijingInput = value => value ? new Date(Date.parse(value) + 8 * 3600000).toISOString().slice(0, 16) : "";
    const beijingTime = value => value ? new Date(value + ":00+08:00").toISOString() : null;

    function BusinessPolicyPanel({resource, rows, close, save, busy}) {
      const [draft, setDraft] = useState();
      const [permanent, setPermanent] = useState(false);
      const [validation, setValidation] = useState("");
      const start = row => {
        setDraft({...row, validFrom: beijingInput(row.validFrom), validUntil: beijingInput(row.validUntil)});
        setPermanent(Boolean(row.id && !row.validUntil));
        setValidation("");
      };
      const update = (key, value) => setDraft(previous => ({...previous, [key]:value}));
      const textField = (label, key, options = {}) => field(label, h("input", {...options,
        value:draft[key] || "", onChange:e=>update(key,e.target.value)}));
      const submit = async e => {
        e.preventDefault();
        const validFrom = beijingTime(draft.validFrom);
        const validUntil = permanent ? null : beijingTime(draft.validUntil);
        if (validFrom && validUntil && validFrom >= validUntil) {
          setValidation("截止时间须晚于生效时间");
          return;
        }
        if (await save({...draft,validFrom,validUntil})) setDraft(undefined);
      };
      const status = row => {
        const now = Date.now();
        if (!row.enabled) return "已停用";
        if (row.validUntil && Date.parse(row.validUntil) <= now) return "已过期";
        if (row.validFrom && Date.parse(row.validFrom) > now) return "待生效";
        return row.validUntil ? "有效至 " + beijingInput(row.validUntil).replace("T"," ") : "永久有效";
      };
      return h("section",{className:"wb-section"},
        h("div",{className:"wb-heading"},h("h2",null,resource.name),
          h("div",{className:"wb-actions"},button("新增口径",()=>start({topic:"product",enabled:true})),button("关闭",close))),
        draft && h("form",{className:"wb-form",onSubmit:submit},
          h("h2",null,draft.id ? "编辑口径" : "新增口径"),
          h("div",{className:"wb-columns"},
            field("事项类型",h("select",{value:draft.topic,onChange:e=>update("topic",e.target.value)},
              ...Object.entries(businessTopics).map(([value,label])=>h("option",{key:value,value},label)))),
            textField("对象名称","name",{required:true,maxLength:160})),
          textField("常见叫法","aliases",{maxLength:512}),
          field("对客口径（含适用条件、链接）",h("textarea",{value:draft.answer||"",required:true,maxLength:1200,onChange:e=>update("answer",e.target.value)})),
          h("div",{className:"wb-columns"},
            textField("生效时间（北京时间，可留空）","validFrom",{type:"datetime-local"}),
            !permanent && textField("截止时间（北京时间）","validUntil",{type:"datetime-local",required:true})),
          h("label",{className:"wb-check"},h("input",{type:"checkbox",checked:permanent,onChange:e=>setPermanent(e.target.checked)}),"永久有效"),
          h("label",{className:"wb-check"},h("input",{type:"checkbox",checked:draft.enabled,onChange:e=>update("enabled",e.target.checked)}),"启用"),
          validation && h("p",{role:"alert"},validation),
          h("div",{className:"wb-actions"},h("button",{type:"submit",disabled:busy},"保存"),button("取消",()=>setDraft(undefined)))),
        h("div",{className:"wb-list"},...rows.map(row=>h("div",{className:"wb-row",key:row.id},
          h("div",{className:"wb-row-main"},h("strong",null,row.name),h("div",{className:"wb-muted"},businessTopics[row.topic] || row.topic)),
          h("span",{className:"wb-tag"},status(row)),
          button("编辑",()=>start(row),{"aria-label":"编辑 "+row.name}),
          row.enabled && button("停用",()=>save({...row,enabled:false}),{disabled:busy,"aria-label":"停用 "+row.name}))),
          !rows.length && h("p",{className:"wb-muted"},"还没有口径。")));
    }

    const vaultCss = `
      .wb-vault{display:flex;flex-direction:column;overflow:hidden;container:vault / inline-size}
      .wb-vault-toolbar{display:flex;align-items:center;gap:12px;flex-wrap:wrap;padding:12px 20px;border-bottom:1px solid var(--dsw-alias-border-l3)}
      .wb-vault-toolbar strong{flex:1;min-width:0;overflow-wrap:anywhere}
      .wb-vault-layout{display:grid;grid-template-columns:clamp(180px,24%,280px) minmax(0,1fr);flex:1;min-height:0}
      .wb-vault-tree{padding:16px;font-size:14px;border-right:1px solid var(--dsw-alias-border-l3);overflow:auto}
      .wb-vault-tree input,.wb-vault-tree-tools{width:100%;margin-bottom:12px}
      .wb-vault-tree details>div{padding-inline-start:8px}
      .wb-vault-tree summary,.wb-vault-backlinks summary,.wb-vault-source summary{cursor:pointer;padding:5px 0;overflow-wrap:anywhere}
      .wb-vault-tree summary svg{width:15px;height:15px;vertical-align:-2px;margin-inline:4px 6px}
      .wb-vault-tree-tools,.wb-vault-tree-nav,.wb-vault-tabs{display:flex;align-items:center;gap:6px;flex-wrap:wrap}
      .wb-vault-tree-tools button,.wb-vault-tree-nav button,.wb-vault-tabs button{padding:4px 8px}
      .wb-vault-tree-nav{margin-block:0 8px}
      .wb-vault-entry{display:flex;align-items:center;min-width:0}
      summary.wb-vault-entry{display:list-item}
      .wb-vault-folder-row{display:inline-flex;align-items:center;vertical-align:middle;width:calc(100% - 16px)}
      .wb-vault-folder-row>span{flex:1;min-width:0}
      .wb-vault-entry>button:first-child{flex:1;min-width:0}
      .wb-vault-entry>button,.wb-vault-folder-row>button{background:transparent;border:0;text-align:start;padding:5px 8px}
      .wb-vault-entry>.wb-vault-row-menu{width:auto;padding:2px 8px;flex:none}
      .wb-vault-folder-row>.wb-vault-row-menu{width:auto;padding:2px 8px;flex:none}
      .wb-vault-entry[data-drop=true],.wb-vault-root[data-drop=true]{outline:2px solid var(--dsw-alias-label-link,#1768d8);outline-offset:-2px;border-radius:6px}
      .wb-vault-root{padding:5px 8px;color:var(--dsw-alias-label-secondary)}
      .wb-vault-hit{display:block;font-size:12px;color:var(--dsw-alias-label-secondary);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
      .wb-vault-tabs{padding:6px 14px;border-bottom:1px solid var(--dsw-alias-border-l3);overflow:auto;flex-wrap:nowrap}
      .wb-vault-tab{display:flex;align-items:center;gap:2px;white-space:nowrap}
      .wb-vault-tab button[aria-selected=true]{background:var(--dsw-alias-interactive-bg-hover)}
      .wb-vault-context{font-size:14px;margin-bottom:16px}
      .wb-vault-context ol{list-style:none;padding:0;margin:4px 0}
      .wb-vault-context li button{border:0;padding-block:2px;text-align:left}
      .wb-vault-edit-layout{display:grid;gap:12px;min-width:0}
      .wb-vault-edit-layout[data-mode=split]{grid-template-columns:minmax(0,1fr) minmax(0,1fr)}
      .wb-vault-preview{min-width:0;max-height:62vh;overflow:auto;border:1px solid var(--dsw-alias-border-l3);border-radius:8px;padding:12px}
      .wb-vault-export{display:inline-block;font-size:13px;color:var(--dsw-alias-label-link,#1768d8);margin-top:16px}
      [role=dialog]:has(.wb-vault-media){width:min(720px,calc(100vw - 24px));max-width:calc(100vw - 24px)}
      .wb-vault-media{width:100%;min-width:0;padding-block:4px}
      .wb-vault-media img{display:block;max-width:100%;max-height:70vh;margin-inline:auto;object-fit:contain}
      .wb-vault-pdf-pages{display:flex;align-items:center;justify-content:center;gap:12px;margin-bottom:8px}
      .wb-vault-pdf-viewport{overflow:auto;max-height:64vh;min-width:0;scrollbar-gutter:stable}
      .wb-vault-pdf-viewport canvas{display:block;max-width:100%;margin-inline:auto}
      .wb-vault-media-source{display:inline-block;margin-top:10px}
      .wb-vault-tree details button,.wb-vault-tree>button{background:transparent;border:0;padding:5px 8px;width:100%;text-align:left;overflow-wrap:anywhere}
      .wb-vault-tree button:hover,.wb-vault-tree button[aria-current=true]{background:var(--dsw-alias-interactive-bg-hover)}
      .wb-vault-body{padding:28px clamp(18px,4%,40px) 36px;overflow:auto;min-width:0;overflow-wrap:anywhere;font-size:17px;line-height:1.65}
      .wb-vault-reading{max-width:800px;margin-inline:auto;min-width:0}
      .wb-vault-body h1{font-size:30px;margin:0 0 20px;line-height:1.25}
      .wb-vault-markdown h2{font-size:22px;margin:24px 0 12px;line-height:1.35}
      .wb-vault-markdown h3{font-size:19px;line-height:1.4}
      .wb-vault-markdown li{margin-block:5px}
      .wb-vault-markdown a{color:var(--dsw-alias-label-link,#1768d8);text-decoration:underline}
      .wb-vault-markdown pre,.wb-vault-table{max-width:100%;overflow:auto}
      .wb-vault-markdown pre{padding:12px;background:var(--dsw-alias-interactive-bg-hover);border-radius:6px}
      .wb-vault-markdown pre code{white-space:pre}
      .wb-vault-markdown blockquote{margin-inline:0;padding-inline-start:12px;border-inline-start:2px solid var(--dsw-alias-border-l3)}
      .wb-vault-markdown table{border-collapse:collapse;min-width:max-content}
      .wb-vault-markdown th,.wb-vault-markdown td{padding:4px 8px;border:1px solid var(--dsw-alias-border-l3)}
      .wb-vault-backlinks,.wb-vault-source{border-top:1px solid var(--dsw-alias-border-l3);margin-top:24px;padding-top:10px}
      .wb-vault-source{font-size:13px;color:var(--dsw-alias-label-secondary)}
      .wb-vault-source dl{display:grid;grid-template-columns:auto minmax(0,1fr);gap:4px 10px}
      .wb-vault-source dd{margin:0}
      .wb-vault-backlinks ul{margin:4px 0;padding-inline-start:22px}
      .wb-vault-backlinks button{border:0;padding:4px}
      .wb-vault-note-tools{display:flex;justify-content:flex-end;gap:8px;margin-bottom:12px;font-size:14px}
      .wb-vault-note-menu{position:relative}
      .wb-vault-note-popover{position:absolute;inset-inline-end:0;top:100%;z-index:5;min-width:164px;padding:4px;background:var(--dsw-alias-bg-base);border:1px solid var(--dsw-alias-border-l3);border-radius:8px;box-shadow:0 4px 16px #0002}
      .wb-vault-note-popover button{display:block;width:100%;border:0;text-align:start}
      .wb-vault-note-popover button:hover{background:var(--dsw-alias-interactive-bg-hover)}
      .wb-vault-danger{color:var(--dsw-alias-label-danger,#b42318)!important}
      .wb-vault-dialog{display:grid;gap:12px;padding:8px 2px;height:auto}
      .wb-vault-dialog p{margin:0}
      .wb-vault-dialog .wb-actions{justify-content:flex-end}
      .wb-vault-copy-paths{max-height:32vh;overflow:auto;border:1px solid var(--dsw-alias-border-l3);border-radius:6px;padding:4px 8px;overflow-wrap:anywhere}
      .wb-vault-body form{display:grid;gap:12px}
      .wb-vault-editor{border:1px solid var(--dsw-alias-border-l3);border-radius:8px;overflow:hidden;min-height:260px}
      .wb-vault-editor .cm-editor{min-height:260px;max-height:62vh;font-size:14px}
      .wb-vault-mobile{display:none}
      .wb-page .wb-vault-open{padding:0;border:0;text-align:left;font-weight:500;background:none}
      .wb-vault-open:hover{text-decoration:underline}
      @container vault (max-width:720px){
        .wb-vault-toolbar{padding:10px 12px;gap:8px}
        .wb-vault-toolbar strong{flex-basis:40%}
        .wb-vault-mobile{display:inline-block}
        .wb-vault-layout{display:flex;flex-direction:column;overflow:auto}
        .wb-vault-tree{display:none}
        .wb-vault-tree[data-open=true]{display:block;max-height:38vh;flex:none;padding:12px;border-right:0;border-bottom:1px solid var(--dsw-alias-border-l3)}
        .wb-vault-tree button,.wb-vault-tree summary,.wb-vault-note-tools button{min-height:44px}
        .wb-vault-tree summary{box-sizing:border-box;padding-block:0}
        .wb-vault-body{overflow:visible;padding:18px 14px;font-size:16px}
        .wb-vault-body h1{font-size:24px;margin-bottom:16px}
        .wb-vault-markdown h2{font-size:20px;margin-top:18px}
        .wb-vault-edit-layout[data-mode=split]{grid-template-columns:minmax(0,1fr)}
        .wb-vault-tree button.wb-vault-row-menu{min-width:44px}
      }
    `;

    const noteLabel = path => path.split("/").at(-1).replace(/(?:\.md)+$/i, "").replace(/\.txt$/i, "");
    const markdownPath = value => /\.md$/i.test(value.trim()) ? value.trim() : value.trim() + ".md";

    const vaultName = path => path.split("/").at(-1);
    const vaultParent = path => path.slice(0,Math.max(0,path.lastIndexOf("/")));
    const vaultJoin = (folder,name) => folder ? `${folder}/${name}` : name;
    const vaultInside = (path,folder) => path === folder || path.startsWith(folder + "/");

    // Tree and drop feedback adapted from Obsidian Web Gateway (MIT), App.tsx.
    // Only a drag started in this mounted tree can request a same-library move.
    function VaultTree({notes, folders, active, disabled, editable, open, showMenu, move, collapsed, fold, reverse}) {
      const dragged = useRef(null);
      const [dropTarget,setDropTarget] = useState(null);
      const root = {folders:new Map(), notes:[]};
      const folderAt = path => {
        let node = root;
        for (const part of path ? path.split("/") : []) {
          if (!node.folders.has(part)) node.folders.set(part,{folders:new Map(),notes:[]});
          node = node.folders.get(part);
        }
        return node;
      };
      for (const folder of folders) folderAt(folder);
      for (const note of notes) folderAt(vaultParent(note.path)).notes.push(note);
      const resetDrag = () => { dragged.current=null; setDropTarget(null); };
      const canDrop = folder => {
        const item=dragged.current;
        return editable && !disabled && item && vaultParent(item.path)!==folder &&
          (item.kind!=="folder" || !vaultInside(folder,item.path));
      };
      const target = folder => ({
        "data-drop":dropTarget===folder,
        onDragOver:event=>{
          if(!canDrop(folder))return;
          event.preventDefault();event.stopPropagation();event.dataTransfer.dropEffect="move";setDropTarget(folder);
        },
        onDragLeave:event=>{if(!(event.relatedTarget instanceof Node)||!event.currentTarget.contains(event.relatedTarget))setDropTarget(value=>value===folder?null:value);},
        onDrop:event=>{
          if(!canDrop(folder))return;
          event.preventDefault();event.stopPropagation();
          const item=dragged.current;
          resetDrag();
          move(item,folder);
        },
      });
      const source = (kind,path) => ({
        draggable:editable&&!disabled,
        onDragStart:event=>{
          if(!editable||disabled)return;
          event.stopPropagation();
          dragged.current={kind,path};
          event.dataTransfer.setData("application/x-workbench-vault-entry",path);
          event.dataTransfer.effectAllowed="move";
        },
        onDragEnd:resetDrag,
        onContextMenu:!disabled ? event=>showMenu(event,{kind,path}) : undefined,
      });
      const menuButton = (kind,path) => button("⋯",event=>showMenu(event,{kind,path}),{
        className:"wb-vault-row-menu",disabled,"aria-haspopup":"menu","aria-label":`${kind==="folder"?"文件夹":"笔记"}操作：${vaultName(path)}`,
      });
      const sortNames = (a,b) => (reverse?-1:1)*a.localeCompare(b,"zh");
      const render = (node,parent) => h(React.Fragment,null,
        ...[...node.folders].sort(([a],[b])=>sortNames(a,b)).map(([name,child])=>{
          const path=vaultJoin(parent,name);
          return h("details",{open:!collapsed.has(path),key:path},
            h("summary",{className:"wb-vault-entry",...target(path),...source("folder",path),onClick:event=>{event.preventDefault();fold(path);}},
              h("span",{className:"wb-vault-folder-row"},IconFolderOpenOutlineRegular&&h(IconFolderOpenOutlineRegular,{"aria-hidden":true}),
                h("span",{title:path},name),menuButton("folder",path))),
            h("div",null,render(child,path)));
        }),
        ...[...node.notes].sort((a,b)=>sortNames(a.path,b.path)).map(({path,snippet})=>
          h("div",{className:"wb-vault-entry",key:path},button(h(React.Fragment,null,noteLabel(path),
            snippet&&h("span",{className:"wb-vault-hit"},snippet)),()=>open(path),{
            disabled,title:path,"aria-current":path===active,...source("note",path),
          }),menuButton("note",path))));
      return h(React.Fragment,null,editable&&h("div",{className:"wb-vault-root",...target("")},"根目录"),render(root,""));
    }

    // Adapted from Obsidian Web Gateway's note menu and MutationModal (MIT).
    // Upstream 99901b27d040360516b54ff86b7214908488ae2a, web/src/App.tsx.
    // See OBSIDIAN-WEB-GATEWAY.LICENSE.txt.
    function VaultNoteActions({menu, toggle, actions, close, disabled, floating=false}) {
      const trigger = useRef(null);
      const menuRef = useRef(null);
      useEffect(()=>{
        if (!menu) return;
        const dismiss = event => {
          if (!menuRef.current?.contains(event.target)) close();
        };
        const escape = event => {
          if (event.key !== "Escape") return;
          close();
          trigger.current?.focus();
        };
        menuRef.current?.querySelector("[role=menuitem]")?.focus();
        document.addEventListener("pointerdown",dismiss);
        document.addEventListener("keydown",escape);
        return()=>{
          document.removeEventListener("pointerdown",dismiss);
          document.removeEventListener("keydown",escape);
        };
      },[menu]);
      return h("div",{className:"wb-vault-note-menu",ref:menuRef},
        !floating&&button("⋯",toggle,{ref:trigger,disabled,"aria-label":"笔记操作","aria-expanded":Boolean(menu),"aria-haspopup":"menu"}),
        menu&&h("div",{className:"wb-vault-note-popover",role:"menu","aria-label":floating?"文件树操作":"笔记操作",
          style:menu.x===undefined?undefined:{position:"fixed",left:menu.x,top:menu.y,right:"auto"},
          onKeyDown:event=>{
            if (!["ArrowDown","ArrowUp","Home","End"].includes(event.key)) return;
            const items = [...event.currentTarget.querySelectorAll("[role=menuitem]")];
            const index = items.indexOf(document.activeElement);
            const next = {ArrowDown:(index+1)%items.length,ArrowUp:(index+items.length-1)%items.length,Home:0,End:items.length-1}[event.key];
            event.preventDefault();
            items[next]?.focus();
          }},
          ...actions.map(([label,action])=>button(label,()=>{close();action();},{key:label,role:"menuitem",
            className:label==="删除"?"wb-vault-danger":undefined}))));
    }

    function VaultMutationModal({dialog,folders,busy,error,onChange,onClose,onSubmit}) {
      const destructive = dialog.kind === "delete";
      const folder=dialog.entry==="folder";
      const subject=folder?"文件夹":"笔记";
      const name=folder?vaultName(dialog.path):noteLabel(dialog.path);
      const copy={
        create:{title:"新建文件夹",label:"文件夹名称",action:"创建"},
        rename:{title:`重命名或移动${subject}`,label:"新路径",action:"保存"},
        move:{title:`移动${subject}`,label:"移动到",action:"移动"},
        duplicate:{title:`复制${subject}`,label:"副本路径",action:"复制"},
        delete:{title:`删除${subject}`,action:"删除",body:folder
          ? `删除「${name}」及其内容？其中有 ${dialog.count} 篇笔记。`
          : `删除「${name}」？${dialog.backlinks?`有 ${dialog.backlinks} 篇笔记链接到它。`:""}`},
      }[dialog.kind];
      const moving=dialog.kind==="move";
      const destinations=["",...folders].filter(path=>!folder||!vaultInside(path,dialog.path));
      return h(UI.Modal,{open:true,title:copy.title,closeLabel:"关闭",onClose:()=>{if(!busy)onClose();}},
        h("form",{className:"wb-page wb-vault-dialog",onSubmit},
          copy.body&&h("p",null,copy.body),
          !destructive&&field(copy.label,moving?h("select",{autoFocus:true,value:dialog.value,disabled:busy,onChange:event=>onChange(event.target.value)},
            ...destinations.map(path=>h("option",{key:path,value:path},path||"根目录"))):
            h("input",{autoFocus:true,value:dialog.value,disabled:busy,onChange:event=>onChange(event.target.value)})),
          alert(error),
          h("div",{className:"wb-actions"},button("取消",onClose,{disabled:busy}),
            h("button",{type:"submit",className:destructive?"wb-vault-danger":"wb-primary",
              disabled:busy||(moving?dialog.value===vaultParent(dialog.path):!destructive&&!dialog.value.trim())},copy.action))));
    }

    function VaultCopyModal({data,close,copy,openCopy}) {
      const [name,setName]=useState([...data.resource.name].slice(0,92).join("")+" · 可编辑副本");
      const markdownCount=data.notes.filter(note=>/\.md$/i.test(note.path)).length;
      const [mode,setMode]=useState("all"),[selection,setSelection]=useState(()=>new Set(data.document?[data.document.path]:[]));
      const [busy,setBusy]=useState(false),[error,setError]=useState(""),[invalid,setInvalid]=useState([]);
      const [receipt,setReceipt]=useState();
      const attempt=useRef(),pending=useRef(false);
      const locked=busy||Boolean(receipt);
      const submit=async event=>{
        event.preventDefault();
        if(pending.current||!name.trim()||(mode==="all"&&!markdownCount))return;
        const paths=mode==="all"?undefined:mode==="current"?[data.document.path]:[...selection].sort();
        if(paths?.length===0)return;
        pending.current=true;
        setBusy(true);
        setError("");
        setInvalid([]);
        let destination=receipt;
        try{
          if(!destination){
            const key=JSON.stringify([name.trim(),paths]);
            if(attempt.current?.key!==key)attempt.current={key,id:"vault-"+crypto.randomUUID()};
            destination=await copy({id:attempt.current.id,name:name.trim(),paths});
            if(destination.id!==attempt.current.id||!destination.currentVersion)throw Error("未收到复制回执，请重试。");
            setReceipt(destination);
          }
          await openCopy(destination);
        }catch(e){
          setError(destination?.currentVersion?"副本已创建，打开失败。":e.message);
          setInvalid(e.invalidPaths||[]);
        }finally{pending.current=false;setBusy(false);}
      };
      return h(UI.Modal,{open:true,title:"复制为可编辑库",closeLabel:"关闭",onClose:()=>{if(!busy)close();}},
        h("form",{className:"wb-page wb-vault-dialog",onSubmit:submit},
          field("新语料库名称",h("input",{autoFocus:true,required:true,maxLength:100,disabled:locked,value:name,onChange:e=>setName(e.target.value)})),
          field("复制范围",h("select",{disabled:locked,value:mode,onChange:e=>setMode(e.target.value)},
            h("option",{value:"all"},`整库 Markdown（${markdownCount} 篇）`),
            data.document&&h("option",{value:"current"},"当前笔记"),
            h("option",{value:"selection"},"勾选笔记"))),
          mode==="selection"&&h("div",{className:"wb-vault-copy-paths"},
            ...data.notes.map(({path})=>h("label",{className:"wb-check",key:path},
              h("input",{type:"checkbox",disabled:locked,checked:selection.has(path),onChange:e=>{
                const checked=e.target.checked;
                setSelection(current=>{
                  const next=new Set(current);
                  if(checked)next.add(path);else next.delete(path);
                  return next;
                });
              }}),path))),
          alert(error),
          invalid.length>0&&h("ul",{className:"wb-vault-copy-paths","aria-label":"无法复制的笔记"},...invalid.map(path=>h("li",{key:path},path))),
          h("div",{className:"wb-actions"},button("取消",close,{disabled:busy}),
            h("button",{type:"submit",className:"wb-primary",disabled:busy||!name.trim()||
              (mode==="all"&&!markdownCount)||(mode==="selection"&&!selection.size)},
              receipt?"重试打开":busy?"复制中…":"创建副本"))));
    }

    function VaultEditor({text,onChange,onStatus,attempt,readOnly}) {
      const element=useRef(null);
      const editor=useRef();
      const locked=useRef(readOnly);
      locked.current=readOnly;
      useEffect(()=>{
        let disposed=false;
        const source="/workbench-api/vault-editor.js"+(attempt?"?retry="+attempt:"");
        import(source).then(({openEditor})=>{
          if(disposed)return;
          editor.current=openEditor(element.current,text,onChange,locked.current);
          onStatus("ready");
        }).catch(()=>{if(!disposed)onStatus("failed");});
        return()=>{disposed=true;editor.current?.destroy();editor.current=undefined;};
      },[attempt]);
      useEffect(()=>{editor.current?.setReadOnly(readOnly);},[readOnly]);
      return h("div",{ref:element,className:"wb-vault-editor"});
    }

    async function vaultPdfData(url,signal) {
      const response=await fetch(url,{signal,credentials:url.startsWith("/")?"same-origin":"omit",referrerPolicy:"no-referrer"});
      if(!response.ok)throw Error(response.status===404?"找不到附件。":"无法读取 PDF。");
      const limit=24*1024*1024;
      if(Number(response.headers.get("content-length"))>limit)throw Error("PDF 超过 24 MiB。");
      const reader=response.body.getReader(),chunks=[];
      let size=0;
      try{
        for(;;){
          const {done,value}=await reader.read();
          if(done)break;
          size+=value.length;
          if(size>limit)throw Error("PDF 超过 24 MiB。");
          chunks.push(value);
        }
      }finally{await reader.cancel().catch(()=>{});}
      const data=new Uint8Array(size);
      let offset=0;
      for(const chunk of chunks){data.set(chunk,offset);offset+=chunk.length;}
      return data;
    }

    // Mozilla PDF.js display API, using the bundled browser worker and fonts.
    function VaultPdf({url,initialPage}) {
      const [pdf,setPdf]=useState(),[page,setPage]=useState(1),[problem,setProblem]=useState(""),[rendered,setRendered]=useState(false);
      const viewport=useRef(null),canvas=useRef(null);
      const [width,setWidth]=useState(0);
      useEffect(()=>{
        const abort=new AbortController();
        let task;
        const base="/workbench-api/vault-pdf/";
        Promise.all([import(base+"build/pdf.min.mjs"),vaultPdfData(url,abort.signal)]).then(async([pdfjs,data])=>{
          if(abort.signal.aborted)return;
          pdfjs.GlobalWorkerOptions.workerSrc=base+"build/pdf.worker.min.mjs";
          task=pdfjs.getDocument({data,isEvalSupported:false,enableXfa:false,
            cMapUrl:base+"cmaps/",cMapPacked:true,standardFontDataUrl:base+"standard_fonts/",wasmUrl:base+"wasm/",iccUrl:base+"iccs/"});
          const document=await task.promise;
          if(abort.signal.aborted)return;
          setPage(Math.max(1,Math.min(initialPage||1,document.numPages)));
          setPdf(document);
        }).catch(error=>{if(!abort.signal.aborted)setProblem(error instanceof TypeError?"来源站未允许库内加载此 PDF。":error.message);});
        return()=>{abort.abort();task?.destroy().catch(()=>{});};
      },[url,initialPage]);
      useEffect(()=>{
        const el=viewport.current;
        const resize=new ResizeObserver(()=>setWidth(el.clientWidth));
        resize.observe(el);
        return()=>resize.disconnect();
      },[]);
      useEffect(()=>{
        if(!pdf||!width)return;
        let cancelled=false,render;
        setRendered(false);
        setProblem("");
        pdf.getPage(page).then(sheet=>{
          if(cancelled)return;
          const natural=sheet.getViewport({scale:1});
          const view=sheet.getViewport({scale:Math.min(width/natural.width,2,Math.sqrt(4e6/(natural.width*natural.height)))});
          const ratio=Math.min(window.devicePixelRatio||1,2,Math.sqrt(4e6/(view.width*view.height)));
          const target=canvas.current;
          target.width=Math.max(1,Math.floor(view.width*ratio));target.height=Math.max(1,Math.floor(view.height*ratio));
          target.style.width=Math.floor(view.width)+"px";
          render=sheet.render({canvas:target,viewport:view,transform:[ratio,0,0,ratio,0,0]});
          return render.promise.then(()=>{if(!cancelled){setRendered(true);viewport.current.scrollTop=0;}});
        }).catch(()=>{if(!cancelled)setProblem("无法绘制这一页 PDF。");});
        return()=>{cancelled=true;render?.cancel();};
      },[pdf,page,width]);
      return h(React.Fragment,null,
        pdf&&h("div",{className:"wb-vault-pdf-pages"},
          button("上一页",()=>setPage(page-1),{disabled:page<=1}),
          h("span",{role:"status"},`第 ${page} / ${pdf.numPages} 页`),
          button("下一页",()=>setPage(page+1),{disabled:page>=pdf.numPages})),
        problem?alert(problem):!rendered&&h("p",{role:"status",className:"wb-muted"},"正在加载 PDF…"),
        h("div",{ref:viewport,className:"wb-vault-pdf-viewport"},h("canvas",{ref:canvas,hidden:!rendered,"aria-label":`PDF 第 ${page} 页`})));
    }

    function VaultMediaPreview({media,close}) {
      const [imageFailed,setImageFailed]=useState(false);
      return h(UI.Modal,{open:true,title:media.label,closeLabel:"关闭",onClose:close},
        h("div",{className:"wb-page wb-vault-media"},
          media.type==="application/pdf"?h(VaultPdf,{url:media.url,initialPage:media.page}):
            imageFailed?alert("找不到图片或来源暂不可用。"):h("img",{src:media.url,alt:media.label,referrerPolicy:"no-referrer",onError:()=>setImageFailed(true)}),
          media.href&&h("a",{href:media.href,target:"_blank",rel:"noopener noreferrer",className:"wb-vault-media-source"},"打开原文件")));
    }

    function VaultPanel({data,load,refresh,save,rename,structure,preview,download,archive,copy,openCopy,close}) {
      const [search,setSearch]=useState(""),[matches,setMatches]=useState();
      const [treeOpen,setTreeOpen]=useState(false),[draft,setDraft]=useState(),[saving,setSaving]=useState(false);
      const [problem,setProblem]=useState(""),[searchFailed,setSearchFailed]=useState(false),[anchor,setAnchor]=useState();
      const [editorStatus,setEditorStatus]=useState("loading"),[editorAttempt,setEditorAttempt]=useState(0);
      const [receipt,setReceipt]=useState(),[conflict,setConflict]=useState(false);
      const [opening,setOpening]=useState(false);
      const [menu,setMenu]=useState(),[mutation,setMutation]=useState(),[mutationError,setMutationError]=useState("");
      const [copyOpen,setCopyOpen]=useState(false),[notice,setNotice]=useState("");
      const [refreshNeeded,setRefreshNeeded]=useState(false);
      const [collapsed,setCollapsed]=useState(()=>new Set()),[reverse,setReverse]=useState(false),[searchFolder,setSearchFolder]=useState("");
      const [tabs,setTabs]=useState([]),[editMode,setEditMode]=useState("edit"),[draftPreview,setDraftPreview]=useState();
      const [media,setMedia]=useState();
      // Same opt-in and idle-save behavior as the upstream editing workspace.
      const [autosave,setAutosave]=useState(()=>{
        try{return localStorage.getItem("wb-vault-autosave")==="true";}catch{return false;}
      });
      const inFlight=useRef(false), navigation=useRef(0);
      const bodyRef=useRef(null),treeRef=useRef(null),searchRef=useRef(null),menuAnchor=useRef(null);
      const note=data.document;
      const folders=data.folders??[];
      const blocked=Boolean(draft||opening||saving||receipt);
      const fold=path=>setCollapsed(current=>{
        const next=new Set(current);
        if(next.has(path))next.delete(path);else next.add(path);
        return next;
      });
      useEffect(()=>{
        const active=note?.path;
        const existing=new Set(data.notes.map(item=>item.path));
        setTabs(current=>{
          const available=current.filter(path=>existing.has(path));
          return active&&!available.includes(active)?[...available,active]:available;
        });
        if(active)setCollapsed(current=>new Set([...current].filter(path=>!vaultInside(active,path))));
      },[note?.path,data.version]);
      useEffect(()=>{
        if(!treeRef.current?.clientHeight||window.innerWidth<900)return;
        treeRef.current.querySelector('button[aria-current="true"]')?.scrollIntoView({block:"nearest"});
      },[note?.path,treeOpen]);
      useEffect(()=>{
        setMatches(undefined);
        setSearchFailed(false);
        if(!search.trim())return;
        const abort=new AbortController();
        const timer=setTimeout(()=>{
          const params={resourceId:data.resource.id,version:data.version,search,snippets:"1"};
          if(data.projectId)params.projectId=data.projectId;
          fetch("/workbench-api"+query("/vault-view",params),{signal:abort.signal})
            .then(response=>{if(!response.ok)throw Error();return response.json();})
            .then(view=>{if(!abort.signal.aborted)setMatches(view.notes);})
            .catch(()=>{if(!abort.signal.aborted)setSearchFailed(true);});
        },180);
        return()=>{clearTimeout(timer);abort.abort();};
      },[search,data.resource.id,data.projectId,data.version]);
      useEffect(()=>{
        setDraftPreview(undefined);
        if(!draft||editMode==="edit"||!draft.path.trim())return;
        let cancelled=false;
        const timer=setTimeout(async()=>{
          try{
            const path=draft.existing?draft.path:vaultJoin(draft.folder,markdownPath(draft.path));
            const result=await preview(path,draft.text);
            if(!cancelled)setDraftPreview(result);
          }catch(error){if(!cancelled)setDraftPreview({error:error.message});}
        },240);
        return()=>{cancelled=true;clearTimeout(timer);};
      },[draft?.path,draft?.text,editMode,data.version]);
      useEffect(()=>{
        if(!anchor||anchor.path!==note?.path)return;
        const body=bodyRef.current;
        const slug=value=>value.normalize("NFC").trim().toLowerCase().replace(/\s+/g,"-");
        let element;
        if(anchor.heading.startsWith("^")){
          const id="user-content-vault-block-"+anchor.heading.slice(1);
          element=[...body.querySelectorAll("[id]")].find(el=>el.id===id);
        }else{
          const wanted=anchor.heading.split("#").map(slug);
          const levels=[];
          element=[...body.querySelectorAll(".wb-vault-markdown :is(h1,h2,h3,h4,h5,h6)")].find(el=>{
            const level=Number(el.tagName[1]);
            levels.length=level-1;
            levels.push(slug(el.textContent));
            const titles=levels.filter(Boolean);
            return titles.length>=wanted.length && wanted.every((name,i)=>{
              const title=titles[titles.length-wanted.length+i];
              return name===title||name===title.replace(/[^\p{L}\p{M}\p{N}_-]/gu,"");
            });
          });
        }
        if(element)element.scrollIntoView({block:"start"});
        else setProblem("未找到链接指定的标题或段落");
        setAnchor(undefined);
      },[anchor,note?.path,note?.html]);
      const open=async(path,heading,newTab=false)=>{
        if(draft||saving||receipt)return false;
        const request=++navigation.current;
        setTreeOpen(false);
        setMenu(undefined);
        setProblem("");
        setNotice("");
        setAnchor(undefined);
        try{
          if(path!==note?.path||opening){
            setOpening(true);
            await load(path);
          }
          if(request===navigation.current){
            if(!newTab&&!tabs.includes(path)&&note?.path&&note.path!==path){
              setTabs(current=>[...new Set(current.map(item=>item===note.path?path:item))]);
            }
            if(heading)setAnchor({path,heading});
          }
          return request===navigation.current;
        }catch(e){if(request===navigation.current)setProblem(e.message);return false;}
        finally{if(request===navigation.current)setOpening(false);}
      };
      const follow=e=>{
        const link=e.target.closest?.("a");
        const href=link?.getAttribute("href");
        const attachment=href?.match(/^#wb-vault-media\/(\d+)$/);
        if(attachment){
          e.preventDefault();
          const reading=note?.media??[];
          const fromDraft=Boolean(link.closest(".wb-vault-preview"));
          const item=(fromDraft?draftPreview?.media:reading)?.[Number(attachment[1])];
          if(!item)return;
          const id=fromDraft?reading.findIndex(ref=>ref.asset===item.asset&&ref.type===item.type):Number(attachment[1]);
          if(!item.href&&id<0){setProblem("保存笔记后可查看这个附件。");return;}
          const params={resourceId:data.resource.id,version:data.version,note:note?.path,id};
          if(data.projectId)params.projectId=data.projectId;
          setMedia({...item,url:item.href??"/workbench-api"+query("/vault-media",params)});
          return;
        }
        const target=href?.match(/^#wb-vault\/([^/]+)\/([^/]*)$/);
        if(!target)return;
        e.preventDefault();
        try{open(decodeURIComponent(target[1]),decodeURIComponent(target[2]),e.metaKey||e.ctrlKey);}
        catch{setProblem("笔记链接格式有误");}
      };
      const start=(existing,folder="")=>{
        setProblem("");
        setNotice("");
        setConflict(false);
        setEditorStatus("loading");
        setTreeOpen(false);
        setMenu(undefined);
        setEditMode("edit");
        setDraft(existing?{path:existing.path,text:existing.text,original:existing.text,existing:true}:{path:"",folder,text:"",existing:false});
      };
      const readSaved=async saved=>{
        try{
          await load(saved.path,saved.version);
          if(saved.keepEditing)setDraft(current=>current&&({...current,original:saved.text}));
          else setDraft(undefined);
          setReceipt(undefined);
          setNotice(saved.message);
          setRefreshNeeded(false);
        }catch{setProblem(saved.message+"，读回失败。");}
      };
      const retryRead=async()=>{
        if(inFlight.current)return;
        inFlight.current=true;setSaving(true);setProblem("");
        try{await readSaved(receipt);}
        finally{inFlight.current=false;setSaving(false);}
      };
      const commit=async (request,path,message,options={})=>{
        if(inFlight.current||receipt)return;
        inFlight.current=true;
        setSaving(true);
        setProblem("");
        setNotice("");
        setMutationError("");
        try{
          const saved=await request();
          if(typeof saved.version!=="string"||!saved.version)throw Error("未收到保存回执，请重试。");
          const confirmed={path,version:saved.version,message,keepEditing:options.keepEditing,text:options.text};
          if(options.remap)setTabs(current=>[...new Set(current.map(options.remap))]);
          if(!options.keepEditing&&message!=="笔记已保存"){setSearch("");setSearchFolder("");}
          setMutation(undefined);
          setReceipt(confirmed);
          await readSaved(confirmed);
        }catch(e){
          if(e.status===409)setRefreshNeeded(true);
          if(draft){
            setConflict(e.status===409);
            setProblem(e.status===409?"语料库已更新。请复制草稿，再返回语料重新打开。":e.message);
          }else if(mutation){
            setMutationError(e.status===409?"操作冲突。请检查路径是否重名，或刷新语料库后重试。":e.message);
          }else{
            setProblem(e.status===409?"语料库已更新。请刷新后重试。":e.message);
          }
        }finally{inFlight.current=false;setSaving(false);}
      };
      const submit=async e=>{
        e.preventDefault();
        if(inFlight.current||receipt||conflict||editorStatus!=="ready"||draft.text===draft.original||!draft.path.trim())return;
        const path=draft.existing?draft.path:vaultJoin(draft.folder,markdownPath(draft.path));
        if(!draft.existing&&data.notes.some(item=>item.path.normalize("NFC").toLowerCase()===path.normalize("NFC").toLowerCase())){
          setProblem("这篇笔记已存在");
          return;
        }
        await commit(()=>save(path,draft.text,!draft.existing),path,"笔记已保存");
      };
      useEffect(()=>{
        if(!autosave||!draft?.existing||draft.text===draft.original||editorStatus!=="ready"||saving||conflict||receipt||problem)return;
        const {path,text}=draft;
        const timer=setTimeout(()=>commit(()=>save(path,text,false),path,"已自动保存",{keepEditing:true,text}),1100);
        return()=>clearTimeout(timer);
      },[autosave,draft,editorStatus,saving,conflict,receipt,data.version,problem]);
      const openMutation=(kind,item)=>{
        setMenu(undefined);
        setMutationError("");
        const {path,kind:entry}=item;
        let value=path;
        if(kind==="create")value="";
        if(kind==="move")value=vaultParent(path);
        if(kind==="duplicate"){
          const suffix=entry==="note"?".md":"";
          const name=entry==="note"?noteLabel(path):vaultName(path);
          const occupied=new Set([...folders,...data.notes.map(item=>item.path)].map(path=>path.normalize("NFC").toLowerCase()));
          for(let i=1;;i++){
            value=vaultJoin(vaultParent(path),`${name} 副本${i===1?"":` ${i}`}${suffix}`);
            if(!occupied.has(value.normalize("NFC").toLowerCase()))break;
          }
        }
        setMutation({kind,entry,path,value,backlinks:note?.path===path?data.backlinks.length:0,
          count:entry==="folder"?data.notes.filter(item=>vaultInside(item.path,path)).length:0});
      };
      const moveEntry=(item,to)=>{
        const {path,kind}=item;
        if(to===path)return;
        if(kind==="note"){
          const remap=entry=>entry===path?to:entry;
          return commit(()=>rename(path,to),remap(note?.path),"笔记已移动或重命名",{remap});
        }
        const remap=entry=>entry&&vaultInside(entry,path)?to+entry.slice(path.length):entry;
        return commit(()=>structure("move-folder",path,to),remap(note?.path),"文件夹已移动或重命名",{remap});
      };
      const submitMutation=async event=>{
        event.preventDefault();
        const {kind,entry,path,value}=mutation;
        if(kind==="create"){
          if(!value.trim())return;
          const target=vaultJoin(path,value.trim());
          await commit(()=>structure("create-folder",undefined,target),note?.path,"文件夹已创建");
          setCollapsed(current=>new Set([...current].filter(folder=>!vaultInside(target,folder))));
          return;
        }
        if(kind==="delete"){
          const removing=candidate=>entry==="folder"?vaultInside(candidate,path):candidate===path;
          const remaining=note?.path&&!removing(note.path)?note.path:data.notes.find(item=>!removing(item.path))?.path;
          await commit(entry==="folder"?()=>structure("delete-folder",path):()=>save(path,null,false),remaining,
            entry==="folder"?"文件夹已删除":"笔记已删除");
          return;
        }
        const target=kind==="move"?vaultJoin(value,vaultName(path)):entry==="note"?markdownPath(value):value.trim();
        if(kind==="duplicate"){
          await commit(()=>structure(`duplicate-${entry}`,path,target),entry==="note"?target:note?.path,
            entry==="folder"?"文件夹已复制":"笔记已复制");
          return;
        }
        if(target===path){setMutation(undefined);return;}
        await moveEntry({path,kind:entry},target);
      };
      const refreshVault=async()=>{
        setOpening(true);
        try{
          await refresh();
          setRefreshNeeded(false);setProblem("");
        }catch(e){setProblem(e.message);}
        finally{setOpening(false);}
      };
      const showMenu=(event,item)=>{
        event.preventDefault();
        event.stopPropagation();
        const rect=event.currentTarget.getBoundingClientRect();
        menuAnchor.current=event.currentTarget;
        const x=Math.max(8,Math.min(event.type==="contextmenu"?event.clientX:rect.left,window.innerWidth-186));
        const y=Math.max(8,Math.min(event.type==="contextmenu"?event.clientY:rect.bottom,window.innerHeight-292));
        setMenu({...item,x,y});
      };
      const closeMenu=()=>{setMenu(undefined);menuAnchor.current?.focus({preventScroll:true});};
      const entryActions=item=>{
        if(!item?.path)return [];
        const mutation=(label,kind)=>[label,()=>openMutation(kind,item)];
        const copyPath=["复制路径",async()=>{
          try{await navigator.clipboard.writeText(item.path);setNotice("路径已复制");}
          catch{setProblem("无法复制路径");}
        }];
        if(item.kind==="folder")return [
          ...(data.editable?[["在此新建笔记",()=>start(undefined,item.path)],mutation("新建子文件夹","create")]:[]),
          ["在此文件夹搜索",()=>{setSearchFolder(item.path);searchRef.current?.focus();}],copyPath,
          ...(data.editable?[mutation("重命名或移动","rename"),mutation("移动到","move"),mutation("复制文件夹","duplicate"),mutation("删除","delete")]:[]),
        ];
        return [["在新标签打开",()=>open(item.path,undefined,true)],["下载 Markdown",async()=>{
          try{await download(item.path);}catch(error){setProblem(error.message);}
        }],copyPath,...(data.editable?[mutation("重命名或移动","rename"),mutation("移动到","move"),mutation("复制笔记","duplicate"),mutation("删除","delete")]:[])];
      };
      const closeTab=path=>{
        const left=tabs.filter(item=>item!==path);
        if(!left.length)return;
        if(path===note?.path)open(left[Math.max(0,tabs.indexOf(path)-1)]??left[0]);
        setTabs(left);
      };
      const shown=(matches??data.notes).filter(item=>!searchFolder||vaultInside(item.path,searchFolder));
      const showHeading=index=>bodyRef.current?.querySelectorAll(".wb-vault-markdown :is(h1,h2,h3,h4,h5,h6)")[index]?.scrollIntoView({block:"start"});
      return h("section",{className:"wb-page wb-vault"},
        h("style",null,css+vaultCss),
        h("header",{className:"wb-vault-toolbar"},
          button("返回语料",close,{disabled:saving||Boolean(draft&&!receipt)}),
          h("strong",null,data.resource.name),
          button("目录",()=>setTreeOpen(!treeOpen),{className:"wb-vault-mobile","aria-expanded":treeOpen}),
          !data.editable&&button("复制为可编辑库",()=>setCopyOpen(true),{disabled:opening})),
        tabs.length>1&&h("div",{className:"wb-vault-tabs",role:"tablist","aria-label":"已打开的笔记"},
          ...tabs.map((path,index)=>h("div",{key:path,className:"wb-vault-tab",role:"presentation"},
            button(noteLabel(path),()=>open(path),{role:"tab",title:path,disabled:blocked,"aria-selected":note?.path===path,
              onKeyDown:event=>{
                const step=event.key==="ArrowLeft"?-1:event.key==="ArrowRight"?1:0;
                if(!step)return;
                event.preventDefault();
                const next=(index+step+tabs.length)%tabs.length;
                const buttons=event.currentTarget.closest('[role="tablist"]').querySelectorAll('[role="tab"]');
                buttons[next]?.focus();
                open(tabs[next]);
              }}),
            button("×",()=>closeTab(path),{disabled:blocked,"aria-label":`关闭${noteLabel(path)}`})))),
        h("div",{className:"wb-vault-layout"},
          h("nav",{className:"wb-vault-tree",ref:treeRef,"aria-label":"笔记目录","data-open":treeOpen},
            data.editable&&h("div",{className:"wb-vault-tree-tools"},
              button("新建笔记",()=>start(),{disabled:blocked}),
              button("新建文件夹",()=>openMutation("create",{kind:"folder",path:""}),{disabled:blocked})),
            h("input",{ref:searchRef,value:search,placeholder:"搜索笔记","aria-label":"搜索笔记",type:"search",
              onChange:e=>setSearch(e.target.value),onKeyDown:event=>{if(event.key==="Enter"&&!blocked&&shown[0])open(shown[0].path);}}),
            h("div",{className:"wb-vault-tree-nav"},
              folders.length>0&&button(collapsed.size?"展开全部":"折叠全部",()=>setCollapsed(collapsed.size?new Set():new Set(folders))),
              button(reverse?"名称降序":"名称升序",()=>setReverse(!reverse),{"aria-label":reverse?"切换为名称升序":"切换为名称降序"}),
              searchFolder&&button(`搜索：${searchFolder} ×`,()=>setSearchFolder(""),{"aria-label":"清除文件夹筛选"})),
            search.trim()&&!matches?h("p",{role:"status",className:"wb-muted"},searchFailed?"搜索失败，请重新输入":"正在搜索"):
              (matches||searchFolder)&&shown.length===0?h("p",{className:"wb-muted"},"没有匹配的笔记"):
                h(VaultTree,{notes:shown,folders:matches||searchFolder?[]:folders,active:note?.path,disabled:blocked,
                  editable:data.editable,open,showMenu,move:(item,folder)=>moveEntry(item,vaultJoin(folder,vaultName(item.path))),
                  collapsed:matches||searchFolder?new Set():collapsed,fold,reverse})),
          h("article",{className:"wb-vault-body",ref:bodyRef,onClickCapture:follow},
            h("div",{className:"wb-vault-reading"},
              alert(problem),
              !draft&&refreshNeeded&&button("刷新语料库",refreshVault,{disabled:opening||saving}),
              notice&&h("p",{role:"status"},notice),
              !draft&&receipt&&button("重试读回",retryRead,{disabled:saving}),
              draft?h("form",{onSubmit:submit,onKeyDown:event=>{if((event.metaKey||event.ctrlKey)&&event.key.toLowerCase()==="s")submit(event);}},
                draft.existing?h("strong",{title:draft.path},noteLabel(draft.path)):field("笔记名称",h("input",{required:true,value:draft.path,disabled:saving||Boolean(receipt),
                  onChange:e=>{const path=e.target.value;setDraft(current=>({...current,path}));}})),
                draft.folder&&h("span",{className:"wb-muted"},`位置：${draft.folder}`),
                h("div",{className:"wb-actions","aria-label":"编辑视图"},...[["edit","源码"],["preview","预览"],["split","并排预览"]].map(([mode,label])=>
                  button(label,()=>setEditMode(mode),{key:mode,"aria-pressed":editMode===mode,disabled:saving})),
                  draft.existing&&h("label",{className:"wb-check"},h("input",{type:"checkbox",checked:autosave,disabled:saving,onChange:event=>{
                    const enabled=event.target.checked;
                    setAutosave(enabled);
                    try{localStorage.setItem("wb-vault-autosave",String(enabled));}catch{}
                  }}),"自动保存")),
                h("div",{className:"wb-vault-edit-layout","data-mode":editMode},
                  h("div",{hidden:editMode==="preview"},h(VaultEditor,{text:draft.text,attempt:editorAttempt,onStatus:setEditorStatus,readOnly:saving||Boolean(receipt),
                    onChange:text=>setDraft(current=>({...current,text}))})),
                  editMode!=="edit"&&h("section",{className:"wb-vault-preview","aria-label":"Markdown 预览"},
                    !draft.path.trim()?h("p",{className:"wb-muted"},"填写笔记名称后显示预览。"):
                    draftPreview?.error?alert(draftPreview.error):draftPreview?h("div",{className:"wb-vault-markdown",dangerouslySetInnerHTML:{__html:draftPreview.html}}):
                      h("p",{role:"status",className:"wb-muted"},"正在渲染"))),
                editorStatus==="failed"&&alert("编辑器加载失败"),
                h("div",{className:"wb-actions"},receipt?button("重试读回",retryRead,{disabled:saving}):h(React.Fragment,null,
                  h("button",{type:"submit",disabled:saving||conflict||editorStatus!=="ready"||draft.text===draft.original},"保存"),
                  editorStatus==="failed"&&button("重试加载编辑器",()=>{setEditorStatus("loading");setEditorAttempt(editorAttempt+1);}),
                  conflict&&button("复制草稿",async()=>{
                    try{await navigator.clipboard.writeText(draft.text);setProblem("草稿已复制。");}
                    catch{setProblem("复制失败，请选中正文手动复制。");}
                  }),
                  button(draft.existing&&draft.text===draft.original?"返回阅读":"取消",()=>{setDraft(undefined);setProblem("");},{disabled:saving})))):
                note?h(React.Fragment,null,
                  h("div",{className:"wb-vault-note-tools"},
                    data.editable&&button("编辑正文",()=>start(note),{disabled:blocked}),
                    h(VaultNoteActions,{menu:menu&&!menu.path?menu:undefined,toggle:()=>setMenu(menu?undefined:{}),
                      actions:entryActions({kind:"note",path:note.path}),close:()=>setMenu(undefined),disabled:blocked})),
                  note.outline?.length>1&&h("details",{className:"wb-vault-context"},h("summary",null,"大纲"),
                    h("ol",null,...note.outline.map(({level,text},index)=>h("li",{key:index,style:{paddingInlineStart:(level-1)*10}},
                      button(text||"无标题",()=>showHeading(index)))))),
                  !note.hasTitle&&h("h1",null,noteLabel(note.path)),
                  note.properties&&h("details",null,h("summary",null,"属性"),h("pre",null,note.properties)),
                  h("div",{className:"wb-vault-markdown",dangerouslySetInnerHTML:{__html:note.html}}),
                  h("details",{className:"wb-vault-source",key:note.path},
                    h("summary",null,note.source||data.resource.vaultOrigin?"来源信息":"笔记路径"),
                    note.source?.summary&&h("p",null,note.source.summary),
                    h("dl",null,
                      h("dt",null,"笔记路径"),h("dd",null,note.path),
                      note.source&&h(React.Fragment,null,h("dt",null,"原路径"),h("dd",null,note.source.path),
                        h("dt",null,"SHA256"),h("dd",null,note.source.sha256),h("dt",null,"片段"),h("dd",null,note.source.fragment)),
                      data.resource.vaultOrigin&&h(React.Fragment,null,h("dt",null,"来源库"),h("dd",null,data.resource.vaultOrigin.resourceId),
                        h("dt",null,"来源版本"),h("dd",null,data.resource.vaultOrigin.version)))),
                  h("details",{className:"wb-vault-backlinks",key:note.path+"-backlinks"},
                    h("summary",null,`反向链接（${data.backlinks.length}）`),
                    data.backlinks.length?h("ul",null,...data.backlinks.map(path=>h("li",{key:path},
                      button(noteLabel(path),()=>open(path),{title:path})))):h("p",{className:"wb-muted"},"暂无反向链接")),
                  note.outgoing?.length>0&&h("details",{className:"wb-vault-backlinks",key:note.path+"-outgoing"},
                    h("summary",null,`链接到（${note.outgoing.length}）`),h("ul",null,...note.outgoing.map(path=>h("li",{key:path},
                      button(noteLabel(path),()=>open(path),{title:path})))))):
                  h("p",{className:"wb-muted"},"还没有笔记。")),
              !draft&&h("a",{className:"wb-vault-export",href:archive,download:data.resource.name+".zip"},"下载语料库 ZIP"))),
        menu?.path&&h(VaultNoteActions,{floating:true,menu,actions:entryActions(menu),close:closeMenu}),
        mutation&&h(VaultMutationModal,{dialog:mutation,folders,busy:saving,error:mutationError,
          onChange:value=>setMutation({...mutation,value}),onClose:()=>setMutation(undefined),onSubmit:submitMutation}),
        copyOpen&&h(VaultCopyModal,{data,close:()=>setCopyOpen(false),copy,openCopy}),
        media&&h(VaultMediaPreview,{media,close:()=>setMedia(undefined)}));
    }

    function ResourcePage() {
      const [projectFilter,setProjectFilter]=useState(resourceProject);
      const [kind,setKind]=useState("database"),[vault,setVault]=useState(),[record,setRecord]=useState(),[newVault,setNewVault]=useState();
      const documentRequest=useRef(0),previewRequest=useRef(0);
      useEffect(()=>{const listener=()=>setProjectFilter(resourceProject);window.addEventListener("wb-resource-project",listener);return()=>window.removeEventListener("wb-resource-project",listener)},[]);
      const [catalog, setCatalog] = useState(),
        [connections, setConnections] = useState([]),
        [grants, setGrants] = useState([]),
        [error, setError] = useState(""),
        [notice, setNotice] = useState(""),
        [busy, setBusy] = useState(false),
        [registration, setRegistration] = useState(),
        [editingResource, setEditingResource] = useState(),
        [connection, setConnection] = useState(),
        [sharing, setSharing] = useState(),
        [preview,setPreview]=useState(),[business,setBusiness]=useState(),[documents,setDocuments]=useState();
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
      const openBusiness = async resource => {
        resetView();
        const n = ++previewRequest.current;
        try {
          const items = await api(query("/business-policies",{resourceId:resource.id}));
          if (n === previewRequest.current) setBusiness({resource,items});
        } catch (e) { if (n === previewRequest.current) setError(e.message); }
      };
      useEffect(()=>{
        if (!catalog) return;
        if (requestedResourcePermissions) {
          const id = requestedResourcePermissions;
          requestedResourcePermissions = null;
          const resource = catalog.resources.find(item=>item.id===id);
          if (!resource) {
            setError("当前账号无法打开这份资源");
            return;
          }
          setSharing(resource);
          return;
        }
        if (!requestedBusinessPolicy) return;
        const id = requestedBusinessPolicy;
        requestedBusinessPolicy = null;
        const resource = catalog.resources.find(item=>item.id===id && item.relation===businessPolicyRelation && !item.connectionId);
        if (!resource) {
          setError("当前账号无法打开这张业务口径表");
          return;
        }
        setProjectFilter(resource.ownerProject);
        openBusiness(resource);
      },[catalog]);
      const saveBusiness = async policy => {
        setBusy(true); setError(""); setNotice("");
        try {
          const saved = await api("/business-policies",{resourceId:business.resource.id,policy});
          setBusiness(current=>current?.resource.id===business.resource.id ? {...current,
            items:[...current.items.filter(row=>row.id!==saved.id),saved]} : current);
          setNotice("已保存");
          return true;
        } catch (e) { setError(e.message); return false; }
        finally { setBusy(false); }
      };
      const openPreview=async(r,section=r.sections[0]?.id,after="")=>{
        const n=++previewRequest.current;setError("");setVault(undefined);setBusiness(undefined);documentRequest.current++;
        try{const page=await api(query("/resource-page",{resourceId:r.id,version:r.currentVersion,section,after}));if(n===previewRequest.current)setPreview({resource:r,section,page})}
        catch(e){if(n===previewRequest.current)setError(e.message)}
      };
      const openLegacyCorpus=async(r,uri)=>{
        const n=++documentRequest.current;
        try{
          const v=await api(query("/resource-documents",{resourceId:r.id,...(uri?{uri}:{})}));
          if(n!==documentRequest.current)return;
          if(v.external){setDocuments({resource:r,...v});return;}
          await openPreview(r);
        }catch(e){if(n===documentRequest.current)setError(e.message);}
      };
      const openCorpus=async(r,note,version,projectId,required=false)=>{
        const n=++documentRequest.current;setError("");setPreview(undefined);previewRequest.current++;
        try{
          const v=await api(query("/vault-view",{resourceId:r.id,...(projectId?{projectId}:{}),...(note?{note}:{}),...(version?{version}:{})}));
          if(n!==documentRequest.current)return;
          if(v.supported&&(!required||((!version||v.version===version)&&(!note||v.document?.path===note)))){
            setVault({resource:r,...v});return;
          }
          if(required)throw Error("无法读回这篇笔记");
          await openLegacyCorpus(r);
        }catch(e){if(required)throw e;if(n===documentRequest.current)setError(e.message)}
      };
      const resetView=()=>{documentRequest.current++;previewRequest.current++;setVault(undefined);setDocuments(undefined);setPreview(undefined);setRecord(undefined);setBusiness(undefined)};
      const createVault=async e=>{
        e.preventDefault();
        if(busy)return;
        setBusy(true);setError("");
        try{
          const r=await api("/vault-create",newVault);
          if(r.id!==newVault.id||!r.currentVersion)throw Error("未收到创建回执，请重试。");
          setNewVault(undefined);
          await load();
          await openCorpus(r);
        }catch(e){setError(e.message);}
        finally{setBusy(false);}
      };
      const change = (key, value) => setRegistration({ ...registration, [key]: value });
      const rows = (catalog?.resources ?? []).filter(
        (r) => ((r.kind || (connections.find(c=>c.id===r.connectionId)?.kind==="directory" || Boolean(catalog?.documentResourceIds?.includes(r.id)) || r.name.includes("语料") || r.sections.some(s=>s.fields?.includes("markdown")) ? "corpus" : "database")) === kind) && (
          !projectFilter ||
          r.ownerProject === projectFilter ||
          r.maintainerProject === projectFilter ||
          grants.some(
            (g) => g.resourceId === r.id && g.projectId === projectFilter,
          )),
      );
      if(vault){
        const {resource,version,projectId}=vault;
        const base={resourceId:resource.id,version};
        return h(VaultPanel,{key:resource.id,data:vault,close:resetView,
          load:(note,selectedVersion=version)=>openCorpus(resource,note,selectedVersion,projectId,true),
          refresh:()=>openCorpus(resource,undefined,undefined,projectId,true),
          save:(path,text,create)=>api("/vault-save",{...base,changes:[{path,text,create}]}),
          rename:(from,to)=>api("/vault-rename",{...base,from,to}),
          structure:(action,from,to)=>api("/vault-structure",{...base,action,from,to}),
          preview:(note,text)=>api("/vault-preview",{...base,note,text}),
          archive:"/workbench-api"+query("/vault-export",{...base,...(projectId?{projectId}:{})}),
          download:async path=>{
            const doc=vault.document?.path===path?vault.document:
              (await api(query("/vault-view",{...base,note:path,...(projectId?{projectId}:{})}))).document;
            if(typeof doc?.text!=="string")throw Error("无法读取这篇笔记");
            const url=URL.createObjectURL(new Blob([doc.text],{type:"text/markdown;charset=utf-8"}));
            const link=document.createElement("a");
            link.href=url;link.download=vaultName(path);link.click();
            setTimeout(()=>URL.revokeObjectURL(url),1000);
          },
          copy:input=>api("/vault-copy",{...base,projectId,...input}),
          openCopy:async destination=>{await load();await openCorpus(destination,undefined,destination.currentVersion,undefined,true);}});
      }
      return h(
        "section",
        { className: "wb-page" },
        h("style", null, css+vaultCss),
        h(
          "div",
          { className: "wb-content" },
          h(
            "div",
            { className: "wb-heading" },
            h("h1", null, "知识库"),
            kind==="database"&&h("select",{"aria-label":"按项目查看",value:projectFilter,onChange:e=>{resetView();resourceProject=e.target.value;setProjectFilter(resourceProject)}},h("option",{value:""},"全部项目"),...(catalog?.projects??[]).map(p=>h("option",{key:p.id,value:p.id},p.name))),
            h(
              "div",
              { className: "wb-actions" },
              kind==="corpus"?button("新增语料库",()=>setNewVault({id:"vault-"+crypto.randomUUID(),name:""}),{disabled:busy}):button("连接配置", () =>
                setConnection({
                  id: "c_" + crypto.randomUUID().slice(0, 8),
                  name: "",
                  kind: "mysql",
                  secretEnv: "WORKBENCH_SOURCE_",
                  location: "",
                }),
              ),
              kind==="database"&&button("注册资源", () => setRegistration({...emptyResource(),kind}), {disabled:!catalog||busy}),
            ),
          ),
          h("div",{className:"wb-tabs","aria-label":"资料类型"},
            button("语料库",()=>{resetView();setKind("corpus")},{"aria-pressed":kind==="corpus"}),
            button("数据库",()=>{resetView();setKind("database")},{"aria-pressed":kind==="database"})),
          kind==="database"&&h(
            "p",
            { className: "wb-muted" },
            "项目权限在各库设置。SuperAgent 默认可以管理所有库；外接只读资料可查询，支持同步的来源可以同步。",
          ),
          error && h("p", { role: "alert", className: "wb-note" }, error),
          notice && h("p", { role: "status", className: "wb-note" }, notice),
          !catalog && h("p", { role: "status" }, "正在读取资源…"),
          kind==="corpus"&&newVault&&h("form",{className:"wb-form",onSubmit:createVault},
            field("名称",h("input",{autoFocus:true,required:true,maxLength:100,value:newVault.name,disabled:busy,
              onChange:e=>setNewVault({...newVault,name:e.target.value})})),
            h("div",{className:"wb-actions"},h("button",{type:"submit",disabled:busy},"创建"),
              button("取消",()=>setNewVault(undefined),{disabled:busy}))),
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
          registration &&
            h(
              "form",
              {
                className: "wb-form",
                onSubmit: (e) => {
                  e.preventDefault();
                  act(async () => {
                    const {schemaText,checksText,sourceFieldsText,...resource}=registration;
                    await api("/resources", {
                      ...resource,
                      jsonSource:connections.find(c=>c.id===registration.connectionId)?.kind==="http-json" ? {...registration.jsonSource,fields:sourceFieldsText===undefined?registration.jsonSource?.fields:JSON.parse(sourceFieldsText||"{}")} : undefined,
                      recordSchema:schemaText===undefined?registration.recordSchema:JSON.parse(schemaText||"{}"),
                      checks:checksText===undefined?registration.checks:JSON.parse(checksText||"[]"),
                      sections: registration.sections.map(({selectText,...s}) => ({
                        ...s,
                        fields: s.fields.map((f) => f.trim()).filter(Boolean),
                        select:selectText===undefined?s.select:JSON.parse(selectText||"{}"),
                      })),
                    });
                    setRegistration(undefined);
                  });
                },
              },
              h("h2", null, "注册资源"),
              field("用途描述",h("textarea",{value:registration.description||"",maxLength:500,placeholder:"写下这份资料包含什么、能回答哪些问题",onChange:e=>change("description",e.target.value)})),
              h("p",{className:"wb-muted"},"所有内部项目都能看到用途描述，请勿填写个人资料或连接信息。"),
              h(
                "div",
                { className: "wb-columns" },
                field(
                  "资源名称",
                  h("input", {
                    required: true,
                    value: registration.name,
                    onChange: (e) => change("name", e.target.value),
                  }),
                ),
                field(
                  "所属项目",
                  h(
                    "select",
                    {
                      value: registration.ownerProject,
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
                      value: registration.connectionId,
                      onChange: (e) => change("connectionId", e.target.value),
                    },
                    h("option", { value: "" }, "生成产物（无需来源连接）"),
                    ...connections.map((c) =>
                      h("option", { key: c.id, value: c.id }, c.name),
                    ),
                  ),
                ),
                connections.find(c=>c.id===registration.connectionId)?.kind!=="http-json" && field(
                  "表 / 视图 / 文件名称",
                  h("input", {
                    value: registration.relation,
                    onChange: (e) => change("relation", e.target.value),
                  }),
                ),
              ),
              connections.find(c=>c.id===registration.connectionId)?.kind!=="http-json" && field(
                "记录唯一编号字段",
                h("input", {
                  value: registration.keyField,
                  onChange: (e) => change("keyField", e.target.value),
                }),
              ),
              connections.find(c=>c.id===registration.connectionId)?.kind==="http-json" && h("section",{className:"wb-section"},h("h2",null,"快照记录映射"),
                field("记录数组字段（顶层数组则留空）",h("input",{value:registration.jsonSource?.arrayField||"",onChange:e=>change("jsonSource",{...registration.jsonSource,arrayField:e.target.value})})),
                field("唯一编号路径（JSON Pointer）",h("input",{value:registration.jsonSource?.keyPointer||"",placeholder:"/id",onChange:e=>change("jsonSource",{...registration.jsonSource,keyPointer:e.target.value})})),
                field("保存字段与来源路径",h("textarea",{value:registration.sourceFieldsText??JSON.stringify(registration.jsonSource?.fields||{},null,2),onChange:e=>change("sourceFieldsText",e.target.value)})),
                h("p",{className:"wb-muted"},"例如 {\"name\":\"/display/name\"}。资料板块从这些保存字段中选择；空路径表示保留整条原文。")),
              field("维护项目",h("select",{value:registration.maintainerProject||"",onChange:e=>change("maintainerProject",e.target.value)},h("option",{value:""},"未指定维护项目"),...(catalog?.projects??[]).map(p=>h("option",{key:p.id,value:p.id},p.name)))),
              h("details",null,h("summary",null,"使用条件（可选）"),h("p",{className:"wb-muted"},"登记可供专家选择的固定校验条件。发送时，资格和收件对象从此资料中核对；专家不能在调用时放宽条件。"),field("条件定义",h("textarea",{rows:6,value:registration.checksText??JSON.stringify(registration.checks||[],null,2),onChange:e=>change("checksText",e.target.value)}))),
              h("details",null,h("summary",null,"发布校验（可选）"),field("最少记录数",h("input",{type:"number",min:0,value:registration.minRecords||0,onChange:e=>change("minRecords",Number(e.target.value))})),field("至少保留上一版数量的百分比",h("input",{type:"number",min:0,max:100,value:registration.minRetainedPercent||0,onChange:e=>change("minRetainedPercent",Number(e.target.value))})),field("来源发布版本字段",h("input",{value:registration.watermarkField||"",onChange:e=>change("watermarkField",e.target.value)})),field("记录结构（JSON Schema）",h("textarea",{value:registration.schemaText??JSON.stringify(registration.recordSchema||{},null,2),onChange:e=>change("schemaText",e.target.value)}))),
              ...registration.sections.map((s, i) =>
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
                            registration.sections.map((v, j) =>
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
                            registration.sections.map((v, j) =>
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
                  h("details",null,h("summary",null,"资料切片（可选）"),field("输出字段与 JSON Pointer",h("textarea",{value:s.selectText??JSON.stringify(s.select||{},null,2),onChange:e=>change("sections",registration.sections.map((v,j)=>j===i?{...v,selectText:e.target.value}:v))})),h("p",{className:"wb-muted"},"只从本板块包含的字段选择子项。留空时保留全部已选字段；缺少的资料返回空值。")),
                  registration.sections.length > 1 &&
                    button("移除板块", () =>
                      change(
                        "sections",
                        registration.sections.filter((_, j) => j !== i),
                      ),
                    ),
                ),
              ),
              h(
                "div",
                { className: "wb-actions" },
                button("增加资料板块", () =>
                  change("sections", [
                    ...registration.sections,
                    {
                      id: "s_" + crypto.randomUUID().slice(0, 8),
                      name: "",
                      fields: [],
                    },
                  ]),
                ),
                h("button", { type: "submit", disabled: busy }, "保存资源"),
                button("取消", () => setRegistration(undefined)),
              ),
            ),
          editingResource&&h(ResourceMetadataDialog,{
            key:editingResource.id,resource:editingResource,close:()=>setEditingResource(undefined),
            saved:updated=>{
              const updateResource=resource=>resource.id===updated.id?{...resource,...updated}:resource;
              const updatePreview=panel=>panel?{...panel,resource:updateResource(panel.resource)}:panel;
              setCatalog(current=>({...current,resources:current.resources.map(updateResource)}));
              setDocuments(updatePreview);
              setBusiness(updatePreview);
              setPreview(updatePreview);
              setNotice("已保存");
              setEditingResource(undefined);
            },
          }),
          sharing&&h(ResourcePermissionsDialog,{key:sharing.id,resource:sharing,close:()=>setSharing(undefined),saved:async()=>{setSharing(undefined);await load();setNotice("项目权限已保存");}}),
          documents&&h("section",{className:"wb-section"},
            h("div",{className:"wb-heading"},h("h2",null,documents.resource.name),button("关闭预览",resetView)),
            documents.content!==undefined?h(React.Fragment,null,button("返回语料目录",()=>setDocuments({...documents,content:undefined})),markdown(documents.content)):
              h("div",{className:"wb-list"},...(documents.documents||[]).map(d=>h("div",{className:"wb-row",key:d.uri},h("span",{className:"wb-row-main"},d.title||d.name),button("阅读",()=>openLegacyCorpus(documents.resource,d.uri)))))),
          business&&h(BusinessPolicyPanel,{key:business.resource.id,resource:business.resource,rows:business.items,close:resetView,save:saveBusiness,busy}),
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
            ...rows.map((r) => kind==="corpus"?
              h("div",{className:"wb-row",key:r.id},
                button(r.name,()=>openCorpus(r),{className:"wb-vault-open",disabled:!r.currentVersion}),
                button("权限设置",()=>setSharing(r),{"aria-label":"权限设置："+r.name}),
                button("编辑",()=>setEditingResource(r),{"aria-label":"编辑 "+r.name})):
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
                    r.description || r.sections.map((s) => s.name).join(" · "),
                  ),
                  r.ownerProject&&h("div",{className:"wb-muted"},"所属项目："+(catalog.projects.find(p=>p.id===r.ownerProject)?.name||r.ownerProject)),
                ),
                h(
                  "span",
                  { className: "wb-tag" },
                  r.relation===businessPolicyRelation ? "实时表" : r.currentVersion ? "已发布" : "尚未发布",
                ),
                r.relation===businessPolicyRelation ? button("查看资料",()=>openBusiness(r)) : r.currentVersion&&button("查看资料",()=>kind==="corpus"?openCorpus(r):openPreview(r)),
                button("权限设置", () => setSharing(r), {"aria-label":"权限设置："+r.name}),
                button("编辑", () => setEditingResource(r), {"aria-label":"编辑 "+r.name}),
              ),
            ),
            catalog &&
              rows.length === 0 &&
              h(
                "p",
                { className: "wb-muted" },
                kind==="corpus"?"还没有语料库。":"还没有资源。先配置连接，再注册需要使用的资料。",
              ),
          ),
        ),
      );
    }

    const UI = require("@deepseek-ai/dsh-client-ui-primitives");
    const workbenchCSS = `
.wb-expert-dialog{width:min(680px,calc(100vw - 24px))}.wb-expert-modal{box-sizing:border-box;width:100%;height:auto;max-height:70dvh;padding:12px 20px;overflow-wrap:anywhere}.wb-expert-modal .wb-form{padding:8px 0}.wb-expert-modal pre{white-space:pre-wrap}
      .wb-project-shortcut{width:100%;display:flex;align-items:center;gap:8px;padding:8px 10px;border:0;border-radius:8px;background:transparent;color:inherit;font:inherit;text-align:left;cursor:pointer}
      .wb-project-shortcut:hover,.wb-project-shortcut[aria-current=page]{background:var(--dsw-alias-interactive-bg-hover)}
      .wb-project-shortcut>span:first-of-type{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
      .wb-project-shortcuts{padding:0 8px 8px}
      .wb-project-tree{display:flex;flex-direction:column;flex:1;min-height:0;overflow:auto}
      .wb-project-tree>div:first-child{flex:0 0 auto}
      .wb-page .wb-primary{background:var(--dsw-alias-label-primary);color:var(--dsw-alias-bg-base);border-color:transparent}
      .wb-work-area{display:flex;height:100%;min-height:0;min-width:0;position:relative;background:var(--dsw-alias-bg-base)}
      .wb-native-conversation{flex:1;min-width:0;height:100%;overflow:hidden}
      .wb-metadata-form{margin:0;padding:4px 0;gap:14px}
      .wb-metadata-form .wb-actions{justify-content:flex-end;padding-top:4px}
      :is(dialog,[role=dialog]):has(.wb-permission-page){width:min(520px,calc(100vw - 48px));max-width:calc(100vw - 48px)}
      .wb-permission-page{width:100%;box-sizing:border-box;max-height:68vh}
      .wb-permission-page .wb-form{margin:0;padding:6px 0;gap:12px}
      .wb-permission-page p{margin:0}
      .wb-permission-page .wb-actions{justify-content:flex-end;padding-top:8px}
      .wb-permission-list{max-height:34vh;overflow:auto}
      .wb-permission-row{display:grid;grid-template-columns:minmax(0,1fr) auto;align-items:center;gap:8px;padding:8px 0;border-bottom:1px solid var(--dsw-alias-border-l3)}
      .wb-permission-row details{grid-column:1/-1;padding-left:22px}
      .wb-permission-identity{min-width:0;overflow-wrap:anywhere}
      .wb-permission-identity .wb-muted{display:block;font-size:12px}
      .wb-permission-row .wb-check{padding:2px 0}
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
    function ResourceMetadataDialog({resource,close,saved}) {
      const [name,setName]=useState(resource.name);
      const [description,setDescription]=useState(resource.description||"");
      const [busy,setBusy]=useState(false),[error,setError]=useState("");
      const pending=useRef(false);
      const save=async event=>{
        event.preventDefault();
        const title=name.trim();
        if (!title||pending.current) return;
        pending.current=true;
        setBusy(true);
        setError("");
        try {
          const updated=await api("/resource-metadata",{resourceId:resource.id,name:title,description:description.trim()});
          saved({id:updated.id,name:updated.name,description:updated.description||"",...(updated.kind?{kind:updated.kind}:{})});
        } catch (error) {
          setError(error.message);
        } finally {
          pending.current=false;
          setBusy(false);
        }
      };
      return h(UI.Modal,{open:true,title:"编辑资源",closeLabel:"关闭",onClose:()=>{if(!pending.current)close()}},
        h("form",{className:"wb-page wb-form wb-metadata-form",onSubmit:save},
          field("名称",h("input",{value:name,onChange:event=>setName(event.target.value),required:true,disabled:busy})),
          field("描述",h("textarea",{value:description,onChange:event=>setDescription(event.target.value),rows:3,maxLength:500,disabled:busy})),
          alert(error),
          h("div",{className:"wb-actions"},h("button",{type:"submit",className:"wb-primary",disabled:busy||!name.trim()},busy?"保存中…":"保存"))));
    }
    function ResourcePermissionsDialog({resource,close,saved}) {
      const [data,setData]=useState(),[choices,setChoices]=useState({}),[search,setSearch]=useState("");
      const [error,setError]=useState(""),[busy,setBusy]=useState(false);
      const pending=useRef(false);
      const load=()=>{
        setData(undefined);setError("");
        return Promise.all([api("/project-list"),api(query("/resource-permissions",{resourceId:resource.id}))]);
      };
      const show=([projects, permissions])=>{
        setData({projects:projects.projects, permissions});
        setChoices(Object.fromEntries(permissions.grants.map(grant=>[grant.projectId,grant])));
      };
      useEffect(()=>{let active=true;load().then(result=>{if(active)show(result)}).catch(e=>{if(active)setError(e.message)});return()=>{active=false};},[]);
      const set=(id,update)=>setChoices(old=>({...old,[id]:update(old[id]||{projectId:id,mode:"none",sections:resource.sections.map(s=>s.id)})}));
      const sections=resource.sections;
      const projects=(data?.projects||[]).filter(p=>p.name.toLocaleLowerCase().includes(search.trim().toLocaleLowerCase()));
      const kindName=type=>({ordinary:"普通项目","auto-reply":"自动化应答","auto-operations":"自动化运营"}[type]||"专项项目");
      return h(UI.Modal,{open:true,title:"权限设置",closeLabel:"关闭",onClose:()=>{if(!pending.current)close()}},
        h("div",{className:"wb-page wb-permission-page"},h("form",{className:"wb-form",onSubmit:async event=>{
          event.preventDefault();if(pending.current||!data||data.permissions.locked)return;
          pending.current=true;setBusy(true);setError("");
          try{
            const grants=Object.values(choices).filter(g=>g.mode!=="none");
            if(grants.some(g=>!g.sections.length))throw Error("请为已授权项目至少选择一个板块。");
            await api("/resource-permissions",{resourceId:resource.id,revision:data.permissions.revision,grants});
            await saved();
          }catch(e){setError(e.message)}finally{pending.current=false;setBusy(false)}
        }},
          h("strong",null,resource.name),
          h("p",{className:"wb-muted"},"可同时设置多个项目。只读可查资料；管理可编辑获准且支持写入的内容。未授权仍可看库名与用途。"),
          data?.permissions.locked&&h("p",{className:"wb-muted"},"当前只能查看权限。"),
          field("搜索项目",h("input",{type:"search",value:search,placeholder:"输入项目名称",onChange:e=>setSearch(e.target.value)})),
          alert(error),
          !data&&!error&&h("p",{role:"status"},"正在读取项目权限…"),
          error&&button("重新读取",()=>load().then(show).catch(e=>setError(e.message)),{disabled:busy}),
          data&&h("div",{className:"wb-permission-list"},...projects.map(p=>{
            const grant=choices[p.id],mode=grant?.mode||"none",raw=grant?.sections||["*"];
            const all=raw.includes("*");
            return h("div",{key:p.id,className:"wb-permission-row"},
              h("label",{className:"wb-check"},h("input",{type:"checkbox",checked:mode!=="none",disabled:busy||data.permissions.locked,onChange:e=>set(p.id,v=>({...v,mode:e.target.checked?"read":"none"}))}),
                h("span",{className:"wb-permission-identity"},p.name,h("span",{className:"wb-muted"},kindName(p.type)))),
              h("select",{"aria-label":p.name+"的权限",value:mode,disabled:busy||data.permissions.locked,onChange:e=>set(p.id,v=>({...v,mode:e.target.value}))},
                h("option",{value:"none"},"未授权"),h("option",{value:"read"},"只读"),h("option",{value:"manage"},"管理")),
              mode!=="none"&&sections.length>1&&h("details",null,h("summary",null,all||raw.length===sections.length?"全部板块":"已选 "+raw.length+" 个板块"),
                ...sections.map(sec=>h("label",{key:sec.id,className:"wb-check"},h("input",{type:"checkbox",checked:all||raw.includes(sec.id),disabled:busy||data.permissions.locked,onChange:e=>{
                  const selected=all?sections.map(s=>s.id):raw;
                  const next=e.target.checked?[...selected,sec.id]:selected.filter(id=>id!==sec.id);
                  set(p.id,v=>({...v,sections:next}));
                }}),sec.name||sec.id))));
          }),projects.length===0&&h("p",{className:"wb-muted"},"没有找到符合条件的项目")),
          h("div",{className:"wb-actions"},button("取消",close,{disabled:busy}),h("button",{className:"wb-primary",type:"submit",disabled:busy||!data||data.permissions.locked},"保存权限")))));
    }
    const onEvent = (event, fn) => { window.addEventListener(event,fn); return () => window.removeEventListener(event,fn); };
    const notifyProjects = () => window.dispatchEvent(new Event("wb-project-list"));
    function useProjectList() {
      const [state,setState]=useState({});
      useEffect(()=>{
        let request=0,alive=true;
        const read=()=>{const n=++request;api("/project-list").then(data=>{
          if(alive&&n===request){
            rememberPersonal(data.personalSessionId);
            const hidden=new Set([data.personalWorkspaceId,...data.projects.filter(p=>p.type!=="ordinary").map(p=>p.workspaceId)]);
            if(hidden.size!==replyWorkspaceIds.size||[...hidden].some(id=>!replyWorkspaceIds.has(id)))replyWorkspaceIds=hidden;
            setState({data});
          }
        }).catch(error=>{if(alive&&n===request)setState(previous=>({...previous,error:error.message}))});};
        read();
        const detach=onEvent("wb-project-list",read);
        return()=>{alive=false;detach()};
      },[]);
      return state;
    }
    function NewProject({onClose,onCreated,ordinaryOnly=false}) {
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
        !ordinaryOnly&&h("fieldset",{disabled:busy||!!created,className:"wb-form"},
          h("legend",null,"项目类型"),
          ...[["ordinary","普通项目","在项目内创建多个对话。"],["auto-reply","专项项目 · 自动化应答","使用现有的客户会话与应答设置。"],["auto-operations","专项项目 · 自动化运营","核对名单、用户档案和运营设置。"]].map(([id,label,hint])=>
            h("label",{key:id,className:"wb-check"},h("input",{type:"radio",name:"project-type",checked:type===id,onChange:()=>setType(id)}),h("span",null,label,h("small",{className:"wb-muted",style:{display:"block"}},hint))))),
        field("项目名称",h("input",{required:true,disabled:busy||!!created,maxLength:80,value:name,onChange:e=>setName(e.target.value),placeholder:type==="ordinary"?"例如：产品研究":"例如：自动化应答"})),
        !ordinaryOnly&&h("p",{className:"wb-muted"},"项目类型在创建时确定。"),
        alert(error),
        h("div",{className:"wb-actions"},button("取消",onClose,{disabled:busy}),
          h("button",{type:"submit",className:"wb-primary",disabled:busy||!name.trim()},busy?"正在打开…":created?"打开已创建项目":"创建项目")))));
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
      return h("section",{className:"wb-rules-editor wb-page","aria-label":"SuperAgent Markdown 配置"},
        h("h2",null,"SuperAgent 配置"),h("p",{className:"wb-muted"},"设置和 SuperAgent 的合作方式。"),
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
        h("aside",{className:"wb-companion",style:pane?undefined:{display:"none"},"aria-label":"SuperAgent 辅助栏"},
          h("div",{className:"wb-companion-head"},h("strong",null,pane==="rules"?"配置":"关注中的事"),
            h("div",{className:"wb-actions"},button(pane==="rules"?"关注":"配置",pane==="rules"?()=>setPane("follow"):showRules,{className:"wb-inline"}),
              button("收起",()=>setPane(""),{className:"wb-inline","aria-label":"收起SuperAgent 辅助栏"}))),
          h("div",{className:"wb-companion-scroll",hidden:pane!=="follow"},
            h(SuperAgentWork,{openSession,active:pane==="follow"})),
          rulesOpened&&h("div",{className:"wb-companion-scroll",hidden:pane!=="rules"},h(TodayRules))));
    }
    function LegacyExpertsPage() {
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
    const expertTitle = row => row.displayName || row.name;
    const expertOrigins = {
      personal: "本人主会话", bundled: "随安装提供", "user-dsh": "工作台", "user-agents": "工作台",
      managed: "工作台编辑版", "project-dsh": "项目方法", "project-agents": "项目方法",
      "operations-original": "运营原方法", "operations-local": "运营工作台版",
    };
    const expertDetails = id => api(query("/experts", {id}));
    const expertMain = "SKILL.md";
    const expertNotes = new Map();
    let requestedExpertProject = "";
    const noteTitle = ref => /^#[ \t]+(.+?)\s*$/m.exec(ref.content)?.[1]?.slice(0,100) || ref.name.replace(/\.md$/i,"");
    const expertPayload = row => ({displayName:expertTitle(row),description:row.description||"",content:row.content||"",references:row.references||[]});
    const expertSaved = (a,b) => JSON.stringify(a)===JSON.stringify(b);
    const expertCSS = `
.wb-expert-page{display:flex;flex-direction:column;overflow:hidden}
.wb-expert-top{display:flex;flex-wrap:wrap;align-items:center;gap:12px;padding:12px 20px;border-bottom:1px solid var(--dsw-alias-border-l3)}
.wb-expert-top strong{flex:1;min-width:130px;font-size:16px}
.wb-expert-top span{font-size:12px}
.wb-expert-layout{display:grid;grid-template-columns:clamp(168px,20%,260px) minmax(0,1fr);min-height:0;flex:1}
.wb-expert-docs{padding:20px 14px;border-right:1px solid var(--dsw-alias-border-l3);overflow:auto}
.wb-expert-docs h2{font-size:15px;margin:0 0 12px}.wb-expert-docs ul{padding:0;margin:14px 0;list-style:none}
.wb-expert-docs li{display:flex;align-items:center;gap:4px;min-width:0;margin:2px 0;border-radius:6px}
.wb-expert-docs li[data-current=true]{background:var(--dsw-alias-interactive-bg-hover)}
.wb-expert-docs .wb-doc-item{border:0;background:transparent;flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;text-align:left;padding:6px 8px}
.wb-expert-docs small{padding-right:6px;white-space:nowrap;font-size:11px}
.wb-expert-docs .wb-doc-menu{border:0;background:transparent;padding:4px 6px;min-height:28px}
.wb-expert-work{padding:18px clamp(12px,2vw,28px);overflow:auto;min-width:0}
.wb-expert-work h1{font-size:18px;font-weight:600;margin:0}
.wb-expert-doc-heading{display:flex;flex-wrap:wrap;align-items:center;gap:8px;justify-content:space-between;margin-bottom:12px}
.wb-expert-panes{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:12px}
.wb-expert-pane{min-width:0;border:1px solid var(--dsw-alias-border-l3);border-radius:8px;min-height:clamp(300px,60vh,760px);overflow:hidden}
.wb-expert-pane h2{font-size:14px;font-weight:500;color:var(--dsw-alias-label-secondary);margin:0;padding:9px 12px;border-bottom:1px solid var(--dsw-alias-border-l3)}
.wb-expert-pane .wb-vault-editor{border:0;min-height:0}
.wb-expert-pane .cm-editor{min-height:clamp(260px,55vh,720px);font-size:14px}
.wb-expert-pane .cm-gutters{display:none}
.wb-expert-pane .cm-content{font-family:inherit;padding:12px;line-height:1.65}
.wb-expert-pane .wb-vault-markdown{padding:12px 16px;overflow-wrap:anywhere}
.wb-expert-pane .wb-vault-markdown :is(h1,h2,h3){margin:0 0 12px;color:inherit}
.wb-expert-pane .wb-vault-markdown :is(p,ul,ol){margin:0 0 14px}
.wb-expert-pane .wb-vault-markdown pre{overflow:auto}
.wb-expert-meta{margin:12px 0 0;color:var(--dsw-alias-label-secondary)}
.wb-expert-mobile{display:none}
.wb-expert-create{max-width:540px;margin:24px auto;padding:0 24px}
@media(max-width:700px){
 .wb-expert-mobile{display:inline-block}.wb-expert-layout{grid-template-columns:minmax(0,1fr)}
 .wb-expert-docs{display:none}.wb-expert-docs[data-open=true]{display:block;max-height:32vh;border-right:0;border-bottom:1px solid var(--dsw-alias-border-l3)}
 .wb-expert-layout:has(.wb-expert-docs[data-open=true]){grid-template-rows:auto minmax(0,1fr)}
 .wb-expert-panes{display:block}.wb-expert-panes[data-view=edit] .wb-expert-pane[data-preview],.wb-expert-panes[data-view=preview] .wb-expert-pane:not([data-preview]){display:none}
 .wb-expert-top{padding:8px 10px;gap:6px}.wb-expert-top strong{font-size:14px}.wb-expert-top button{padding:5px 8px}
}
`;
    function ExpertDocumentPage({row, projects, back, changed, backLabel="返回专家", allowEnable=false, openSuperAgent}) {
      const cached=expertNotes.get(row.id);
      const [item,setItem]=useState(()=>cached?.item||row);
      const [draft,setDraft]=useState(()=>cached?.draft||expertPayload(row));
      const [active,setActive]=useState(expertMain),[tree,setTree]=useState(false),[mobile,setMobile]=useState("edit");
      const [error,setError]=useState(""),[notice,setNotice]=useState(""),[busy,setBusy]=useState(false);
      const [add,setAdd]=useState(null),[menu,setMenu]=useState(""),[removal,setRemoval]=useState(),[undo,setUndo]=useState();
      const [editor,setEditor]=useState("loading"),[editorKey,setEditorKey]=useState(0),[preview,setPreview]=useState();
      const [sample,setSample]=useState(""),[strategy,setStrategy]=useState(""),[trial,setTrial]=useState();
      const pending=useRef(false);
      const live=useRef(true);
      const cacheOwner=useRef(Symbol()), copyKey=useRef("");
      const clearCache=()=>{if(expertNotes.get(item.id)?.owner===cacheOwner.current)expertNotes.delete(item.id)};
      useEffect(()=>{live.current=true;return()=>{live.current=false}},[]);
      const operations=item.source?.startsWith("operations-"), local=item.source==="operations-local";
      const hasRefs=!operations&&item.source!=="personal";
      const dirty=!expertSaved(draft,expertPayload(item));
      const selected=draft.references.find(ref=>"references/"+ref.name===active);
      const activeTitle=selected?noteTitle(selected):"方法正文";
      const activeText=selected?selected.content:draft.content;
      const open=path=>{
        if(path!==active){setActive(path);setEditor("loading");setPreview(undefined)}
        setTree(false);setMenu("");
      };
      const resetEditor=()=>{setEditor("loading");setEditorKey(key=>key+1)};
      useEffect(()=>{
        if(dirty)expertNotes.set(item.id,{item,draft,owner:cacheOwner.current});
        else clearCache();
      },[item,draft,dirty]);
      useEffect(()=>{
        if(!dirty)return;
        const prevent=e=>{e.preventDefault();e.returnValue=""};
        window.addEventListener("beforeunload",prevent);
        return()=>window.removeEventListener("beforeunload",prevent);
      },[dirty]);
      useEffect(()=>{
        let alive=true;
        const handle=setTimeout(()=>{
          api("/expert-documents",{action:"preview",path:active,content:draft.content,references:draft.references})
            .then(value=>{if(alive)setPreview({path:active,html:value.html})})
            .catch(e=>{if(alive)setPreview({path:active,error:e.message})});
        },180);
        return()=>{alive=false;clearTimeout(handle)};
      },[active,draft.content,draft.references]);
      const act=async fn=>{
        if(pending.current)return;
        pending.current=true;setBusy(true);setError("");setNotice("");
        try{await fn()}catch(e){if(live.current)setError(e.message)}
        finally{pending.current=false;if(live.current)setBusy(false)}
      };
      const replace=saved=>{
        clearCache();
        if(!live.current){changed?.(saved);return}
        setItem(saved.id===item.id&&saved.usage===undefined?{...saved,usage:item.usage}:saved);
        setDraft(expertPayload(saved));setUndo(undefined);setRemoval(undefined);setTrial(undefined);
        changed?.(saved);
      };
      const update=(name,value)=>setDraft(prev=>({...prev,[name]:value}));
      const save=action=>act(async()=>{
        const saved=await api("/experts",{id:item.id,revision:item.revision,action,...draft});
        replace(saved);setNotice(saved.draft?"草稿已保存":"已保存");
      });
      const canSave=item.editable&&editor==="ready"&&!busy&&!removal&&!!draft.content.trim();
      const saveMain=()=>{if(canSave&&(dirty||item.draft))void save(item.draft?"publish-draft":"update")};
      const addDocument=event=>{
        event.preventDefault();
        const title=add.trim().replace(/\s+/g," ").slice(0,100);
        if(!title||draft.references.length>=20||pending.current)return;
        const name="doc-"+crypto.randomUUID().replaceAll("-","").slice(0,16)+".md";
        setDraft(prev=>({...prev,references:[...prev.references,{name,content:"# "+title+"\n\n"}]}));
        open("references/"+name);setAdd(null);
      };
      const addLink=()=>{
        if(!selected)return;
        const label=noteTitle(selected).replace(/[\\\[\]]/g,"\\$&");
        const link="["+label+"](references/"+selected.name+")";
        if(!draft.content.includes("](references/"+selected.name+")"))update("content",draft.content.trimEnd()+"\n\n- "+link+"\n");
        open(expertMain);
      };
      const remove=(path,confirmed=false)=>act(async()=>{
        const response=await api("/expert-documents",{action:"remove",path,confirmed,content:draft.content,references:draft.references});
        if(response.references===undefined){setRemoval({path,usedBy:response.usedBy});return}
        const after={...draft,content:response.content,references:response.references};
        setUndo({before:draft,after,path});setDraft(after);setRemoval(undefined);
        if(active===path)open(expertMain);
        else if(active===expertMain?draft.content!==after.content:
          selected?.content!==after.references.find(ref=>"references/"+ref.name===active)?.content)resetEditor();
        setNotice("文档已从本次修改中移除");
      });
      const restore=()=>{
        const {before,after,path}=undo;
        const saved=before.references.find(ref=>"references/"+ref.name===path);
        const refs=draft.references.map(ref=>{
          const old=before.references.find(x=>x.name===ref.name);
          const changed=after.references.find(x=>x.name===ref.name);
          return old&&changed?.content===ref.content?old:ref;
        });
        if(saved&&!refs.some(ref=>ref.name===saved.name))refs.push(saved);
        setDraft({...draft,content:draft.content===after.content?before.content:draft.content,references:refs});
        setUndo(undefined);setNotice("已撤销删除");resetEditor();
      };
      const follow=e=>{
        const href=e.target.closest?.("a")?.getAttribute("href")||"";
        const match=/^#wb-vault\/([^/]+)\//.exec(href);
        if(!match)return;
        e.preventDefault();
        if(pending.current||removal)return;
        const path=decodeURIComponent(match[1]);
        if(path===expertMain||draft.references.some(ref=>"references/"+ref.name===path))open(path);
      };
      const copy=()=>act(async()=>{
        copyKey.current ||= "expert-"+crypto.randomUUID().replaceAll("-","");
        const result=await api("/experts",operations?{id:item.id,action:"copy",revision:item.revision}:
          {id:item.id,action:"copy",name:copyKey.current,projectId:item.projectId||"",revision:item.revision,
            displayName:(expertTitle(item).slice(0,116)+" 副本")});
        copyKey.current="";
        open(expertMain);resetEditor();replace(result);
      });
      const opsAction=(action,extra={})=>act(async()=>{
        const result=await api("/experts",{id:item.id,action,revision:item.revision,...extra});
        if(action==="trial"){setTrial(result);if(!result.fixed)setItem({...item,tested:true})}
        else replace(result);
      });
      const trialRun=()=>{
        let input;
        try{input=JSON.parse(sample)}catch{setError("请输入 JSON 格式的试跑数据");return}
        if(!input||typeof input!=="object"||Array.isArray(input)){setError("试跑数据需要是一个 JSON 对象");return}
        opsAction("trial",{sample:input});
      };
      const scope=projects.find(p=>p.id===item.projectId)?.name;
      return h("section",{className:"wb-page wb-expert-page","aria-label":"专家明细",onKeyDown:e=>{
        if((e.metaKey||e.ctrlKey)&&e.key.toLowerCase()==="s"){e.preventDefault();if(canSave)void save(item.draft?"save-draft":"update")}
      }},
        h("style",null,expertCSS),
        h("header",{className:"wb-expert-top"},
          button(backLabel,()=>{if(!dirty||window.confirm("离开且不保存本次修改？")){clearCache();back()}},{disabled:busy}),
          h("strong",null,expertTitle(item)),
          button("目录",()=>setTree(v=>!v),{className:"wb-expert-mobile","aria-expanded":tree}),
          h("span",{role:"status",className:"wb-muted"},busy?"处理中…":dirty?"有未保存的修改":notice||(item.draft?"草稿":item.editable?"已保存":"只读")),
          item.draft&&button("保存草稿",()=>save("save-draft"),{disabled:!canSave||!dirty}),
          item.editable&&button(item.draft?"保存并启用":"保存修改",saveMain,{className:"wb-primary",disabled:!canSave||!dirty&&!item.draft})),
        h("div",{className:"wb-expert-layout"},
          h("nav",{className:"wb-expert-docs","aria-label":"专家文档","data-open":tree},
            h("h2",null,"专家文档"),
            hasRefs&&item.editable&&(add===null?button("新建文档",()=>setAdd(""),{disabled:busy||!!removal||draft.references.length>=20}):
              h("form",{onSubmit:addDocument},field("文档名称",h("input",{required:true,maxLength:100,value:add,onChange:e=>setAdd(e.target.value),autoFocus:true})),
                h("div",{className:"wb-actions"},h("button",{type:"submit",disabled:!add.trim()||busy},"添加"),button("取消",()=>setAdd(null))))),
            h("ul",null,h("li",{"data-current":active===expertMain},
              button("方法正文",()=>open(expertMain),{className:"wb-doc-item","aria-current":active===expertMain,disabled:busy}),
              h("small",{className:"wb-muted"},"主文档")),
              ...draft.references.map(ref=>{
                const path="references/"+ref.name,title=noteTitle(ref);
                return h("li",{key:path,"data-current":active===path},
                  button(title,()=>open(path),{className:"wb-doc-item","aria-current":active===path,disabled:busy,title}),
                  item.editable&&h(UI.Menu,{open:menu===path,onClose:()=>setMenu(""),portal:true,align:"end",
                    anchor:button(h(UI.IconEllipsisOutlineMedium,{size:16}),()=>setMenu(menu===path?"":path),{className:"wb-doc-menu","aria-label":title+"的文档操作","aria-haspopup":"menu","aria-expanded":menu===path,disabled:busy||!!removal}),
                    items:[{id:"remove",label:"删除文档",danger:true}],onSelect:()=>{setMenu("");remove(path)}}));
              }))),
          h("article",{className:"wb-expert-work"},
            h("div",{className:"wb-expert-doc-heading"},h("h1",null,activeTitle),
              h("div",{className:"wb-actions"},
                button("编辑",()=>setMobile("edit"),{className:"wb-expert-mobile","aria-pressed":mobile==="edit"}),
                button("预览",()=>setMobile("preview"),{className:"wb-expert-mobile","aria-pressed":mobile==="preview"}),
                selected&&item.editable&&button("在正文引用",addLink,{disabled:busy||!!removal}),
                undo&&button("撤销删除",restore,{disabled:busy||!!removal}))),
            alert(error),
            removal&&h("section",{className:"wb-section","aria-label":"删除文档的引用"},
              h("p",null,"以下文档引用了此文档："+removal.usedBy.map(path=>path===expertMain?"方法正文":noteTitle(draft.references.find(ref=>"references/"+ref.name===path)||{content:"",name:path})).join("、")),
              h("div",{className:"wb-actions"},button("移除引用并删除",()=>remove(removal.path,true),{disabled:busy}),button("取消",()=>setRemoval(undefined),{disabled:busy}))),
            h("div",{className:"wb-expert-panes","data-view":mobile},
              h("section",{className:"wb-expert-pane","aria-label":"编辑"},h("h2",null,item.editable?"编辑":"正文"),
                h(VaultEditor,{key:active+":"+editorKey,attempt:editorKey,text:activeText,onStatus:setEditor,readOnly:!item.editable||busy||!!removal,
                  onChange:text=>setDraft(prev=>selected?{...prev,references:prev.references.map(ref=>ref.name===selected.name?{...ref,content:text}:ref)}:{...prev,content:text})}),
                editor==="failed"&&button("重试加载编辑器",resetEditor)),
              h("section",{className:"wb-expert-pane","data-preview":true,"aria-label":"预览",onClickCapture:follow},h("h2",null,"预览"),
                preview?.path===active?(preview.error?alert(preview.error):h("div",{className:"wb-vault-markdown",dangerouslySetInnerHTML:{__html:preview.html}})):
                  h("p",{role:"status",className:"wb-muted"},"正在渲染"))),
            h("details",{className:"wb-expert-meta"},h("summary",null,"基本信息"),
              h("p",null,[scope,expertOrigins[item.source]].filter(Boolean).join(" · ")),
              item.editable&&!operations&&item.source!=="personal"?h("div",{className:"wb-form"},
                field("名称",h("input",{value:draft.displayName,maxLength:120,disabled:busy,onChange:e=>update("displayName",e.target.value)})),
                field("描述",h("textarea",{value:draft.description,maxLength:1024,disabled:busy,onChange:e=>update("description",e.target.value)}))):h("p",null,item.description),
              item.usage?.length>0&&h("p",null,"使用位置："+item.usage.join("、")),
              item.revision&&h("p",null,"修订 "+item.revision),
              openSuperAgent&&button("到 Super Agent 起草",openSuperAgent,{disabled:busy})),
            !item.draft&&item.source!=="personal"&&(!operations||!local)&&button(operations?"复制为工作台版":"复制专家",copy,{disabled:busy||dirty}),
            item.fixedOutput&&h("p",{className:"wb-muted"},"带固定模板的条目仍由原规则生成。"),
            local&&h("details",{className:"wb-expert-meta"},h("summary",null,"试跑与启用"),
              item.tested&&h("p",null,"当前版本已通过输出校验"),
              item.requiredFields?.length>0&&h("p",null,"必填字段 "+item.requiredFields.join("、")),
              field("试跑数据",h("textarea",{rows:4,value:sample,onChange:e=>setSample(e.target.value),placeholder:"输入脱敏后的 JSON 对象"})),
              button("运行校验",trialRun,{disabled:busy||dirty||!sample.trim()}),
              trial&&h(React.Fragment,null,trial.fixed&&h("p",null,"固定模板已通过校验。启用需再试跑一条需要模型生成的数据。"),h("pre",null,trial.text)),
              allowEnable&&h("div",{className:"wb-form"},
                field("启用场景",h("select",{value:strategy,onChange:e=>setStrategy(e.target.value)},h("option",{value:""},"选择运营策略"),
                  ...(item.strategies||[]).map(key=>h("option",{key,value:key},key)))),
                button("用于该策略的新批次",()=>opsAction("enable",{strategy}),{disabled:busy||dirty||!strategy||!item.tested||item.usage?.includes(strategy)}))))));
    }
    function NewExpert({project,back,created}) {
      const [name,setName]=useState(""),[description,setDescription]=useState(""),[error,setError]=useState(""),[busy,setBusy]=useState(false);
      const [createKey]=useState(()=>crypto.randomUUID().replaceAll("-",""));
      const pending=useRef(false);
      const submit=async e=>{
        e.preventDefault();
        if(pending.current||!name.trim()||!description.trim())return;
        pending.current=true;setBusy(true);setError("");
        try{created(await api("/experts",{action:"create-draft",createKey,projectId:project?.id||"",displayName:name,description}))}
        catch(e){setError(e.message)}
        finally{pending.current=false;setBusy(false)}
      };
      return h("section",{className:"wb-page"},h("style",null,expertCSS),
        h("form",{className:"wb-form wb-expert-create",onSubmit:submit},
          h("h1",null,"新建专家"),h("p",{className:"wb-muted"},"所属范围："+(project?.name||"工作台共享")),alert(error),
          field("名称",h("input",{required:true,maxLength:120,value:name,disabled:busy,onChange:e=>setName(e.target.value),autoFocus:true})),
          field("描述",h("textarea",{required:true,rows:3,maxLength:1024,value:description,disabled:busy,onChange:e=>setDescription(e.target.value)})),
          h("div",{className:"wb-actions"},button("返回专家",back,{disabled:busy}),
            h("button",{type:"submit",className:"wb-primary",disabled:busy||!name.trim()||!description.trim()},busy?"创建中…":"创建"))));
    }
    function ExpertsPage({openSuperAgent}) {
      const {data}=useProjectList();
      const [rows,setRows]=useState(),[item,setItem]=useState(),[creating,setCreating]=useState(()=>requestedExpertProject||false);
      const [search,setSearch]=useState(""),[error,setError]=useState("");
      const viewport=useRef(null),scroll=useRef(0),request=useRef(0);
      const reload=()=>api("/experts").then(({items,unavailable})=>{setRows(items);setError(unavailable.length?"部分来源暂不可读 · "+unavailable.join("、"):"")}).catch(e=>setError(e.message));
      useEffect(()=>{
        requestedExpertProject="";
        return onEvent("wb-create-project-expert",e=>{request.current++;setItem(undefined);setCreating(e.detail)});
      },[]);
      useEffect(()=>{if(data?.expertManagement)reload();return()=>{request.current++}},[data?.expertManagement]);
      useEffect(()=>{if(!item&&!creating&&viewport.current)viewport.current.scrollTop=scroll.current},[item,creating]);
      if(!data)return h("section",{className:"wb-page"},h("div",{className:"wb-content",role:"status"},"正在读取专家…"));
      if(!data.expertManagement)return h(LegacyExpertsPage);
      const project=data.projects.find(p=>p.type==="ordinary"&&p.id===creating);
      const show=async row=>{
        const n=++request.current;setError("");scroll.current=viewport.current?.scrollTop||0;
        try{const loaded=await expertDetails(row.id);if(n===request.current)setItem(loaded)}catch(e){if(n===request.current)setError(e.message)}
      };
      if(item)return h(ExpertDocumentPage,{key:item.id,row:item,projects:data.projects,back:()=>setItem(undefined),changed:reload,openSuperAgent});
      if(creating)return h(NewExpert,{project,back:()=>setCreating(false),created:row=>{setCreating(false);setItem(row);reload()}});
      const filtered=(rows||[]).filter(row=>[expertTitle(row),row.name,row.description,data.projects.find(p=>p.id===row.projectId)?.name]
        .join(" ").toLowerCase().includes(search.trim().toLowerCase()));
      return h("section",{className:"wb-page",ref:viewport},h("div",{className:"wb-content"},
        h("div",{className:"wb-heading"},h("h1",null,"专家"),button("新建专家",()=>{request.current++;scroll.current=viewport.current?.scrollTop||0;setCreating(true)},{className:"wb-primary"})),
        field("搜索专家",h("input",{type:"search",value:search,onChange:e=>setSearch(e.target.value)})),
        alert(error),!rows&&!error&&h("p",{role:"status"},"正在读取专家…"),
        rows&&!filtered.length&&h("p",{role:"status",className:"wb-muted"},"没有匹配的专家"),
        h("div",{className:"wb-list",style:{marginTop:16}},...filtered.map(row=>{
          const scope=data.projects.find(p=>p.id===row.projectId)?.name;
          return h("div",{key:row.id,className:"wb-row"},h("div",{className:"wb-row-main"},
            h("strong",null,expertTitle(row)),
            h("div",{className:"wb-muted"},[scope,expertOrigins[row.source],row.draft?"草稿":""].filter(Boolean).join(" · ")),
            h("p",null,row.description)),button("查看专家明细",()=>show(row),{"aria-label":"查看专家明细："+expertTitle(row)}));
        }))));
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
          button("插件管理",openNativePlugins),button("刷新",load))),
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
    const stateText = r => ({Running:"执行中",Completed:r.published?"结果已发布":"执行已结束",Failed:"执行失败",Canceled:"已停止",Terminated:"已终止",TimedOut:"已超时",Unknown:"状态待核实"}[r.execution]||r.execution);
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
    function usePanelRead(source, active, interval) {
      const [result,setResult]=useState();
      const [revision,refresh]=React.useReducer(value=>value+1,0);
      useEffect(()=>{
        if(!active)return;
        let request,timer;
        const stop=()=>{
          clearTimeout(timer);
          request?.abort();
          request=undefined;
        };
        const read=async()=>{
          if(document.hidden||request)return;
          clearTimeout(timer);
          const controller=new AbortController();
          request=controller;
          const signal=AbortSignal.any([controller.signal,AbortSignal.timeout(12000)]);
          try{
            const value=typeof source==="string"?await api(source,undefined,signal):await source(signal);
            if(!controller.signal.aborted)setResult({source,data:value,error:""});
          }catch(e){
            if(!controller.signal.aborted)setResult(previous=>({source,
              data:previous?.source===source?previous.data:undefined,
              error:signal.aborted?"读取超时，请重试。":e.message}));
          }finally{
            if(!controller.signal.aborted){
              request=undefined;
              if(interval&&!document.hidden)timer=setTimeout(read,interval);
            }
          }
        };
        const visibility=()=>{if(document.hidden)stop();else read()};
        document.addEventListener("visibilitychange",visibility);
        read();
        return()=>{document.removeEventListener("visibilitychange",visibility);stop()};
      },[source,active,interval,revision]);
      return {data:result?.source===source?result.data:undefined,error:result?.source===source?result.error:"",refresh};
    }
    function PanelReadState({read,loading}) {
      if(read.error)return h("div",null,
        h("p",{role:"alert"},read.data===undefined?read.error:"刷新失败，显示上次读取结果。"+read.error),
        button("重试",read.refresh));
      if(read.data===undefined)return h("p",{role:"status"},loading);
      return null;
    }
    function SchedulesPage({openSession,openPanel}) {
      const schedules=usePanelRead("/schedules",true,15000);
      const [error,setError]=useState(""),[busy,setBusy]=useState(false);
      const act=async fn=>{
        setBusy(true);setError("");
        try{await fn();schedules.refresh()}catch(e){setError(e.message)}finally{setBusy(false)}
      };
      return h("section",{className:"wb-page"},h("style",null,css),h("div",{className:"wb-content"},
        h("div",{className:"wb-heading"},h("h1",null,"定时任务")),alert(error),
        h(PanelReadState,{read:schedules,loading:"正在读取定时任务…"}),
        schedules.data&&(schedules.data.length?h(ScheduleRows,{rows:schedules.data,openSession,act,busy,openPanel}):
          h("p",{className:"wb-muted"},"暂无定时任务。"))));
    }
    const sameNotificationTarget=(left,right)=>left.connectionId===right.connectionId&&
      left.target.conversationId===right.target.conversationId&&left.target.actorId===right.target.actorId;
    const loadNotificationSettings=async signal=>{
      const [settings,targets,personal]=await Promise.all([
        api("/notifications",undefined,signal),
        api("/notification-targets",undefined,signal),
        api("/personal/settings",undefined,signal),
      ]);
      return {settings:{...settings,sessionId:personal.sessionId,targets:settings.targets||[]},
        targets:targets.filter(target=>target.sessionId===personal.sessionId)};
    };
    function SuperAgentWork({openSession,active}) {
      const [expanded,setExpanded]=useState({});
      const [error,setError]=useState(""),[notice,setNotice]=useState(""),[busy,setBusy]=useState(false);
      const [edit,setEdit]=useState(),[answers,setAnswers]=useState({});
      const [settingsDraft,setSettings]=useState(),[notificationDraft,setNotifications]=useState();
      const writing=useRef(false);
      const decisions=usePanelRead("/decisions",active,15000);
      const runs=usePanelRead("/runs",active,15000);
      const memory=usePanelRead("/memory",active&&!!expanded.memory);
      const schedules=usePanelRead("/schedules",active&&!!expanded.schedules,15000);
      const notify=usePanelRead(loadNotificationSettings,active&&!!expanded.notifications);
      const personal=usePanelRead("/personal/settings",active&&!!expanded.personal);
      const settings=settingsDraft||personal.data;
      const notifications=notificationDraft||notify.data?.settings;
      const toggle=key=>e=>{
        const open=e.currentTarget.open;
        setExpanded(value=>({...value,[key]:open}));
      };
      const act=async (fn,refresh)=>{
        if(writing.current)return;
        writing.current=true;setBusy(true);setError("");setNotice("");
        try{await fn();refresh?.();setNotice("已保存")}
        catch(e){setError(e.message)}
        finally{writing.current=false;setBusy(false)}
      };
      return h("section",{className:"wb-page"},h("style",null,css),h("div",{className:"wb-content"},
        alert(error),notice&&h("p",{role:"status"},notice),
        h("div",{className:"wb-section"},
          h("h2",null,decisions.data?`等待你决定 · ${decisions.data.length}`:"等待你决定"),
          h(PanelReadState,{read:decisions,loading:"正在读取待决定事项…"}),
          decisions.data?.length===0&&h("p",{className:"wb-muted"},"暂无待决定事项。"),
          decisions.data?.map(d=>h("form",{key:d.id,className:"wb-form",onSubmit:e=>{
            e.preventDefault();
            act(async()=>{
              await api("/decisions/answer",{...d,answer:answers[d.id]});
              setAnswers(v=>({...v,[d.id]:""}));
            },decisions.refresh);
          }},
            h("p",{className:"wb-note",style:{whiteSpace:"pre-wrap"}},d.question),
            field("你的决定",h("textarea",{required:true,value:answers[d.id]||"",onChange:e=>setAnswers({...answers,[d.id]:e.target.value})})),
            h("div",{className:"wb-actions"},h("button",{type:"submit",disabled:busy},"确认并继续原事项"))))),
        h("div",{className:"wb-section"},
          h("h2",null,"近期工作"),
          h("p",{className:"wb-muted"},"执行状态直接核对后台任务；发送与送达以渠道回执为准。"),
          h(PanelReadState,{read:runs,loading:"正在核对近期工作…"}),
          runs.data?.length===0&&h("p",null,"暂无任务。"),
          runs.data?.map(r=>h("div",{key:r.id,className:"wb-row"},
            h("div",{className:"wb-row-main"},h("strong",null,r.title),
              h("div",{className:"wb-muted"},time(r.createdAt)+(r.closedAt?" · "+time(r.closedAt)+" 结束":"")),
              h("div",null,stateText(r)),
              r.stats?.total>0&&h("div",{className:"wb-muted"},`${r.stats.total} 条 · 成功 ${r.stats.succeeded} · 失败 ${r.stats.failed} · 待处理 ${r.stats.pending}`),
              r.deliveries&&h("div",{className:"wb-note"},receiptText(r))),
            button("查看对话",()=>openSession(r.sessionId))))),
        h("details",{className:"wb-section",onToggle:toggle("memory")},
          h("summary",null,memory.data?`个人规则与记忆 · ${memory.data.length}`:"个人规则与记忆"),
          h(PanelReadState,{read:memory,loading:"正在读取个人规则与记忆…"}),
          memory.data&&h(React.Fragment,null,
            h("p",{className:"wb-muted"},"可以纠正或忘记；推断与本人确认分别保存。"),
            button("添加个人规则",()=>setEdit({id:"memory_"+crypto.randomUUID(),revision:0,text:"",source:"本人在 SuperAgent 设置中填写",confirmed:true,forgotten:false})),
            memory.data.map(m=>h("div",{key:m.id,className:"wb-row"},
              h("div",{className:"wb-row-main"},h("p",null,m.text),h("div",{className:"wb-muted"},`${m.confirmed?"本人确认":"待确认"} · ${m.source}`)),
              button("纠正",()=>setEdit({...m})),
              button("忘记",()=>act(()=>api("/memory",{...m,forgotten:true}),memory.refresh),{disabled:busy}))),
            edit&&h("form",{className:"wb-form",onSubmit:e=>{
              e.preventDefault();
              act(async()=>{await api("/memory",{...edit,confirmed:true});setEdit(undefined)},memory.refresh);
            }},
              field("记忆内容",h("textarea",{required:true,value:edit.text,onChange:e=>setEdit({...edit,text:e.target.value})})),
              field("来源",h("input",{required:true,value:edit.source,onChange:e=>setEdit({...edit,source:e.target.value})})),
              h("div",{className:"wb-actions"},h("button",{type:"submit",disabled:busy},"确认保存"),button("取消",()=>setEdit(undefined)))))),
        h("details",{className:"wb-section",onToggle:toggle("schedules")},
          h("summary",null,schedules.data?`定时工作 · ${schedules.data.length}`:"定时工作"),
          h("p",{className:"wb-muted"},"在项目对话中说明资料、方法和执行时间即可配置；每次结果回到原对话，上一轮未结束时跳过重叠执行。"),
          h(PanelReadState,{read:schedules,loading:"正在读取定时工作…"}),
          schedules.data?.length===0&&h("p",{className:"wb-muted"},"暂无定时任务。"),
          schedules.data&&h(ScheduleRows,{rows:schedules.data,openSession,act:fn=>act(fn,schedules.refresh),busy})),
        h("details",{className:"wb-section",onToggle:toggle("notifications")},
          h("summary",null,"本人消息通知"),
          h("p",{className:"wb-muted"},"只通知有实质结果、异常和需要你决定的事项；不会因此重新执行原任务。"),
          h(PanelReadState,{read:notify,loading:"正在读取通知设置…"}),
          notify.data&&(notify.data.targets.length===0?h("p",null,"尚未连接本人微信或飞书。完成绑定后，可在这里选择通知渠道。"):
            h("form",{className:"wb-form",onSubmit:e=>{
              e.preventDefault();act(()=>api("/notifications",{
                ...notifications,targets:notifications.targets.filter(n=>notify.data.targets.some(t=>sameNotificationTarget(n,t))),
              }),notify.refresh);
            }},
              h("label",{className:"wb-check"},
                h("input",{type:"checkbox",checked:notifications.enabled,onChange:e=>setNotifications({...notifications,enabled:e.target.checked})}),"开启本人通知"),
              notify.data.targets.map(t=>h("label",{key:t.connectionId,className:"wb-check"},
                h("input",{type:"checkbox",checked:notifications.targets.some(n=>sameNotificationTarget(n,t)),
                  onChange:e=>setNotifications({...notifications,targets:e.target.checked?
                    [...notifications.targets.filter(n=>n.connectionId!==t.connectionId),{connectionId:t.connectionId,target:t.target}]:
                    notifications.targets.filter(n=>n.connectionId!==t.connectionId)})}),t.name)),
              h("button",{type:"submit",disabled:busy},"保存通知设置")))),
        h("details",{className:"wb-section",onToggle:toggle("personal")},
          h("summary",null,"每日安排时间"),
          h(PanelReadState,{read:personal,loading:"正在读取每日安排时间…"}),
          settings&&h("form",{className:"wb-form",onSubmit:e=>{
            e.preventDefault();act(()=>api("/personal/settings",settings),personal.refresh);
          }},
            field("北京时间",h("input",{type:"time",required:true,value:String(settings.hour).padStart(2,"0")+":"+String(settings.minute).padStart(2,"0"),
              onChange:e=>{const [hour,minute]=e.target.value.split(":").map(Number);setSettings({...settings,hour,minute})}})),
            h("label",{className:"wb-check"},h("input",{type:"checkbox",checked:settings.paused,
              onChange:e=>setSettings({...settings,paused:e.target.checked})}),"暂停每日汇总"),
            h("button",{type:"submit",disabled:busy},"保存安排")))));
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
      const [open,setOpen]=useState(false);
      const read=usePanelRead(query("/reply-evidence",{sessionId,runId}),open,5000);
      const {data}=read;
      const toggle=()=>setOpen(v=>!v);
      const label=name=>name==='skill'?'加载专家方法':/context$/.test(name)?'读取客户上下文':/search$/.test(name)?'检索资料':/read$|excerpt$/.test(name)?'读取来源原文':/check$|validate$/.test(name)?'核对回答依据':/reply$/.test(name)?'提交回复':/receipt$/.test(name)?'核对发送回执':name.split('__').pop();
      return h('div',{className:'ar-evidence'},button(open?'收起处理明细':'查看依据与处理明细',toggle,{'aria-expanded':open}),open&&h('div',null,h(PanelReadState,{read,loading:'正在读取本轮记录…'}),data&&h('ol',null,...data.calls.map(c=>h('li',{key:c.id},h('strong',null,label(c.name)),c.failed&&' · 未完成',h('details',null,h('summary',null,'查看记录'),h('pre',null,c.output||c.input))))),data?.calls.length===0&&h('p',null,'此轮暂无可读取的工具记录。')));
    }
    function ReplyPage({openSession,sessionId='',selectedProject=''}){
      const UI=require('@deepseek-ai/dsh-client-ui-primitives'),{Switch,Modal,MarkdownText,IconSettingsOutlineRegular,IconCloseOutlineRegular,IconPaperPlaneOutlineRegular,IconChevronLeftOutlineRegular,IconAgentPresetOutlineRegular}=UI;
      const [catalog,setCatalog]=useState(),[projects,setProjects]=useState([]),[project,setProject]=useState(replyProject),[page,setPage]=useState({items:[]}),[q,setQ]=useState(''),[cursor,setCursor]=useState(''),[trail,setTrail]=useState([]),[route,setRoute]=useState(),[timeline,setTimeline]=useState({items:[]}),[edit,setEdit]=useState(),[activeSettings,setActiveSettings]=useState(),[accounts,setAccounts]=useState([]),[experts,setExperts]=useState([]),[resources,setResources]=useState([]),[settings,setSettings]=useState(false),[modal,setModal]=useState(),[error,setError]=useState(''),[notice,setNotice]=useState(''),[busy,setBusy]=useState(false),[loading,setLoading]=useState(true),[tick,setTick]=useState(0),[draft,setDraft]=useState(''),[intent,setIntent]=useState();
      const [defaultExpert,setDefaultExpert]=useState(),[expertChoice,setExpertChoice]=useState(""),[expertDetail,setExpertDetail]=useState();
      const stream=useRef(null),stick=useRef(true),sending=useRef(false),timelineRef=useRef([]),refreshOffset=useRef(0);
      useEffect(()=>{timelineRef.current=timeline.items},[timeline.items]);
      const reset=()=>{setCursor('');setTrail([])};
      const reloadAccounts=async id=>{try{setAccounts(await api('/reply-accounts?projectId='+encodeURIComponent(id||project)))}catch(e){setError(e.message)}};
      useEffect(()=>{let current=true;replyProjects().then(async v=>{if(!current)return;setCatalog(v.catalog);setProjects(v.projects);let id=selectedProject||replyProject||v.projects[0]?.projectId||'';if(sessionId){const r=await api('/reply-session?sessionId='+encodeURIComponent(sessionId));if(!current)return;setRoute(r);id=r.principal.projectId}setProject(id);replyProject=id;setLoading(false)}).catch(e=>{if(current){setError(e.message);setLoading(false)}});return()=>{current=false}},[sessionId,selectedProject]);
      useEffect(()=>{if(!project)return;let current=true;api('/message-routes?'+new URLSearchParams({projectId:project,q,after:cursor})).then(v=>{if(current){setPage(v);setLoading(false)}}).catch(e=>{if(current)setError(e.message)});return()=>{current=false}},[project,q,cursor,tick]);
      useEffect(()=>{if(!project)return;let current=true;Promise.all([api('/reply-settings?projectId='+project),api('/reply-resources?projectId='+project),api('/reply-experts?projectId='+project)]).then(([s,r,e])=>{if(current){setEdit(s);setActiveSettings(s);setResources(r);if(Array.isArray(e)){setDefaultExpert(undefined);setExperts(e)}else{setExperts(e.options);setDefaultExpert(e);setExpertChoice(e.preferred||"")}}}).catch(e=>{if(current)setError(e.message)});reloadAccounts(project);return()=>{current=false}},[project]);
      useEffect(()=>{if(!sessionId)return;let current=true;setTimeline({items:[]});setDraft('');setIntent(undefined);stick.current=true;const load=async()=>{try{const [r,t]=await Promise.all([api('/reply-session?sessionId='+sessionId),api('/reply-timeline?sessionId='+sessionId)]);const saved=await api('/reply-settings?projectId='+r.principal.projectId);const candidates=timelineRef.current.filter(x=>!['delivered','failed','ignored','handoff','paused','human','reply_sent','reply_delivered','reply_failed'].includes(x.state));const start=candidates.length?refreshOffset.current%candidates.length:0;const keys=[...candidates.slice(start),...candidates.slice(0,start)].slice(0,100).map(x=>x.key);refreshOffset.current+=100;const states=keys.length?await api('/reply-states?'+new URLSearchParams({sessionId,keys:JSON.stringify(keys)})):{};if(current){setRoute(r);setActiveSettings(saved);setTimeline(v=>{if(!v.items.length)return t;const keys=new Set(v.items.map(x=>x.key));if(t.items.length&&!t.items.some(x=>keys.has(x.key)))return t;return{items:[...new Map([...v.items,...t.items].map(x=>[x.key,x])).values()].map(x=>states[x.key]?{...x,state:states[x.key]}:x).sort(timelineOrder),before:v.before}})}}catch(e){if(current)setError(e.message)}};load();const timer=setInterval(()=>{load();setTick(v=>v+1)},5000);return()=>{current=false;clearInterval(timer)}},[sessionId]);
      useEffect(()=>{if(stick.current&&stream.current)stream.current.scrollTop=stream.current.scrollHeight},[timeline.items.length,sessionId]);
      const change=(key,value)=>setEdit(v=>({...v,[key]:value}));
      const act=async fn=>{setBusy(true);setError('');setNotice('');try{await fn();setTick(v=>v+1)}catch(e){setError(e.message)}finally{setBusy(false)}};
      const save=e=>{e.preventDefault();act(async()=>{const saved=await api('/reply-settings',edit);setEdit(saved);setActiveSettings(saved);setNotice('设置已保存')})};
      const mode=()=>act(async()=>{await api('/message-routes',{id:route.id,revision:route.revision,autoReply:!route.autoReply});setRoute(await api('/reply-session?sessionId='+sessionId))});
      const send=async()=>{if(sending.current)return;sending.current=true;try{await act(async()=>{const request=intent||{sessionId,key:crypto.randomUUID(),text:draft,revision:route.revision};setIntent(request);try{await api('/reply-manual',request)}catch(e){if([400,403,404,409].includes(e.status)){setIntent(undefined);setRoute(await api('/reply-session?sessionId='+sessionId))}throw e}setDraft('');setIntent(undefined);setRoute(await api('/reply-session?sessionId='+sessionId));const t=await api('/reply-timeline?sessionId='+sessionId);stick.current=true;setTimeline(t);setNotice('消息已提交，正在等待渠道回执')})}finally{sending.current=false}};
      const older=()=>act(async()=>{const t=await api('/reply-timeline?'+new URLSearchParams({sessionId,before:timeline.before}));stick.current=false;setTimeline(v=>({items:[...new Map([...t.items,...v.items].map(x=>[x.key,x])).values()],before:t.before}))});
      const openExpert=()=>act(async()=>{
        if(defaultExpert){setExpertDetail(await expertDetails(expertChoice));return}
        const v=await api('/reply-experts?'+new URLSearchParams({projectId:project,name:edit.expertSkill}));
        setModal({kind:'expert',title:'回答专家',...v});
      });
      const refreshReplyExpert=saved=>{
        const refresh=rows=>rows.map(row=>row.id===saved.id?
          {...row,displayName:saved.displayName,description:saved.description,revision:saved.revision}:row);
        setExperts(refresh);
        setDefaultExpert(prev=>prev?{...prev,options:refresh(prev.options)}:prev);
      };
      const saveDefault=()=>act(async()=>{
        const saved=await api('/expert-default',{projectId:project,expertId:expertChoice,expected:defaultExpert.configured||""});
        setDefaultExpert({...defaultExpert,configured:saved.expertId,preferred:saved.expertId});
        setNotice("新会话默认专家已保存");
      });
      const showResources=()=>setModal({kind:'resources',title:'回答语料',resourceIds:selectedIds});
      const previewResource=chosen=>act(async()=>{const latest=await api('/reply-resources?projectId='+project);setResources(latest);const r=latest.find(v=>v.id===chosen.id);if(!r)throw Error('该资料已不可读取');const selected=edit.knowledge.find(v=>v.resourceId===r.id);const ref={resourceId:r.id,version:r.currentVersion,section:selected?.section||r.sections[0]?.id};const documents=await api('/resource-documents?'+new URLSearchParams({projectId:project,resourceId:r.id}));if(documents.external){setModal({kind:'documents',title:r.name,resource:r,...documents});return}const v=await api('/resource-page?'+new URLSearchParams(ref));setModal({kind:'resource',title:r.name,resource:r,page:v})});
      const readDocument=d=>act(async()=>{const v=await api('/resource-documents?'+new URLSearchParams({projectId:project,resourceId:modal.resource.id,uri:d.uri}));setModal({...modal,...v,documentTitle:d.title||d.name})});
      const currentAccount=accounts.find(a=>a.connectionId===route?.connectionId),settingsAccount=accounts.find(a=>a.connectionId===edit?.connectionId);
      const expertName=e=>e.displayName||(e.description||e.name).split(/[。；]/)[0].slice(0,26);
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
          h('div',{className:'ar-setting'},h('span',{className:'ar-setting-title'},defaultExpert?'新会话默认专家':'回答专家'),
            h('div',{className:'ar-select-row'},
              h('select',{'aria-label':defaultExpert?'新会话默认专家':'回答专家',value:defaultExpert?expertChoice:edit.expertSkill,
                disabled:!defaultExpert,onChange:e=>setExpertChoice(e.target.value)},
                defaultExpert&&!expertChoice&&h('option',{value:""},"选择专家"),
                ...experts.map(e=>h('option',{key:e.id||e.name,value:e.id||e.name},expertName(e)))),
              button('查看专家明细',openExpert,{className:'ar-link',disabled:busy||defaultExpert&&!expertChoice})),
            defaultExpert&&h(React.Fragment,null,h('p',{className:'ar-time'},'已有客户继续使用原专家。'),
              button('保存默认专家',saveDefault,{className:'ar-link',disabled:busy||!expertChoice||expertChoice===defaultExpert.preferred}))),
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
      return h(React.Fragment,null,
        expertDetail&&h(ExpertDocumentPage,{row:expertDetail,projects:catalog.projects,back:()=>setExpertDetail(undefined),
          backLabel:"返回应答设置",changed:refreshReplyExpert}),
        h('section',{className:'ar-root',style:expertDetail?{display:"none"}:undefined},
        h('style',null,replyCSS),
        h('header',{className:'ar-top'},h('h1',null,projectName),button(h(React.Fragment,null,h(IconSettingsOutlineRegular,{size:17}),'设置'),()=>setSettings(v=>!v),{className:'ar-btn','aria-expanded':settings})),
        error&&h('p',{className:'ar-error',role:'alert'},error),
        h('div',{className:'ar-layout','data-settings':settings,'data-selected':!!sessionId},sidebar,conversation,settingsPanel),
        h(Modal,{open:!!modal&&!expertDetail,onClose:()=>setModal(undefined),title:modal?.title||'查看资料',closeLabel:'关闭',className:'ar-modal',contentClassName:'ar-modal-content'},modalContent)));

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
      const {data:projectList}=useProjectList();
      const [methodRows,setMethodRows]=useState([]),[methodDetail,setMethodDetail]=useState();
      const [methodExpanded,setMethodExpanded]=useState(false);
      const settingsForm=useRef(),settingsOffset=useRef(0);
      useEffect(()=>{
        if(methodDetail||!settingsForm.current)return;
        const handle=requestAnimationFrame(()=>{if(settingsForm.current)settingsForm.current.scrollTop=settingsOffset.current});
        return()=>cancelAnimationFrame(handle);
      },[methodDetail]);
      const readMethods=()=>api("/experts").then(({items})=>setMethodRows(items.filter(row=>row.projectId===projectId&&row.source.startsWith("operations-"))));
      const [view,setView]=useState(null),[tab,setTab]=useState("work"),[page,setPage]=useState("home");
      const [runId,setRunId]=useState(""),[selected,setSelected]=useState(""),[profile,setProfile]=useState(null);
      const [profiles,setProfiles]=useState([]),[profileNext,setProfileNext]=useState(""),[profileState,setProfileState]=useState("");
      const [profileMeta,setProfileMeta]=useState({}),[searchText,setSearchText]=useState(""),[query,setQuery]=useState("");
      const [today,setToday]=useState(null),[todayError,setTodayError]=useState("");
      const [sourcePlan,setSourcePlan]=useState(null),[profileBack,setProfileBack]=useState("home");
      const [sourceCopy,setSourceCopy]=useState(null),[sourceConfirm,setSourceConfirm]=useState(null),[sourceSubmission,setSourceSubmission]=useState(""),[sourceItem,setSourceItem]=useState(null);
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
      const loadSourceCopy=(plan,run="",item="",after="")=>plan.daily
        ? api(path("source-today",{daily:plan.daily,item,after}))
        : api(path("source",{view:"package",package:plan.package_id,week:plan.week,run,item}));
      const loadVisibleSourceCopy=async(plan,count)=>{
        const data=await loadSourceCopy(plan,plan.run_id);
        while(data.next&&data.rows.length<count){
          const page=await loadSourceCopy(plan,data.run,"",data.next);
          if(page.reviewVersion!==data.reviewVersion)return loadSourceCopy(plan,plan.run_id);
          data.rows.push(...page.rows);
          data.next=page.next;
        }
        return data;
      };
      const loadSourceHistory=week=>act(async()=>{
        setSourceHistoryWeek(week);
        const [local,original]=await Promise.allSettled([
          api(path("source-today",{history:1})),api(path("source",{view:"package_history",week}))
        ]);
        if(local.status==="rejected"&&original.status==="rejected")throw original.reason;
        const published=(local.value?.packages||[]).filter(plan=>plan.week===week);
        const localRuns=new Set(published.map(plan=>plan.run_id));
        setSourceHistory({runs:[...published,...(original.value?.runs||[]).filter(plan=>!localRuns.has(plan.run_id))]});
      });
      const openPlan=plan=>{
        setSourcePlan(plan);setSourceCopy(null);setSourceItem(null);setSelected("");setCopyEdit(null);setCopyMessage("");
        setSourceConfirm(null);setSourceSubmission("");setPage("source-plan");
        act(async()=>setSourceCopy(await loadSourceCopy(plan,plan.run_id)));
      };
      const confirmSource=()=>act(async()=>{
        const {daily,reviewVersion}=sourceConfirm;
        const result=await api(path("source-confirm"),{daily,reviewVersion});
        setSourceConfirm(null);
        setSourceSubmission(result.runId);
        setCopyMessage("正在记录本次确认。");
      });
      useEffect(()=>{
        if(page!=="source-plan"||!sourcePlan?.daily||!sourceCopy||copyEdit||sourceConfirm)return;
        let live=true,timer;
        const poll=async()=>{
          try{
            if(sourceSubmission){
              const result=await api(path("source-package-status",{id:sourceSubmission}));
              if(!live)return;
              if(result.state==="Completed"){
                setCopyMessage(result.result==="source_changed"?"名单、稿件或发送入口有变化，请重新核对。":"本次确认已记录，可在本包查看提交进度与逐人回执。");
                setSourceSubmission("");
              }else if(["Failed","Canceled","Terminated","TimedOut"].includes(result.state)){
                setCopyMessage("本次确认未完成，请查看包内状态。");
                setSourceSubmission("");
              }
            }
            const data=await loadVisibleSourceCopy(sourcePlan,sourceCopy.rows.length);
            if(!live)return;
            setSourceCopy(data);
            if(selected&&selected!==data.first?.key){
              const person=await loadSourceCopy(sourcePlan,data.run,selected);
              if(live)setSourceItem(person);
            }
          }catch(e){if(live&&sourceSubmission)setError(e.message)}
          if(live)timer=setTimeout(poll,5000);
        };
        timer=setTimeout(poll,sourceSubmission?800:5000);
        return()=>{live=false;clearTimeout(timer)};
      },[page,sourcePlan,sourceCopy?.run,sourceCopy?.rows.length,sourceSubmission,sourceConfirm,copyEdit,selected]);
      const showSettings=kind=>act(async()=>{
        if(kind==="project"){
          if(view.config.readSource){
            const list=(await api("/transports")).filter(account=>account.kind==="crm-cli"&&account.enabled&&account.projects?.includes(projectId));
            const binding=list.find(account=>account.id===view.config.transport)||(list.length===1?list[0]:null);
            setAccounts(view.staffName&&binding?[{id:binding.id,name:view.staffName}]:[]);
          }else{
            const list=await api("/transports");
            setAccounts(list.filter(account=>account.kind==="crm-cli"&&account.enabled&&account.projects?.includes(projectId)));
          }
          if(projectList?.expertManagement&&view.config.readSource)await readMethods();
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
      const modal=settings&&!methodDetail&&h(UI.Modal,{open:true,title:{project:"专项设置",profile:"档案设置",strategy:"策略设置"}[settings.kind],closeLabel:"关闭",onClose:()=>{if(!busy){settingsOffset.current=0;setMethodExpanded(false);setSettings(null)}}},h("form",{className:"wb-page wb-form op-modal-content",onSubmit:saveSettings,ref:settingsForm},
        settings.kind==="project"
          ? h(React.Fragment,null,
            field("关联知识库",h("select",{value:settings.knowledge.resourceId+"/"+settings.knowledge.section,onChange:e=>{
              const item=view.knowledge?.find(item=>item.resourceId+"/"+item.section===e.target.value);
              setSettings({...settings,knowledge:{resourceId:item?.resourceId||"",section:item?.section||"",version:""}});
            }},h("option",{value:"/"},"请选择"),...(view.knowledge||[]).map(item=>h("option",{key:item.resourceId+"/"+item.section,value:item.resourceId+"/"+item.section},item.label)))),
            field("运营企微账号",h("select",{value:settings.transport,onChange:e=>setSettings({...settings,transport:e.target.value})},
              h("option",{value:""},"请选择"),...accounts.map(account=>h("option",{key:account.id,value:account.id},account.name)))),
            settings.transport&&(!settings.staffId||!accounts.length)&&h("p",{className:"wb-muted"},"待登记实际运营企微账号。"),
            methodRows.length>0&&h("details",{className:"wb-section",open:methodExpanded,onToggle:e=>setMethodExpanded(e.currentTarget.open)},h("summary",null,"运营专家"),
              ...methodRows.map(row=>h("div",{className:"wb-row",key:row.id},h("div",{className:"wb-row-main"},expertTitle(row),
                h("div",{className:"wb-muted"},expertOrigins[row.source])),
                button("查看专家明细",()=>act(async()=>{settingsOffset.current=settingsForm.current?.scrollTop||0;setMethodDetail(await expertDetails(row.id))}),{disabled:busy})))) )
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
      const displayMembers=sourceCopy?.rows||[];
      const activeMember=displayMembers.find(item=>item.key===selected)||displayMembers[0];
      const itemKey=activeMember?.key;
      const draft=sourceCopy?.first&&sourceCopy.first.key===itemKey?sourceCopy.first:
        sourceItem?.run===sourceCopy?.run&&sourceItem?.digest===sourceCopy?.digest&&sourceItem?.rows[0]?.key===itemKey?sourceItem?.rows[0]:null;
      const firstKey=sourceCopy?.first?.key;
      useEffect(()=>{
        if(page!=="source-plan"||!sourceCopy?.run||!itemKey||itemKey===firstKey)return;
        let active=true;
        loadSourceCopy(sourcePlan,sourceCopy.run,itemKey).then(data=>{
          if(active)setSourceItem(data);
        }).catch(e=>{if(active)setError(e.message)});
        return()=>{active=false};
      },[page,sourcePlan,sourceCopy?.run,sourceCopy?.digest,itemKey,firstKey,reload]);
      const selectSourceMember=key=>{
        if(copyEdit||key===itemKey)return;
        setSelected(key);setCopyMessage("");setError("");
      };
      const moreSourceMembers=()=>act(async()=>{
        const next=await loadSourceCopy(sourcePlan,sourceCopy.run,"",sourceCopy.next);
        if(next.reviewVersion!==sourceCopy.reviewVersion){
          setSourceCopy(await loadSourceCopy(sourcePlan,sourceCopy.run));
          return;
        }
        setSourceCopy(current=>({...current,rows:[...current.rows,...next.rows],next:next.next}));
      });
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
          const copy=await loadVisibleSourceCopy(sourcePlan,sourceCopy.rows.length);
          const person=copy.first?.key===item?{...copy,rows:[copy.first]}:await loadSourceCopy(sourcePlan,input.run,item);
          if(copy.digest!==person.digest||person.rows[0]?.message!==text.trim()||person.rows[0]?.revision!==result.revision)
            throw Error("保存结果待核对。");
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
      if(methodDetail)return h(ExpertDocumentPage,{row:methodDetail,projects:projectList.projects,allowEnable:true,
        back:()=>setMethodDetail(undefined),backLabel:"返回运营设置",changed:()=>readMethods().catch(e=>setError(e.message))});
      return h("section",{className:"wb-page op-page"},
        h("header",{className:"op-header"},h("div",{className:"wb-actions"},page!=="home"&&button("返回",page==="profile"&&profileBack==="source-plan"?()=>setPage("source-plan"):page==="source-plan"&&sourcePlan?.historical?()=>setPage("history"):home,{className:"op-link",disabled:!!copyEdit}),h("h1",null,sectionTitle)),
          view&&h("div",{className:"wb-actions"},
            page==="home"&&button("历史",()=>{setPage("history");if(readSource)loadSourceHistory(shanghaiWeek())},{className:"op-link"}),
            page==="home"&&button("设置",()=>showSettings("project")),
            page==="source-plan"&&sourcePlan?.daily&&!sourcePlan.historical&&sourceCopy&&!sourceCopy.reviewed&&sourceCopy.pending>0&&
              button("审核并确认发送",()=>setSourceConfirm({daily:sourcePlan.daily,reviewVersion:sourceCopy.reviewVersion,count:sourceCopy.pending,account:sourceCopy.sendAccount}),
                {className:"wb-primary",disabled:busy||!sourceCopy.sendEnabled||!!copyEdit||!!sourceSubmission}),
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
          sourcePlan.daily&&!sourcePlan.historical&&sourceCopy&&!sourceCopy.sendEnabled&&!sourceCopy.reviewed&&!sourceCopy.syncing&&h("p",{className:"wb-muted"},"当前开放核对与改稿，发送尚未启用。"),
          sourceCopy?.syncing&&h("p",{role:"status"},"正在核对原稿保存结果。"),
          sourceCopy?.formal&&h("p",{role:"status"},sourceCopy.formal==="running"?"原中心正在处理本人确认。":"原中心审核失败，请核对原包。"),
          sourceCopy?.plan&&h("p",{className:"wb-muted"},"原 CRM 计划 "+sourceCopy.plan),
          sourceCopy?h(React.Fragment,null,
              displayMembers.length?h("div",{className:"op-detail"},
                h("table",{className:"op-table"},h("thead",null,h("tr",null,...["客户昵称","入选依据","状态"].map(label=>h("th",{key:label,scope:"col"},label)))),
                  h("tbody",null,...displayMembers.map(item=>h("tr",{key:item.key,"aria-selected":item.key===itemKey,onClick:()=>selectSourceMember(item.key)},
                    h("td",null,button(item.nickname||"未记录称呼",()=>selectSourceMember(item.key),{disabled:!!copyEdit})),h("td",null,h("div",{className:"op-reason"},item.reason||"原运行名单")),
                    h("td",null,item.status))))),
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
                  !!draft?.attachments?.length&&h("section",null,h("details",{className:"op-review"},
                    h("summary",null,"预览随附卡片 · "+draft.attachments.length+" 张"),...draft.attachments.map(cardPreview))),
                  draft?.progress&&h("section",null,h("strong",null,sourceCopy.reviewed||sourceCopy.handoff||draft.status==="已发送"?"CRM 回执":"状态明细"),h("p",null,draft.progress)),
                  h("div",{className:"wb-actions"},button("查看用户档案",()=>openProfile(activeMember.id),{disabled:busy||!!copyEdit})))):h("p",null,"本工作包当前无可处理名单"),
              sourceCopy.next&&h("div",{className:"op-footer"},button("加载更多",moreSourceMembers,{disabled:busy||!!copyEdit})),
              null):busy?h("p",{role:"status"},"正在读取工作包…"):button("重新核对",()=>openPlan(sourcePlan))),
        sourceConfirm&&h(UI.Modal,{open:true,title:"审核并确认发送",closeLabel:"关闭",onClose:()=>{if(!busy)setSourceConfirm(null)}},
          h("section",{className:"wb-page wb-form op-modal-content"},
            h("p",null,"本次发送 "+sourceConfirm.count+" 人"),
            h("p",{className:"wb-muted"},"运营企微账号 "+sourceConfirm.account),
            alert(error),h("div",{className:"wb-actions"},button("返回",()=>setSourceConfirm(null),{disabled:busy}),
              button("确认发送",confirmSource,{className:"wb-primary",disabled:busy})))),
        page==="history"&&view&&h(React.Fragment,null,
          readSource&&h("section",null,h("h2",{className:"op-title"},"原工作包"),
            h("form",{className:"op-search",onSubmit:e=>{e.preventDefault();loadSourceHistory(sourceHistoryWeek)}},
              h("input",{type:"week",required:true,value:sourceHistoryWeek,onChange:e=>setSourceHistoryWeek(e.target.value),"aria-label":"历史周"}),
              h("button",{type:"submit",disabled:busy},"查询")),
            sourceHistory?.runs.length===0&&h("p",{className:"wb-muted"},"本周暂无原工作包记录"),
            ...(sourceHistory?.runs||[]).map(item=>h("div",{className:"op-strategy",key:item.daily||item.run_id},
              h("strong",null,item.label),h("span",{className:"wb-muted"},item.createdAt?new Date(item.createdAt).toLocaleString("zh-CN",{timeZone:"Asia/Shanghai"}):item.day||item.week),
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

    const brandTitle = "浅蓝的一号位工作台";
    const linkTitle = "链接飞书/微信";
    const linkStates = {
      unlinked: "未链接", pending: "待验证", linked: "已链接", reconnect: "需重连", partial: "部分链接",
    };
    const linkCSS = `
.wb-brand-name{font-size:16px;font-weight:650;letter-spacing:0;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.wb-brand-mark{display:block;flex:none}.wb-more-anchor{width:100%}
div:has(> span > [data-slot="conversation.hero.brand.mark"] > [data-wb-empty-hero]){display:none}
[data-slot="settings.section"] [class$="_rowCard"]:has(button[aria-label*="(deepseek-official)"]) [class$="_editorRoute"]{display:none}
.wb-expert-trigger{border:0;border-radius:var(--dsw-radius-sm);background:transparent;color:var(--dsw-alias-label-primary);display:inline-flex;align-items:center;gap:4px;max-width:min(100%,360px);min-height:28px;padding:0 8px;cursor:pointer;font:inherit;font-size:13px;font-weight:500;line-height:20px}
.wb-expert-trigger:hover,.wb-expert-trigger[aria-expanded=true]{background:var(--dsw-alias-interactive-bg-hover)}
.wb-expert-trigger:disabled{opacity:.6;cursor:default}
.wb-expert-trigger span{min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.wb-expert-trigger svg{flex:none}
.wb-expert-error{font-size:12px}
.wb-session-projects{display:grid;gap:8px}
.wb-session-projects button{text-align:left}
.wb-more-trigger{display:flex;align-items:center;gap:10px;width:100%;min-height:36px;padding:0 12px;background:transparent;color:inherit;border:0;border-radius:8px;font:inherit;text-align:left;cursor:pointer}
.wb-more-trigger[data-rail]{width:36px;justify-content:center;padding:0}
.wb-more-trigger:hover,.wb-more-trigger[aria-expanded=true],.wb-project-find:hover{background:var(--dsw-alias-interactive-bg-hover)}
.wb-link-modal{width:min(620px,calc(100vw - 24px));max-height:calc(100dvh - 24px)}
.wb-link-content{display:block;overflow:auto;overscroll-behavior:contain}
.wb-link-panel{padding:2px 0 12px;min-width:0}
.wb-link-panel section+section{border-top:1px solid var(--dsw-alias-border-l3);margin-top:10px;padding-top:16px}
.wb-link-section-heading{display:flex;align-items:center;justify-content:space-between;gap:12px;margin:10px 0 6px}
.wb-link-section-heading h3{font-size:16px;margin:0}.wb-link-row{display:flex;align-items:center;gap:10px;min-height:52px}
.wb-link-row+.wb-link-row{border-top:1px solid var(--dsw-alias-border-l3)}
.wb-link-row strong{font-weight:550;min-width:85px}.wb-link-state{color:var(--dsw-alias-label-secondary);flex:1}
.wb-link-state[data-linked]{color:var(--dsw-alias-state-business-success,#197143)}
.wb-link-step{display:grid;gap:12px;margin-top:12px;padding:16px;border-radius:10px;background:var(--dsw-alias-bg-l2,#f5f6f7)}
.wb-link-step p{margin:0}.wb-link-step img{width:min(232px,100%);height:auto;border-radius:8px}.wb-link-step code{overflow-wrap:anywhere}
.wb-link-missing{margin:4px 0 12px;color:var(--dsw-alias-label-secondary)}.wb-link-missing ul{max-height:135px;overflow:auto;padding-left:20px;overflow-wrap:anywhere}
.wb-project-lookup{padding:0 8px 8px}.wb-project-find{background:none;border:0;color:var(--dsw-alias-label-secondary);font:inherit;font-size:12px;border-radius:6px;padding:4px 8px;cursor:pointer}
.wb-project-lookup input{box-sizing:border-box;width:100%;margin:4px 0;padding:6px 8px;font:inherit;color:inherit;background:var(--dsw-alias-bg-base);border:1px solid var(--dsw-alias-border-l3);border-radius:6px}
.wb-project-lookup [role=status]{padding:4px 8px;font-size:12px}
@media(max-width:480px){.wb-link-row{flex-wrap:wrap;padding:8px 0}.wb-link-row strong{min-width:74px}.wb-link-panel{padding-bottom:8px}}
`;

    const SessionExpert = React.createContext(null);
    const expertRequired = "请先选择会话专家";
    function ExpertConversation({ctx, sessionId, children}) {
      const {data,error:projectError}=useProjectList();
      const workspaceId=useSyncExternalStore(
        listener=>ctx.workspaces.list.subscribe(listener),
        ()=>ctx.workspaces.list.getSnapshot().items.find(item=>item.sessionIds.includes(sessionId))?.workspaceId||"");
      const project=data?.expertManagement&&data.projects.find(item=>item.type==="ordinary"&&(item.workspaceId===workspaceId||item.sessionIds.includes(sessionId)));
      const key=project&&sessionId?project.id+":"+sessionId:"";
      const loadingProject=!!sessionId&&!data;
      const blank=useSyncExternalStore(
        listener=>ctx.sessions.list.subscribe(listener),
        ()=>ctx.sessions.list.getSnapshot().byId[sessionId]?.blank);
      const [state,setState]=useState(),[busy,setBusy]=useState(""),[error,setError]=useState(""),[refresh,setRefresh]=useState(0);
      const pending=useRef(false),newSession=useRef(),readSequence=useRef(0);
      const info=state?.key===key?state.info:undefined;
      const selected=info&&(info.bound||(info.preferred?
        info.options.find(option=>option.id===info.preferred)?.id:info.options.length<=2?info.options.at(-1)?.id:""));
      const read=()=>api(query("/expert-options",{projectId:project.id,sessionId}));
      useEffect(()=>{
        if(!key)return;
        let live=true;const sequence=++readSequence.current;
        read().then(value=>{
          if(live&&sequence===readSequence.current){setState({key,info:value});setError("")}
        }).catch(e=>{if(live&&sequence===readSequence.current)setError(e.message)});
        return()=>{live=false};
      },[key,blank,refresh]);
      const reason=loadingProject?(projectError?"项目暂不可读":"正在读取会话项目…"):
        key&&(!selected||busy)?(busy?"正在保存会话专家…":!info&&!error?"正在读取会话专家…":expertRequired):"";
      useEffect(()=>{
        if(!reason)return;
        const blocks=ctx.get("conversation")?.blocks;
        if(!blocks)return;
        const store=blocks.storeFor(sessionId),block={reason};
        const protect=()=>{if(store.getSnapshot()===undefined)blocks.set(sessionId,block)};
        protect();
        const detach=store.subscribe(protect);
        return()=>{detach();if(store.getSnapshot()===block)blocks.set(sessionId,undefined)};
      },[ctx,sessionId,reason]);
      const choose=async id=>{
        if(!info||pending.current||id===info.bound)return;
        pending.current=true;setBusy(id);setError("");
        const navigation=id==="set-default"?undefined:ctx.layout.beginNavigation();
        try{
          if(id==="set-default"){
            const saved=await api("/expert-default",{projectId:project.id,expertId:info.bound,expected:info.configured||""});
            readSequence.current++;
            setState({key,info:{...info,configured:saved.expertId,preferred:saved.expertId}});
          }else{
            const current=info.bound?info:await read();
            if(navigation.aborted)return;
            readSequence.current++;
            setState({key,info:current});
            if(id===current.bound)return;
            const changing=!!current.bound;
            if(changing&&newSession.current?.expertId!==id)newSession.current={expertId:id,id:crypto.randomUUID()};
            const result=await api("/session/start",{workspaceId:project.workspaceId,
              ...(changing?{newSessionId:newSession.current.id}:{sessionId}),expertId:id});
            if(changing){
              await ctx.sessions.refresh();
              if(!navigation.aborted&&ctx.layout.panelInfo.getSnapshot().activePanelId===null&&
                ctx.uiWorkspace.selection.getSnapshot().sessionId===sessionId)ctx.uiWorkspace.openSession(result.sessionId);
              newSession.current=undefined;
            }else{
              readSequence.current++;
              setState({key,info:{...current,bound:id}});
            }
          }
        }catch(e){
          if(e.status===409){
            const latest=await read().catch(()=>null);
            if(latest){readSequence.current++;setState({key,info:latest})}
          }
          setError(e.message);
        }finally{
          pending.current=false;setBusy("");
        }
      };
      const createProjectExpert=()=>{
        requestedExpertProject=project.id;
        ctx.layout.selectPanel("wb-experts");
        window.dispatchEvent(new CustomEvent("wb-create-project-expert",{detail:project.id}));
      };
      const context=key?{info,selected,busy,error,choose,createProjectExpert,retry:()=>setRefresh(value=>value+1)}:
        loadingProject?{error:projectError,retry:notifyProjects}:null;
      return h(SessionExpert.Provider,{value:context},children);
    }
    function SessionExpertMenu() {
      const session=React.useContext(SessionExpert);
      const [open,setOpen]=useState(false);
      if(!session)return null;
      const {info,selected,busy,error,choose,createProjectExpert,retry}=session;
      const name=info?.options.find(option=>option.id===selected);
      const label=name?expertTitle(name):selected?.split(":").at(-1)||(info||error?"选择专家":"读取专家…");
      const items=info?.options.map(option=>({id:option.id,label:expertTitle(option)}))||[];
      if(info?.bound&&info.bound!==info.configured)
        items.push({id:"set-default",label:"设为项目默认",separatorBefore:true});
      if(info)items.push({id:"new-expert",label:"新建本项目专家",separatorBefore:true});
      return h(React.Fragment,null,h(UI.Menu,{
        open:open&&!!items.length,onClose:()=>setOpen(false),items,selectedId:selected,
        onSelect:id=>{setOpen(false);if(id==="new-expert")createProjectExpert();else void choose(id)},side:"bottom",align:"start",portal:true,
        anchor:button(h(React.Fragment,null,h(UI.IconAgentPresetOutlineRegular,{size:16}),h("span",null,label),h(UI.IconChevronDownOutlineRegular,{size:12})),
          ()=>{if(!open&&!info?.bound)retry();setOpen(value=>!value||!info)},{
            className:"wb-expert-trigger","aria-label":info?.bound?"已绑定 "+label+"，换专家并新建会话":"选择会话专家",
            "aria-haspopup":"menu","aria-expanded":open&&!!items.length,disabled:!!busy||!info&&!error,
          }),
      }),error&&h("span",{className:"wb-muted wb-expert-error",role:"alert"},error));
    }
    function installNewSession(ctx, openProject) {
      const showPicker = "wb-new-session-project";
      const workspace = ctx.uiWorkspace;
      const original = workspace.startSession;
      const startIn = (project, options) => original.call(workspace, project.workspaceId, options);
      function ProjectChooser() {
        const [choice, setChoice] = useState();
        useEffect(() => onEvent(showPicker, event => setChoice(event.detail)), []);
        if (!choice) return null;
        const close = () => setChoice(undefined);
        const select = project => { close(); startIn(project, choice.options); };
        if (!choice.error && !choice.projects.length) return h(NewProject, { ordinaryOnly: true, onClose: close, onCreated: openProject });
        return h(UI.Modal, { open: true, title: "新会话", closeLabel: "关闭", onClose: close },
          h("div", { className: "wb-page wb-session-projects" },
            choice.error ? h(React.Fragment, null, alert(choice.error), button("重试", () => { close(); start(choice.workspaceId, choice.options); }))
              : h(React.Fragment, null, h("p", { className: "wb-muted" }, "选择会话所属的项目"),
                ...choice.projects.map(project => button(project.name, () => select(project), { key: project.id })))));
      }
      function start(workspaceId, options) {
        const navigation = ctx.layout.beginNavigation();
        const current = ctx.layout.panelInfo.getSnapshot().activePanelId === null
          ? Object.values(ctx.sessions.list.getSnapshot().byId).find(session => session.retainedBy?.mainView > 0) : undefined;
        const activeWorkspaceId = current && visibleSession(current.id)
          ? ctx.workspaces.list.getSnapshot().items.find(item => item.sessionIds.includes(current.id))?.workspaceId : undefined;
        const show = detail => window.dispatchEvent(new CustomEvent(showPicker, { detail: { ...detail, options, workspaceId } }));
        void api("/project-list").then(({ projects, expertManagement }) => {
          if (navigation.aborted) return;
          if (workspaceId && !expertManagement) { original.call(workspace, workspaceId, options); return; }
          const ordinary = projects.filter(project => project.type === "ordinary");
          const target = ordinary.find(project => project.workspaceId === (workspaceId||activeWorkspaceId)) ||
            (ordinary.length === 1 ? ordinary[0] : undefined);
          if (target) startIn(target, options);
          else show({ projects: ordinary });
        }).catch(error => { if (!navigation.aborted) show({ error: error.message }); });
      }
      ctx.slots.inject("shell.overlay", () => ctx.slots.register({ name: "shell.overlay", id: "workbench-new-session" }, ProjectChooser));
      ctx.slots.inject("conversation.session.header.utilities",()=>ctx.slots.register({
        name:"conversation.session.header.utilities",id:"workbench-session-expert",order:-25,inject:sessionId=>({sessionId}),
      },({sessionId})=>sessionId&&!sessionId.startsWith("object-")?h(SessionExpertMenu,{key:sessionId}):null));
      function HeroExpert({Original,props}) {
        const {data}=useProjectList();
        const expert=React.useContext(SessionExpert);
        const selected=useSyncExternalStore(
          listener=>workspace.selection.subscribe(listener),
          ()=>workspace.selection.getSnapshot().sessionId||"");
        if (!expert&&!data?.expertManagement) return h(Original,props);
        return selected?h(SessionExpertMenu,{key:selected}):null;
      }
      function HeaderPreset({Original,props}) {
        const {data}=useProjectList();
        return data?.expertManagement?null:h(Original,props);
      }
      function HeroWorkspace({Original,props}) {
        const {data}=useProjectList();
        if(!data)return null;
        if(!data.expertManagement)return h(Original,props);
        return h(Original,{...props,useWorkspaces:select=>props.useWorkspaces(state=>select(filteredWorkspaces(state)))});
      }
      wrapSlot(ctx,"conversation.hero.agentPreset",undefined,(Original,props)=>h(HeroExpert,{Original,props}));
      wrapSlot(ctx,"conversation.hero.workspace",undefined,(Original,props)=>h(HeroWorkspace,{Original,props}));
      wrapSlot(ctx,"conversation.session.header.actions","agent-preset",(Original,props)=>h(HeaderPreset,{Original,props}));
      ctx.effect(() => {
        workspace.startSession = start;
        return () => { if (workspace.startSession === start) workspace.startSession = original; };
      }, "workbench: new session project selection");
    }

    function wrapSlot(ctx, slot, id, render) {
      ctx.slots.inject(slot, () => {
        let wrapped, restore;
        const update = () => {
          const entry = ctx.slots.entries(slot).find(item => item.component && (!id || (item.options.id ?? item.options.key) === id));
          if (entry === wrapped) return;
          restore?.();
          wrapped = entry;
          restore = undefined;
          if (!entry) return;
          const Original = entry.component;
          const WorkbenchSlot = props => render(Original, props);
          entry.component = WorkbenchSlot;
          restore = () => { if (entry.component === WorkbenchSlot) entry.component = Original; };
        };
        const unsubscribe = ctx.slots.subscribe(slot, update);
        update();
        return () => { unsubscribe(); restore?.(); };
      });
    }

    function installWorkbenchBrand(ctx) {
      const builtinProvider = id => id === "deepseek-official" || id === "deepseek-account";
      const unbrand = text => text.replace(/DeepSeek\s+Harness/g, brandTitle).replace(/\bDeepSeek\b/g, "内置模型");
      const translate = t => (key, values) => unbrand(t(key, values));
      const modelLabels = new WeakMap();
      const modelSettings = new WeakMap();
      const onboardingSteps = new WeakMap();
      ctx.slots.inject("conversation.hero.brand.mark", () => ctx.slots.register({ name: "conversation.hero.brand.mark" },
        () => h("span", { hidden: true, "data-wb-empty-hero": "" })));
      const displayDirectory = snapshot => {
        if (!modelLabels.has(snapshot)) modelLabels.set(snapshot, { ...snapshot,
          groups: snapshot.groups.map(group => !builtinProvider(group.id) ? group : {
            ...group, name: "内置模型", models: group.models.map(model => ({ ...model,
              name: model.name.replace(/^DeepSeek[-\s]+/i, ""),
            })),
          }),
          failures: snapshot.failures.map(failure => builtinProvider(failure.id)
            ? { ...failure, name: "内置模型", message: unbrand(failure.message) } : failure),
        });
        return modelLabels.get(snapshot);
      };
      ctx.inject(["modelDirectories"], scope => {
        scope.effect(() => {
          const resolver = scope.modelDirectories;
          const original = resolver.directoryFor;
          const projected = new WeakMap();
          function directoryFor(sessionId) {
            const directory = original.call(this, sessionId);
            if (!projected.has(directory)) projected.set(directory, {
              store: {
                subscribe: listener => directory.store.subscribe(listener),
                getSnapshot: () => displayDirectory(directory.store.getSnapshot()),
              },
              load: () => directory.load().then(displayDirectory),
              select: selection => directory.select(selection),
            });
            return projected.get(directory);
          }
          resolver.directoryFor = directoryFor;
          return () => { if (resolver.directoryFor === directoryFor) resolver.directoryFor = original; };
        }, "workbench: built-in model display labels");
      });
      wrapSlot(ctx, "conversation.input.model", undefined, (Original, props) => h(Original, { ...props, t: translate(props.t) }));
      wrapSlot(ctx, "settings.section", "models", (Original, props) => h(Original, { ...props,
        t: (key, values) => unbrand(props.t(key === "keyFallbackDetected" ? "keyFallbackDetectedHidden" : key, values)),
        useSnapshot: select => props.useSnapshot(snapshot => {
          if (!modelSettings.has(snapshot)) modelSettings.set(snapshot, { ...snapshot,
            rows: snapshot.rows.map(row => builtinProvider(row.entry.provider)
              ? { ...row, entry: { ...row.entry, displayName: "内置模型" } } : row),
          });
          return select(modelSettings.get(snapshot));
        }),
      }));
      wrapSlot(ctx, "settings.general.item", "session-log-deepseek", (Original, props) => h(Original, { ...props,
        t(key) {
          const copy = props.t(key);
          if (key !== "description") return copy;
          return copy.startsWith("帮助") ? "向内置模型提供方共享会话日志，以帮助改进服务。"
            : copy.replace(/\bDeepSeek\b/g, "AI provider");
        },
      }));
      wrapSlot(ctx, "main", "plugins", (Original, props) => h(Original, { ...props, t: translate(props.t) }));
      wrapSlot(ctx, "sidebar.settings", undefined, (Original, props) => h(Original, { ...props,
        useOnboardingSteps: select => props.useOnboardingSteps(steps => {
          if (!onboardingSteps.has(steps)) onboardingSteps.set(steps,
            steps.filter(step => step.id !== "welcome-notice" && step.id !== "deepseek-official"));
          return select(onboardingSteps.get(steps));
        }),
      }));
      ctx.effect(() => {
        const title = document.head.querySelector("title");
        if (!title) return;
        const refresh = () => {
          const current = document.title;
          const next = current.replace(/(^| — )DeepSeek Harness$/, `$1${brandTitle}`);
          if (next !== current) document.title = next;
        };
        const observer = new MutationObserver(refresh);
        observer.observe(title, { childList: true, characterData: true, subtree: true });
        refresh();
        return () => observer.disconnect();
      }, "workbench: browser title");
    }

    function ChannelLinks({ close }) {
      const [status, setStatus] = useState();
      const [flow, setFlow] = useState();
      const [kind, setKind] = useState();
      const [manual, setManual] = useState(false);
      const [busy, setBusy] = useState(false);
      const [error, setError] = useState("");
      const [copied, setCopied] = useState(false);
      const refreshing = useRef(0);
      const load = async () => {
        const revision = ++refreshing.current;
        try {
          const next = await api("/channel-links");
          if (refreshing.current === revision) {
            setStatus(next);
            if (revision === 1 && next.active) { setKind(next.active.kind); setFlow(next.active.flow); }
          }
        } catch (failure) {
          if (refreshing.current === revision) setError(failure.message);
        }
      };
      useEffect(() => {
        void load();
        return () => { refreshing.current++; };
      }, []);
      useEffect(() => {
        if (!kind) return;
        let alive = true;
        let timer;
        async function poll() {
          try {
            const next = await api("/channel-links/" + kind);
            if (!alive) return;
            if (next?.id && flow?.id && next.id !== flow.id) return;
            setFlow(next);
            if (["done", "linked", "error"].includes(next?.step)) { void load(); return; }
          } catch (failure) {
            if (alive) setError(failure.message);
          }
          if (alive) timer = setTimeout(poll, 1200);
        }
        timer = setTimeout(poll, 500);
        return () => { alive = false; clearTimeout(timer); };
      }, [kind, flow?.id]);

      async function act(operation, values, nextKind = kind) {
        setBusy(true);
        setError("");
        setCopied(false);
        try {
          const next = await api("/channel-links/" + operation, values);
          setKind(nextKind);
          setFlow(next);
          setManual(false);
          if (operation === "confirm") void load();
        } catch (failure) { setError(failure.message); }
        finally { setBusy(false); }
      }
      function start(nextKind, method) {
        return act("start", { kind: nextKind, method }, nextKind);
      }
      const bot = status?.feishu?.bot;
      const cli = flow?.step === "done" && kind === "cli" ? flow.result : status?.feishu?.cli;
      const wx = status?.wechat;
      const label = (item) => h("span", {
        className: "wb-link-state", "data-linked": item?.state === "linked" || undefined, role: "status",
      }, linkStates[item?.state] || "读取中");
      const buttonProps = { disabled: busy };
      function row(title, item, action) {
        return h("div", { className: "wb-link-row" }, h("strong", null, title), label(item), action);
      }
      function step() {
        if (manual) return h("form", { className: "wb-link-step", onSubmit: (e) => {
          e.preventDefault();
          const form = new FormData(e.currentTarget);
          act("start", { kind: "feishu", method: "existing", appId: form.get("appId").trim(), appSecret: form.get("appSecret") }, "feishu");
        } },
        field("App ID", h("input", { name: "appId", required: true, maxLength: 128, autoComplete: "off" })),
        field("App Secret", h("input", { name: "appSecret", type: "password", required: true, maxLength: 256, autoComplete: "off" })),
        h("div", { className: "wb-actions" }, button("取消", () => setManual(false)), h("button", { disabled: busy }, "验证应用")));
        if (!flow) return null;
        if (flow.step === "error") return h("p", { role: "alert" }, flow.error || "暂时无法链接，请重试。");
        if (flow.step === "done") return flow.result?.error ? h("p", { role: "alert" }, flow.result.error) : null;
        if (flow.step === "linked") return null;
        if (flow.step === "received") return h("div", { className: "wb-link-step" },
          h("p", { role: "status" }, "平台已受理测试回复。收到回复后，请确认。"),
          h("div", null, button("已收到回复", () => act("confirm", { kind, id: flow.id }), buttonProps)));
        if (flow.step === "test") return h("div", { className: "wb-link-step" },
          h("p", null, "请在机器人私聊中发送下面这条消息。"),
          h("code", null, flow.text),
          flow.botURL && h("a", { href: flow.botURL, target: "_blank", rel: "noreferrer" }, "在飞书打开机器人"),
          h("div", null, button(copied ? "已复制" : "复制测试口令", async () => {
            try { await navigator.clipboard.writeText(flow.text); setCopied(true); }
            catch { setError("请选中并复制测试口令。"); }
          })));
        if (flow.step === "pin") return h("form", { className: "wb-link-step", onSubmit: (e) => {
          e.preventDefault();
          act("pin", { id: flow.id, code: new FormData(e.currentTarget).get("pin").trim() });
        } }, field("微信配对码", h("input", { name: "pin", inputMode: "numeric", pattern: "[0-9]{4,8}", required: true, autoComplete: "off" })),
        h("div", null, h("button", { disabled: busy }, "提交配对码")));
        return h("div", { className: "wb-link-step" },
          h("p", { role: "status" }, ({
            starting: "正在准备链接…", scan: kind === "wechat" ? "请用微信扫码。" : "请用飞书扫码创建机器人。",
            authorize: "请在飞书中确认所需权限。", confirm: "请在微信中确认链接。", sending: "正在验证私聊回复…",
          })[flow.step] || "正在检查链接…"),
          flow.qr && h("img", { src: flow.qr, alt: kind === "wechat" ? "微信链接二维码" : "飞书授权二维码" }),
          flow.url && h("a", { href: flow.url, target: "_blank", rel: "noreferrer" }, "打开飞书授权页"));
      }
      const waiting = flow && !["error", "done", "linked"].includes(flow.step);
      const botAction = (bot?.state === "unlinked" || bot?.canScan) ? h("div", { className: "wb-actions" },
        button("扫码链接", () => start("feishu", "scan"), buttonProps),
        button("使用已有应用", () => { setManual(true); setKind("feishu"); setFlow(undefined); }, buttonProps))
        : bot && bot.state !== "linked" && button("验证私聊", () => start("feishu", "verify"), buttonProps);
      const wxAction = wx?.state === "unlinked" ? button("扫码链接", () => start("wechat", "scan"), buttonProps)
        : wx && wx.state !== "linked" && button(wx.state === "reconnect" ? "重新链接" : "验证私聊",
          () => start("wechat", wx.state === "reconnect" && wx.canScan ? "scan" : "verify"), buttonProps);
      const feishuPartial = status && (bot?.state !== "unlinked" || cli?.state !== "unlinked") &&
        (bot?.state !== "linked" || cli?.state !== "linked");
      return h(UI.Modal, { open: true, title: linkTitle, closeLabel: "关闭", onClose: close, className: "wb-link-modal", contentClassName: "wb-link-content" },
        h("div", { className: "wb-page wb-link-panel" }, alert(error),
          !status && error && button("重试", load),
          h("section", null,
            h("div", { className: "wb-link-section-heading" }, h("h3", null, "飞书"),
              feishuPartial && h("span", { className: "wb-muted" }, "部分链接")),
            row("机器人对话", bot, botAction),
            row("CLI 授权", cli, cli && cli.state !== "linked" && button("授权",
              () => start("cli", "authorize"), { disabled: busy || bot?.state !== "linked" })),
            cli?.missing?.length > 0 && (cli.state === "partial" || flow?.step === "done") && h("details", { className: "wb-link-missing" },
              h("summary", null, "查看未开通权限"), h("ul", null, ...cli.missing.map(scope => h("li", { key: scope }, scope)))),
            (kind === "feishu" || kind === "cli") && step()),
          h("section", null,
            h("div", { className: "wb-link-section-heading" }, h("h3", null, "微信")),
            row("个人微信", wx, wxAction), kind === "wechat" && step()),
          waiting && h("p", { className: "wb-muted" }, "关闭浮窗后可重新打开查看进度。")));
    }

    function FooterMore({ wide, openSettings, settingsShortcut }) {
      const [open, setOpen] = useState(false);
      const [links, setLinks] = useState(false);
      const trigger = useRef();
      const closeLinks = () => { setLinks(false); requestAnimationFrame(() => trigger.current?.focus()); };
      return h(React.Fragment, null,
        h(UI.Menu, {
          open, side: "top", portal: true, autoFocus: true, className: "wb-more-anchor",
          anchor: h("button", {
            ref: trigger, type: "button", className: "wb-more-trigger", "data-rail": !wide || undefined,
            "aria-label": "更多", "aria-haspopup": "menu", "aria-expanded": open, onClick: () => setOpen(!open),
          }, h(UI.IconEllipsisOutlineMedium, { size: 16 }), wide && "更多"),
          items: [
            { id: "settings", label: "设置", icon: h(UI.IconSettingsOutlineMedium, { size: 16 }), shortcut: settingsShortcut },
            { id: "channels", label: linkTitle, icon: h(UI.IconLinkOutlineRegular || UI.IconPluginPinwheelOutlineRegular, { size: 16 }) },
          ],
          onClose: () => setOpen(false),
          onSelect: (id) => {
            setOpen(false);
            if (id === "settings") { trigger.current?.focus(); openSettings(); }
            else setLinks(true);
          },
        }), links && h(ChannelLinks, { close: closeLinks }));
    }

    function ProjectLookup({ projects, openProject }) {
      const [open, setOpen] = useState(false), [query, setQuery] = useState(""), [error, setError] = useState("");
      const trigger = useRef();
      const matches = projects.filter(project => project.name.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()));
      function close() { setOpen(false); setQuery(""); trigger.current?.focus(); }
      return h("div", { className: "wb-project-lookup" },
        button("查找项目", () => open ? close() : setOpen(true), { ref: trigger, className: "wb-project-find", "aria-expanded": open }),
        open && h(React.Fragment, null, h("input", {
          type: "search", "aria-label": "搜索项目名称", placeholder: "搜索项目名称", autoFocus: true,
          value: query, onChange: e => setQuery(e.target.value), onKeyDown: e => { if (e.key === "Escape") close(); },
        }), query.trim() && (matches.length ? matches.map(project =>
          button(project.name, async () => { try { await openProject(project); close(); } catch (failure) { setError(failure.message); } },
            { className: "wb-project-shortcut", key: project.id })) : h("div", { role: "status" }, "没有匹配的项目")), alert(error)));
    }

    function installWorkbenchViews(ctx,openProject){
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
          return h("section",{className:"wb-project-tree","aria-label":"项目与会话"},
            props.wide !== false && h(ProjectLookup,{projects:data.projects,openProject}),
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
          if(props.sessionId?.startsWith('object-'))return h(ReplyPage,{key:props.sessionId,sessionId:props.sessionId,openSession:openCustomer});
          const conversation=h(Original,props);
          if(personalId&&props.sessionId===personalId)return h(TodayConversation,{openSession},conversation);
          return h(ExpertConversation,{key:props.sessionId,ctx,sessionId:props.sessionId},conversation);
        }
        base.component=CustomerConversation;return()=>{if(base.component===CustomerConversation)base.component=Original};
      });
    }
    return {
      inject: ["slots", "layout", "uiWorkspace", "sessions", "workspaces"],
      apply(ctx) {
        ctx.effect(()=>{const style=document.createElement('style');style.textContent=css+workbenchCSS+operationsCSS+linkCSS;document.head.appendChild(style);return()=>style.remove()},'workbench shared native extension styles');
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
            else if(native||(await api("/project-list")).expertManagement)ctx.uiWorkspace.startSession(p.workspaceId,{clearPreviousDraft:false});
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
        installWorkbenchViews(ctx,openProject);
        ctx.slots.inject("settings.launcher", () => ctx.slots.register({ name: "settings.launcher", priority: -10 }, FooterMore));
        ctx.slots.inject("sidebar.brand.mark", () => ctx.slots.register({ name: "sidebar.brand.mark", priority: -10 },
          ({ size }) => h("img", { src: "/workbench-brand.png", width: size, height: size, alt: brandTitle, title: brandTitle, className: "wb-brand-mark" })));
        ctx.slots.inject("sidebar.brand.name", () => ctx.slots.register({ name: "sidebar.brand.name", priority: -10 },
          () => h("span", { className: "wb-brand-name", title: brandTitle }, brandTitle)));
        installWorkbenchBrand(ctx);
        installNewSession(ctx, openProject);
        function PreviousProjectEntry() {
          useEffect(() => {
            ctx.layout.selectPanel(null);
            requestAnimationFrame(() => document.querySelector(".wb-project-find")?.focus());
          }, []);
          return null;
        }
        ctx.slots.inject("main", () => ctx.slots.register({ name: "main", key: "wb-projects" }, PreviousProjectEntry));
        function SuperAgentEntry(){
          const [error,setError]=useState("");
          const [retry,setRetry]=useState(0);
          useEffect(()=>{let alive=true;setError("");api("/project-list").then(c=>{
            if(!alive)return;rememberPersonal(c.personalSessionId);ctx.uiWorkspace.openSession(c.personalSessionId);
          }).catch(e=>{if(alive)setError(e.message)});return()=>{alive=false}},[retry]);
          return h("section",{className:"wb-page"},h("div",{className:"wb-content"},alert(error),
            error?button("重试",()=>setRetry(n=>n+1)):h("p",{role:"status"},"正在打开 SuperAgent 对话…")));
        }
        for (const [id, label, mark, Component, extra] of [
          ["wb-today","SuperAgent",IconClockOutlineRegular,SuperAgentEntry,{}],
          ["wb-resources","知识库",IconDatabaseOutlineRegular,ResourcePage,{}],
          ["wb-experts","专家",UI.IconAgentPresetOutlineRegular||IconDatabaseOutlineRegular,ExpertsPage,{openSuperAgent:()=>ctx.layout.selectPanel("wb-today")}],
          ["wb-mcp","MCP 插件",UI.IconPluginPinwheelOutlineRegular||IconDatabaseOutlineRegular,MCPPage,{openNativePlugins:()=>ctx.layout.selectPanel("plugins")}],
          ["wb-schedules","定时任务",IconClockOutlineRegular,SchedulesPage,{openPanel:id=>ctx.layout.selectPanel(id),openSession:id=>ctx.uiWorkspace.openSession(id)}],
        ]) {
          ctx.slots.inject("sidebar.panellist", () =>
            ctx.slots.register(
              {
                name: "sidebar.panellist",
                id,
                order: -30+["wb-today","wb-resources","wb-experts","wb-mcp","wb-schedules"].indexOf(id),
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
        if (requestedBusinessPolicy || requestedResourcePermissions) ctx.effect(()=>{
          ctx.layout.selectPanel("wb-resources");
        },"open linked business policy table");
      },
    };
  },
});
