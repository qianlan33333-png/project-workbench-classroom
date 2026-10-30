import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
const live=process.env.PW_LIVE==='1';
const cfg=live?JSON.parse(await fs.readFile(path.join(os.homedir(),'.config/project-workbench/cli.json'),'utf8')):{url:process.env.PW_UI_URL||'http://127.0.0.1:3094',password:process.env.PWB_PASSWORD||'workbench-ux-test'};
const url=cfg.url.replace(/\/$/,'');
const auth='Basic '+Buffer.from('owner:'+cfg.password).toString('base64');
async function api(method,route,body){const r=await fetch(url+'/api/v1'+route,{method,headers:{Authorization:auth,'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});const v=await r.json();assert.equal(r.status,200,JSON.stringify(v));return v}
const projects=[],plugins=[];
const report={passed:false,url,runs:[]};
async function run(p,text){const m=await api('POST','/messages',{eventId:crypto.randomUUID(),sessionId:p.defaultSessionId,text});for(let i=0;i<100;i++){const r=(await api('GET','/runs?sessionId='+p.defaultSessionId)).find(x=>x.messageId===m.id);if(r&&['completed','failed','cancelled','interrupted'].includes(r.state)){assert.equal(r.state,'completed',r.error);report.runs.push({runId:r.id,state:r.state,result:r.result});return r}await new Promise(r=>setTimeout(r,600))}throw Error('MCP task timeout')}
try{
 const echo=await api('POST','/plugins',{name:'临时验收 MCP '+Date.now(),transport:'stdio',command:'node',args:['/opt/workbench-runtime/test-mcp.mjs'],enabled:true});plugins.push(echo);
 for(const [suffix,token] of [['A','ALPHA314'],['B','BETA271']]){
  const p=await api('POST','/projects',{name:'全局MCP验收 '+suffix+' '+Date.now()});projects.push(p);
  const r=await run(p,'只调用名为 '+echo.name+' 的全局 MCP echo 工具，传 text='+token+'，原样回复工具返回值。不要使用命令等其他工具。');assert(r.result.includes('GLOBAL_MCP:'+token),r.result);
  const events=await api('GET','/events?json=1&sessionId='+p.defaultSessionId);assert(events.some(e=>e.type==='harness.tool/call'&&JSON.stringify(e).includes('echo')),'no actual echo tool call');
 }
 const hang=await api('POST','/plugins',{name:'临时超时验收 '+Date.now(),transport:'stdio',command:'node',args:['-e','setInterval(()=>{},1000)'],enabled:true});plugins.push(hang);
 const start=Date.now();const r=await run(projects[1],'本轮不调用任何工具，只回复：普通聊天继续。');report.unavailableServerChatMs=Date.now()-start;assert(r.result.includes('普通聊天继续'));assert(report.unavailableServerChatMs<30000,'bad MCP stalls chat');
 report.passed=true;
}catch(e){report.error=String(e);throw e}finally{
 for(const p of projects){for(const r of await api('GET','/runs?sessionId='+p.defaultSessionId)){if(['running','queued','waiting','stopping'].includes(r.state)){await api('POST','/runs/'+r.id+'/stop',{});for(let i=0;i<60;i++){if(!['running','queued','waiting','stopping'].includes((await api('GET','/runs/'+r.id)).state))break;await new Promise(r=>setTimeout(r,500))}}}await api('DELETE','/projects/'+p.id)}
 for(const p of plugins)await api('DELETE','/plugins/'+p.id);
 await fs.mkdir('artifacts/lifecycle',{recursive:true});await fs.writeFile('artifacts/lifecycle/'+(live?'live':'cloud')+'-mcp-report.json',JSON.stringify(report,null,2));console.log(JSON.stringify(report));
}
