// Keep the pinned native MCP implementation and scope ownership. Optional
// global integrations get a finite startup budget, including tool discovery.
import {pathToFileURL} from 'node:url';
import http from 'node:http';

// The SDK uses ordinary HTTP. A loopback-only listener for this one plugin
// carries MCP protocol fields to the task's Unix socket, including SSE streams.
export function gatewayMCPServer(id, socketPath = '/bridge.sock') {
 return http.createServer((req, res) => {
  if (req.url !== '/' || !['GET', 'POST', 'DELETE'].includes(req.method)) {
   res.writeHead(403).end(); return;
  }
  const headers = {};
  for (const name of ['accept','mcp-protocol-version','mcp-session-id','last-event-id']) {
   if (req.headers[name]) headers[name] = req.headers[name];
  }
  const upstream = http.request({method:req.method, path:'/gateway-mcp/' + encodeURIComponent(id), socketPath, headers}, reply => {
   const output = {};
   for (const name of ['content-type','mcp-session-id']) if (reply.headers[name]) output[name] = reply.headers[name];
   res.writeHead(reply.statusCode, output);
   res.flushHeaders();
   reply.on('error', () => res.destroy());
   reply.pipe(res);
  });
  upstream.on('error', () => {
   if (!res.headersSent) res.writeHead(502).end('MCP unavailable');
   else res.destroy();
  });
  res.on('close', () => { if (!res.writableFinished) upstream.destroy(); });
  req.on('aborted', () => upstream.destroy());
  req.on('error', () => upstream.destroy());
  req.pipe(upstream);
 });
}

export const name='workbench-global-mcp';
export const inject=['tools'];
export async function apply(ctx,config){
 const {workbenchHarnessRoot="/opt/harness", gatewayHTTPPlugin, ...native}=config;
 if (gatewayHTTPPlugin) {
  const server = gatewayMCPServer(gatewayHTTPPlugin);
  ctx.on('dispose', () => new Promise(resolve => {server.close(resolve); server.closeAllConnections();}));
  await new Promise((resolve,reject) => {server.once('error',reject); server.listen(0,'127.0.0.1',resolve);});
  native.url = 'http://127.0.0.1:' + server.address().port + '/';
 }

 const upstream=await import(pathToFileURL(workbenchHarnessRoot+'/node_modules/@deepseek-ai/dsh-mcp-client/lib/index.js'));
 const child=ctx.plugin(upstream,{...native,failOnStartupError:true,reconnect:{enabled:false}});
 let timer;
 try{
  await Promise.race([child.await(),new Promise((_,reject)=>{timer=setTimeout(()=>reject(new Error('MCP startup exceeded 5000ms')),5000)})]);
 }catch(error){
  await child.dispose();
  ctx.logger.warn(`Global MCP ${config.serverName} unavailable: ${String(error.message)}`);
  ctx.inject(['systemPrompt'],inner=>inner.systemPrompt.section({name:`workbench-mcp-error:${config.serverName}`,order:inner.systemPrompt.getSectionOrder('MCP_SERVERS'),interpolate:false,text:`全局 MCP ${config.serverName} 本轮连接失败或超时，其工具不可用。需要此工具时请明确报告；其他工作继续。`}));
 }finally{clearTimeout(timer)}
}
