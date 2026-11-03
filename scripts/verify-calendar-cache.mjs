import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import {createServer} from 'node:http';
import {chromium} from '../runtime/node_modules/playwright/index.mjs';
let sync={lastSuccess:Date.now()-60000,lastAttempt:Date.now()-60000,syncing:false,paused:false};
let requests=0;let calendarReads=0;
const at=Date.now()+1800000;
const item={id:'fs:cached_0',remoteId:'cached_0',kind:'event',title:'已保存的飞书会议',description:'断联期间仍可查看',start:at,end:at+3600000,destination:'feishu',version:0};
const server=createServer(async(req,res)=>{try{
 const u=new URL(req.url,'http://localhost');
 if(u.pathname.startsWith('/api/v1')){
  const p=u.pathname.slice(7);
  if(p==='/events'&&!u.searchParams.has('json')){res.writeHead(200,{'Content-Type':'text/event-stream'});res.write(': connected\n\n');return;}
  let data=[];
  if(p==='/auth')data={authenticated:true};
  if(p==='/notices/count')data={unread:0};
  if(p==='/calendar'){
   calendarReads++;assert.equal(u.searchParams.has('remote'),false,'view performed live provider read');
   data={items:[item],sync,providerError:sync.error||''};
  }
  if(p==='/calendar/sync'){
   requests++;sync={...sync,syncing:true};data=sync;
   setTimeout(()=>{sync={...sync,syncing:false,paused:true,error:'飞书用户授权已失效，请重新登录；已同步日程仍可查看'}},150);
  }
  res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify(data));return;
 }
 const f=path.join('web/dist',u.pathname==='/'?'index.html':u.pathname);const body=await fs.readFile(f);res.writeHead(200,{'Content-Type':f.endsWith('.js')?'text/javascript':f.endsWith('.css')?'text/css':'text/html'});res.end(body);
}catch(e){res.writeHead(500);res.end(e.message)}});
await new Promise(r=>server.listen(0,'127.0.0.1',r));
const browser=await chromium.launch({headless:true,executablePath:process.env.PW_CHROME||'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'});
try{
 const page=await browser.newPage({viewport:{width:1440,height:1000}});
 await page.goto('http://127.0.0.1:'+server.address().port+'/#page=calendar');
 await page.getByRole('button',{name:/已保存的飞书会议/}).waitFor();
 await page.getByText(/飞书上次同步/).waitFor();
 assert.equal(requests,0,'opening page triggered synchronization');
 await page.getByRole('button',{name:'刷新日历',exact:true}).click();
 await page.getByText(/飞书用户授权已失效/).waitFor({timeout:12000});
 assert.equal(await page.getByRole('button',{name:/已保存的飞书会议/}).count(),1);
 await fs.mkdir('artifacts/calendar-cache',{recursive:true});await page.screenshot({path:'artifacts/calendar-cache/desktop-offline.png',fullPage:true});
 for(const mode of ['日','月','周']){await page.getByRole('button',{name:mode,exact:true}).click();await page.getByRole('button',{name:/已保存的飞书会议/}).waitFor();}
 assert.equal(requests,1,'changing views triggered provider sync');
 await page.setViewportSize({width:390,height:844});
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'mobile horizontal overflow');
 await page.screenshot({path:'artifacts/calendar-cache/mobile-offline.png',fullPage:true});
 console.log(JSON.stringify({passed:true,manualSyncCalls:requests,cacheReads:calendarReads,offlineEventRetained:true,mobileWidth:390}));
}finally{await browser.close();server.closeAllConnections();await new Promise(r=>server.close(r));}
