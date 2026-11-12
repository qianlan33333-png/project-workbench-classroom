import { createHash, randomUUID } from 'node:crypto';
import { lstat, mkdir, readFile, readdir, realpath, rename, rm, writeFile } from 'node:fs/promises';
import { join, resolve, sep } from 'node:path';
import { expertDocumentLinks, expertMainDocument, expertReferencePattern as referencePattern, removeExpertReference, vaultView } from './vault-markdown.js';

const namePattern = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const projectPattern = /^p_[a-f0-9]{24}$/;
const hash = value => createHash('sha256').update(value).digest('hex');
const fail = (message, status = 400, code) => Object.assign(new Error(message), { status, code });
export const generalExpert = { id: 'general', name: 'general', displayName: '通用助理', description: '' };
export const personalExpertId = 'personal:superagent';
const draftTemplate = `## 任务描述

<!-- 什么时候使用这个专家？ -->

## 判断顺序

<!-- 先核对什么，信息不足时如何处理？ -->

## 输出要求

<!-- 交付什么，哪些动作需要确认？ -->`;

export function skillId(projectId, name) {
  return projectId ? `project:${projectId}:${name}` : `shared:${name}`;
}

function validName(value) {
  return typeof value === 'string' && value.length <= 64 && namePattern.test(value);
}

function splitSkillId(id) {
  const [scope, first, second, extra] = String(id).split(':');
  if (scope === 'shared' && validName(first) && second === undefined) return ['shared', first];
  if (scope === 'project' && projectPattern.test(first) && validName(second) && extra === undefined) return [first, second];
  throw fail('无效的专家编号');
}

async function optional(fn) {
  try { return await fn(); }
  catch (error) { if (error.code === 'ENOENT') return undefined; throw error; }
}

async function atomicWrite(path, text) {
  const temp = path + '.' + randomUUID();
  try {
    await writeFile(temp, text, { flag: 'wx', mode: 0o600 });
    await rename(temp, path);
  } finally { await rm(temp, { force: true }); }
}

function documentsFor(content, references, readBudget = false) {
  if (typeof content !== 'string' || Buffer.byteLength(content) > (readBudget ? 256 : 128) * 1024 ||
      !Array.isArray(references) || references.length > 20) throw fail('专家文档超过保存范围');
  const names = new Set();
  let bytes = 0;
  const docs = [{path: expertMainDocument, text: content}];
  for (const ref of references) {
    const validFile = typeof ref?.name === 'string' && (readBudget
      ? ref.name.length <= 255 && /^[^./\\\u0000-\u001f][^/\\\u0000-\u001f]*\.md$/.test(ref.name)
      : referencePattern.test(ref.name));
    if (!validFile || names.has(ref.name) || typeof ref.content !== 'string')
      throw fail('参考文件必须是名称唯一的 Markdown 文件');
    names.add(ref.name);
    bytes += Buffer.byteLength(ref.content);
    if (bytes > (readBudget ? 256 : 48) * 1024) throw fail(readBudget ? '参考文件超过预览范围' : '参考文件总大小超过 48 KiB');
    docs.push({path: 'references/' + ref.name, text: ref.content});
  }
  return docs;
}

export function expertDocumentAction(input) {
  const docs = documentsFor(input.content, input.references, true);
  if (input.action === 'preview') {
    const result = vaultView(docs, input.path, {includeBacklinks: false, expert: true}).document;
    if (!result) throw fail('文档不存在', 404);
    return {html: result.html};
  }
  if (input.action !== 'remove' || typeof input.path !== 'string') throw fail('无效的文档操作');
  const result = removeExpertReference(docs, input.path, input.confirmed === true);
  if (!result) throw fail('只能删除已存在的辅助文档');
  if (!result.documents) return {usedBy: result.usedBy};
  const [main, ...refs] = result.documents;
  return {usedBy: result.usedBy, content: main.text, references: refs.map(item => ({name: item.path.slice(11), content: item.text}))};
}

function validateBody(input, previous, verifyLinks = false) {
  const displayName = input.displayName ?? previous?.displayName ?? input.name;
  const description = input.description ?? previous?.description ?? '';
  const content = input.content ?? previous?.content ?? '';
  const references = input.references ?? previous?.references ?? [];
  if (typeof displayName !== 'string' || !displayName.trim() || displayName.length > 120 ||
      typeof description !== 'string' || !description.trim() || description.length > 1024 ||
      typeof content !== 'string' || !content.trim()) throw fail('请输入有效的专家名称、用途和方法正文');
  const docs = documentsFor(content, references);
  if (verifyLinks) {
    const present = new Set(docs.map(item => item.path));
    const known = [...new Set([...present, ...(previous?.references || []).map(ref => 'references/' + ref.name)])];
    for (const item of docs) {
      const missing = expertDocumentLinks(item.text, item.path, known).find(link => !present.has(link.target));
      if (missing) throw fail('方法引用了缺失的参考文件：' + missing.target.slice(11));
    }
  }
  return { displayName: displayName.trim(), description: description.trim(), content: content.trim(),
    invocation: { modelInvocable: previous?.invocation?.modelInvocable !== false, userInvocable: previous?.invocation?.userInvocable !== false },
    references: references.map(({name, content}) => ({name, content})).sort((a,b) => a.name < b.name ? -1 : a.name === b.name ? 0 : 1) };
}

// The package builder already appends linked files for the filesystem-free
// runtime. Strip that exact generated suffix before editing or pinning.
export function unpackExpertReferences(content, references = []) {
  const marker = '\n\n---\n\n## 随专家加载的参考：';
  const start = content.indexOf(marker);
  if (start < 0) return content;
  const body = content.slice(0, start);
  const names = [...new Set([...body.matchAll(/\]\(references\/([^\s)]+\.md)\)/g)].map(match => match[1]))];
  const files = new Map(references.map(ref => [ref.name, ref.content]));
  if (!names.length || names.some(name => !files.has(name))) return content;
  const suffix = names.map(name => marker + 'references/' + name + '\n\n' + files.get(name)).join('');
  return content.slice(start).trimEnd() === suffix.trimEnd() ? body.trimEnd() : content;
}

// Local copies have one source and an atomic publication pointer. Runtime
// snapshots retain the exact text already offered to a turn.
export function expertLibrary({ root, catalog, personalPath, readRule, saveRule, call, projects }) {
  root = resolve(root);
  const managedRoot = join(root, 'workbench-experts');
  let pending = Promise.resolve();
  const serial = operation => {
    const result = pending.then(operation);
    pending = result.catch(() => {});
    return result;
  };
  const scopeOf = row => row.source === 'project-dsh' || row.source === 'project-agents';
  const displayName = row => row.metadata?.displayName ||
    (scopeOf(row) && /^[\u3400-\u9fff]/.test(row.description) ? row.description.split(/[。；，]/)[0].slice(0, 26) : row.name);

  async function localFile(directory, name, maxBytes) {
    const path = join(directory, name);
    const info = await lstat(path);
    if (!info.isFile() || info.size > maxBytes) throw fail('专家文件无效', 503);
    return readFile(path, 'utf8');
  }

  async function safeDirectory(path) {
    const info = await lstat(path);
    if (!info.isDirectory()) throw fail('专家目录无效', 403);
    const boundary = await realpath(root);
    const expected = join(boundary, path.slice(root.length + 1));
    if (await realpath(path) !== expected || !expected.startsWith(boundary + sep)) throw fail('专家目录无效', 403);
    return path;
  }

  async function managed(id) {
    const [scope, name] = splitSkillId(id);
    const base = join(managedRoot, scope, name);
    if (!await optional(() => safeDirectory(base))) return undefined;
    const revision = (await optional(() => localFile(base, 'current', 128)))?.trim();
    if (revision === undefined) return undefined;
    if (!/^[a-f0-9]{64}$/.test(revision)) throw fail('专家版本指针无效', 503);
    return readRevision(id, base, scope, name, revision);
  }

  async function readRevision(id, base, scope, name, revision) {
    const directory = await safeDirectory(join(base, revision));
    const raw = await localFile(directory, 'SKILL.md', 132 * 1024);
    const skill = /^---\n([^\n]+)\n---\n([\s\S]+)$/.exec(raw);
    if (!skill) throw fail('专家文件无法读取', 503);
    const header = JSON.parse(skill[1]);
    if (header.name !== name) throw fail('专家文件名称不一致', 503);
    const refDir = await optional(() => safeDirectory(join(directory, 'references')));
    const names = refDir ? (await readdir(refDir)).sort() : [];
    if (names.length > 20 || names.some(file => !referencePattern.test(file))) throw fail('参考文件目录无效', 503);
    const references = await Promise.all(names.map(async name => ({name, content: await localFile(refDir, name, 48 * 1024)})));
    const doc = validateBody({ displayName: header.metadata?.displayName, description: header.description, content: skill[2], references }, {
      invocation: { modelInvocable: !header['disable-model-invocation'], userInvocable: header['user-invocable'] !== false },
    });
    if (hash(JSON.stringify(doc)) !== revision) throw fail('专家版本摘要不一致', 503);
    return { ...doc, id, name, revision, source: 'managed', projectId: scope === 'shared' ? '' : scope, editable: true };
  }

  async function managedRows(scope) {
    const dir = await optional(() => safeDirectory(join(managedRoot, scope)));
    const names = dir ? await readdir(dir) : [];
    return (await Promise.all(names.filter(validName).map(name => managed(skillId(scope === 'shared' ? '' : scope, name))))).filter(Boolean);
  }

  async function readDraft(id) {
    const [scope, name] = splitSkillId(id);
    const base = join(managedRoot, scope, name);
    if (!await optional(() => safeDirectory(base))) return undefined;
    if (await optional(() => lstat(join(base, 'current')))) return undefined;
    const text = await optional(() => localFile(base, 'draft.json', 1024 * 1024));
    if (text === undefined) return undefined;
    const stored = JSON.parse(text);
    const doc = validateBody(stored);
    const revision = hash(JSON.stringify(doc));
    if (stored.revision !== revision) throw fail('专家草稿摘要不一致', 503);
    return {...doc, id, name, revision, source: 'managed', projectId: scope === 'shared' ? '' : scope,
      editable: true, draft: true, usage: []};
  }

  async function draftRows(projectId) {
    const scope = projectId || 'shared';
    const dir = await optional(() => safeDirectory(join(managedRoot, scope)));
    const names = dir ? await readdir(dir) : [];
    return (await Promise.all(names.filter(validName).map(name => readDraft(skillId(projectId, name))))).filter(Boolean);
  }

  async function makeBase(scope, name) {
    const base = join(managedRoot, scope, name);
    for (const path of [managedRoot, join(managedRoot, scope), base]) {
      try { await mkdir(path, { mode: 0o700 }); }
      catch (error) { if (error.code !== 'EEXIST') throw error; }
      await safeDirectory(path);
    }
    return base;
  }

  async function storeDraft(id, doc) {
    const base = await makeBase(...splitSkillId(id));
    await atomicWrite(join(base, 'draft.json'), JSON.stringify({...doc, revision: hash(JSON.stringify(doc))}));
    return readDraft(id);
  }

  function summary(row) {
    const { id, name, displayName, description, source, projectId, editable, revision, usage, invocation, draft } = row;
    return { id, name, displayName, description, source, projectId, editable, revision, usage, invocation, draft };
  }

  async function listSkills(workspace, projectId = '') {
    const result = new Map();
    const [shared, local] = await Promise.all([catalog(workspace, undefined, 'shared'), projectId ? catalog(workspace, undefined, 'project') : []]);
    for (const row of [...shared, ...local]) {
      const scoped = scopeOf(row);
      if (scoped && !projectId) continue;
      const id = skillId(scoped ? projectId : '', row.name);
      result.set(id, { ...row, id, displayName: displayName(row), projectId: scoped ? projectId : '', editable: scoped || row.editable });
    }
    for (const row of await managedRows('shared')) result.set(row.id, summary(row));
    if (projectId) for (const row of await managedRows(projectId)) result.set(row.id, summary(row));
    return [...result.values()];
  }

  async function resolveLegacy(id, project) {
    const name = id.slice('legacy:'.length);
    if (!validName(name)) return '';
    // Historic slugs resolve against the original filesystem discovery.
    // A newly created managed Expert cannot shadow a historic binding.
    const row = (await catalog(project.workspace)).find(item => item.name === name);
    return row ? skillId(scopeOf(row) ? project.id : '', name) : '';
  }

  async function usage(id) {
    const [entries, workspaces] = await Promise.all([call('/experts/usage'), projects()]);
    const names = [];
    for (const entry of entries) {
      const project = workspaces.find(item => item.id === entry.projectId);
      if (!project) continue;
      const matched = entry.expertId === id || entry.expertId.startsWith('legacy:') &&
        entry.expertId.slice('legacy:'.length) === id.split(':').at(-1) &&
        await resolveLegacy(entry.expertId, project) === id;
      if (!matched) continue;
      if (entry.sessions) names.push(project.name + ' · 普通会话 ' + entry.sessions);
      if (entry.customers) names.push(project.name + ' · 客户会话 ' + entry.customers);
      if (entry.defaultKind) names.push(project.name + (entry.defaultKind === 'reply' ? ' · 新客户默认' : ' · 项目默认'));
    }
    return names;
  }

  async function runtime(id, project) {
    if (id === generalExpert.id || id === '') return undefined;
    if (id.startsWith('legacy:')) {
      id = await resolveLegacy(id, project);
      if (!id) throw fail('历史专家已不可用', 409);
    }
    const [scope, name] = splitSkillId(id);
    if (scope !== 'shared' && scope !== project.id) throw fail('该专家不属于当前项目', 403);
    const saved = await managed(id);
    if (saved) return saved;
    const skill = await catalog(project.workspace, name, scope === 'shared' ? 'shared' : 'project');
    if (scopeOf(skill) !== (scope !== 'shared')) throw fail('原专家来源不可用', 409);
    const revision = hash(JSON.stringify([skill.content, skill.description, skill.references, skill.invocation, skill.metadata]));
    const content = unpackExpertReferences(skill.content, skill.references);
    return { ...skill, content, id, revision, projectId: scope === 'shared' ? '' : scope,
      displayName: displayName(skill), editable: scope !== 'shared' || !!skill.revision };
  }

  async function opsCatalog() {
    return call('/operations/experts/catalog');
  }

  async function list() {
    const rows = new Map();
    const unavailable = [];
    rows.set(personalExpertId, { id: personalExpertId, name: 'superagent', displayName: 'Super Agent', description: '本人主会话的工作方法', source: 'personal', editable: true });
    const workspaces = [{workspace: personalPath, id: '', name: '工作台共享'}, ...await projects()];
    const catalogues = await Promise.allSettled([
      ...workspaces.map(async project => [...await listSkills(project.workspace, project.id), ...await draftRows(project.id)]),
      opsCatalog(),
    ]);
    for (const [index, project] of workspaces.entries()) {
      const result = catalogues[index];
      if (result.status === 'rejected') { unavailable.push(project.name); continue; }
      for (const row of result.value) rows.set(row.id, row);
    }
    const operations = catalogues.at(-1);
    if (operations.status === 'fulfilled') {
      for (const row of operations.value.items) rows.set(row.id, row);
      for (const id of operations.value.unavailable || [])
        unavailable.push(workspaces.find(project => project.id === id)?.name || '自动化运营');
    } else {
      unavailable.push('自动化运营');
    }
    return {items: [...rows.values()].map(summary).sort((a, b) => a.displayName.localeCompare(b.displayName, 'zh-CN')),
      unavailable: [...new Set(unavailable)]};
  }

  async function detail(id) {
    if (id === personalExpertId) {
      const rules = await readRule('AGENT.md');
      return { ...rules, id, name: 'superagent', displayName: 'Super Agent', description: '本人主会话的工作方法', source: 'personal', editable: true, references: [], usage: ['本人 Super Agent 主会话'] };
    }
    if (id.startsWith('operations:')) {
      const row = (await opsCatalog()).items.find(item => item.id === id);
      if (!row) throw fail('运营专家不存在', 404);
      return row;
    }
    const [scope] = splitSkillId(id);
    const project = scope === 'shared' ? { id: '', workspace: personalPath } : (await projects()).find(item => item.id === scope);
    if (!project) throw fail('所属项目不存在', 404);
    const draft = await readDraft(id);
    if (draft) return draft;
    const method = await runtime(id, project);
    return {...method, usage: await usage(id)};
  }

  async function save(input) {
    return serial(async () => {
      if (input.action === 'create-draft') {
        if (typeof input.createKey !== 'string' || !/^[a-f0-9]{32}$/.test(input.createKey)) throw fail('无效的创建编号');
        const projectId = input.projectId || '';
        const project = projectId ? (await projects()).find(item => item.id === projectId) : {id: '', workspace: personalPath};
        if (!project) throw fail('所属项目不存在', 404);
        const name = 'expert-' + input.createKey;
        const id = skillId(projectId, name);
        const doc = validateBody({displayName: input.displayName, description: input.description, content: draftTemplate});
        const current = await readDraft(id);
        if (current) {
          if (current.displayName !== doc.displayName || current.description !== doc.description) throw fail('专家创建编号已使用', 409);
          return current;
        }
        if ((await listSkills(project.workspace, projectId)).some(row => row.id === id)) throw fail('专家创建编号已使用', 409);
        return storeDraft(id, doc);
      }
      if (input.id === personalExpertId && input.action !== 'copy') {
        if (input.action !== 'update' || typeof input.content !== 'string' || !input.content.trim() || input.references?.length) throw fail('本人主文档只能编辑');
        await saveRule({ name: 'AGENT.md', content: input.content, revision: input.revision });
        return detail(input.id);
      }
      if (input.id?.startsWith('operations:')) {
        const {id, action, revision, content, sample, strategy} = input;
        return call('/operations/experts/save', undefined, {id, action, revision, content, sample, strategy}, AbortSignal.timeout(action === 'trial' ? 120000 : 30000));
      }
      const source = input.action === 'copy' ? await detail(input.id) : undefined;
      if (source?.draft) throw fail('草稿启用后才能复制专家', 409);
      if (source && input.revision !== undefined && input.revision !== source.revision) throw fail('方法已更新，请重新打开后复制', 409);
      let id = input.id;
      let previous;
      if (input.action === 'create' || input.action === 'copy') {
        if (!validName(input.name)) throw fail('专家标识使用小写字母、数字和连字符');
        const scope = input.projectId || '';
        if (source?.source === 'personal') throw fail('本人方法请使用原主会话配置');
        if (source?.projectId && source.projectId !== scope) throw fail('项目方法只能在所属项目内复制', 403);
        const project = scope ? (await projects()).find(item => item.id === scope) : { id: '', workspace: personalPath };
        if (!project) throw fail('所属项目不存在', 404);
        id = skillId(scope, input.name);
        const existing = await managed(id);
        if (source && input.revision !== undefined && existing &&
            hash(JSON.stringify(validateBody({...input, name: input.name}, source, true))) === existing.revision) return existing;
        if (existing || await readDraft(id) || (await listSkills(project.workspace, scope)).some(row => row.id === id)) throw fail('已存在同名专家', 409);
        previous = source;
      } else {
        previous = await detail(id);
        if (!previous.editable) throw fail('随发行包提供的方法可以复制', 403);
        if (input.revision !== previous.revision) throw fail('方法已更新，请重新打开后编辑', 409);
        if (previous.draft) {
          if (input.action === 'save-draft') return storeDraft(id, validateBody(input, previous, true));
          if (input.action !== 'publish-draft') throw fail('请先启用该专家', 409);
        } else if (input.action !== 'update') throw fail('无效的专家保存操作');
      }
      const [scope, name] = splitSkillId(id);
      const doc = validateBody({ ...input, name }, previous, true);
      if (previous?.draft && doc.content === draftTemplate) throw fail('请填写方法正文后再启用');
      const revision = hash(JSON.stringify(doc));
      const base = await makeBase(scope, name);
      const directory = join(base, revision);
      if (!await optional(() => lstat(directory))) {
        const temp = join(base, '.' + randomUUID());
        await mkdir(temp, { mode: 0o700 });
        try {
          const header = { name, description: doc.description, metadata: {displayName: doc.displayName},
            'disable-model-invocation': !doc.invocation.modelInvocable, 'user-invocable': doc.invocation.userInvocable };
          await writeFile(join(temp, 'SKILL.md'), `---\n${JSON.stringify(header)}\n---\n${doc.content}\n`, { mode: 0o600 });
          if (doc.references.length) {
            const dir = join(temp, 'references');
            await mkdir(dir, { mode: 0o700 });
            for (const item of doc.references) await writeFile(join(dir, item.name), item.content, { mode: 0o600 });
          }
          await rename(temp, directory);
        } finally { await rm(temp, { recursive: true, force: true }); }
      }
      const prepared = await readRevision(id, base, scope, name, revision);
      await atomicWrite(join(base, 'current'), revision + '\n');
      if (previous?.draft) await rm(join(base, 'draft.json'), {force: true}).catch(() => {});
      return prepared;
    });
  }

  return { list, detail, save, listSkills, runtime, resolveLegacy };
}

export function selectProjectExpert(options, configured, requested) {
  const allowed = [generalExpert, ...options];
  const selected = requested || configured;
  if (selected) {
    if (!allowed.some(item => item.id === selected)) throw fail('该项目无法使用此专家', 403);
    return selected;
  }
  if (options.length === 1) return options[0].id;
  if (!options.length) return generalExpert.id;
  throw fail('请选择专家或设置项目默认专家', 409, 'project_expert_required');
}
