// Keep the pinned native MCP implementation and scope ownership. Optional
// global integrations get a finite startup budget, including tool discovery.
import {pathToFileURL} from 'node:url';

export const name='workbench-global-mcp';
export const inject=['tools'];
export async function apply(ctx,config){
 const {workbenchHarnessRoot="/opt/harness",...native}=config;
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
