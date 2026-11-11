import http from 'node:http';
import { readFileSync, readdirSync, realpathSync } from 'node:fs';
import { join } from 'node:path';
import { timingSafeEqual } from 'node:crypto';
import {runJSONChild,activeGroups} from '/service/child-json.mjs';
import { McpServer, ResourceTemplate } from '@modelcontextprotocol/sdk/server/mcp.js';
import { StreamableHTTPServerTransport } from '@modelcontextprotocol/sdk/server/streamableHttp.js';
import { z } from 'zod';

const config=JSON.parse(readFileSync('/service/native-access.json','utf8'));
const token=Buffer.from('Bearer '+readFileSync('/service/token','utf8').trim());
const controllers=new Set();
const safeID=x=>typeof x==='string'&&/^[A-Za-z0-9_-]{1,160}$/.test(x);
async function resolve(req){
  const p={kind:req.headers['x-workbench-kind'],projectId:req.headers['x-workbench-project'],sessionId:req.headers['x-workbench-session']};
  if(!['project','personal'].includes(p.kind)||p.kind==='project'&&!safeID(p.projectId)||!safeID(p.sessionId))throw Error('scope');
  const pinned=req.headers['x-workbench-sources']?JSON.parse(req.headers['x-workbench-sources']):undefined;
  if(pinned&&(!Array.isArray(pinned)||pinned.length!==1||pinned[0].resourceId!==config.resourceId||pinned[0].section!=='catalog'))throw Error('source scope');
  let response=await fetch(config.worker+(pinned?'/session/external-sources':'/session/resolve-sources'),{method:'POST',headers:{authorization:'Bearer '+config.token,'content-type':'application/json','X-Workbench-Kind':p.kind,'X-Workbench-Project':p.projectId,'X-Workbench-Session':p.sessionId},body:JSON.stringify(pinned||[{resourceId:config.resourceId,version:config.baselineVersion,section:'catalog'}]),signal:AbortSignal.timeout(30000)});
  let legacy;
  if(response.status===404&&!pinned){
    // Previous native release has fixed declared references and the same
    // external-sources gate, but no dynamic resolver. Keep that rollback usable.
    const catalog=await fetch(config.worker+'/resources',{headers:{authorization:'Bearer '+config.token,'X-Workbench-Kind':'owner','X-Workbench-Session':'source-compatibility'},signal:AbortSignal.timeout(30000)});
    if(!catalog.ok)throw Error('source unavailable');const resource=(await catalog.json()).find(r=>r.id===config.resourceId);
    if(!resource)throw Error('source missing');legacy=[{resourceId:resource.id,version:resource.currentVersion,section:'catalog'}];
    response=await fetch(config.worker+'/session/external-sources',{method:'POST',headers:{authorization:'Bearer '+config.token,'content-type':'application/json','X-Workbench-Kind':p.kind,'X-Workbench-Project':p.projectId,'X-Workbench-Session':p.sessionId},body:JSON.stringify(legacy),signal:AbortSignal.timeout(30000)});
  }
  if(!response.ok)throw Error('source not allowed');
  const refs=await response.json();const [ref]=pinned||legacy||refs;if(!safeID(ref?.version))throw Error('source');
  const root=ref.version===config.baselineVersion?'/data':'/versions/'+ref.version+'/data';
  if(root!=='/data'&&JSON.parse(readFileSync(join(root,'../published-source.json'),'utf8')).version!==ref.version)throw Error('unpublished source');
  return {root,ref};
}
function python(args,input,signal,timeout=45000){return runJSONChild(args,input,signal,timeout,{env:{...process.env,PYTHONPATH:'/service/vendor',PATH:'/service:'+process.env.PATH,LANG:'C.UTF-8',OPENBLAS_NUM_THREADS:'1',OMP_NUM_THREADS:'2'}})}

let retrievals=0,refreshing=false;
async function server(req,signal,refreshCall){
 let sourcePromise;const getSource=()=>sourcePromise??=resolve(req);
 const s=new McpServer({name:'answer-corpus',version:'2.0.0'});
 const run=async(op,a)=>{
  if(retrievals>=1)return{content:[{type:'text',text:'检索繁忙，请稍后重试'}],isError:true};retrievals++;
  try{const source=await getSource();const result=await python(['/service/dispatch.py',op,source.root],a,signal);const again=await resolve(req);if(again.ref.version!==source.ref.version)throw Error('source changed');return{content:[{type:'text',text:JSON.stringify(result)}]}}
  catch{return{content:[{type:'text',text:'{"action":"retrieval_error","reason":"tool_unavailable"}'}],isError:true}}
  finally{retrievals--}
 };
 s.registerTool('qa_search',{description:'按完整客户问题检索原答疑知识和语料，返回真实证据回执。',inputSchema:{question:z.string().min(1).max(4000),queries:z.array(z.string().min(1).max(512)).min(1).max(6),limit:z.number().int().min(1).max(18).default(10)}},a=>run('search',a));
 s.registerTool('qa_read',{description:'读取原检索来源上下文，数字消息ID使用records=true。',inputSchema:{ids:z.array(z.string().min(1).max(160)).min(1).max(4),records:z.boolean().default(false)}},a=>run('read',a));
 s.registerTool('qa_check',{description:'沿用原专家的完整问题覆盖、证据及适用范围检查；answer/ack可回复，human转人工，needs_retrieval继续检索。',inputSchema:{request:z.union([z.object({question:z.string().min(1).max(4000),draft:z.string().min(1).max(4000).optional()}).passthrough(),z.string().min(2).max(50000)])}},a=>run('check',a));
 if(refreshCall&&safeID(req.headers['x-workbench-run'])&&req.headers['x-workbench-project']===config.projectId)s.registerTool('qa_refresh',{description:'刷新本人配置的CRM私聊语料，全部采集及索引成功后返回标准资源清单；仅供后台资料维护。',inputSchema:{operationKey:z.string().regex(/^[A-Za-z0-9_-]{1,160}$/)}},async a=>{
  if(!refreshCall||a.operationKey!==req.headers['x-workbench-run']||req.headers['x-workbench-project']!==config.projectId||refreshing)return{content:[{type:'text',text:'资料维护不可用'}],isError:true};
  refreshing=true;try{const result=await python(['/service/refresh.py',a.operationKey],undefined,signal,3*60*60*1000);return{content:[{type:'text',text:JSON.stringify(result)}],structuredContent:result}}
  catch{return{content:[{type:'text',text:'采集未全部完成或索引失败；未发布。使用原操作编号重试。'}],isError:true}}finally{refreshing=false}
 });
 const files=async()=>{const source=await getSource();const base=realpathSync(join(source.root,'知识库'));return{source,base,names:readdirSync(base).filter(n=>n.endsWith('.md')).sort()}};
 s.registerResource('knowledge',new ResourceTemplate('corpus://knowledge/{name}',{list:async()=>{const {names}=await files();return{resources:names.map(name=>({name,uri:'corpus://knowledge/'+encodeURIComponent(name),mimeType:'text/markdown'}))}}}),{mimeType:'text/markdown'},async uri=>{
  const {source,base,names}=await files();const name=decodeURIComponent(uri.pathname.slice(1));if(!names.includes(name))throw Error('document not in resource');
  const again=await resolve(req);if(again.ref.version!==source.ref.version)throw Error('source changed');
  const path=realpathSync(join(base,name));if(!path.startsWith(base+'/'))throw Error('path');const text=readFileSync(path,'utf8');if(Buffer.byteLength(text)>262144)throw Error('document budget');return{contents:[{uri:uri.href,mimeType:'text/markdown',text}]};
 });

 return s;
}
const listener=http.createServer(async(req,res)=>{
 const auth=Buffer.from(req.headers.authorization||'');if(auth.length!==token.length||!timingSafeEqual(auth,token)){res.writeHead(401).end();return}
 if(req.method!=='POST'||req.url!=='/mcp'||req.headers.origin){res.writeHead(405).end();return}
 const abort=new AbortController();controllers.add(abort);let finished=false;res.on('close',()=>{controllers.delete(abort);if(!finished)abort.abort()});
 try{
  let bytes=0,chunks=[];for await(const b of req){bytes+=b.length;if(bytes>65536){res.writeHead(413).end();return}chunks.push(b)}
  const body=JSON.parse(Buffer.concat(chunks).toString());const refreshCall=body.method==='tools/call'&&body.params?.name==='qa_refresh';
  const s=await server(req,abort.signal,refreshCall||!!req.headers['x-workbench-run']);const t=new StreamableHTTPServerTransport({sessionIdGenerator:undefined,enableJsonResponse:true});
  res.on('finish',()=>{finished=true;void t.close();void s.close()});await s.connect(t);await t.handleRequest(req,res,body);
 }catch{if(!res.headersSent)res.writeHead(503).end()}
}).listen(Number(process.env.PORT||3109),'127.0.0.1');

function shutdown(){for(const controller of controllers)controller.abort();listener.close();listener.closeIdleConnections();const timer=setInterval(()=>{if(!activeGroups.size){clearInterval(timer);process.exit(0)}},50)}
process.once('SIGTERM',shutdown);process.once('SIGINT',shutdown);
