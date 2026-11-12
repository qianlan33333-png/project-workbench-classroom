import {posix as path} from 'node:path';
import {unified} from 'unified';
import remarkParse from 'remark-parse';
import remarkGfm from 'remark-gfm';
import remarkObsidian from '@quartz-community/remark-obsidian';
import remarkRehype from 'remark-rehype';
import rehypeSanitize, {defaultSchema} from 'rehype-sanitize';
import rehypeStringify from 'rehype-stringify';

const parser = unified().use(remarkParse).use(remarkGfm).use(remarkObsidian, {preset: 'strict'});
const renderer = unified().use(remarkParse).use(remarkGfm).use(annotateBlocks).use(remarkRehype)
  .use(rehypeSanitize, {...defaultSchema,tagNames:defaultSchema.tagNames.filter(tag => tag !== 'img')})
  .use(restoreFootnoteLinks)
  .use(wrapTables)
  .use(rehypeStringify);
const noteExtension = /\.(md|txt)$/i;
const labelText = value => value.replace(/[\\[\]`*_<>]/g, '\\$&');

function descendants(node, result) {
  result.push(node);
  for (const child of node.children || []) descendants(child, result);
  return result;
}

function key(value) {
  return value.normalize('NFC').toLowerCase();
}

function notePath(value) {
  return typeof value === 'string' && noteExtension.test(value) &&
    !/[\\\u0000-\u001f]/.test(value) &&
    value.split('/').every(segment => segment && !segment.startsWith('.'));
}

function addLookup(map, name, value) {
  for (const spelling of [key(name), '\0' + key(name).replace(noteExtension, '')]) {
    map.set(spelling, map.has(spelling) ? null : value);
  }
}

function lookup(map, name) {
  return map.get((noteExtension.test(name) ? '' : '\0') + key(name));
}

function annotateBlocks() {
  return tree => {
    for (const node of descendants(tree, [])) {
      if (node.type !== 'paragraph') continue;
      const last = node.children.at(-1);
      const marker = last?.type === 'text' && /(?:^|\s)\^([a-z0-9-]+)$/i.exec(last.value);
      if (!marker) continue;
      last.value = last.value.slice(0, marker.index).trimEnd();
      node.data = {hProperties:{id:'vault-block-' + marker[1]}};
    }
  };
}

function restoreFootnoteLinks() {
  return tree => {
    const nodes = descendants(tree, []);
    const ids = new Set(nodes.flatMap(node => node.properties?.id ? [node.properties.id] : []));
    for (const node of nodes) {
      const props = node.properties;
      if (node.tagName !== 'a' || !props || (!Object.hasOwn(props, 'dataFootnoteRef') && !Object.hasOwn(props, 'dataFootnoteBackref'))) continue;
      const target = props.href?.slice(1);
      const prefixed = defaultSchema.clobberPrefix + target;
      if (props.href?.startsWith('#') && !ids.has(target) && ids.has(prefixed)) props.href = '#' + prefixed;
    }
  };
}

function wrapTables() {
  return tree => {
    for (const node of descendants(tree, [])) {
      if (!node.children) continue;
      node.children = node.children.map(child => child.tagName === 'table' ? {
        type: 'element', tagName: 'div', children: [child],
        properties: {className: ['wb-vault-table'], role: 'region', ariaLabel: '表格', tabIndex: 0},
      } : child);
    }
  };
}

function resolveLink(node, from, byPath, byName, definitions) {
  if (node.type === 'wikilink') {
    const target = node.path || '';
    const relative = target.startsWith('./') || target.startsWith('../');
    const note = !target ? from : relative ? lookup(byPath, path.join(path.dirname(from), target)) :
      lookup(target.includes('/') ? byPath : byName, target);
    return {note, heading: node.heading || '', label: node.alias || node.value || target};
  }
  const url = node.type === 'linkReference' ? definitions.get(node.identifier) : node.url;
  if (url === undefined || /^(?:[a-z][a-z0-9+.-]*:|\/\/)/i.test(url)) return undefined;
  const [address, ...fragment] = url.split('#');
  let target, heading;
  try {
    target = decodeURIComponent(address);
    heading = decodeURIComponent(fragment.join('#'));
  } catch {
    return {note:undefined};
  }
  if (!target) return heading ? {note: from, heading} : undefined;
  if (target.startsWith('/')) return {note:undefined};
  const relative = path.normalize(path.join(path.dirname(from), target));
  return {note:lookup(byPath, relative), heading};
}

function rewrite(text, edits) {
  let cursor = 0;
  let output = '';
  for (const {start, end, replacement} of edits.sort((a, b) => a.start - b.start || b.end - a.end)) {
    if (start < cursor) continue;
    output += text.slice(cursor, start) + replacement;
    cursor = end;
  }
  return output + text.slice(cursor);
}

function orderedNotes(documents) {
  return documents.filter(doc => notePath(doc.path)).sort((a, b) => a.path.localeCompare(b.path, 'zh'));
}

function noteLookup(documents) {
  const byPath = new Map();
  const byName = new Map();
  for (const doc of documents) {
    addLookup(byPath, doc.path, doc.path);
    addLookup(byName, path.basename(doc.path), doc.path);
  }
  return {byPath, byName};
}

function parseNote(text) {
  const tree = parser.runSync(parser.parse(text), text);
  const nodes = descendants(tree, []);
  const definitions = new Map();
  for (const node of nodes) {
    if (node.type === 'definition' && !definitions.has(node.identifier)) definitions.set(node.identifier, node.url);
  }
  return {tree, nodes, definitions};
}

function nodeText(node) {
  return descendants(node, []).map(child => child.value ?? child.alt ?? '').join('');
}

function sourceInformation(tree) {
  const [title, description] = tree.children.filter(node => node.type !== 'yaml');
  if (title?.type !== 'heading' || title.depth !== 1 || !description) return;
  const sourcePath = /^原中心配置资料\s*[·・]\s*(.+)$/.exec(nodeText(title))?.[1];
  if (!sourcePath) return;
  const summary = nodeText(description);
  if (!summary.startsWith('仅作为原方法与配置资料引用')) return;
  const metadata = /来源\s*SHA256\s*[:：]\s*([0-9a-f]{64})\s*[;；]\s*片段\s*(\d+\s*\/\s*\d+)/i.exec(summary);
  if (!metadata) return;
  return {
    title, end: description.position.end.offset,
    display: {path: sourcePath, sha256: metadata[1], fragment: metadata[2], summary: summary.slice(0, metadata.index).trim()},
  };
}

export function searchVault(documents, search, {snippets = false} = {}) {
  const query = key(search.trim());
  return orderedNotes(documents).flatMap(doc => {
    if (!query) return [{path:doc.path}];
    const content = doc.text.normalize('NFC');
    const hit = key(content).indexOf(query);
    if (hit < 0 && !key(doc.path).includes(query)) return [];
    if (!snippets || hit < 0) return [{path:doc.path}];
    const start = Math.max(0, hit - 36);
    const end = Math.min(content.length, hit + query.length + 80);
    return [{path:doc.path, snippet:(start ? '…' : '') + content.slice(start, end).replace(/\s+/g, ' ') + (end < content.length ? '…' : '')}];
  });
}

export function vaultView(documents, selected, {originalCenter = false, includeBacklinks = true} = {}) {
  const docs = orderedNotes(documents);
  const {byPath, byName} = noteLookup(docs);
  const current = selected === undefined ? docs[0]?.path : selected;
  const notes = docs.map(doc => ({path: doc.path}));
  const backlinks = [];
  let document;
  for (const {path: name, text} of docs) {
    if (!includeBacklinks && name !== current) continue;
    const {tree, nodes, definitions} = parseNote(text);
    const source = name === current && originalCenter ? sourceInformation(tree) : undefined;
    const edits = [];
    if (source) edits.push({start:source.title.position.start.offset, end:source.end, replacement:''});
    let properties;
    let linked = false;
    const outgoing = new Set();
    for (const node of nodes) {
      const {start, end} = node.position || {};
      if (start?.offset === undefined || end?.offset === undefined) continue;
      if (node.type === 'yaml' && name === current) {
        properties = node.value;
        edits.push({start: start.offset, end: end.offset, replacement: ''});
      }
      if (node.type === 'image' && name === current) {
        edits.push({start: start.offset, end: end.offset, replacement: labelText(node.alt || '')});
      }
      if (!['wikilink', 'link', 'linkReference'].includes(node.type)) continue;
      const link = resolveLink(node, name, byPath, byName, definitions);
      if (!link) continue;
      if (name !== current && link.note && link.note === current) linked = true;
      if (name !== current) continue;
      if (link.note && link.note !== current) outgoing.add(link.note);
      const label = link.label ?? descendants(node, []).map(child => child.value || child.alt || '').join('');
      const replacement = link.note
        ? `[${labelText(label)}](<#wb-vault/${encodeURIComponent(link.note)}/${encodeURIComponent(link.heading)}>)`
        : labelText(label);
      edits.push({start: start.offset, end: end.offset, replacement});
    }
    if (linked) backlinks.push(name);
    if (name === current) document = {
      path: name, text, html: String(renderer.processSync(rewrite(text, edits))), properties,
      source: source?.display,
      outline: nodes.filter(node => node.type === 'heading' && node !== source?.title)
        .map(node => ({level:node.depth,text:nodeText(node)})),
      outgoing: [...outgoing],
      hasTitle: nodes.some(node => node.type === 'heading' && node.depth === 1 && node !== source?.title),
    };
  }
  return {notes, document, backlinks};
}

function relativeNote(from, to) {
  const relative = path.relative(path.dirname(from), to);
  return relative.startsWith('../') ? relative : './' + relative;
}

function markdownDestination(raw, labelEnd) {
  const marker = labelEnd === undefined ? /^\[[^\n]*?\]:\s*/.exec(raw) :
    /^\]\(\s*/.exec(raw.slice(labelEnd));
  if (!marker) return;
  const start = (labelEnd ?? 0) + marker[0].length;
  if (raw[start] === '<') {
    const end = raw.indexOf('>', start + 1);
    if (end !== -1) return {start, end: end + 1, target: raw.slice(start + 1, end)};
    return;
  }
  let end = start;
  let parentheses = 0;
  for (; end < raw.length && !/\s/.test(raw[end]); end++) {
    if (raw[end] === '\\') { end++; continue; }
    if (raw[end] === '(') parentheses++;
    if (raw[end] === ')') {
      if (parentheses === 0) break;
      parentheses--;
    }
  }
  if (end > start) return {start, end, target: raw.slice(start, end)};
}

export function renameVaultNote(documents, oldPath, newPath) {
  const docs = orderedNotes(documents);
  if (!docs.some(doc => doc.path === oldPath)) throw Object.assign(Error('笔记已移动或删除，请刷新语料库。'), {status:409});
  if (oldPath === newPath || !notePath(newPath) || !/\.md$/i.test(newPath)) throw Object.assign(Error('请输入新的 Markdown 笔记路径。'), {status:400});
  if (docs.some(doc => doc.path !== oldPath && key(doc.path) === key(newPath))) {
    throw Object.assign(Error('该路径已存在，请选择其他名称。'), {status:409});
  }
  return renameVaultPaths(docs, new Map([[oldPath,newPath]]));
}

function rewriteMovedLinks(doc, from, before, after, moved) {
    const {nodes, definitions} = parseNote(doc.text);
    const refs = new Set(nodes.filter(node => node.type === 'linkReference').map(node => node.identifier));
    const defined = new Set();
    const edits = [];
    for (const node of nodes) {
      if (node.type === 'definition') {
        if (!refs.has(node.identifier) || defined.has(node.identifier)) continue;
        defined.add(node.identifier);
      } else if (node.type !== 'wikilink' && node.type !== 'link') continue;
      const original = resolveLink(node, doc.path, before.byPath, before.byName, definitions)?.note;
      if (!original) continue;
      const target = moved.get(original) ?? original;
      const explicit = node.type === 'wikilink' ? node.path : node.url.split('#', 1)[0];
      if ((!explicit || !moved.has(original)) && resolveLink(node, from, after.byPath, after.byName, definitions)?.note === target) continue;
      const {start, end} = node.position;
      const raw = doc.text.slice(start.offset, end.offset);
      let replacement;
      if (node.type === 'wikilink') {
        const candidates = [path.basename(target).replace(noteExtension, ''), target.replace(noteExtension, ''), target, relativeNote(from, target)];
        const linkPath = candidates.find(candidate => resolveLink({type:'wikilink', path:candidate}, from, after.byPath, after.byName)?.note === target);
        if (linkPath) replacement = raw.replace(/^(!?\[\[)[^#|\]]+/, (_, prefix) => prefix + linkPath);
      } else {
        const labelEnd = node.type === 'link' ? (node.children.at(-1)?.position?.end.offset ?? start.offset + 1) - start.offset : undefined;
        const destination = markdownDestination(raw, labelEnd);
        if (destination) {
          const hash = destination.target.indexOf('#');
          const fragment = hash === -1 ? '' : destination.target.slice(hash);
          const href = relativeNote(from, target).split('/').map(encodeURIComponent).join('/');
          replacement = raw.slice(0, destination.start) + '<' + href + fragment + '>' + raw.slice(destination.end);
        }
      }
      if (replacement && replacement !== raw) edits.push({start:start.offset, end:end.offset, replacement});
    }
    return rewrite(doc.text, edits);
}

function renameVaultPaths(docs, moved) {
  const before = noteLookup(docs);
  const after = noteLookup(docs.map(doc => ({path:moved.get(doc.path) ?? doc.path})));
  const changes = [...moved.keys()].map(path => ({path,text:null}));
  for (const doc of docs) {
    const from = moved.get(doc.path) ?? doc.path;
    const text = rewriteMovedLinks(doc, from, before, after, moved);
    if (from !== doc.path || text !== doc.text) changes.push({path:from,text,create:from !== doc.path});
  }
  return changes;
}

export function vaultFolderPaths(documents, stored = []) {
  const folders = new Set(stored);
  for (const entry of [...documents.map(doc => doc.path), ...stored]) {
    for (let parent = path.dirname(entry); parent !== '.' && parent !== '/'; parent = path.dirname(parent)) {
      if (folders.has(parent)) break;
      folders.add(parent);
    }
  }
  return [...folders].sort();
}

function structureError(message, status = 400) {
  return Object.assign(new Error(message), {status});
}

function structurePath(name) {
  return typeof name === 'string' && name.length <= 1024 && !/[\\:*?"<>|\[\]#^\u0000-\u001f\u007f]/u.test(name) &&
    name.split('/').every(part => part && part.length <= 255 && !part.startsWith('.') && !part.endsWith('.') && part === part.trim());
}

export function vaultStructure(documents, stored, {action, from, to}) {
  const docs = orderedNotes(documents);
  const folders = vaultFolderPaths(docs, stored);
  const existingFolder = folders.includes(from);
  const note = docs.find(item => item.path === from);
  const inside = (candidate, parent) => candidate === parent || candidate.startsWith(parent + '/');
  const isFolder = action.endsWith('-folder');
  if (action !== 'create-folder' && !(isFolder ? existingFolder : note)) {
    throw structureError('资料已移动或删除，请刷新语料库。', 409);
  }
  if (action === 'delete-folder') {
    return {folders:folders.filter(folder => !inside(folder, from)), changes:docs.filter(doc => inside(doc.path, from)).map(doc => ({path:doc.path,text:null}))};
  }
  if (!structurePath(to) || (action === 'duplicate-note' && !/\.md$/i.test(to))) {
    throw structureError('请输入有效的库内路径。');
  }
  if (from === to || (existingFolder && inside(key(to), key(from)) && key(to) !== key(from))) {
    throw structureError('请选择其他位置。');
  }
  const moving = action === 'move-folder';
  if ([...folders, ...docs.map(doc => doc.path)].some(entry => key(entry) === key(to) && !(moving && entry === from))) {
    throw structureError('该路径已存在，请选择其他名称。',409);
  }
  if (action === 'create-folder') return {folders:[...folders,to],changes:[]};
  const transfer = candidate => to + candidate.slice(from.length);
  if (moving) {
    const moved = new Map(docs.filter(doc => inside(doc.path, from)).map(doc => [doc.path,transfer(doc.path)]));
    return {folders:folders.map(folder => inside(folder, from) ? transfer(folder) : folder), changes:renameVaultPaths(docs,moved)};
  }
  const copied = action === 'duplicate-folder' ? docs.filter(doc => inside(doc.path, from)) : [note];
  const mapped = new Map(copied.map(doc => [doc.path,transfer(doc.path)]));
  const before = noteLookup(docs);
  const after = noteLookup([...docs,...copied.map(doc => ({path:mapped.get(doc.path)}))]);
  const changes = docs.flatMap(doc => {
    const text = rewriteMovedLinks(doc,doc.path,before,after,new Map());
    return text === doc.text ? [] : [{path:doc.path,text}];
  });
  for (const doc of copied) {
    const path = mapped.get(doc.path);
    changes.push({path,text:rewriteMovedLinks(doc,path,before,after,mapped),create:true});
  }
  const newFolders = action === 'duplicate-folder' ? folders.filter(folder => inside(folder, from)).map(transfer) : [];
  return {folders:[...folders,...newFolders],changes};
}

export async function directoryVault(call, resource, section) {
  const documents = [];
  const seen = new Set();
  const cursors = new Set();
  let after;
  let bytes = 0;
  do {
    const query = new URLSearchParams({resourceId: resource.id, version: resource.currentVersion, section});
    if (after) query.set('after', after);
    const page = await call('/resource-page?' + query);
    for (const {fields} of page.records) {
      const text = fields.text ?? fields.markdown;
      if (!notePath(fields.name) || typeof text !== 'string') continue;
      bytes += Buffer.byteLength(text);
      if (seen.has(fields.name) || bytes > 32 * 1024 * 1024 || documents.length >= 10000) {
        throw Error('语料目录包含重复路径或超过读取预算');
      }
      seen.add(fields.name);
      documents.push({path: fields.name, text});
    }
    if (page.next) {
      if (cursors.has(page.next) || cursors.size >= 1000) throw Error('语料目录游标异常');
      cursors.add(page.next);
    }
    after = page.next;
  } while (after);
  return documents;
}
