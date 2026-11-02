import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import fs from 'node:fs/promises';
import path from 'node:path';
import {chromium} from '../runtime/node_modules/playwright/index.mjs';
const project={id:'p',name:'调用次数验收',description:'',aliases:[],defaultSessionId:'s',expertIds:[],pluginIds:[]};
const session={id:'s',projectId:'p',title:'历史调用记录',kind:'assistant',updatedAt:1};
const runs=[1,3,8].map((count,i)=>({id:'r'+i,projectId:'p',sessionId:'s',messageId:'m'+i,state:'completed',text:'验收问题 '+i,result:'已完成 '+i,createdAt:i+1,startedAt:i+1,finishedAt:i+2}));
let seq=0;
const history=runs.flatMap((r,i)=>Array.from({length:[1,3,8][i]},()=>({seq:++seq,sessionId:'s',runId:r.id,type:'harness.tool/call',data:{name:'read_file',arguments:{}},createdAt:1})));
const clients=new Set();
const server=createServer(async(req,res)=>{
 try{
  const url=new URL(req.url,'http://localhost');
  if(url.pathname.startsWith('/api/v1/')){
   const route=url.pathname.slice(7);
   if(route==='/events'&&!url.searchParams.has('json')){res.writeHead(200,{'Content-Type':'text/event-stream','Cache-Control':'no-cache'});res.flushHeaders();res.write(': connected\n\n');clients.add(res);req.on('close',()=>clients.delete(res));return;}
   const data=route==='/auth'?{authenticated:true}:route==='/projects'?[project]:route==='/projects/p/sessions'?[session]:route==='/sessions/s'?session:route==='/runs'?runs:route==='/events'?history.filter(e=>e.seq>Number(url.searchParams.get('after')||0)):route==='/notices/count'?{unread:0}:[];
   res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify(data));return;
  }
  const file=path.join(process.env.PW_TOOL_COUNT_DIST||'web/dist',url.pathname==='/'?'index.html':url.pathname);
  const body=await fs.readFile(file);res.writeHead(200,{'Content-Type':file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'});res.end(body);
 }catch{res.writeHead(404);res.end();}
});
await new Promise(r=>server.listen(0,'127.0.0.1',r));
const browser=await chromium.launch({headless:true,executablePath:process.env.PW_CHROME||'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'});
try{
 const page=await browser.newPage({viewport:{width:1440,height:1000}});
 await page.goto('http://127.0.0.1:'+server.address().port+'/#project=p&session=s');
 for(const n of [1,3,8])await page.getByRole('button',{name:new RegExp('执行过程 · '+n+' 次工具调用')}).waitFor();
 await page.evaluate(()=>{window.zeroCounts=0;window.counterObserver=new MutationObserver(()=>{if([...document.querySelectorAll('button')].some(b=>/执行过程 ·\s*0 次工具调用/.test(b.textContent)))window.zeroCounts++});window.counterObserver.observe(document.body,{childList:true,subtree:true,characterData:true})});
 for(let i=0;i<2400;i++){
  const event={seq:++seq,sessionId:'unrelated-project',runId:'other',type:'harness.tool/output',data:{text:'background'},createdAt:1};
  for(const client of clients)client.write('id: '+event.seq+'\ndata: '+JSON.stringify(event)+'\n\n');
  if(i%100===0)await new Promise(r=>setTimeout(r,10));
 }
 // Replay duplicate historical events after pressure, as on reconnect.
 for(const event of history)for(const client of clients)client.write('id: '+event.seq+'\ndata: '+JSON.stringify(event)+'\n\n');
 await page.waitForTimeout(700);
 for(const n of [1,3,8])assert.equal(await page.getByRole('button',{name:new RegExp('执行过程 · '+n+' 次工具调用')}).count(),1);
 assert.equal(await page.evaluate(()=>window.zeroCounts),0,'the display regressed to zero');
 await fs.mkdir('artifacts/tool-counts',{recursive:true});await page.screenshot({path:'artifacts/tool-counts/stable-counts.png',fullPage:true});
 console.log('PASS: actual rendered counts remain 1/3/8 through 2400 background events and duplicate replay; zero-count frames=0');
}finally{await browser.close();for(const client of clients)client.destroy();await new Promise(r=>server.close(r));}
