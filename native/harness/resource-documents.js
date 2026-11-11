import {createHash} from 'node:crypto';
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { StreamableHTTPClientTransport } from '@modelcontextprotocol/sdk/client/streamableHttp.js';

// The owner selects a registered resource, not an endpoint or arbitrary file.
// Standard MCP Resources supplies the actual knowledge document catalogue.
export async function resourceDocuments(config,call,projectId,resourceId,uri,identity){
 const p=identity||{kind:'project',projectId,sessionId:'resource-view-'+createHash('sha256').update(projectId).digest('hex').slice(0,24)};
 const server=(config.toolServers||[]).find(s=>s.sources?.some(r=>r.resourceId===resourceId));
 if(!server)return{external:false};
 const url=new URL(server.url);if(url.username||url.password||url.search||url.hash||(url.protocol!=='https:'&&(url.protocol!=='http:'||url.hostname!=='127.0.0.1')))throw Error('Invalid MCP endpoint');
 if(!/^WORKBENCH_MCP_[A-Z0-9_]+$/.test(server.tokenEnv)||!process.env[server.tokenEnv])throw Error('MCP credential unavailable');
 const refs=await call('/session/resolve-sources',p,server.sources.filter(r=>r.resourceId===resourceId));
 await call('/session/external-sources',p,refs);
 const client=new Client({name:'workbench-resource-view',version:'1'});
 const transport=new StreamableHTTPClientTransport(url,{requestInit:{headers:{authorization:'Bearer '+process.env[server.tokenEnv],'X-Workbench-Kind':p.kind,'X-Workbench-Project':p.projectId,'X-Workbench-Session':p.sessionId,'X-Workbench-Sources':JSON.stringify(refs)}}});
 try{
  await client.connect(transport);
  const documents=[];let cursor;const seen=new Set();
  do {const page=await client.listResources(cursor?{cursor}:{});documents.push(...page.resources);if(documents.length>200)throw Error('资料目录过大，请使用检索');cursor=page.nextCursor;if(cursor){if(seen.has(cursor))throw Error('资料目录游标异常');seen.add(cursor)}}while(cursor);
  let content;
  if(uri){if(!documents.some(d=>d.uri===uri))throw Error('资料不在该资源目录中');const result=await client.readResource({uri});content=result.contents.filter(c=>typeof c.text==='string').map(c=>c.text).join('\n');if(Buffer.byteLength(content)>(p.kind==='owner'?262144:36000))throw Error('资料超过查看预算，请通过已授权的检索工具读取片段')}
  await call('/session/external-sources',p,refs);
  return{external:true,documents:documents.map(d=>({uri:d.uri,name:d.name,title:d.title})),content,version:refs[0].version};
 }finally{await client.close()}
}
