import {test} from 'node:test';
import assert from 'node:assert/strict';
import {zipSync,strToU8} from 'fflate';
import {decodeExpertPackage,encodeExpertPackage} from './expert-package.js';
const encode = files => Buffer.from(zipSync(Object.fromEntries(Object.entries(files).map(([k,v])=>[k,strToU8(v)])))).toString('base64');
const main='---\nname: demo\ndescription: 回答问题\n---\n[判断](references/judgment.md)';
test('standard YAML ZIP and export preserve methods, references and invocation',()=>{
 const parsed=decodeExpertPackage(encode({'demo/SKILL.md':main,'demo/references/judgment.md':'判断依据。'}));
 assert.equal(parsed.name,'demo');assert.equal(parsed.references[0].content,'判断依据。');
 const output=encodeExpertPackage({...parsed,id:'shared:demo',invocation:{modelInvocable:false,userInvocable:false}});
 const round=decodeExpertPackage(output.data);
 assert.equal(round.content.trim(),parsed.content);assert.deepEqual(round.references,parsed.references);
 assert.deepEqual(round.invocation,{modelInvocable:false,userInvocable:false});
});
test('unsafe paths, unsupported payloads, oversized expansion and malformed header are rejected',()=>{
 for(const files of [
  {'demo/SKILL.md':main,'../escape.md':'x'},
  {'demo/SKILL.md':main,'demo/scripts/run.sh':'echo unexpected'},
  {'demo/SKILL.md':main,'demo/references/bomb.md':'x'.repeat(300*1024)},
  {'demo/SKILL.md':'---\nname: demo\nname: duplicate\n---\nx'},
  {'other/SKILL.md':main},
  {'demo/SKILL.md':main.replace('description:','allowed-tools: Bash\ndescription:')},
 ]) assert.throws(()=>decodeExpertPackage(encode(files)),{status:400});
 assert.throws(()=>decodeExpertPackage('not zip'),{status:400});
});

test('forged expanded size, CRC damage and truncated ZIP are rejected before import',()=>{
 const base=Buffer.from(encode({'demo/SKILL.md':main,'demo/references/judgment.md':'x'.repeat(20000)}),'base64');
 let central=base.readUInt32LE(base.length-6);
 const second=central+46+base.readUInt16LE(central+28)+base.readUInt16LE(central+30)+base.readUInt16LE(central+32);
 const forged=Buffer.from(base);forged.writeUInt32LE(1,second+24);
 assert.throws(()=>decodeExpertPackage(forged.toString('base64')),{status:400});
 const damaged=Buffer.from(base);damaged.writeUInt32LE(0,central+16);
 assert.throws(()=>decodeExpertPackage(damaged.toString('base64')),{status:400});
 assert.throws(()=>decodeExpertPackage(base.subarray(0,-2).toString('base64')),{status:400});
});
