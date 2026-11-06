// User-authorized isolated Playwright; fictional fixtures, no production delivery.
import {chromium} from '../runtime/node_modules/playwright/index.mjs';
import fs from 'node:fs/promises';import assert from 'node:assert/strict';
const meta=JSON.parse(await fs.readFile('/tmp/workbench-activity-ui/preview.json','utf8'));const dir='.var/activity-ui';await fs.mkdir(dir,{recursive:true});
const browser=await chromium.launch({executablePath:'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',headless:true});const page=await browser.newPage({viewport:{width:1440,height:1000}});page.setDefaultTimeout(15000);const errors=[];page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept());
try {
 await page.goto(`${meta.url}/#project=${meta.project.id}&session=${meta.project.defaultSessionId}`);await page.getByRole('region',{name:'批次抽检与回执'}).waitFor();await page.getByText('11月体验活动 · 2000人',{exact:true}).waitFor();await page.screenshot({path:dir+'/review-desktop.png',fullPage:true});
 // No custom dashboard: same activity conversation, one fixed-version review.
 await page.getByRole('navigation',{name:'项目导航',exact:true}).getByRole('button',{name:'对话',exact:true}).click();
 assert.equal(await page.getByText('合并前的记录与配置',{exact:true}).count(),0);
 const history=(await page.request.get(meta.url+'/api/v1/projects/'+meta.project.id+'/history-sessions'));
 assert(history.ok());
 const [historicalSession]=await history.json();
 assert(historicalSession);
 const runs=await page.request.get(meta.url+'/api/v1/projects/'+meta.project.id+'/history-runs?sessionId='+encodeURIComponent(historicalSession.id));
 assert(runs.ok());
 assert(JSON.stringify(await runs.json()).includes('合并前的配置和记录完整保留'));
 await page.setViewportSize({width:390,height:844});await page.screenshot({path:dir+'/review-mobile.png',fullPage:true});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'390 overflow');
 await page.setViewportSize({width:320,height:640});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'320 overflow');await page.screenshot({path:dir+'/review-small-mobile.png',fullPage:true});
 await page.setViewportSize({width:1440,height:1000});await page.getByRole('button',{name:'确认这批，执行发送',exact:true}).click();await page.getByText('已收回结果',{exact:true}).waitFor();await page.locator('.review-card > summary').click();await page.getByText('查看执行与实际回执',{exact:true}).click();await page.locator('.review-card pre').filter({hasText:'sent_count'}).waitFor();await page.screenshot({path:dir+'/receipt-desktop.png',fullPage:true});
 const reviews=await (await page.request.get(meta.url+'/api/v1/sessions/'+meta.project.defaultSessionId+'/component-reviews')).json();assert.equal(reviews[0].state,'completed');assert.equal(reviews[0].result.receipt.sent_count,2000);assert.deepEqual(errors,[]);
 await fs.writeFile(dir+'/report.json',JSON.stringify({passed:true,desktop:true,mobile390:true,mobile320:true,frozenPreview:true,readonlyHistory:true,ownerConfirm:true,receiptInSameSession:true,pageErrors:errors},null,2));console.log('Activity UI: desktop/mobile, readonly history, fixed review and same-session receipts passed');
} finally {await browser.close()}
