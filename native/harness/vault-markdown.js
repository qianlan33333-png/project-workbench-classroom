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

function resolveLink(node, from, byPath, byName, definitions) {
  if (node.type === 'wikilink') {
    const target = node.path || '';
    const note = !target ? from : lookup(target.includes('/') ? byPath : byName, target);
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

export function searchVault(documents, search) {
  const query = key(search.trim());
  return orderedNotes(documents).filter(doc => !query || [doc.path, doc.text].some(text =>
    key(text).includes(query))).map(doc => ({path: doc.path}));
}

export function vaultView(documents, selected) {
  const byPath = new Map();
  const byName = new Map();
  const docs = orderedNotes(documents);
  for (const doc of docs) {
    addLookup(byPath, doc.path, doc.path);
    addLookup(byName, path.basename(doc.path), doc.path);
  }
  const current = selected === undefined ? docs[0]?.path : selected;
  const notes = docs.map(doc => ({path: doc.path}));
  const backlinks = [];
  let document;
  for (const {path: name, text} of docs) {
    const nodes = descendants(parser.runSync(parser.parse(text), text), []);
    const definitions = new Map();
    for (const node of nodes) {
      if (node.type === 'definition' && !definitions.has(node.identifier)) definitions.set(node.identifier, node.url);
    }
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
      if (!['wikilink', 'link', 'linkReference'].includes(node.type)) continue;
      const link = resolveLink(node, name, byPath, byName, definitions);
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
