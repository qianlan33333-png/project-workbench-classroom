import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
const url=process.env.PW_UI_URL||'http://127.0.0.1:3094';
const auth='Basic '+Buffer.from('owner:'+(process.env.PWB_PASSWORD||'workbench-ux-test')).toString('base64');
async function api(method,p,b){const r=await fetch(url+'/api/v1'+p,{method,headers:{Authorization:auth,'Content-Type':'application/json'},body:b?JSON.stringify(b):undefined});const v=await r.json();assert.equal(r.status,200,JSON.stringify(v));return v}
const report={passed:false,tools:[]};
try{
 for(const id of ['lark','aicrm']){
  const tool=(await api('GET','/cli-tools')).find(t=>t.id===id);assert(!tool.installed,'Use a fresh isolated installation, never an existing tool tree');
  const job=await api('POST','/cli-tools/'+id+'/install',{});let current;
  for(let i=0;i<300;i++){current=(await api('GET','/tool-jobs')).find(j=>j.id===job.id);if(current&&['completed','failed','interrupted'].includes(current.state))break;await new Promise(r=>setTimeout(r,1000))}
  assert.equal(current?.state,'completed',current?.error);const result=(await api('GET','/cli-tools')).find(t=>t.id===id);assert(result.installed);assert(/^[a-f0-9]{64}$/.test(result.sha256));report.tools.push({id,version:result.version,sha256:result.sha256,jobId:job.id});console.log(id,'fresh install completed');
 }
 report.passed=true;
}catch(e){report.error=String(e);throw e}finally{await fs.writeFile('artifacts/lifecycle/fresh-install-report.json',JSON.stringify(report,null,2));console.log(JSON.stringify(report))}
