import {posix as path} from 'node:path';
import {unified} from 'unified';
import remarkParse from 'remark-parse';
import remarkGfm from 'remark-gfm';
import remarkObsidian from '@quartz-community/remark-obsidian';
import remarkRehype from 'remark-rehype';
import rehypeSanitize, {defaultSchema} from 'rehype-sanitize';
import rehypeStringify from 'rehype-stringify';

const parser = unified().use(remarkParse).use(remarkGfm).use(remarkObsidian, {preset: 'strict'});
const renderer = unified().use(remarkParse).use(remarkGfm).use(remarkRehype)
  .use(rehypeSanitize, {...defaultSchema,tagNames:defaultSchema.tagNames.filter(tag => tag !== 'img')})
  .use(rehypeStringify);
const noteExtension = /\.(md|txt)$/i;
const labelText = value => value.replace(/[\\[\]`*_<>]/g, '\\$&');

function descendants(node, result) {
  result.push(node);
  for (const child of node.children || []) descendants(child, result);
  return result;
}

function key(value) {
  return value.normalize('NFC').toLowerCase().replace(noteExtension, '');
}

function notePath(value) {
  return typeof value === 'string' && noteExtension.test(value) &&
    !/[\\\u0000-\u001f]/.test(value) &&
    value.split('/').every(segment => segment && !segment.startsWith('.'));
}

function addLookup(map, name, value) {
  map.set(name, map.has(name) ? null : value);
}

function resolveLink(node, from, byPath, byName) {
  if (node.type === 'wikilink') {
    const target = node.path || '';
    const note = !target ? from : target.includes('/') ? byPath.get(key(target)) : byName.get(key(target));
    return {note, heading: node.heading || '', label: node.alias || node.value || target};
  }
  const [address, ...fragment] = node.url.split('#');
  let target, heading;
  try {
    target = decodeURIComponent(address);
    heading = decodeURIComponent(fragment.join('#'));
  } catch {
    return undefined;
  }
  if (!target) return heading ? {note: from, heading} : undefined;
  if (target.startsWith('/') || /^[a-z][a-z0-9+.-]*:/i.test(target)) return undefined;
  const relative = path.normalize(path.join(path.dirname(from), target));
  const note = byPath.get(key(relative));
  return note || noteExtension.test(target) ? {note, heading} : undefined;
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

export function vaultView(documents, selected, search = '') {
  const byPath = new Map();
  const byName = new Map();
  const docs = documents.filter(doc => notePath(doc.path)).sort((a, b) => a.path.localeCompare(b.path, 'zh'));
  for (const doc of docs) {
    addLookup(byPath, key(doc.path), doc.path);
    addLookup(byName, key(path.basename(doc.path)), doc.path);
  }
  const current = selected === undefined ? docs[0]?.path : selected;
  const query = search.trim().normalize('NFC').toLowerCase();
  const notes = docs.filter(doc => !query || [doc.path, doc.text].some(text =>
    text.normalize('NFC').toLowerCase().includes(query))).map(doc => ({path: doc.path}));
  const backlinks = [];
  let document;
  for (const {path: name, text} of docs) {
    const nodes = descendants(parser.runSync(parser.parse(text), text), []);
    const edits = [];
    let properties;
    let linked = false;
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
      if (!['wikilink', 'link'].includes(node.type)) continue;
      const link = resolveLink(node, name, byPath, byName);
      if (!link) continue;
      if (name !== current && link.note && link.note === current) linked = true;
      if (name !== current) continue;
      const label = link.label ?? descendants(node, []).map(child => child.value || child.alt || '').join('');
      const replacement = link.note
        ? `[${labelText(label)}](<#wb-vault/${encodeURIComponent(link.note)}/${encodeURIComponent(link.heading)}>)`
        : labelText(label);
      edits.push({start: start.offset, end: end.offset, replacement});
    }
    if (linked) backlinks.push(name);
    if (name === current) document = {
      path: name, text, html: String(renderer.processSync(rewrite(text, edits))), properties,
      hasTitle: nodes.some(node => node.type === 'heading' && node.depth === 1),
    };
  }
  return {notes, document, backlinks};
}

export async function directoryVault(call, resource, section) {
  const documents = [];
  const seen = new Set();
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
    if (page.next && page.next === after) throw Error('语料目录游标异常');
    after = page.next;
  } while (after);
  return documents;
}
