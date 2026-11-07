import test from 'node:test';
import assert from 'node:assert/strict';
import {attachmentText} from './attachment-text.js';
import {deliveryProjection as p} from './delivery-state.js';

test('only owner upload events register files, and text reads finish integrity verification',async()=>{
 const file={attachmentId:'sha256:abc',name:'notes.txt',bytes:6};
 const message=kind=>({type:'user/message',data:{id:'u',source:{kind},content:[{type:'file',attachment:file}]}});
 assert.equal(p.apply(p.init(),message('workbench-result')).files.length,0);
 const state=p.apply(p.init(),message('user'));
 assert.deepEqual(p.stateSchema.parse(state).files,[file]);
 let finished=false;
 const provider={async *readFileStream(ref){assert.deepEqual(ref,file);yield Buffer.from('你好');finished=true}};
 assert.equal(await attachmentText(provider,file),'你好');assert.equal(finished,true);
 await assert.rejects(()=>attachmentText({async *readFileStream(){yield Buffer.from('text');throw Error('digest mismatch')}},file),/digest mismatch/);
 await assert.rejects(()=>attachmentText(provider,{...file,bytes:256*1024+1}),/限额/);
 await assert.rejects(()=>attachmentText({async *readFileStream(){yield Buffer.from([0xff])}},file));
});
