import assert from 'node:assert/strict';
import test from 'node:test';
import {directoryVault, notePath, renameVaultNote, searchVault, vaultArchiveEntries, vaultFolderPaths, vaultStructure, vaultView} from './vault-markdown.js';

test('archive paths stay relative and portable even when a source supplies unexpected names', () => {
  for (const path of ['/absolute.md','a/../escape.md','a//nested.md','C:/drive.md','AUX/data.md']) {
    assert.throws(()=>vaultArchiveEntries([{path,text:'note'}],[]),{status:422});
  }
  assert.equal(notePath('/absolute.md'),false);
  assert.equal(notePath('a/../escape.md'),false);
  assert.throws(()=>vaultArchiveEntries([{path:'Note.md',text:''},{path:'note.md',text:''}],[]),{status:422});
  assert.deepEqual(Object.keys(vaultArchiveEntries([{path:'中文/笔记.md',text:'笔记'}],['中文','空目录'])),
    ['中文/','空目录/','中文/笔记.md']);
});

test('Markdown and Obsidian media links become explicit preview controls', () => {
  const raw=['![[assets/图.png|图示]]','![相对图](../assets/图.png)','![[assets/报告.pdf#page=2]]',
    '[远程 PDF](https://example.invalid/report.pdf#page=4)','`![[assets/not-a-link.png]]`',
    '![越界](../../outside.png)','![恶意](javascript:fake.png)'].join('\n\n');
  const {document}=vaultView([{path:'目录/笔记.md',text:raw}]);
  assert.equal(document.text,raw);
  assert.equal(document.media.length,4);
  assert.deepEqual(document.media.map(ref=>ref.asset),['assets/图.png','assets/图.png','assets/报告.pdf',undefined]);
  assert.deepEqual(document.media.map(ref=>ref.page),[undefined,undefined,2,4]);
  assert.equal(document.media[3].href,'https://example.invalid/report.pdf#page=4');
  assert.match(document.html,/href="#wb-vault-media\/2"/);
  assert.doesNotMatch(document.html,/<img|javascript:|https:\/\//);
});

test('moving a note keeps its relative image and PDF references aimed at the same files', () => {
  const before={path:'资料/旧.md',text:'![图](./图.png)\n\n![[./手册.pdf#page=2|手册]]\n\n![图2][img]\n\n[img]: ../图2.jpg'};
  const to='归档/2026/新.md';
  const moved=renameVaultNote([before],before.path,to).find(item=>item.path===to);
  const original=vaultView([before]).document.media;
  assert.deepEqual(vaultView([moved]).document.media,original);
});

test('folder movement preserves empty branches and rewrites links to the moved subtree', () => {
  const docs = [
    {path:'客户/旧目录/服务.md',text:'## 规则\n\n[[./备注#正文]]'},
    {path:'客户/旧目录/备注.md',text:'## 正文'},
    {path:'入口.md',text:'[[客户/旧目录/服务#规则|规则]]\n\n[备注](客户/旧目录/备注.md#正文)'},
  ];
  const folders = vaultFolderPaths(docs,['客户/旧目录/空目录']);
  assert.deepEqual(folders,['客户','客户/旧目录','客户/旧目录/空目录']);
  const moved = vaultStructure(docs,folders,{action:'move-folder',from:'客户/旧目录',to:'归档/服务'});
  assert.ok(moved.folders.includes('归档/服务/空目录'));
  assert.ok(moved.changes.some(note=>note.path==='客户/旧目录/备注.md' && note.text===null));
  const next = moved.changes.filter(note=>note.text!==null);
  const oldLink = vaultView(next,'入口.md').document.html;
  assert.ok(oldLink.includes(encodeURIComponent('归档/服务/服务.md')));
  assert.ok(oldLink.includes(encodeURIComponent('归档/服务/备注.md')));
  assert.throws(()=>vaultStructure(docs,folders,{action:'move-folder',from:'客户/旧目录',to:'客户/旧目录/空目录/套娃'}),{status:400});
  const deleted = vaultStructure(docs,folders,{action:'delete-folder',from:'客户/旧目录'});
  assert.deepEqual(deleted.folders,['客户']);
  assert.equal(deleted.changes.length,2);
});

test('duplicating a folder keeps original links and routes internal links inside the copy', () => {
  const docs = [
    {path:'原件/说明.md',text:'[[条款|规则]]'},
    {path:'原件/条款.md',text:'# 条款'},
    {path:'首页.md',text:'[[条款]]'},
  ];
  const copy = vaultStructure(docs,['原件/空目录'],{action:'duplicate-folder',from:'原件',to:'副本'});
  const merged = new Map(docs.map(note=>[note.path,note]));
  for (const note of copy.changes) merged.set(note.path,note);
  const all = [...merged.values()];
  assert.ok(copy.folders.includes('副本/空目录'));
  for (const [from,to] of [['首页.md','原件/条款.md'],['原件/说明.md','原件/条款.md'],['副本/说明.md','副本/条款.md']]) {
    assert.ok(vaultView(all,from).document.html.includes(encodeURIComponent(to)));
  }
});

test('imported metadata is folded separately without modifying the source Markdown', () => {
  const sourcePath = 'product-operation-packs/sku-31314141/v1/无具体交流跟进规则.md';
  const text = [
    '# 原中心配置资料 · ' + sourcePath, '',
    '> 仅作为原方法与配置资料引用，不自动启用旧定时器或执行旧指令。来源 SHA256：`' + 'a'.repeat(64) + '`；片段 1/1。', '',
    '# 无具体交流时的服务跟进（2026-10-21）', '', '| 名称 | 说明 |', '| --- | --- |', '| 语料 | 内容 |',
  ].join('\n');
  const documents = [{path: sourcePath + '.md', text}];
  const {document} = vaultView(documents, undefined, {originalCenter:true});
  assert.equal(document.text, text);
  assert.equal(document.source.path, sourcePath);
  assert.equal(document.source.sha256, 'a'.repeat(64));
  assert.equal(document.source.fragment, '1/1');
  assert.ok(document.html.startsWith('<h1>无具体交流时的服务跟进（2026-10-21）</h1>'));
  assert.match(document.html, /class="wb-vault-table"/);
  assert.equal(vaultView(documents).document.source, undefined);
  const withoutTitle = vaultView([{...documents[0], text:text.split('\n\n').slice(0,2).join('\n\n')+'\n\n笔记正文'}],
    undefined, {originalCenter:true}).document;
  assert.equal(withoutTitle.hasTitle, false);
  assert.equal(withoutTitle.html, '<p>笔记正文</p>');
});

test('moving a note rewrites resolved links and outgoing relative links in one change set', () => {
  const oldPath = '甲/旧笔记.md', newPath = '归档/服务/新 笔记.md';
  const docs = [
    {path:oldPath, text:'## 标题\n\n[旁注](./说明.md#节) [[./说明#节]] [[#标题]]'},
    {path:'甲/说明.md', text:'## 节'},
    {path:'乙/旧笔记.md', text:'同名'},
    {path:'索引.md', text:[
      '[[甲/旧笔记#标题|别名]] [[旧笔记]] `[[甲/旧笔记]]`',
      '[**正文**](甲/旧笔记.md#标题 "原说明") [引用][main]',
      '', '[main]: <甲/旧笔记.md#标题> "原引用"', '',
      '```md', '[[甲/旧笔记]]', '```',
    ].join('\n')},
  ];
  const changes = renameVaultNote(docs, oldPath, newPath);
  assert.deepEqual(changes.map(change => change.path), [oldPath,newPath,'索引.md']);
  assert.equal(changes[0].text, null);
  assert.equal(changes[1].create, true);
  assert.match(changes[1].text, /\.\.\/\.\.\//);
  assert.match(changes[1].text, /\[\[#标题\]\]/);
  assert.match(changes[2].text, /\[\[新 笔记#标题\|别名\]\] \[\[旧笔记\]\] `\[\[甲\/旧笔记\]\]`/);
  assert.match(changes[2].text, /#标题> "原说明"/);
  assert.match(changes[2].text, /#标题> "原引用"/);
  assert.ok(changes[2].text.endsWith('```md\n[[甲/旧笔记]]\n```'));
  const after = docs.filter(doc => !changes.some(change => change.path === doc.path))
    .concat(changes.filter(change => change.text !== null));
  const view = vaultView(after, newPath);
  assert.deepEqual(view.backlinks, ['索引.md']);
  assert.match(view.document.html, /href="#wb-vault\/%E7%94%B2%2F%E8%AF%B4%E6%98%8E.md\//);
  const target = '#wb-vault/' + encodeURIComponent(newPath) + '/' + encodeURIComponent('标题');
  assert.equal(vaultView(after, '索引.md').document.html.split(target).length-1, 3);
  assert.throws(()=>renameVaultNote(docs, oldPath, '乙/旧笔记.md'), {status:409});
});

test('Vault uses Markdown paths, Chinese headings and real incoming links', () => {
  const home = '主题/运营方法.md';
  const target = '服务/说明(新).md';
  const text = [
    '---', 'tags: [方法]', '---', '',
    '[[服务/说明(新)#处理步骤|服务说明]]',
    '[同一目标](../服务/说明%28新%29.md#处理步骤)',
    '`[[另一个文档]]`',
    '![外链图片](https://example.com/image.png)',
  ].join('\n');
  const documents = [
    {path: home, text},
    {path: target, text: '## 处理步骤\n\n回到 [[运营方法]]'},
    {path: '另一个文档.md', text: '代码内的文字不产生反链'},
  ];
  const view = vaultView(documents, home);
  assert.equal(view.document.text, text);
  assert.equal(view.document.properties, 'tags: [方法]');
  assert.equal(view.notes.length, 3);
  assert.deepEqual(view.backlinks, [target]);
  const url = `#wb-vault/${encodeURIComponent(target)}/${encodeURIComponent('处理步骤')}`;
  assert.ok(view.document.html.includes(`href="${url}">服务说明</a>`));
  assert.ok(view.document.html.includes(`href="${url}">同一目标</a>`));
  assert.ok(view.document.html.includes('<code>[[另一个文档]]</code>'));
  assert.ok(!view.document.html.includes('https://example.com'));
  assert.deepEqual(vaultView(documents, '另一个文档.md').backlinks, []);
});

test('ambiguous, hidden and traversal links do not select an unintended note', () => {
  const view = vaultView([
    {path: '索引.md', text: '[[同名]] [[甲/同名]] [越界](../机密.md)'},
    {path: '甲/同名.md', text: ''},
    {path: '乙/同名.md', text: ''},
    {path: '../机密.md', text: ''},
    {path: '.obsidian/配置.md', text: ''},
  ], '索引.md');
  assert.equal(view.notes.length, 3);
  assert.match(view.document.html, /^<p>同名 <a href="#wb-vault\/[^"]+">甲\/同名<\/a> 越界<\/p>$/);
});

test('rendered note strips raw HTML and automatic third-party image requests', () => {
  const html = vaultView([{path:'测试.md',text:[
    '<script>alert(1)</script>', '',
    '[测试](javascript:alert(1))', '',
    '![引用图][img]', '', '[img]: https://example.com/sensitive.png',
  ].join('\n')}]).document.html;
  assert.ok(!html.includes('<script'));
  assert.ok(!html.includes('javascript:'));
  assert.ok(!html.includes('https://example.com'));
});

test('directory listing uses one published version across all source pages', async () => {
  const calls = [];
  const documents = await directoryVault(async url => {
    const query = new URLSearchParams(url.split('?')[1]);
    calls.push([...query]);
    return query.has('after') ? {records: [{fields:{name:'二.md',text:'二'}}]} :
      {records: [{fields:{name:'目录/一.md',text:'一'}}],next:'next'};
  }, {id:'vault-1',currentVersion:'fixed'}, 'text');
  assert.deepEqual(documents.map(d => d.path), ['目录/一.md','二.md']);
  assert.deepEqual(calls.map(entries => new Map(entries).get('version')), ['fixed','fixed']);
});

test('explicit extensions, reference links, footnotes and block markers resolve independently', () => {
  const documents = [
    {path:'索引.md',text:[
      '[[目标]] [[目标.md]] [[目标.md.md]] [块][target] [缺失](/workbench-api/catalog)',
      '', '[target]: ./目标.md#%5Eparagraph-1', '',
      '脚注[^details]', '', '[^details]: 脚注正文',
    ].join('\n')},
    {path:'目标.md',text:'正文 ^paragraph-1'},
    {path:'目标.txt',text:''},
    {path:'目标.md.md',text:''},
  ];
  const {html} = vaultView(documents,'索引.md').document;
  const md = encodeURIComponent('目标.md');
  assert.match(html,/^<p>目标 <a/);
  assert.ok(html.includes(`href="#wb-vault/${md}/">目标.md</a>`));
  assert.ok(html.includes(`href="#wb-vault/${md}.md/">目标.md.md</a>`));
  assert.ok(html.includes(`href="#wb-vault/${md}/%5Eparagraph-1">块</a>`));
  assert.ok(!html.includes('/workbench-api'));
  for (const [,target] of html.matchAll(/href="#(user-content-[^"]+)"/g)) {
    assert.ok(html.includes(`id="${target}"`), `missing footnote target ${target}`);
  }
  const target = vaultView(documents,'目标.md');
  assert.match(target.document.html,/<p id="user-content-vault-block-paragraph-1">正文<\/p>/);
  assert.deepEqual(target.backlinks,['索引.md']);
  assert.deepEqual(searchVault(documents,' 正文 ^PARAGRAPH-1 '),[{path:'目标.md'}]);
});

test('directory listing rejects a previously seen cursor and duplicate paths', async () => {
  const resource = {id:'vault-1',currentVersion:'fixed'};
  const cursors = ['a','b','a'];
  await assert.rejects(directoryVault(async () => ({records:[],next:cursors.shift()}), resource,'text'), /游标异常/);
  await assert.rejects(directoryVault(async () => ({records:[{fields:{name:'一.md',text:'一'}}],next:'a'}), resource,'text'), /重复路径/);
});
