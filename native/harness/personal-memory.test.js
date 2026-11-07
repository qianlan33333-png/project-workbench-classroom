import{test}from'node:test';import assert from'node:assert/strict';
import{installPersonalMemory}from'./personal-memory.js';
test('each personal assembly replaces edited/forgotten memory in the current step only',async()=>{
 let hook,rows=[{id:'rule',revision:1,text:'first',confirmed:true}],calls=0;
 const agent={ctx:{on:(name,fn)=>{assert.equal(name,'system-prompt/assemble');hook=fn}}};
 installPersonalMemory(agent,{kind:'project'},()=>{throw Error('project read personal rules')});assert.equal(hook,undefined);
 installPersonalMemory(agent,{kind:'personal'},async()=>{calls++;return rows});
 const next=async()=>({sections:[{name:'workbench:method',text:'method'},{name:'workbench:personal-memory',text:'stale'}],contexts:[],tools:[]});
 let out=await hook({}, {}, next);assert.match(out.sections[1].text,/first/);assert(!out.sections[1].text.includes('stale'));assert.equal(out.sections[0].text,'method');
 rows=[{id:'rule',revision:2,text:'corrected',confirmed:true},{id:'guess',text:'hypothesis',confirmed:false}];
 out=await hook({}, {}, next);assert.match(out.sections[1].text,/corrected/);assert.match(out.sections[1].text,/unconfirmed/);assert(!out.sections[1].text.includes('first'));
 rows=[{id:'rule',text:'corrected',forgotten:true}];out=await hook({}, {}, next);assert(!out.sections[1].text.includes('corrected'));assert.equal(calls,3);
});
