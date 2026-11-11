import {createHash} from 'node:crypto';
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { StreamableHTTPClientTransport } from '@modelcontextprotocol/sdk/client/streamableHttp.js';

// The owner selects a registered resource, not an endpoint or arbitrary file.
// Standard MCP Resources supplies the actual knowledge document catalogue.
export async function resourceDocuments(config,call,projectId,resourceId,uri,{all=false,version,signal,identity}={}){
 const projectPrincipal=id=>({kind:'project',projectId:id,sessionId:'resource-view-'+createHash('sha256').update(id).digest('hex').slice(0,24)});
 const p=identity||projectPrincipal(projectId);
 const server=(config.toolServers||[]).find(s=>s.sources?.some(r=>r.resourceId===resourceId));
 if(!server)return{external:false};
 const url=new URL(server.url);if(url.username||url.password||url.search||url.hash||(url.protocol!=='https:'&&(url.protocol!=='http:'||url.hostname!=='127.0.0.1')))throw Error('Invalid MCP endpoint');
 if(!/^WORKBENCH_MCP_[A-Z0-9_]+$/.test(server.tokenEnv)||!process.env[server.tokenEnv])throw Error('MCP credential unavailable');
 const declared=server.sources.filter(r=>r.resourceId===resourceId);
 const refs=version?declared.map(ref=>({...ref,version})):await call('/session/resolve-sources',p,declared,signal);
 await call('/session/external-sources',p,refs,signal);
 // The external connector may only accept its registered project identity.
 // Check the requesting principal separately before and after the remote read.
 let connector=p;
 if(server.projects?.length&&!server.projects.includes(p.projectId)){
  let denied;
  for(const id of server.projects){
   try{
    const candidate=projectPrincipal(id);
    await call('/session/external-sources',candidate,refs,signal);
    connector=candidate;
    denied=undefined;
    break;
   }catch(error){
    if(error.status!==403)throw error;
    denied=error;
   }
  }
  if(denied)throw denied;
 }
 const client=new Client({name:'workbench-resource-view',version:'1'});
 const transport=new StreamableHTTPClientTransport(url,{requestInit:{headers:{authorization:'Bearer '+process.env[server.tokenEnv],'X-Workbench-Kind':connector.kind,'X-Workbench-Project':connector.projectId||'','X-Workbench-Session':connector.sessionId,'X-Workbench-Sources':JSON.stringify(refs)}}});
 try{
  await client.connect(transport,{signal});
  const documents=[];let cursor;const seen=new Set();
  do {const page=await client.listResources(cursor?{cursor}:{},{signal});documents.push(...page.resources);if(documents.length>200)throw Error('资料目录过大，请使用检索');cursor=page.nextCursor;if(cursor){if(seen.has(cursor)||seen.size>=200)throw Error('资料目录游标异常');seen.add(cursor)}}while(cursor);
  const read=async uri=>{
   const result=await client.readResource({uri},{signal});
   const content=result.contents.filter(c=>typeof c.text==='string').map(c=>c.text).join('\n');
   const maxBytes=all||p.kind==='owner'?262144:36000;
   if(Buffer.byteLength(content)>maxBytes)throw Error('资料超过查看预算，请通过已授权的检索工具读取片段');
   return content;
  };
  let content,notes;
  if(uri){if(!documents.some(d=>d.uri===uri))throw Error('资料不在该资源目录中');content=await read(uri)}
  if(all){
   notes=[];let total=0;
   const paths=new Set();
   for(const document of documents){
    if(!/\.(md|txt)$/i.test(document.name))continue;
    if(paths.has(document.name))throw Error('资料目录包含重复路径');
    paths.add(document.name);
    const text=await read(document.uri);
    total+=Buffer.byteLength(text);
    if(total>32*1024*1024)throw Error('资料目录超过读取预算');
    notes.push({path:document.name,text});
   }
  }
  if(connector!==p)await call('/session/external-sources',connector,refs,signal);
  await call('/session/external-sources',p,refs,signal);
  return{external:true,documents:documents.map(d=>({uri:d.uri,name:d.name,title:d.title})),content,notes,version:refs[0].version};
 }finally{await client.close()}
}
