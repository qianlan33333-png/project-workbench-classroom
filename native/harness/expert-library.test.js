import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { expertDocumentAction, expertLibrary, selectProjectExpert, unpackExpertReferences } from './expert-library.js';

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

test('a named draft can be resumed without exposing it to a Session until publication', async t => {
  const {library} = await fixture(t);
  const create = {action:'create-draft',createKey:'a'.repeat(32),projectId:project.id,displayName:'草稿专家',description:'只给名称和描述'};
  const draft = await library.save(create);
  assert.match(draft.content,/判断顺序/);
  assert.equal((await library.save(create)).id,draft.id);
  assert.equal((await library.list()).items.find(row=>row.id===draft.id).draft,true);
  assert.ok(!(await library.listSkills(project.workspace,project.id)).some(row=>row.id===draft.id));
  await assert.rejects(library.save({id:draft.id,action:'publish-draft',revision:draft.revision}),{status:400});
  const saved=await library.save({id:draft.id,action:'save-draft',revision:draft.revision,content:'可恢复的测试方法。'});
  assert.equal((await library.detail(draft.id)).revision,saved.revision);
  await assert.rejects(library.save({id:draft.id,action:'copy',name:'draft-bypass',projectId:project.id}),{status:409});
  await assert.rejects(library.save({id:draft.id,action:'publish-draft',revision:draft.revision}),{status:409});
  const active=await library.save({id:draft.id,action:'publish-draft',revision:saved.revision});
  assert.equal(active.id,draft.id);
  assert.ok(!(await library.detail(draft.id)).draft);
  assert.equal((await library.runtime(draft.id,project)).content,saved.content);
  assert.ok((await library.listSkills(project.workspace,project.id)).some(row=>row.id===draft.id));
});

test('legacy exact-case filenames remain readable and explicit missing Wiki links are rejected', async t => {
  const {library} = await fixture(t);
  const source={...input,content:'[upper](references/Example.md)\n\n[lower](references/example.md)',
    references:[{name:'Example.md',content:'大写'},{name:'example.md',content:'小写'}]};
  // Resolve both spellings as distinct files on Linux. On case-insensitive
  // developer filesystems use the pure document action for this assertion.
  const stored=await library.save({...source,content:'[upper](references/Example.md)',references:[source.references[0]]});
  const result=expertDocumentAction({...source,action:'remove',path:'references/example.md',confirmed:true});
  assert.match(result.content,/\[upper\]\(references\/Example.md\)/);
  assert.doesNotMatch(result.content,/references\/example.md/);
  const single={content:'[lower](references/EXAMPLE.md)',references:[source.references[1]]};
  assert.match(expertDocumentAction({...single,action:'preview',path:'SKILL.md'}).html,/#wb-vault\/references%2Fexample.md/);
  assert.deepEqual(expertDocumentAction({...single,action:'remove',path:'references/example.md'}),{usedBy:['SKILL.md']});
  await assert.rejects(library.save({id:stored.id,revision:stored.revision,action:'update',references:[
    {...source.references[0],content:'[[references/missing.md]]'},
  ]}),{status:400});
});

test('deleting a referenced document preserves text, cleans cross-links and commits atomically', async t => {
  const {library} = await fixture(t);
  const source = {content:'先阅读 [示例](references/case.md#facts)。\n\n[流程][case]\n\n[case]: ./references/case.md\n\n`[保留原样](references/case.md)`',
    references:[{name:'case.md',content:'# 实际案例\n原版本标记。'},{name:'boundaries.md',content:'保留 [[case|案例]] 的文字。'}]};
  const previous=await library.save({...input,...source});
  const removed={action:'remove',path:'references/case.md',...source};
  assert.deepEqual(expertDocumentAction(removed),{usedBy:['SKILL.md','references/boundaries.md']});
  assert.throws(()=>expertDocumentAction({...removed,path:'SKILL.md',confirmed:true}),{status:400});
  assert.match(expertDocumentAction({...source,path:'SKILL.md',action:'preview'}).html,/#wb-vault\/references%2Fcase\.md/);
  await assert.rejects(library.save({id:previous.id,revision:previous.revision,action:'update',references:source.references.slice(1)}),{status:400});
  assert.equal((await library.detail(previous.id)).revision,previous.revision);
  const result=expertDocumentAction({...removed,confirmed:true});
  assert.deepEqual(result.references,[{name:'boundaries.md',content:'保留 案例 的文字。'}]);
  assert.ok(result.content.includes('先阅读 示例。')&&result.content.includes('流程'));
  assert.ok(!result.content.includes('[case]:'));
  assert.match(result.content,/`\[保留原样\]\(references\/case.md\)`/);
  const changed=await library.save({id:previous.id,revision:previous.revision,action:'update',...result});
  assert.notEqual(changed.revision,previous.revision);
  assert.deepEqual((await library.runtime(previous.id,project)).references,result.references);
  await assert.rejects(library.save({id:changed.id,revision:changed.revision,action:'update',content:''}),{status:400});
});
