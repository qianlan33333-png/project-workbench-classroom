// User-authorized existing isolated Playwright; fictional data only.
import { chromium } from '../runtime/node_modules/playwright/index.mjs';
import fs from 'node:fs/promises';
import assert from 'node:assert/strict';
const dir='.var/tool-components-ui';await fs.mkdir(dir,{recursive:true});
const source=JSON.parse(await fs.readFile('/tmp/tool-components-ui-source/preview.json','utf8'));
const meta=JSON.parse(await fs.readFile('/tmp/tool-components-full-ui/preview.json','utf8'));
const browser=await chromium.launch({executablePath:'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',headless:true});
const context=await browser.newContext({viewport:{width:1440,height:1000}});const page=await context.newPage();page.setDefaultTimeout(10000);page.on('dialog',d=>d.accept());const errors=[];page.on('pageerror',e=>errors.push(e.message));
try{
 await page.goto(source.url+'/#page=settings');await page.getByRole('heading',{name:'连接与工具'}).waitFor();await page.screenshot({path:dir+'/source-settings-desktop.png',fullPage:true});
 await page.goto(meta.url+'/#page=settings');await page.getByRole('heading',{name:'连接与工具'}).waitFor();await page.screenshot({path:dir+'/settings-desktop.png',fullPage:true});
 await page.locator('.settings-entry').filter({hasText:'工具组件'}).click();await page.getByRole('heading',{name:'工具组件',exact:true}).first().waitFor();await page.screenshot({path:dir+'/components-desktop.png',fullPage:true});
 await page.goto(meta.url+'/#project='+meta.project.id+'&session='+meta.project.defaultSessionId);await page.reload();await page.getByRole('button',{name:'工具组件',exact:true}).click();await page.locator('.session-components').getByLabel(/^组件/).selectOption(meta.plugin);await page.locator('.session-components').getByLabel(/^动作/).selectOption('start');await page.getByRole('button',{name:'执行组件',exact:true}).click();await page.getByText(/后台正在处理样例/).waitFor();await page.locator('.component-result summary').first().click();await page.getByRole('button',{name:'刷新状态',exact:true}).click();await page.getByText(/样例已完成/).waitFor();await page.screenshot({path:dir+'/session-desktop.png',fullPage:true});

 const runConfig=page.locator('.component-run-config');await runConfig.locator('summary').first().click();
 await runConfig.getByLabel(/^默认组件/).selectOption(meta.plugin);await runConfig.getByLabel(/^执行动作/).selectOption('read');
 await runConfig.getByRole('button',{name:'保存运行配置',exact:true}).click();await runConfig.locator('summary').first().filter({hasText:'第1版'}).waitFor();
 await runConfig.getByRole('button',{name:'按配置执行一次',exact:true}).click();
 const saved=await(await page.request.get(meta.url+'/api/v1/sessions/'+meta.project.defaultSessionId+'/component-config')).json();assert.equal(saved.action,'read');assert.equal(saved.pluginId,meta.plugin);
 await runConfig.getByText(/^定时运行/).click();await runConfig.getByRole('button',{name:'保存并启用定时',exact:true}).click();await runConfig.getByRole('button',{name:'暂停定时',exact:true}).click();
 const schedules=await(await page.request.get(meta.url+'/api/v1/schedules?projectId='+meta.project.id)).json();assert.equal(schedules.find(s=>s.component).enabled,false);
 const pending=await(await page.request.post(meta.url+'/api/v1/sessions/'+meta.project.defaultSessionId+'/component-calls',{data:{pluginId:meta.plugin,action:'approve',arguments:{sample:'FICTIONAL_OWNER_REVIEW'},operationKey:crypto.randomUUID()}})).json();assert.equal(pending.state,'awaiting_owner');
 const card=page.locator('.component-result').filter({hasText:pending.id.slice(0,8)});await card.locator('summary').click();await card.getByText('FICTIONAL_OWNER_REVIEW',{exact:false}).waitFor();await card.getByRole('button',{name:'本人确认执行',exact:true}).click();
 const confirmed=await(await page.request.get(meta.url+'/api/v1/sessions/'+meta.project.defaultSessionId+'/component-calls')).json();assert.equal(confirmed.find(c=>c.id===pending.id).state,'ready');
 await page.screenshot({path:dir+'/run-config-desktop.png',fullPage:true});
 await page.setViewportSize({width:390,height:844});await page.screenshot({path:dir+'/session-mobile.png',fullPage:true});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'horizontal overflow');assert.deepEqual(errors,[]);await page.setViewportSize({width:320,height:640});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'320px overflow');await page.screenshot({path:dir+'/session-small-mobile.png'});
 await fs.writeFile(dir+'/report.json',JSON.stringify({passed:true,desktop:true,mobile:true,asyncResult:true,fixedConfiguration:true,schedulePause:true,ownerConfirmation:true,smallMobile:true,pageErrors:errors},null,2));console.log(JSON.stringify({passed:true,desktop:true,mobile:true}));
}finally{await browser.close()}
