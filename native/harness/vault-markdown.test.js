import assert from 'node:assert/strict';
import test from 'node:test';
import {directoryVault, searchVault, vaultView} from './vault-markdown.js';

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
