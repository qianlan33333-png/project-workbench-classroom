import { zipSync, strToU8 } from 'fflate';
import { inflateRawSync, crc32 } from 'node:zlib';
import { parseDocument } from 'yaml';

const fail = message => Object.assign(new Error(message), {status:400});
const namePattern = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const maxBytes = 256 * 1024;
const decoder = new TextDecoder('utf-8', {fatal:true});

// Small Skill archives use ZIP32. Bound actual inflation, not attacker-supplied
// output sizes; verify CRC and central/local agreement before parsing any text.
function boundedUnzip(data) {
  try {
    const u16 = at => data.readUInt16LE(at), u32 = at => data.readUInt32LE(at);
    let end = data.length - 22;
    while (end >= Math.max(0, data.length - 65557) &&
      (u32(end) !== 0x06054b50 || end + 22 + u16(end + 20) !== data.length)) end--;
    if (end < 0 || u32(end) !== 0x06054b50 || u16(end + 4) || u16(end + 6)) throw Error();
    const count = u16(end + 10), central = u32(end + 16);
    if (!count || count > 25 || count !== u16(end + 8) || central + u32(end + 12) !== end) throw Error();
    const files = Object.create(null), seen = new Set();
    let offset = central, total = 0;
    for (let i = 0; i < count; i++) {
      if (u32(offset) !== 0x02014b50) throw Error();
      const flags = u16(offset + 8), method = u16(offset + 10), checksum = u32(offset + 16);
      const compressed = u32(offset + 20), size = u32(offset + 24), length = u16(offset + 28);
      const filename = data.subarray(offset + 46, offset + 46 + length);
      const name = decoder.decode(filename), local = u32(offset + 42);
      offset += 46 + length + u16(offset + 30) + u16(offset + 32);
      if (offset > end || flags & 1 || ![0,8].includes(method) || size > maxBytes || total + size > maxBytes ||
        seen.has(name) || name.startsWith('/') || /[\\\u0000-\u001f]/.test(name) || name.split('/').some(p => p === '..' || p === '.')) throw Error();
      seen.add(name);
      if (u32(local) !== 0x04034b50 || u16(local + 6) !== flags || u16(local + 8) !== method ||
        u16(local + 26) !== length || !data.subarray(local + 30, local + 30 + length).equals(filename)) throw Error();
      const start = local + 30 + length + u16(local + 28);
      if (start + compressed > central) throw Error();
      const bytes = data.subarray(start, start + compressed);
      const content = method === 8 ? inflateRawSync(bytes, {maxOutputLength:maxBytes-total || 1}) : bytes;
      if (content.length !== size || crc32(content) !== checksum) throw Error();
      total += content.length;
      if (name.endsWith('/')) { if (size) throw Error(); }
      else files[name] = content;
    }
    if (offset !== end) throw Error();
    return files;
  } catch { throw fail('ZIP 专家包损坏、包含不安全路径或解压后超过 256 KiB'); }
}

export function decodeExpertPackage(base64) {
  if (typeof base64 !== 'string' || base64.length > 512 * 1024 || base64.length % 4 !== 0 || !/^[A-Za-z0-9+/]*={0,2}$/.test(base64)) throw fail('专家包无效或超过 384 KiB');
  const archive = Buffer.from(base64, 'base64');
  const files = boundedUnzip(archive);
  const paths = Object.keys(files);
  const mains = paths.filter(p => p === 'SKILL.md' || /^[^/]+\/SKILL\.md$/.test(p));
  if (mains.length !== 1) throw fail('专家包必须包含唯一的 SKILL.md');
  const prefix = mains[0].slice(0, -8);
  let bytes = 0;
  const texts = new Map();
  for (const path of paths) {
    const local = path.slice(prefix.length);
    if (!path.startsWith(prefix) || !(local === 'SKILL.md' || /^references\/[^./\\][^/\\]*\.md$/.test(local))) throw fail('仅支持 SKILL.md 和 references 下的 Markdown 文档：' + path);
    bytes += files[path].byteLength;
    if (bytes > maxBytes) throw fail('解压后的专家包超过 256 KiB');
    try { texts.set(local, decoder.decode(files[path])); } catch { throw fail('专家文档必须使用 UTF-8 编码'); }
  }
  const main = texts.get('SKILL.md');
  const match = /^---[ \t]*\r?\n([\s\S]*?)\r?\n---[ \t]*\r?\n([\s\S]*)$/.exec(main);
  if (!match) throw fail('SKILL.md 缺少 YAML 元数据');
  let header;
  try {
    const doc = parseDocument(match[1], {uniqueKeys:true});
    if(doc.errors.length) throw Error();
    header = doc.toJS({maxAliasCount:0});
  } catch { throw fail('SKILL.md 元数据无效'); }
  if (!header || typeof header !== 'object' || Array.isArray(header) || typeof header.name !== 'string' || header.name.length > 64 || !namePattern.test(header.name)) throw fail('专家标识必须是小写字母、数字和连字符');
  if(prefix && prefix !== header.name + '/') throw fail('专家目录名与 name 不一致');
  if(header['disable-model-invocation'] !== undefined && typeof header['disable-model-invocation'] !== 'boolean' || header['user-invocable'] !== undefined && typeof header['user-invocable'] !== 'boolean') throw fail('专家调用设置无效');
  // Unsupported behavior cannot be silently imported with weaker semantics.
  const allowed = new Set(['name','description','metadata','disable-model-invocation','user-invocable']);
  if(Object.keys(header).some(key => !allowed.has(key))) throw fail('专家包包含当前工作台不支持的元数据');
  if (header.metadata !== undefined && (!header.metadata || typeof header.metadata !== 'object' || Array.isArray(header.metadata) || Object.keys(header.metadata).some(key => key !== 'displayName'))) throw fail('当前仅支持 displayName 专家元数据');
  return {name:header.name, displayName:header.metadata?.displayName || header.name, description:header.description,
    content:match[2], references:[...texts].filter(([path])=>path!=='SKILL.md').map(([path,content])=>({name:path.slice(11),content})),
    invocation:{modelInvocable:header['disable-model-invocation']!==true,userInvocable:header['user-invocable']!==false}};
}

export function encodeExpertPackage(item) {
  if (!namePattern.test(item.name) || item.source === 'personal' || item.id.startsWith('operations:')) throw fail('此方法暂不支持 Skill 专家包下载');
  const header = {name:item.name,description:item.description,metadata:{displayName:item.displayName},
    'disable-model-invocation':item.invocation?.modelInvocable===false,'user-invocable':item.invocation?.userInvocable!==false};
  const files = {[item.name+'/SKILL.md']:strToU8('---\n'+JSON.stringify(header)+'\n---\n'+item.content+'\n')};
  for(const ref of item.references || []) {
    if(!/^[^./\\\u0000-\u001f][^/\\\u0000-\u001f]*\.md$/.test(ref.name)) throw fail('参考文件路径无效');
    files[item.name+'/references/'+ref.name] = strToU8(ref.content);
  }
  return {filename:item.name+'.zip',data:Buffer.from(zipSync(files)).toString('base64'),revision:item.revision};
}
