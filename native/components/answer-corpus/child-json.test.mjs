import test from 'node:test';import assert from 'node:assert/strict';import {mkdtemp,readFile,rm}from'node:fs/promises';import{tmpdir}from'node:os';import{join}from'node:path';import{setTimeout as delay}from'node:timers/promises';import{runJSONChild,activeGroups}from'./child-json.mjs';
test('fragmented Chinese and emoji stdout keeps exact original UTF-8',async()=>{
 const result=await runJSONChild(['-c',"import json; print(json.dumps({'text':'资料🌊'*40000}, ensure_ascii=False))"],undefined,new AbortController().signal);assert.equal(result.text,'资料🌊'.repeat(40000));assert.equal(activeGroups.size,0);
});
test('cancellation waits for an ignored-SIGTERM captured grandchild to die',async()=>{
 const dir=await mkdtemp(join(tmpdir(),'source-cancel-')),file=join(dir,'ready');const abort=new AbortController();let pid;
 const child="import os,signal,time;from pathlib import Path;signal.signal(signal.SIGTERM,signal.SIG_IGN);Path("+JSON.stringify(file)+").write_text(str(os.getpid()));time.sleep(60)";
 const script="import subprocess,sys,time;subprocess.Popen([sys.executable,'-c',"+JSON.stringify(child)+"],stdout=subprocess.PIPE,stderr=subprocess.PIPE);time.sleep(60)";
 const task=runJSONChild(['-c',script],undefined,abort.signal,20000);task.catch(()=>{});
 try{for(let i=0;i<100;i++){try{pid=Number(await readFile(file,'utf8'));break}catch{}await delay(20)}assert.ok(pid);const start=Date.now();abort.abort();await assert.rejects(task);assert.ok(Date.now()-start>=1800);assert.throws(()=>process.kill(pid,0),{code:'ESRCH'});assert.equal(activeGroups.size,0)}finally{abort.abort();if(pid){try{process.kill(pid,'SIGKILL')}catch{}}await rm(dir,{recursive:true,force:true})}
});
