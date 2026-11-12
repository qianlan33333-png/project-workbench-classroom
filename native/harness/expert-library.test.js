import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { expertLibrary, selectProjectExpert, unpackExpertReferences } from './expert-library.js';

const project = {id: 'p_' + 'a'.repeat(24), name: '项目甲', workspace: '/project-a'};
const input = {action:'create', name:'customer-reply', projectId:project.id, displayName:'项目方法',
  description:'项目自定义工作方法', content:'正文。', references:[]};

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), 'expert-library-'));
  t.after(() => rm(root, {recursive:true,force:true}));
  const skill = {name:'customer-reply', source:'bundled', description:'回答客户的问题', content:'原工作台方法。', references:[]};
  const catalog = async (_workspace, name, scope) => scope === 'project' ? [] : name ? skill : [skill];
  const call = async path => {
    if (path === '/experts/usage') return [];
    throw Error('Optional operations source unavailable');
  };
  return {root, library:expertLibrary({root,catalog,personalPath:'/personal',projects:async()=>[project],call})};
}

test('managed same-name expert never shadows a historical binding', async t => {
  const {library} = await fixture(t);
  const saved = await library.save(input);
  const rows = await library.list();
  assert.deepEqual(rows.unavailable,['自动化运营']);
  assert.ok(rows.items.some(row => row.id === 'personal:superagent'));
  assert.equal(rows.items.filter(row => row.name === input.name).length,2);
  assert.equal((await library.runtime('legacy:customer-reply',project)).id,'shared:customer-reply');
  assert.equal((await library.runtime(saved.id,project)).content,input.content);
  await assert.rejects(library.runtime(saved.id,{...project,id:'p_'+'b'.repeat(24)}),{status:403});
});

test('failed or stale publication preserves the previous successful version', async t => {
  const {root,library} = await fixture(t);
  const first = await library.save(input);
  const edit = {action:'update',id:first.id,revision:first.revision,content:'第二版。'};
  const second = await library.save(edit);
  await assert.rejects(library.save(edit),{status:409});
  await assert.rejects(library.save({...edit,revision:second.revision,content:'[依据](references/missing.md)'}),{status:400});
  const oldFile = join(root,'workbench-experts',project.id,input.name,first.revision,'SKILL.md');
  await writeFile(oldFile,'corrupted');
  await assert.rejects(library.save({...edit,revision:second.revision,content:first.content}),{status:503});
  assert.equal((await library.detail(first.id)).revision,second.revision);
});

test('only the exact release-generated appendix is removed from the editor body', () => {
  const main = '[参考](references/conversation.md)\n';
  const references = [{name:'conversation.md',content:'原参考\n'}];
  const generated = main+'\n\n---\n\n## 随专家加载的参考：references/conversation.md\n\n原参考';
  assert.equal(unpackExpertReferences(generated,references),main.trimEnd());
  assert.equal(unpackExpertReferences(generated+'其他正文',references),generated+'其他正文');
});

test('the picker and automatic dispatch follow the same selection order', () => {
  assert.equal(selectProjectExpert([],undefined,undefined),'general');
  assert.equal(selectProjectExpert([{id:'a'}],undefined,undefined),'a');
  const options=[{id:'a'},{id:'b'}];
  assert.equal(selectProjectExpert(options,'a','b'),'b');
  assert.throws(()=>selectProjectExpert(options,undefined,undefined),{status:409,code:'project_expert_required'});
  assert.throws(()=>selectProjectExpert(options,'a','other'),{status:403});
});
