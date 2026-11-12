import fs from 'node:fs/promises';
import {homedir} from 'node:os';
import {join} from 'node:path';
import {createChannelLinks} from './channel-links.mjs';import crypto from 'node:crypto';import{createServer}from 'node:http';
import{McpServer}from '@modelcontextprotocol/sdk/server/mcp.js';
import{StreamableHTTPServerTransport}from '@modelcontextprotocol/sdk/server/streamableHttp.js';
import{createMcpExpressApp}from '@modelcontextprotocol/sdk/server/express.js';
import{z}from 'zod/v4';import{createChannels}from './channels.mjs';
const token=process.env.WORKBENCH_CONNECTOR_TOKEN,hostToken=process.env.WORKBENCH_HOST_TOKEN;
if(!token||token.length<32||!hostToken||hostToken.length<32)throw Error('Connector credentials missing');
const config=JSON.parse(await fs.readFile(process.env.WORKBENCH_CHANNEL_CONFIG,'utf8'));
const worker=new URL(process.env.WORKBENCH_WORKER_URL||'http://127.0.0.1:7381');if(worker.hostname!=='127.0.0.1'||worker.protocol!=='http:')throw Error('Worker must use loopback');
async function call(path,data){const r=await fetch(new URL(path,worker),{method:data===undefined?'GET':'POST',headers:{authorization:'Bearer '+hostToken,'content-type':'application/json','X-Workbench-Kind':'owner','X-Workbench-Session':'channel-connector'},body:data===undefined?undefined:JSON.stringify(data),signal:AbortSignal.timeout(path.startsWith('/operations/batch-manifest?')&&new URL(path,worker).searchParams.get('check')==='write'?300000:30000)});if(!r.ok){let body;try{body=await r.json()}catch{};throw Object.assign(Error('Worker rejected control request'),{status:r.status,code:body?.error||'unavailable'})};return r.json()}
const baseConnections=[...config],port=Number(process.env.WORKBENCH_CONNECTOR_PORT||7383);
const app=createMcpExpressApp({host:'127.0.0.1'}),abort=new AbortController();
const lifecycle={};
const channels=createChannels(config,call,fetch,undefined,lifecycle);
const links=await createChannelLinks({
 config,channels,workerCall:call,signal:abort.signal,endpoint:'http://127.0.0.1:'+port+'/mcp',
 directory:process.env.WORKBENCH_CHANNEL_LINK_DIR||join(homedir(),'.local/state/project-workbench/channels'),
});
Object.assign(lifecycle,{receive:links.receive,health:links.health});
app.use((req,res,next)=>{const actual=Buffer.from((req.headers.authorization||'').replace(/^Bearer /,'')),expected=Buffer.from(token);if(actual.length!==expected.length||!crypto.timingSafeEqual(actual,expected))return res.status(401).end();next()});
const outputSchema={state:z.enum(['accepted','pending_confirmation','sent','delivered','failed','unknown']),receipt:z.string().optional(),error:z.string().optional()};
app.get('/health',(_req,res)=>res.json({ready:true}));
app.use('/links',(_req,res,next)=>{res.setHeader('Cache-Control','no-store');next()});
function linkRoute(run, rawJSON = false) {
 return async (req,res)=>{
  try {
   const result = await run(req);
   if (rawJSON) res.type('application/json').send(result);
   else res.json(result);
  }
  catch(error) {
   res.status(error.public?error.status:503).json({error:error.public?error.message:'渠道暂时不可用，请重试。'});
  }
 };
}
app.get('/links',linkRoute(()=>links.status()));
app.get('/links/:kind',linkRoute(req=>links.snapshot(req.params.kind)));
app.post('/links/start',linkRoute(req=>links.begin(req.body)));
app.post('/links/pin',linkRoute(req=>links.pin(req.body)));
app.post('/links/confirm',linkRoute(req=>links.confirm(req.body)));
app.post('/links/cli/read',linkRoute(req=>links.read(req.body),true));
app.post('/mcp',async(req,res)=>{
 const server=new McpServer({name:'workbench-message-connectors',version:'0.1.0'});
 server.registerTool('contact_identity',{inputSchema:{connectionId:z.string(),contactId:z.string()}},async(input,extra)=>{const v=await channels.identity(input,{signal:extra.signal});return{content:[{type:'text',text:JSON.stringify(v)}],structuredContent:v}});
 server.registerTool('connection_describe',{inputSchema:{connectionId:z.string()}},async input=>{const v=await channels.describe(input);return{content:[{type:'text',text:JSON.stringify(v)}],structuredContent:v}});
 for(const [name,handler,inputSchema] of [['message_send',channels.send,{connectionId:z.string(),idempotencyKey:z.string(),target:z.record(z.string(),z.string()),text:z.string().max(16000)}],['message_receipt',channels.receipt,{connectionId:z.string(),idempotencyKey:z.string(),receipt:z.string(),state:z.string(),target:z.record(z.string(),z.string()),text:z.string().max(16000).optional()}]]){
  server.registerTool(name,{inputSchema,outputSchema},async (input,extra)=>{const result=await handler(input,{signal:extra.signal});return{content:[{type:'text',text:JSON.stringify(result)}],structuredContent:result}})
 }
 server.registerTool('operations_material_versions',{inputSchema:{connectionId:z.string(),ids:z.array(z.string().regex(/^[1-9][0-9]{0,18}$/)).max(5000),drafts:z.array(z.object({title:z.string(),appId:z.string(),page:z.string()})).max(5000).optional()}},async(input,extra)=>{
  const result=await channels.operationsMaterialVersions(input,{signal:extra.signal});
  return{content:[{type:'text',text:'CRM material versions'}],structuredContent:result};
 });
 for(const [name,send] of [['operations_batch_send',true],['operations_batch_receipt',false]]){
  server.registerTool(name,{inputSchema:{connectionId:z.string(),runId:z.string().uuid()}},async(input,extra)=>{
   const result=await channels.operationsBatch(input,send,{signal:extra.signal});
   return{content:[{type:'text',text:'CRM batch status'}],structuredContent:result};
  });
 }
 const transport=new StreamableHTTPServerTransport({sessionIdGenerator:undefined});res.on('close',()=>{transport.close();server.close()});
 try{await server.connect(transport);await transport.handleRequest(req,res,req.body)}catch{if(!res.headersSent)res.status(500).json({error:'connector unavailable'})}
});
app.get('/mcp',(_req,res)=>res.status(405).end());app.delete('/mcp',(_req,res)=>res.status(405).end());
const server=createServer(async(req,res)=>{
 const match=/^(?:\/api\/v1\/gateway)?\/hooks\/([a-zA-Z0-9_-]{1,96})$/.exec(req.url||'');
 if(!match)return app(req,res);
 if(req.method!=='POST'){res.writeHead(405).end();return}
 try{let size=0;const chunks=[];for await(const chunk of req){size+=chunk.length;if(size>1048576){res.writeHead(413).end();return}chunks.push(chunk)}
 const raw=Buffer.concat(chunks);const control=Object.keys(req.headers).some(k=>k.startsWith('x-ar-'));
 const result=control?await channels.ingestControl(match[1],raw,req.headers):(await channels.ingestGateway(match[1],raw,req.headers),{accepted:true});
 res.writeHead(200,{'content-type':'application/json'}).end(JSON.stringify(result));
 }catch(error){const status=[400,401,403,404,409,503].includes(error.status)?error.status:400;const codes=['invalid_request','invalid_signature','forbidden','not_bound','stale_version','ambiguous','request_conflict','unavailable'];res.writeHead(status,{'content-type':'application/json'}).end(JSON.stringify({error:codes.includes(error.code)?error.code:'invalid_request'}))}
}).listen(port,'127.0.0.1');
links.start();
channels.start(abort.signal,baseConnections.filter(connection=>!links.manages(connection))).catch(()=>{console.error('Channel receiver stopped; restart the connector after checking configuration');process.exitCode=1;server.close()});
for(const signal of ['SIGINT','SIGTERM'])process.on(signal,()=>{abort.abort();server.close()});
