import {createHash} from 'node:crypto';
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { StreamableHTTPClientTransport } from '@modelcontextprotocol/sdk/client/streamableHttp.js';

// The owner selects a registered resource, not an endpoint or arbitrary file.
// Standard MCP Resources supplies the actual knowledge document catalogue.
export async function resourceDocuments(config,call,projectId,resourceId,uri,{all=false,version}={}){
 const p={kind:'project',projectId,sessionId:'resource-view-'+createHash('sha256').update(projectId).digest('hex').slice(0,24)};
 const server=(config.toolServers||[]).find(s=>s.projects?.includes(projectId)&&s.sources?.some(r=>r.resourceId===resourceId));
 if(!server)return{external:false};
 const url=new URL(server.url);if(url.username||url.password||url.search||url.hash||(url.protocol!=='https:'&&(url.protocol!=='http:'||url.hostname!=='127.0.0.1')))throw Error('Invalid MCP endpoint');
 if(!/^WORKBENCH_MCP_[A-Z0-9_]+$/.test(server.tokenEnv)||!process.env[server.tokenEnv])throw Error('MCP credential unavailable');
 const declared=server.sources.filter(r=>r.resourceId===resourceId);
 const refs=version?declared.map(ref=>({...ref,version})):await call('/session/resolve-sources',p,declared);
 await call('/session/external-sources',p,refs);
 const client=new Client({name:'workbench-resource-view',version:'1'});
 const transport=new StreamableHTTPClientTransport(url,{requestInit:{headers:{authorization:'Bearer '+process.env[server.tokenEnv],'X-Workbench-Kind':p.kind,'X-Workbench-Project':p.projectId,'X-Workbench-Session':p.sessionId,'X-Workbench-Sources':JSON.stringify(refs)}}});
 try{
  await client.connect(transport);
  const documents=[];let cursor;const seen=new Set();
  do {const page=await client.listResources(cursor?{cursor}:{});documents.push(...page.resources);if(documents.length>200)throw Error('资料目录过大，请使用检索');cursor=page.nextCursor;if(cursor){if(seen.has(cursor))throw Error('资料目录游标异常');seen.add(cursor)}}while(cursor);
  const read=async uri=>{
   const result=await client.readResource({uri});
   const content=result.contents.filter(c=>typeof c.text==='string').map(c=>c.text).join('\n');
   if(Buffer.byteLength(content)>262144)throw Error('资料超过查看预算');
   return content;
  };
  let content,notes;
  if(uri){if(!documents.some(d=>d.uri===uri))throw Error('资料不在该资源目录中');content=await read(uri)}
  if(all){
   notes=[];let total=0;
   for(const document of documents){
    if(!/\.(md|txt)$/i.test(document.name))continue;
    const text=await read(document.uri);
    total+=Buffer.byteLength(text);
    if(total>32*1024*1024)throw Error('资料目录超过读取预算');
    notes.push({path:document.name,text});
   }
  }
  await call('/session/external-sources',p,refs);
  return{external:true,documents:documents.map(d=>({uri:d.uri,name:d.name,title:d.title})),content,notes,version:refs[0].version};
 }finally{await client.close()}
}
