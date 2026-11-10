import { createServer } from "node:http";
import { Readable } from "node:stream";
import { timingSafeEqual, createHash, randomUUID } from "node:crypto";
import { dirname, join } from "node:path";
import { mkdir, realpath, readFile, writeFile, rename, unlink, lstat } from "node:fs/promises";
import * as mcpClient from "@deepseek-ai/dsh-mcp-client";
import { Client as MCPClient } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { BlockAssembler, createUserMessage } from "@deepseek-ai/dsh-llm";
import { attachmentText } from "./attachment-text.js";
import { deliveryProjection } from "./delivery-state.js";
import { modelCapacity, admittedStream } from './model-capacity.js';
import { toolNamespace, namespaceOfTool } from './tool-namespace.js';
import { installPersonalMemory } from './personal-memory.js';
import { replyEvidenceProjection } from './reply-evidence.js';
import { resourceDocuments } from './resource-documents.js';
import { ownerSkillCatalog } from './owner-catalog.js';

export const inject = [
  "agents",
  "attachments",
  "sessions",
  "sessionProjections",
  "sessionPersistence",
  "agentPresets",
  "workspaceRegistry",
  "webServer",
  "connection",
  "llm",
  "tools",
];
const owner = { kind: "owner", projectId: "", sessionId: "owner-ui" };
const idOf = (path) =>
  "p_" + createHash("sha256").update(path).digest("hex").slice(0, 24);
function json(res, status, value) {
  res.writeHead(status, { "content-type": "application/json; charset=utf-8" });
  res.end(JSON.stringify(value));
}
function requestError(message,status=400){return Object.assign(new Error(message),{status});}
async function body(req) {
  let bytes = 0,
    parts = [];
  for await (const p of req) {
    bytes += p.length;
    if (bytes > 8 * 1024 * 1024) throw requestError("request too large");
    parts.push(p);
  }
  return JSON.parse(Buffer.concat(parts).toString("utf8") || "{}");
}

export async function apply(ctx, config) {
  const admitModel=modelCapacity();
  const token = process.env.WORKBENCH_HOST_TOKEN;
  if (!token || token.length < 32)
    throw new Error("WORKBENCH_HOST_TOKEN is required");
  const worker = new URL(config.workerUrl);
  if (worker.hostname !== "127.0.0.1" || worker.protocol !== "http:")
    throw new Error("worker must use loopback");
  if (!config.personalWorkspace)
    throw new Error("personalWorkspace is required");
  await mkdir(config.personalWorkspace, { recursive: true });
  const personalPath = await realpath(config.personalWorkspace);
  // Rules are owner-authored workspace files, separate from inferred memory.
  const ruleNames = new Set(["SOUL.md", "AGENT.md"]);
  const readRule = async name => {
    if (!ruleNames.has(name)) throw requestError("未知的配置文件", 404);
    const path = join(personalPath, name);
    let info;
    try { info = await lstat(path); }
    catch (error) { if (error.code === "ENOENT") return { name, content: "", revision: "missing" }; throw error; }
    if (!info.isFile() || info.size > 64 * 1024) throw requestError("配置文件不是可读的 Markdown 文件");
    const content = await readFile(path, "utf8");
    return { name, content, revision: createHash("sha256").update(content).digest("hex") };
  };
  let rulesWrite = Promise.resolve();
  const saveRule = input => {
    const write = rulesWrite.then(async () => {
      const current = await readRule(input.name);
      if (input.revision !== current.revision) throw requestError("文件已更新，请重新打开后合并修改", 409);
      if (typeof input.content !== "string" || Buffer.byteLength(input.content) > 64 * 1024)
        throw requestError("配置文件最多 64 KiB");
      const temp = join(personalPath, ".workbench-rule-" + randomUUID());
      try {
        await writeFile(temp, input.content, { flag: "wx", mode: 0o600 });
        await rename(temp, join(personalPath, current.name));
      } finally { await unlink(temp).catch(e => { if (e.code !== "ENOENT") throw e; }); }
      return readRule(current.name);
    });
    rulesWrite = write.catch(() => {});
    return write;
  };
  ctx.on('llm/stream',(options,next)=>{
    if(!options.sessionId)return next();
    const session=ctx.sessions.get(options.sessionId);
    const lane=session?.header.cwd===personalPath?'owner':'interactive';
    return admittedStream(admitModel,lane,options.signal||new AbortController().signal,next);
  });
  const personalWorkspace = await ctx.workspaceRegistry.create(
    personalPath,
    "今日",
  );
  const headers = (p) => ({
    authorization: `Bearer ${token}`,
    "content-type": "application/json",
    "X-Workbench-Kind": p.kind,
    "X-Workbench-Project": p.projectId,
    "X-Workbench-Session": p.sessionId,
  });
  const call = async (path, p = owner, data, signal) => {
    const response = await fetch(new URL(path, worker), {
      method: data === undefined ? "GET" : "POST",
      headers: headers(p),
      body: data === undefined ? undefined : JSON.stringify(data),
      signal: signal ?? AbortSignal.timeout(30000),
    });
    if (!response.ok)
      throw requestError(`worker ${response.status}: ${(await response.text()).slice(0, 300)}`,response.status);
    return response.json();
  };
  const projectCache=new Map();
  async function syncProjects() {
    const projects = [];
    for (const w of ctx.workspaceRegistry.list()) {
      if (w.path === personalPath) continue;
      const p = { id: idOf(w.path), name: w.title, workspace: w.path };
      if(projectCache.get(p.id)!==JSON.stringify(p)) {
        await call("/projects", owner, p);
        projectCache.set(p.id,JSON.stringify(p));
      }
      projects.push({ ...p, workspaceId: w.id, sessionIds:(w.sessionIds??[]).filter(id=>!id.startsWith("object-")) });
    }
    return projects;
  }
  await syncProjects();
  const delivery = await ctx.sessionProjections.register(deliveryProjection);
  const replyEvidence = await ctx.sessionProjections.register(replyEvidenceProjection);
  const skillCatalog = ownerSkillCatalog(ctx);
  function identity(agent) {
    const path = agent.session.header.cwd;
    if (!path) throw new Error("Session has no fixed workspace");
    return {
      kind: path === personalPath ? "personal" : "project",
      projectId: path === personalPath ? "" : idOf(path),
      sessionId: agent.id,
    };
  }
  const owned = new Map();
  async function attach(agent) {
    if (owned.has(agent.id)) return;
    const p = identity(agent);
    await syncProjects();
    const boundary = await call('/session/check',p);
    if (p.kind === "personal") await call("/personal/session", p, {});
    installPersonalMemory(agent,p,call);
    if (p.kind === "personal") agent.ctx.on("system-prompt/assemble", async (_assembly, _context, next) => {
      const files = await Promise.all([...ruleNames].map(readRule));
      const text = files.filter(f => f.content.trim()).map(f => f.name + "\n" + f.content).join("\n\n");
      const result = await next();
      if (!text) return result;
      return { ...result, sections: [...result.sections, {
        name: "workbench:personal-markdown",
        text: "以下是本人保存的当前合作偏好。仍遵守资源范围、工具权限与确认要求。\n" + text,
        interpolate: false,
      }] };
    });
    agent.ctx.on("agent/pre-step", async (payload, next) => {
      await call("/session/check", p, undefined, payload.signal);
      return next();
    });
    // Only global tools are masked. The scoped official MCP client supplies capabilities.
    const dispose = agent.ctx.effect(
      () => agent.ctx.tools.restrict({ deny: ["working_directory"] }),
      "workbench: resource boundary",
    );
    owned.set(agent.id, dispose);
    const extensions=[];
    let workbenchMcp;
    try {
      const namespaces=new Set(['workbench']),externalSources=new Map();
      // Optional standard MCP servers are owner-installed plugins. Customer
      // object Sessions intentionally receive only the fixed message tools.
      for(const server of (config.toolServers||[])) {
        if(!(p.kind==='personal'?server.personal:server.projects?.includes(p.projectId)))continue;
        if(boundary.objectSession&&(!Array.isArray(server.customerTools)||!server.customerTools.length))continue;
        toolNamespace(server.name);if(namespaces.has(server.name))throw Error('Duplicate MCP namespace');
        const url=new URL(server.url);
        if(url.username||url.password||url.search||url.hash||(url.protocol!=='https:'&&(url.protocol!=='http:'||url.hostname!=='127.0.0.1')))throw Error('Invalid MCP endpoint');
        if(!/^WORKBENCH_MCP_[A-Z0-9_]+$/.test(server.tokenEnv)||!process.env[server.tokenEnv])throw Error('MCP connector credential is not configured');
        if(!Array.isArray(server.sources)||!server.sources.length)throw Error('MCP connector must declare registered source references');
        const declared=structuredClone(server.sources);
        externalSources.set(server.name,declared);
        const plugin=await agent.ctx.plugin(mcpClient,{serverName:server.name,transport:'streamable-http',url:url.href,headers:{authorization:'Bearer '+process.env[server.tokenEnv],'X-Workbench-Kind':p.kind,'X-Workbench-Project':p.projectId,'X-Workbench-Session':p.sessionId,...(!boundary.objectSession?{'X-Workbench-Sources':JSON.stringify(declared)}:{})},toolCallTimeoutMs:60000,failOnStartupError:true});
        extensions.push(plugin);namespaces.add(server.name);
      }
      agent.ctx.on('tools/execute',async(exec,next)=>{
        const name=namespaceOfTool(exec.name);
        if(!externalSources.has(name))return next();
        if(boundary.objectSession){const server=config.toolServers.find(s=>s.name===name);const tool=exec.name.replace('mcp__'+name+'__','');if(!server.customerTools.includes(tool))throw Error('Customer tool is not allowed')}
        // Conservatively record before dispatch, including failed/partial calls.
        // Recheck after return so a concurrent revocation cannot reveal a result.
        const refs=boundary.objectSession?await call('/session/resolve-sources',p,externalSources.get(name),exec.signal):externalSources.get(name);
        await call('/session/external-sources',p,refs,exec.signal);
        const result=await next();
        await call('/session/external-sources',p,refs,exec.signal);
        return result;
      });
      const mcpOwner = await agent.ctx.plugin(mcpClient, {
        serverName: "workbench",
        transport: "streamable-http",
        url: new URL("/mcp", worker).href,
        headers: headers(p),
        toolCallTimeoutMs: 60000,
        failOnStartupError: true,
      });
      workbenchMcp=mcpOwner;
      const mask = dispose;
      owned.set(agent.id, () => {
        mask();
        for(const extension of extensions)extension.dispose();
        return mcpOwner.dispose();
      });
      agent.ctx.tools.guard((exec) => {
        if(exec.name!=='skill'&&![...namespaces].some(name=>exec.name.startsWith('mcp__'+name+'__')))return '此工作台通过资源和标准 MCP 工具执行；未配置的工具不可调用。';
        if (!delivery.stateOf(agent.session).feedback) return;
        const readOnly = new Set([
          "attachment_list", "resource_list",
          "resource_read", "resource_search", "resource_excerpt",
          "project_list",
          "run_get", "run_steps",
          "run_list",
          "decision_list",
          "run_items", "message_batch_read",
          "memory_list",
        ]);
        if (!readOnly.has(exec.name.replace(/^mcp__workbench__/, "")))
          return "任务反馈轮只读；继续业务需要本人在对话中明确提出。";
      });
      agent.ctx.systemPrompt.section({
        name: "workbench:method",
        order: 80,
        text:
          p.kind === "personal"
            ? "你是“今日”Personal Agent。本人已明确要求的派发和只读核实应直接执行，不反复征询确认。旧轮次报错不等于本轮失败；必须以本轮实际工具结果汇报。目标项目内部再次派发的错误不代表今日的初次派发失败。先读最新项目目录和任务摘要，按需读取资源。可跨项目理解、安排、持续跟进、等待本人决定。资源读取不代表默认加载全部资料。task_delegate默认省略instructions，由宿主转交本人最近原话；不要主动重写本人已经明确的要求。只有目标项目允许读取当前所有来源时才可改写instructions。来源范围拒绝时可用本人原话重试，不要要求扩大项目权限。不得自行确认发送。"
            : "你在固定项目内工作。专家 Skill 负责判断，标准工具负责执行。资料仅通过已授权资源读取；批量传引用，不逐人塞入主对话。先选择板块与方法，再提交任务。状态、数据发布、内容生成和实际送达必须区分。",
      });
    } catch (e) {
      for(const extension of extensions)extension.dispose();
      workbenchMcp?.dispose();
      dispose();
      owned.delete(agent.id);
      throw e;
    }
  }
  ctx.on("agent/created", ({ agent }) => attach(agent));
  ctx.on("agent/disposed", ({ agent }) => {
    owned.get(agent.id)?.();
    owned.delete(agent.id);
  });
  ctx.effect(
    () => () => {
      for (const dispose of owned.values()) dispose();
      owned.clear();
    },
    "workbench: scoped lifecycle",
  );
  for (const agent of ctx.agents.list()) await attach(agent);
  const agentHandles=new Map(),sessionLoads=new Map(),idleSince=new Map(),objectAdmissions=new Set();
  async function loadSession(sessionId,path) {
    let agent=ctx.agents.get(sessionId);
    if(!agent){
      const persisted=await ctx.sessionPersistence.stat(sessionId);
      const options={provider:config.provider,model:config.model,reasoningEffort:'off',maxTokens:4000};
      const setup=agentCtx=>ctx.agentPresets.mount(agentCtx,'workbench').then(()=>undefined);
      const handle=persisted
       ? await ctx.agents.resume({resumeSessionId:sessionId,agentOptions:options,setup})
       : await ctx.agents.create({sessionId,meta:{cwd:path,agentPreset:'workbench'},agentOptions:options,setup});
      agentHandles.set(sessionId,handle);agent=handle.agent;
    }
    if(agent.session.header.cwd!==path)throw requestError('Session workspace mismatch',403);
    if(!await ctx.sessions.flush(agent.session))throw Error('Session was not persisted');
    return agent;
  }
  async function ensureSession(id,path){
    if(sessionLoads.has(id))await sessionLoads.get(id);
    const loading=loadSession(id,path);sessionLoads.set(id,loading);
    try{return await loading}finally{if(sessionLoads.get(id)===loading)sessionLoads.delete(id)}
  }
  async function settleIncoming(agent){const id=delivery.stateOf(agent.session).incomingReport;if(id)await call('/messages/settle',owner,{runId:id})}
  const releaseTimer=setInterval(async()=>{
    for(const[id,handle]of agentHandles){if(!id.startsWith('object-'))continue;
      if(handle.agent.status!=='idle'||delivery.stateOf(handle.agent.session).pendingIncoming){idleSince.delete(id);continue}
      if(!idleSince.has(id)){idleSince.set(id,Date.now());continue}
      if(Date.now()-idleSince.get(id)<20000||sessionLoads.has(id)||objectAdmissions.has(id))continue;
      const closing=(async()=>{await settleIncoming(handle.agent);if(handle.agent.status!=='idle'||objectAdmissions.has(id))return;await ctx.sessions.flush(handle.agent.session);agentHandles.delete(id);await handle.dispose()})();
      sessionLoads.set(id,closing);try{await closing}catch{idleSince.delete(id)}finally{if(sessionLoads.get(id)===closing)sessionLoads.delete(id)}
    }
  },5000);
  ctx.effect(()=>()=>clearInterval(releaseTimer),'workbench: release idle object agents');
  ctx.effect(()=>async()=>{for(const handle of agentHandles.values())await handle.dispose()},'workbench: owned sessions');
  // Today's daily schedule exists even before the first browser conversation.
  // Workspace session order is mutable (the UI may create another blank chat).
  // Today's identity must survive that ordering and Host restarts.
  const todayId='today_'+createHash('sha256').update(personalPath).digest('hex').slice(0,24);
  await ensureSession(todayId,personalPath);
  await personalWorkspace.attachSession(todayId);
  let deliveryCursor=0, reportCursor="";
  let polling = false,
    stopped = false;
  async function deliver() {
    if (polling || stopped) return;
    polling = true;
    try {
      for(const agent of ctx.agents.list()){if(agent.id.startsWith('object-')&&agent.status==='idle'){try{await settleIncoming(agent)}catch{ctx.logger.warn('Customer status settlement deferred: '+agent.id)}}}
      const reports=await call('/delegation/pending?after='+encodeURIComponent(reportCursor));
      for(const run of reports) {
        reportCursor=run.id;
        if(stopped)break;
        try {
          const w=ctx.workspaceRegistry.list().find(w=>idOf(w.path)===run.principal.projectId&&w.path!==personalPath);
          if(!w)continue;
          // Rebuild the projection from the native persisted Session after restart.
          const agent=await ensureSession(run.principal.sessionId,w.path);
          const report=delivery.stateOf(agent.session).report;
          if(report?.runId===run.id) {
            if(!await ctx.sessions.flush(agent.session))throw Error('Report was not persisted');
            await call('/delegation/report',identity(agent),report);
          }
        } catch(e) { ctx.logger.warn(`Project report ${run.id} remains pending: ${e.message}`); }
      }
      if(reports.length<50)reportCursor='';
      const pending=await call(`/events/pending?after=${deliveryCursor}`);
      for (const event of pending) {
        deliveryCursor=event.sequence;
        if (stopped) break;
        try {
          if(event.sessionId.startsWith('object-')&&!await ctx.sessionPersistence.stat(event.sessionId)){await call('/events/ack',owner,{id:event.id});continue}
          let agent = ctx.agents.get(event.sessionId);
          if(!agent){const w=ctx.workspaceRegistry.list().find(w=>w.sessionIds.includes(event.sessionId));if(!w)throw Error('Result Session workspace unavailable');agent=await ensureSession(event.sessionId,w.path)}
          if (delivery.stateOf(agent.session).through < event.sequence) {
            let text = `执行结果 · ${new Date(event.createdAt).toLocaleString("zh-CN", { timeZone: "Asia/Shanghai" })}\n任务 ${event.runId} · 阶段 ${event.kind}\n${event.text}\n${JSON.stringify(event.details ?? {})}\n${event.kind==='project.reported'?'上文是目标项目助手的文字汇报；模型表述不等于业务事实，若与工具回执冲突，先查询原任务核对，不能凭汇报推翻已有接收回执。':`这是后台任务 ${event.runId} 的真实回执。`}不是让你回顾最近一轮聊天。只针对这个任务说明已做工作、结果与下一步，不把其他聊天的工具调用算入本任务。${event.kind==='personal.daily'?'上文是本次已生成的今日安排，请以该安排为主要反馈；引用的其他项目状态只是汇总依据，不能说这些工作被你重新执行了。':''}只核对原任务，不重跑业务。`;
            if(event.kind==='message.history')text="渠道历史引用（不会触发自动应答）\n"+event.text;
            const resultMessage=createUserMessage({
                content: [{ type: "text", text }],
                source: { kind: "workbench-result", sequence: event.sequence },
              });
            if(event.sessionId.startsWith('object-'))agent.session.append('user/message',resultMessage,{surfaceOp:'append'});else agent.followup(resultMessage);
          }
          if (!(await ctx.sessions.flush(agent.session)))
            throw new Error("Session result was not persisted");
          await call("/events/ack", owner, { id: event.id });
        } catch (e) {
          ctx.logger.warn(`Result ${event.id} remains pending: ${e.message}`);
        }
      }
      if(pending.length<100)deliveryCursor=0;
    } catch (e) {
      ctx.logger.warn("workbench feedback delivery deferred: " + e.message);
    } finally {
      polling = false;
    }
  }
  const timer = setInterval(deliver, 5000);
  ctx.effect(
    () => () => {
      stopped = true;
      clearInterval(timer);
    },
    "workbench: durable result outbox",
  );

  // Browser identity is the native single-owner cookie; the secret Host token never reaches JS.
  ctx.effect(
    () =>
      ctx.webServer.register({
        kind: "prefix",
        path: "/workbench-api",
        handler: async (req, res) => {
          const admission = ctx.connection.admit(req);
          if ("rejection" in admission) {
            res.writeHead(admission.rejection);
            res.end();
            return;
          }
          try {
            const u = new URL(req.url, "http://localhost");
            const path = u.pathname.slice("/workbench-api".length);
            if (path.startsWith("/operations/")) {
              const projectId = u.searchParams.get("projectId");
              const project = (await syncProjects()).find(item => item.id === projectId);
              const ids = await call("/operations/projects");
              if (!project || !ids.includes(projectId)) throw requestError("专项项目不存在", 404);
              const p = {kind: "project", projectId, sessionId: "operations-" + projectId};
              if (req.method === "GET" && path === "/operations/experts") {
                json(res, 200, await skillCatalog(project.workspace));
                return;
              }
              if (req.method === "GET" && ["/operations/view", "/operations/profiles"].includes(path)) {
                json(res, 200, await call(path + u.search, p));
                return;
              }
              if (req.method === "POST" && ["/operations/schedule", "/operations/settings", "/operations/send"].includes(path)) {
                if (!req.headers.origin || new URL(req.headers.origin).host !== req.headers.host) throw requestError("origin rejected", 403);
                const input = await body(req);
                if (path === "/operations/schedule") {
                  const expert = await skillCatalog(project.workspace, input.expert);
                  if (!expert?.content) throw requestError("请选择可用专家");
                  await ensureSession(p.sessionId, project.workspace);
                  await ctx.workspaceRegistry.list().find(w => w.path === project.workspace)?.attachSession(p.sessionId);
                  json(res, 200, await call(path, owner, {
                    projectId, kind: input.kind, expert: expert.name, time: input.time, instructions: expert.content,
                  }));
                } else if (path === "/operations/settings") {
                  json(res, 200, await call(path, owner, {
                    projectId, knowledge: input.knowledge, transport: input.transport, staffId: input.staffId,
                  }));
                } else {
                  json(res, 200, await call(path, p, {runId: input.runId}));
                }
                return;
              }
              throw requestError("接口不存在", 404);
            }
            if(req.method==='GET'&&path==='/resource-documents'){
              const projectId=u.searchParams.get('projectId'),resourceId=u.searchParams.get('resourceId');
              if(!(await syncProjects()).some(p=>p.id===projectId))throw requestError('项目不存在',404);
              json(res,200,await resourceDocuments(config,call,projectId,resourceId,u.searchParams.get('uri')||undefined));return;
            }
            if(req.method==='GET'&&path==='/reply-experts'){
              const project=(await syncProjects()).find(p=>p.id===u.searchParams.get('projectId'));
              if(!project)throw requestError('项目不存在',404);
              json(res,200,await skillCatalog(project.workspace,u.searchParams.get('name')||undefined));return;
            }
            if(req.method==='GET'&&path==='/reply-evidence'){
              const sessionId=u.searchParams.get('sessionId');
              const route=await call('/reply-session?sessionId='+encodeURIComponent(sessionId));
              const project=(await syncProjects()).find(p=>p.id===route.principal.projectId);
              if(!project)throw requestError('会话不可用',404);
              const agent=await ensureSession(sessionId,project.workspace);
              json(res,200,replyEvidence.stateOf(agent.session).rounds.find(r=>r.runId===u.searchParams.get('runId'))||{calls:[]});return;
            }
            if (req.method === "GET" && path === "/catalog") {
              json(res, 200, {
                projects: await syncProjects(),
                resources: await call("/resources"),
                documentResourceIds: [...new Set((config.toolServers || []).flatMap(server => (server.sources || []).map(ref => ref.resourceId)))],
                personalWorkspaceId: personalWorkspace.id,
                personalSessionId: todayId,
              });
              return;
            }
            if (req.method === "GET" && path === "/project-list") {
              const [projects, reply, operations] = await Promise.all([syncProjects(), call("/reply-projects"), call("/operations/projects")]);
              const special = new Set(reply.map(p => p.projectId));
              json(res, 200, { personalWorkspaceId: personalWorkspace.id, personalSessionId: todayId,
                projects: projects.map(({workspace, ...p}) => ({ ...p, type: operations.includes(p.id) ? "auto-operations" : special.has(p.id) ? "auto-reply" : "ordinary" })) });
              return;
            }
            if (req.method === "GET" && path === "/today-rules") {
              json(res, 200, await Promise.all([...ruleNames].map(readRule))); return;
            }
            if (req.method === "GET" && path === "/experts") {
              json(res, 200, await skillCatalog(personalPath, u.searchParams.get("name") || undefined)); return;
            }
            if (req.method === "GET" && path === "/mcp-catalog") {
              json(res, 200, [{ name: "workbench", label: "工作台工具", configured: true },
                ...(config.toolServers || []).map(server => ({ name: server.name, label: server.name,
                  configured: Boolean(process.env[server.tokenEnv]), endpoint: new URL(server.url).origin,
                }))]);
              return;
            }
            if (req.method === "POST" && ["/project/create", "/today-rules", "/mcp-test", "/experts"].includes(path)) {
              const origin = req.headers.origin;
              if (!origin || new URL(origin).host !== req.headers.host) throw requestError("origin rejected", 403);
              const input = await body(req);
              if (path === "/today-rules") { json(res, 200, await saveRule(input)); return; }
              if (path === "/experts") {
                const saved = await skillCatalog.save(personalPath, input);
                const ids = await call("/operations/projects");
                for (const project of (await syncProjects()).filter(p => ids.includes(p.id))) {
                  const selected = await skillCatalog(project.workspace, saved.name);
                  await call("/operations/method", owner, {projectId: project.id, expert: selected.name, instructions: selected.content});
                }
                json(res, 200, saved);
                return;
              }
              if (path === "/mcp-test") {
                const builtin = input.name === "workbench";
                const server = (config.toolServers || []).find(item => item.name === input.name);
                if (!builtin && !server) throw requestError("插件不存在", 404);
                const endpoint = new URL(builtin ? "/mcp" : server.url, worker);
                if (endpoint.username || endpoint.password || endpoint.search || endpoint.hash ||
                    (endpoint.protocol !== "https:" && (endpoint.protocol !== "http:" || endpoint.hostname !== "127.0.0.1")))
                  throw requestError("服务地址无效");
                const credential = builtin ? token : process.env[server.tokenEnv];
                if (!credential) throw requestError("尚未配置凭证");
                const client = new MCPClient({ name: "workbench-owner-check", version: "1" });
                const signal = AbortSignal.timeout(8000);
                try {
                  await client.connect(new StreamableHTTPClientTransport(endpoint, {
                    requestInit: { redirect: "error", headers: { ...headers(owner), authorization: "Bearer " + credential } },
                  }), { signal });
                  const tools = [];
                  let cursor;
                  const seen = new Set();
                  do {
                    const page = await client.listTools(cursor ? { cursor } : undefined, { signal });
                    tools.push(...page.tools.map(t => ({ name: t.name, description: t.description })));
                    cursor = page.nextCursor;
                    if (tools.length > 500 || seen.size >= 30 || cursor && seen.has(cursor)) throw Error("invalid tools catalogue");
                    if (cursor) seen.add(cursor);
                  } while (cursor);
                  json(res, 200, { tools });
                } catch {
                  throw requestError("未能以当前凭证读取工具目录，请检查服务和连接配置", 502);
                } finally { await client.close().catch(() => {}); }
                return;
              }
              const name = typeof input.name === "string" ? input.name.trim() : "";
              if (!name || name.length > 80 || /[\x00-\x1f\x7f]/.test(name) ||
                  !["ordinary", "auto-reply", "auto-operations"].includes(input.type)) throw requestError("请填写项目名称并选择类型");
              const base = join(dirname(personalPath), "projects");
              await mkdir(base, { recursive: true });
              const directory = join(base, "project-" + randomUUID());
              await mkdir(directory, { mode: 0o700 });
              const workspace = await realpath(directory);
              const id = idOf(workspace);
              // Register the specialization before exposing its workspace in the sidebar.
              if (input.type === "auto-reply") await call("/reply-projects", owner, { projectId: id });
              if (input.type === "auto-operations") await call("/operations/register", owner, { projectId: id });
              const w = await ctx.workspaceRegistry.create(workspace, name);
              await syncProjects();
              json(res, 200, { id, name, type: input.type, workspaceId: w.id, sessionIds: [] });
              return;
            }
            if(req.method==='GET'&&path==='/resource-export'){
              const response=await fetch(new URL(path+u.search,worker),{headers:headers(owner)});
              if(!response.ok)throw Error('资料导出失败，请核对读取范围和发布版本');
              res.writeHead(200,{'content-type':response.headers.get('content-type'),'content-disposition':response.headers.get('content-disposition')});
              const stream=Readable.fromWeb(response.body);stream.on('error',()=>res.destroy());res.on('close',()=>stream.destroy());stream.pipe(res);return;
            }
            const readPaths = new Set(["/reply-states","/reply-timeline","/reply-accounts","/reply-resources","/transports","/reply-projects","/reply-settings","/message-routes","/reply-session","/reply-channel-history","/resource-page", "/schedules", "/notification-targets",
              "/resources",
              "/connections",
              "/grants",
              "/memory",
              "/runs",
              "/decisions",
              "/personal/settings",
              "/notifications",
            ]);
            const writePaths = new Set(["/reply-manual","/reply-settings","/message-routes","/schedules/pause",
              "/resources",
              "/connections",
              "/grants",
              "/memory",
              "/decisions/answer",
              "/personal/settings",
              "/notifications",
            ]);
            if (req.method === "GET" && readPaths.has(path)) {
              json(res, 200, await call(path + u.search));
              return;
            }
            if (req.method === "POST" && writePaths.has(path)) {
              const origin = req.headers.origin;
              if (!origin || new URL(origin).host !== req.headers.host) {
                json(res, 403, { error: "origin rejected" });
                return;
              }
              const input = await body(req);
              if(path==="/reply-settings"&&!(await call("/reply-projects")).some(p=>p.projectId===input.projectId))
                throw requestError("请在创建时选择自动化应答专项", 409);
              json(res, 200, await call(path, owner, input));
              return;
            }
            if(req.method==='POST'&&path==='/reply-project/create'){
 const origin=req.headers.origin;if(!origin||new URL(origin).host!==req.headers.host){json(res,403,{error:'origin rejected'});return}
 const projectPath=join(dirname(personalPath),'projects','auto-reply');
 await mkdir(projectPath,{recursive:true});const resolved=await realpath(projectPath);
 const projectId=idOf(resolved);
 const existing=ctx.workspaceRegistry.list().find(w=>w.path===resolved);
 if(existing&&!(await call("/reply-projects")).some(p=>p.projectId===projectId))throw requestError("已有普通项目不能改为专项",409);
 await call('/reply-projects',owner,{projectId});
 const w=await ctx.workspaceRegistry.create(resolved,'自动化应答');await syncProjects();
 json(res,200,{projectId,workspaceId:w.id});return;
 }
 if(req.method==='POST' && ['/session/start','/workspace/create'].includes(path)) {
              const origin=req.headers.origin;
              if(!origin||new URL(origin).host!==req.headers.host){json(res,403,{error:'origin rejected'});return}
              const input=await body(req);
              if(path==='/workspace/create') {
                if(typeof input.path!=='string'||typeof input.name!=='string')throw Error('Invalid project');
                await mkdir(input.path,{recursive:true});
                const w=await ctx.workspaceRegistry.create(await realpath(input.path),input.name);
                await syncProjects();json(res,200,{workspaceId:w.id,projectId:idOf(w.path)});return;
              }
              const w=ctx.workspaceRegistry.list().find(w=>w.id===input.workspaceId);
              if(!w)throw Error('Workspace unavailable');
              const id=input.sessionId||randomUUID();
              const agent=await ensureSession(id,w.path);
              if(id.startsWith('object-')){
                while(true){
                  const before=delivery.stateOf(agent.session).through;
                  const events=await call('/events?after='+before,identity(agent));
                  for(const event of events){if(event.sequence<=delivery.stateOf(agent.session).through)continue;agent.session.append('user/message',createUserMessage({content:[{type:'text',text:(event.kind==='message.history'?'渠道历史引用（不会触发自动应答）':'渠道回执')+'\n'+event.text}],source:{kind:'workbench-result',sequence:event.sequence}}),{surfaceOp:'append'});await ctx.sessions.flush(agent.session);await call('/events/ack',owner,{id:event.id})}
                  if(events.length<200)break;
                  if(delivery.stateOf(agent.session).through<=before)throw Error('History cursor did not advance');
                }
              }

              if(input.text){agent.followup(createUserMessage({content:[{type:'text',text:input.text}],source:{kind:'user'}}));await ctx.sessions.flush(agent.session)}
              json(res,200,{sessionId:id});return;
            }
            json(res, 404, { error: "unknown operation" });
          } catch (e) {
            json(res, e.status||(e instanceof SyntaxError?400:503), { error: e.message });
          }
        },
      }),
    "workbench: owner API",
  );

  // Worker calls the official Harness model service, not a second provider implementation.
  const server = createServer(async (req, res) => {
    const provided = Buffer.from(
      (req.headers.authorization ?? "").replace(/^Bearer /, ""),
    );
    const expected = Buffer.from(token);
    if (
      provided.length !== expected.length ||
      !timingSafeEqual(provided, expected)
    ) {
      json(res, 401, { error: "unauthorized" });
      return;
    }
    if(req.method==='GET'&&req.url==='/health'){json(res,200,{ready:true});return;}
    if(req.method==='POST'&&req.url==='/attachments') {
      try {
        const input=await body(req),p=input.principal;
        const w=p.kind==='personal'?personalWorkspace:ctx.workspaceRegistry.list().find(w=>idOf(w.path)===p.projectId&&w.path!==personalPath);
        if(!w||!['personal','project'].includes(p.kind))throw Error('Unknown Session');
        const persisted=await ctx.sessionPersistence.stat(p.sessionId);if(!persisted)throw Error('Unknown Session');
        const agent=await ensureSession(p.sessionId,w.path);
        const actual=identity(agent);if(actual.kind!==p.kind||actual.projectId!==p.projectId||actual.sessionId!==p.sessionId)throw Error('Session identity mismatch');
        await call('/session/check',p);
        const files=delivery.stateOf(agent.session).files;
        if(!input.attachmentId){json(res,200,files);return;}
        const ref=files.find(f=>f.attachmentId===input.attachmentId);if(!ref)throw Error('Attachment is not in this Session');
        json(res,200,[{...ref,text:await attachmentText(ctx.attachments,ref,AbortSignal.timeout(30000))}]);
      }catch(e){json(res,400,{error:e.message})}
      return;
    }
    if(req.method==='POST' && ['/owner-request','/dispatch'].includes(req.url)) {
      let reservedSid='';
      try {
        const input=await body(req);
        if(req.url==='/owner-request'){
          if(input.kind!=='personal'||typeof input.sessionId!=='string'||!input.sessionId)throw requestError('Personal Session required');
          const agent=await ensureSession(input.sessionId,personalPath);
          json(res,200,delivery.stateOf(agent.session).ownerRequest);return;
        }
        if(!input.principal||typeof input.id!=='string'||!input.id||typeof input.principal.sessionId!=='string'||!input.principal.sessionId)throw requestError('Invalid dispatch identity');
        const incoming=input.operation==='incoming';
        const isOwner=incoming&&input.principal.kind==='personal';
        const w=isOwner?personalWorkspace:ctx.workspaceRegistry.list().find(w=>idOf(w.path)===input.principal.projectId&&w.path!==personalPath);
        if(!w||(!isOwner&&input.principal.kind!=='project')||typeof input.goal!=='string')throw requestError('Target project unavailable',403);
        const sid=input.principal.sessionId;
        if(incoming&&!isOwner){const admission=await call('/messages/admit?runId='+encodeURIComponent(input.id));if(!admission.allowed){if(!admission.terminal)throw requestError('Earlier customer message pending',503);json(res,200,{accepted:true,sessionId:sid});return}}
        if(incoming&&!isOwner&&objectAdmissions.has(sid))throw requestError('Customer admission busy',503);
        if(incoming&&!isOwner&&ctx.agents.get(sid)?.status!=='running'){
          const active=ctx.agents.list().filter(a=>a.id.startsWith('object-')&&a.status==='running').length;
          if(active+objectAdmissions.size>=4)throw requestError('Customer capacity busy',503);
        }
        if(incoming&&!isOwner){objectAdmissions.add(sid);reservedSid=sid}
        let agent;{agent=await ensureSession(sid,w.path);if(incoming&&!isOwner){await settleIncoming(agent);const pending=delivery.stateOf(agent.session).pendingIncoming;if(pending&&pending!==input.id)throw requestError('Customer turn busy',503)}}

        if(incoming&&!isOwner&&!(await call('/messages/admit?runId='+encodeURIComponent(input.id))).allowed){json(res,200,{accepted:true,sessionId:agent.id});return}
        if(!delivery.stateOf(agent.session).dispatches.includes(input.id)) {
          agent.followup(createUserMessage({content:[{type:'text',text:(!incoming?'这是一项已经由今日派发到本项目的任务。当前会话就是目标项目会话，派发这一步已完成。请直接执行本人原话中的目标业务，不要重复执行其中针对今日的转交或task_delegate指令。\n\n本人原话：\n':'')+input.goal+'\n\n本次已绑定的资料引用：'+JSON.stringify([...(input.sources||[]),...(input.lineage||[])])}],source:{kind:isOwner?'workbench-owner':incoming?'workbench-incoming':'workbench-delegation',runId:input.id}}));
          if(!await ctx.sessions.flush(agent.session))throw Error('Project task was not persisted');
        }
        json(res,200,{accepted:true,sessionId:agent.id});
      }catch(e){json(res,e.status||(e instanceof SyntaxError?400:503),{error:e.status?e.message:'Host delivery unavailable'})}finally{if(reservedSid)objectAdmissions.delete(reservedSid)}
      return;
    }
    if (req.url !== "/model" || req.method !== "POST") {
      json(res, 404, {});
      return;
    }
    const abort = new AbortController();
    const timer = setTimeout(() => abort.abort(), 140000);
    res.on("close", () => {
      if (!res.writableEnded) abort.abort();
    });
    try {
      const input = await body(req);
      const method = input.method;
      if (
        !method?.instructions ||
        Buffer.byteLength(JSON.stringify(input.input)) >
          (method.maxInputBytes || 24000)
      )
        throw new Error("invalid model input");
      const result = await admitModel(input.lane, abort.signal, async () => {
      const prepared = await ctx.llm.prepareCall(
        {
          provider: config.provider,
          model: config.model,
          maxTokens: Math.min(method.maxOutputTokens || 1200, 8192),
          reasoningEffort: method.reasoningEffort || "off",
        },
        abort.signal,
      );
      const assembler = new BlockAssembler();
      for await (const chunk of prepared.stream({
        ...prepared.config,
        system: method.instructions,
        messages: [
          createUserMessage({
            content: [{ type: "text", text: JSON.stringify(input.input) }],
            source: { kind: "user" },
          }),
        ],
        purpose: "workbench-generation",
        signal: abort.signal,
      }))
        assembler.push(chunk);
      if (assembler.finish?.kind !== "stop")
        throw new Error(
          "model did not finish successfully: " +
            JSON.stringify(assembler.finish),
        );
      const text = assembler
        .blocks()
        .filter((b) => b.type === "text")
        .map((b) => b.text)
        .join("\n");
      return { text, usage: assembler.usage ?? {} };
      });
      if (!res.destroyed) json(res, 200, result);
    } catch (e) {
      if (!res.destroyed) json(res, 502, { error: e.message });
    } finally {
      clearTimeout(timer);
    }
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(config.bridgePort, "127.0.0.1", resolve);
  });
  ctx.effect(() => () => server.close(), "workbench: private model bridge");
}
