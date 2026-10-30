import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
const cfg=JSON.parse(await fs.readFile(path.join(os.homedir(),'.config/project-workbench/cli.json'),'utf8'));
const auth='Basic '+Buffer.from('owner:'+cfg.password).toString('base64');
async function api(method,route,body){const r=await fetch(cfg.url.replace(/\/$/,'')+'/api/v1'+route,{method,headers:{Authorization:auth,'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});const v=await r.json();assert.equal(r.status,200,JSON.stringify(v));return v}
let p;const report={passed:false};
try{
 p=await api('POST','/projects',{name:'CLI安装对话验收 '+Date.now()});
 const m=await api('POST','/messages',{eventId:crypto.randomUUID(),sessionId:p.defaultSessionId,text:'验收全局工具安装：使用 manage_cli 的 install 动作安装 lark（飞书 CLI），现有版本已经安装，应复用该版本。然后通过 manage_cli jobs 等待本次安装操作完成，再用 list 查询。不要调用登录、配置账号、自更新或业务命令。成功后回复 CLI_INSTALL_OK 和版本。'});
 let run;for(let i=0;i<120;i++){run=(await api('GET','/runs?sessionId='+p.defaultSessionId)).find(r=>r.messageId===m.id);if(run&&['completed','failed','cancelled','interrupted'].includes(run.state))break;await new Promise(r=>setTimeout(r,500))}
 assert.equal(run?.state,'completed',run?.error);assert(run.result.includes('CLI_INSTALL_OK'),run.result);
 const job=(await api('GET','/tool-jobs')).find(j=>j.action==='install'&&j.toolId==='lark'&&j.args?.[1]===run.id);
 assert(job,'no actual installation job bound to requesting run');assert.equal(job.state,'completed',job.error);
 const t=(await api('GET','/cli-tools')).find(t=>t.id==='lark');assert(t.installed&&t.authStatus==='ready');
 report.passed=true;report.runId=run.id;report.jobId=job.id;report.result=run.result;report.version=t.version;
} catch(e){report.error=String(e);throw e}finally{
 if(p){for(const r of await api('GET','/runs?sessionId='+p.defaultSessionId)){if(['queued','running','waiting','stopping'].includes(r.state)){await api('POST','/runs/'+r.id+'/stop',{});for(let i=0;i<60;i++){if(!['queued','running','waiting','stopping'].includes((await api('GET','/runs/'+r.id)).state))break;await new Promise(r=>setTimeout(r,500))}}}await api('DELETE','/projects/'+p.id)}
 await fs.writeFile('artifacts/lifecycle/chat-install-report.json',JSON.stringify(report,null,2));console.log(JSON.stringify(report));
}
