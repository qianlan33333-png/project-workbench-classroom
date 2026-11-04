import { ActionMenu } from "./action-menu";
import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ArrowLeft,
  ChevronRight,
  MoreHorizontal,
  Plus,
  Upload,
  Download,
  Copy,
  Trash2,
  Pencil,
  Save,
  Library,
} from "lucide-react";
import {
  api,
  endpoint,
  stamp,
  type Expert,
  type FileEntry,
  type Project,
} from "./api";
import {
  RichText,
  FileTree,
  FileContent,
  useUnsavedChanges,
  type FileDocument,
} from "./file-ui";

type Detail = {
  expert: Expert;
  usage: { projects: Project[]; sessions: any[]; schedules: any[] };
};
export function ExpertDetail({
  id,
  projects,
  refresh,
  notify,
  onBack,
  onOpenProject,
  onCopy,
  onGuard,
}: {
  id: string;
  projects: Project[];
  refresh: () => Promise<void>;
  notify: (message: string) => void;
  onBack: () => void;
  onOpenProject: (p: Project) => void;
  onCopy: (id: string) => void;
  onGuard: (guard: (() => Promise<boolean>) | null) => void;
}) {
  const [detail, setDetail] = useState<Detail | null>(null),
    [files, setFiles] = useState<FileEntry[]>([]),
    [doc, setDoc] = useState<FileDocument | null>(null),
    [saved, setSaved] = useState(""),
    [directory, setDirectory] = useState(""),
    [preview, setPreview] = useState(false),
    [mode, setMode] = useState("overview"),
    [versions, setVersions] = useState<Expert[]>([]),
    [version, setVersion] = useState(""),
    [name, setName] = useState(""),
    [description, setDescription] = useState(""),
    [busy, setBusy] = useState(true),
    [error, setError] = useState("");
  const [form, setForm] = useState<{
      kind: "create" | "rename" | "directory";
      value: string;
    } | null>(null),
    [targetProject, setTargetProject] = useState("");
  const current = useRef<Expert | null>(null);
  const fileRequest = useRef(0);
  const upload = useRef<HTMLInputElement>(null),
    zip = useRef<HTMLInputElement>(null);
  const fetchDetail = useCallback(async () => {
    const d = await api<Detail>(`/experts/${id}`);
    current.current = d.expert;
    setDetail(d);
    return d;
  }, [id]);
  const loadFiles = async (digest = "") =>
    setFiles(
      await api(`/experts/${id}/files${digest ? `?digest=${digest}` : ""}`),
    );
  const read = async (path: string, digest = "") => {
    const request = ++fileRequest.current;
    const v = await api<FileDocument>(
      `/experts/${id}/file?view=1&path=${encodeURIComponent(path)}${digest ? `&digest=${digest}` : ""}`,
    );
    if (request !== fileRequest.current) return;
    setDoc(v);
    setSaved(v.content);
    setPreview(!!v.binary || !!v.tooLarge);
  };
  useEffect(() => {
    let live = true;
    (async () => {
      try {
        const d = await api<Detail>(`/experts/${id}`);
        if (!live) return;
        current.current = d.expert;
        setDetail(d);
        setName(d.expert.name);
        setDescription(d.expert.description);
        await loadFiles();
        await read("SKILL.md");
        setVersions(await api(`/experts/${id}/versions`));
      } catch (e) {
        if (live) setError((e as Error).message);
      } finally {
        if (live) setBusy(false);
      }
    })();
    return () => {
      live = false;
      fileRequest.current++;
    };
  }, [id]);
  const dirty =
    mode === "metadata"
      ? !!detail &&
        (name !== detail.expert.name ||
          description !== detail.expert.description)
      : !!doc && doc.content !== saved;
  const save = async () => {
    if (!current.current || !dirty) return;
    if (version) throw new Error("历史版本只读，请返回当前版本编辑");
    setBusy(true);
    setError("");
    try {
      const change: any = { baseDigest: current.current.digest };
      if (mode === "metadata") change.metadata = { name, description };
      else
        change.files = [
          { op: "write", path: doc!.path, content: doc!.content },
        ];
      const e = await api<Expert>(`/experts/${id}/changes`, change);
      current.current = e;
      setDetail((old) => (old ? { ...old, expert: e } : old));
      setName(e.name);
      setDescription(e.description);
      if (doc) await read(doc.path, e.digest);
      await refresh();
      await loadFiles();
      setVersions(await api(`/experts/${id}/versions`));
      notify("专家已保存；后续任务使用新版本");
    } finally {
      setBusy(false);
    }
  };
  const { guard, dialog } = useUnsavedChanges(dirty, save);
  useEffect(() => {
    onGuard(async () => {
      if (busy) {
        notify("能力包正在保存或读取，请稍候");
        return false;
      }
      return guard();
    });
    return () => onGuard(null);
  }, [busy, dirty, doc, name, description, detail, version]);
  const resetDraft = () => {
    const e = current.current;
    if (e) {
      setName(e.name);
      setDescription(e.description);
    }
    if (doc) setDoc({ ...doc, content: saved });
  };
  const changeMode = async (next: string) => {
    if (busy) return;
    if (!(await guard())) return;
    resetDraft();
    setMode(next);
    setError("");
    if (next === "files" && doc)
      await read(doc.path, version || current.current?.digest || "");
    if (next === "usage") await fetchDetail();
    if (next === "versions") setVersions(await api(`/experts/${id}/versions`));
  };
  const openFile = async (path: string) => {
    if (busy) return;
    if (doc?.path === path && mode === "files") return;
    if (!(await guard())) return;
    resetDraft();
    setMode("files");
    await read(path, version);
  };
  const openLinkedFile = async (link: string) => {
    const target = link.split("#")[0];
    const relative: string[] = [];
    for (const part of [
      ...(doc?.path.split("/").slice(0, -1) || []),
      ...target.split("/"),
    ]) {
      if (part === "..") relative.pop();
      else if (part && part !== ".") relative.push(part);
    }
    const path = relative.join("/");
    const exact = files.find(
      (f) => !f.directory && (f.path === path || f.path === target),
    );
    const matches = files.filter(
      (f) => !f.directory && f.path.split("/").at(-1) === target,
    );
    const file = exact || (matches.length === 1 ? matches[0] : undefined);
    if (!file) throw new Error("无法唯一定位引用文件，请从文件树选择");
    await openFile(file.path);
  };
  const mutate = async (changes: any, uploads?: File[]) => {
    if (busy) return;
    if (!(await guard())) return;
    setBusy(true);
    setError("");
    try {
      resetDraft();
      let body: any = { ...changes, baseDigest: current.current!.digest };
      if (uploads) {
        const data = new FormData();
        data.append("changes", JSON.stringify(body));
        uploads.forEach((file, i) => data.append(`file${i}`, file));
        body = data;
      }
      const e = await api<Expert>(`/experts/${id}/changes`, body);
      current.current = e;
      await fetchDetail();
      setName(e.name);
      setDescription(e.description);
      await loadFiles();
      setVersion("");
      await refresh();
      setForm(null);
      const updated = await api<FileEntry[]>(`/experts/${id}/files`);
      const next =
        changes.files?.find((x: any) => x.op === "rename")?.to ||
        doc?.path ||
        "SKILL.md";
      await read(
        updated.some((f) => f.path === next && !f.directory)
          ? next
          : "SKILL.md",
      );
      setVersions(await api(`/experts/${id}/versions`));
      notify("能力包已更新");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const work = (fn: () => Promise<unknown>) => {
    void fn().catch((e) => setError(e.message));
  };
  if (!detail)
    return (
      <div className="page">
        <button onClick={onBack}>
          <ArrowLeft size={16} />
          返回专家库
        </button>
        {error ? <p role="alert">{error}</p> : <p>正在读取能力包…</p>}
      </div>
    );
  const expert = detail.expert,
    readOnly = !!version && version !== expert.digest;
  return (
    <div className={`expert-detail mode-${mode}`}>
      <header className="expert-heading">
        <div>
          <button onClick={onBack}>
            <ArrowLeft size={15} />
            专家库
          </button>
          <h1>{expert.name}</h1>
          {mode !== "overview" && <p>{expert.description}</p>}
        </div>
        <div className="expert-actions">
          <button
            className="primary"
            disabled={!dirty || busy || readOnly}
            onClick={() => work(save)}
          >
            <Save size={15} />
            保存
          </button>
          <button
            onClick={() =>
              work(() => changeMode(mode === "overview" ? "metadata" : "usage"))
            }
          >
            {mode === "overview" ? "编辑信息" : "添加到项目"}
          </button>
          <ActionMenu
            label="专家操作"
            context={id + ":" + mode + ":" + version}
            triggerContent={<MoreHorizontal size={19} />}
            triggerClassName="icon"
            panelClassName="suite-action-menu"
          >
            <button
              role="menuitem"
              tabIndex={-1}
              disabled={busy}
              onClick={() =>
                work(async () => {
                  if (!(await guard())) return;
                  const e = await api<Expert>(`/experts/${id}/copy`, {});
                  await refresh();
                  onCopy(e.id);
                })
              }
            >
              <Copy size={15} />
              复制
            </button>
            <a
              role="menuitem"
              tabIndex={-1}
              className="button"
              href={endpoint(
                `/experts/${id}/export${version ? `?digest=${version}` : ""}`,
              )}
            >
              <Download size={15} />
              导出
            </a>
            <button
              role="menuitem"
              tabIndex={-1}
              className="danger"
              disabled={busy}
              onClick={() =>
                work(async () => {
                  if (!(await guard())) return;
                  const d = await fetchDetail();
                  if (
                    !window.confirm(
                      `删除「${d.expert.name}」？将从 ${d.usage.projects.length} 个项目移除，并暂停 ${d.usage.schedules.length} 个定时任务。已经提交的任务、项目笔记和历史成果保留。`,
                    )
                  )
                    return;
                  await api(
                    `/experts/${id}`,
                    { baseDigest: d.expert.digest },
                    "DELETE",
                  );
                  await refresh();
                  onGuard(null);
                  onBack();
                })
              }
            >
              <Trash2 size={15} />
              删除
            </button>
          </ActionMenu>
        </div>
      </header>
      {error && (
        <div className="inline-error" role="alert">
          {error}
          <button onClick={() => setError("")}>关闭</button>
        </div>
      )}
      <div className="expert-workspace">
        <aside className="expert-sidebar">
          <nav className="expert-tabs">
            {[
              ["overview", "说明"],
              ["files", "文件"],
              ["metadata", "编辑信息"],
              ["usage", "使用范围"],
              ["versions", "版本"],
            ].map(([value, label]) => (
              <button
                disabled={busy}
                className={mode === value ? "selected" : ""}
                key={value}
                onClick={() => work(() => changeMode(value))}
              >
                {label}
              </button>
            ))}
          </nav>
          <div className="file-actions">
            <ActionMenu
              label="添加专家文件"
              context={`${id}:${directory}:${mode}:${version}:${busy}:${readOnly}`}
              align="start"
              triggerContent={
                <>
                  <Plus size={14} />
                  添加
                </>
              }
              triggerClassName="file-add-trigger"
              panelClassName="add-menu"
            >
              <button
                role="menuitem"
                tabIndex={-1}
                disabled={readOnly || busy}
                onClick={() => setForm({ kind: "create", value: "新方法.md" })}
              >
                新建文件
              </button>
              <button
                role="menuitem"
                tabIndex={-1}
                disabled={readOnly || busy}
                onClick={() =>
                  setForm({ kind: "directory", value: "参考资料" })
                }
              >
                新建文件夹
              </button>
              <button
                role="menuitem"
                tabIndex={-1}
                disabled={readOnly || busy}
                onClick={() => upload.current?.click()}
              >
                上传文件
              </button>
              <button
                role="menuitem"
                tabIndex={-1}
                disabled={readOnly || busy}
                onClick={() => zip.current?.click()}
              >
                用 ZIP 更新
              </button>
            </ActionMenu>
          </div>
          <input
            hidden
            ref={upload}
            disabled={busy || readOnly}
            aria-label="上传专家文件"
            type="file"
            multiple
            onChange={(e) => {
              const rows = Array.from(e.target.files || []);
              e.target.value = "";
              if (rows.length)
                void mutate(
                  {
                    files: rows.map((f, i) => ({
                      op: "write",
                      path: [directory, f.name].filter(Boolean).join("/"),
                      upload: `file${i}`,
                    })),
                  },
                  rows,
                );
            }}
          />
          <input
            hidden
            ref={zip}
            disabled={busy || readOnly}
            aria-label="更新专家 ZIP"
            type="file"
            accept=".zip,application/zip"
            onChange={(e) => {
              const file = e.target.files?.[0];
              e.target.value = "";
              if (file)
                work(async () => {
                  if (!(await guard())) return;
                  setBusy(true);
                  try {
                    const data = new FormData();
                    data.append("file", file);
                    data.append("id", id);
                    data.append("baseDigest", current.current!.digest);
                    const updated = await api<Expert>("/experts/import", data);
                    current.current = updated;
                    await fetchDetail();
                    setName(updated.name);
                    setDescription(updated.description);
                    setVersion("");
                    await loadFiles();
                    await read("SKILL.md");
                    await refresh();
                    notify("专家已更新");
                  } finally {
                    setBusy(false);
                  }
                });
            }}
          />
          <div className="upload-target">
            保存到：{directory || "能力包根目录"}
          </div>
          <FileTree
            disabled={busy}
            files={files}
            selected={doc?.path || ""}
            selectedDir={directory}
            onDirectory={setDirectory}
            onFile={(p) => work(() => openFile(p))}
          />
        </aside>
        <section className="expert-content">
          {mode === "overview" && (
            <div className="expert-overview">
              <div className="expert-overview-description">
                <strong>说明</strong>
                <RichText text={expert.description} />
              </div>
              <div className="expert-overview-row">
                <strong>文件</strong>
                <button onClick={() => work(() => changeMode("files"))}>
                  {files.filter((f) => !f.directory).length} 个文件
                  <ChevronRight size={15} />
                </button>
              </div>
              <div className="expert-overview-row">
                <strong>使用范围</strong>
                <button onClick={() => work(() => changeMode("usage"))}>
                  {detail.usage.projects.length} 个项目 ·{" "}
                  {detail.usage.schedules.length} 项定时任务
                  <ChevronRight size={15} />
                </button>
              </div>
              <div className="expert-overview-row">
                <strong>版本</strong>
                <button onClick={() => work(() => changeMode("versions"))}>
                  查看版本记录
                  <ChevronRight size={15} />
                </button>
              </div>
            </div>
          )}

          {form && (
            <form
              className="inline-create"
              onSubmit={(e) => {
                e.preventDefault();
                const target = [directory, form.value]
                  .filter(Boolean)
                  .join("/");
                void mutate({
                  files: [
                    form.kind === "rename"
                      ? { op: "rename", path: doc!.path, to: form.value }
                      : {
                          op: form.kind === "directory" ? "mkdir" : "write",
                          path: target,
                          content: "",
                        },
                  ],
                });
              }}
            >
              <label>
                {form.kind === "rename" ? "新的完整路径" : "名称或相对路径"}
                <input
                  aria-label="专家文件路径"
                  required
                  value={form.value}
                  onChange={(e) => setForm({ ...form, value: e.target.value })}
                />
              </label>
              <button type="button" onClick={() => setForm(null)}>
                取消
              </button>
              <button className="primary" disabled={busy}>
                确认
              </button>
            </form>
          )}
          {mode === "files" && doc && (
            <>
              <header className="expert-file-header">
                <strong title={doc.path}>
                  {doc.path}
                  {dirty ? " · 未保存" : ""}
                </strong>
                <div>
                  <button
                    disabled={
                      busy || readOnly || !!doc.binary || !!doc.tooLarge
                    }
                    onClick={() => setPreview(!preview)}
                  >
                    {preview ? "编辑" : "预览"}
                  </button>
                  <button
                    disabled={readOnly || doc.path === "SKILL.md" || busy}
                    onClick={() => setForm({ kind: "rename", value: doc.path })}
                  >
                    <Pencil size={14} />
                    重命名
                  </button>
                  <button
                    disabled={readOnly || doc.path === "SKILL.md" || busy}
                    onClick={() => {
                      if (
                        window.confirm(
                          `删除能力包文件 ${doc.path}？历史版本仍可查看。`,
                        )
                      )
                        void mutate({
                          files: [{ op: "delete", path: doc.path }],
                        });
                    }}
                  >
                    <Trash2 size={14} />
                    删除文件
                  </button>
                  <a
                    href={endpoint(
                      `/experts/${id}/file?raw=1&download=1&path=${encodeURIComponent(doc.path)}${version ? `&digest=${version}` : ""}`,
                    )}
                  >
                    下载
                  </a>
                </div>
              </header>
              {readOnly && (
                <div className="lock-note">
                  正在查看历史版本 {version.slice(0, 10)}，内容只读。
                  <button
                    onClick={() =>
                      work(async () => {
                        setVersion("");
                        const latest = await api<FileEntry[]>(
                          `/experts/${id}/files`,
                        );
                        setFiles(latest);
                        await read(
                          latest.some(
                            (f) => f.path === doc.path && !f.directory,
                          )
                            ? doc.path
                            : "SKILL.md",
                        );
                      })
                    }
                  >
                    返回当前版本
                  </button>
                </div>
              )}
              {readOnly ? (
                <FileContent
                  file={doc}
                  preview={true}
                  onChange={() => {}}
                  url={endpoint(
                    `/experts/${id}/file?raw=1&path=${encodeURIComponent(doc.path)}&digest=${version}`,
                  )}
                  onFile={(p) => work(() => openLinkedFile(p))}
                />
              ) : (
                <FileContent
                  file={doc}
                  readOnly={busy}
                  preview={preview}
                  onChange={(content) => setDoc({ ...doc, content })}
                  url={endpoint(
                    `/experts/${id}/file?raw=1&path=${encodeURIComponent(doc.path)}`,
                  )}
                  onFile={(p) => work(() => openLinkedFile(p))}
                />
              )}
            </>
          )}
          {mode === "metadata" && (
            <form
              className="expert-metadata"
              onSubmit={(e) => {
                e.preventDefault();
                work(save);
              }}
            >
              <h2>基本信息</h2>
              <label>
                专家名称
                <input
                  required
                  disabled={busy || readOnly}
                  aria-label="专家名称"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                />
              </label>
              <label>
                能力简介
                <textarea
                  required
                  rows={5}
                  disabled={busy || readOnly}
                  aria-label="能力简介"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                />
              </label>
              <button className="primary" disabled={!dirty || busy || readOnly}>
                保存信息
              </button>
            </form>
          )}
          {mode === "usage" && (
            <div className="expert-usage">
              <h2>使用这个专家</h2>
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  work(async () => {
                    const p = await api<Project>(`/projects/${targetProject}`);
                    await api(
                      `/projects/${p.id}`,
                      { expertIds: [...new Set([...p.expertIds, id])] },
                      "PATCH",
                    );
                    await refresh();
                    await fetchDetail();
                    setTargetProject("");
                    notify("已添加到项目，可在对话顶部选择此专家");
                  });
                }}
              >
                <select
                  aria-label="添加专家到项目"
                  required
                  value={targetProject}
                  onChange={(e) => setTargetProject(e.target.value)}
                >
                  <option value="">选择项目</option>
                  {projects
                    .filter((p) => !p.archived && !p.expertIds.includes(id))
                    .map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.name}
                      </option>
                    ))}
                </select>
                <button className="primary" disabled={!targetProject}>
                  添加到项目
                </button>
              </form>
              <h3>已启用的项目</h3>
              {detail.usage.projects.length ? (
                detail.usage.projects.map((p) => (
                  <div className="usage-row" key={p.id}>
                    <button onClick={() => onOpenProject(p)}>{p.name}</button>
                    <span>
                      {p.defaultExpertId === id ? "默认专家" : "已启用"}
                    </span>
                    <button
                      onClick={() =>
                        work(async () => {
                          const latest = await api<Project>(
                            `/projects/${p.id}`,
                          );
                          await api(
                            `/projects/${p.id}`,
                            {
                              expertIds: latest.expertIds.filter(
                                (x) => x !== id,
                              ),
                              defaultExpertId:
                                latest.defaultExpertId === id
                                  ? ""
                                  : latest.defaultExpertId,
                            },
                            "PATCH",
                          );
                          await refresh();
                          await fetchDetail();
                        })
                      }
                    >
                      从项目移除
                    </button>
                  </div>
                ))
              ) : (
                <p>尚未添加到项目。</p>
              )}
              <h3>明确绑定的定时任务</h3>
              {detail.usage.schedules.map((s) => (
                <p key={s.id}>
                  {s.name} · {s.enabled ? "启用" : "暂停"}
                </p>
              ))}
              {!detail.usage.schedules.length && <p>没有绑定的定时任务。</p>}
            </div>
          )}
          {mode === "versions" && (
            <div className="expert-versions">
              <h2>版本记录</h2>
              <p>任务使用提交时固定的版本；查看历史不会改变当前版本。</p>
              {versions.map((v) => (
                <div className="usage-row" key={v.digest}>
                  <span>
                    {v.digest.slice(0, 12)}
                    {v.digest === expert.digest ? " · 当前" : ""}
                    <small>{stamp(v.updatedAt || 0)}</small>
                  </span>
                  <button
                    onClick={() =>
                      work(async () => {
                        setVersion(v.digest === expert.digest ? "" : v.digest);
                        setMode("files");
                        await loadFiles(v.digest);
                        await read("SKILL.md", v.digest);
                      })
                    }
                  >
                    查看
                  </button>
                  <a
                    href={endpoint(`/experts/${id}/export?digest=${v.digest}`)}
                  >
                    下载
                  </a>
                </div>
              ))}
            </div>
          )}
        </section>
      </div>
      {dialog}
    </div>
  );
}
