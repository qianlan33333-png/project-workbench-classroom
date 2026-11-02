import { ActionMenu } from "./action-menu";
import React, { useRef, useState } from "react";
import { Plus, Search, History, Download } from "lucide-react";
import { api, endpoint, type FileEntry } from "./api";
import { FileTree } from "./file-ui";

type Item = {
  id: string;
  file: File;
  path: string;
  state: "pending" | "uploading" | "done" | "failed" | "conflict" | "skipped";
  error?: string;
  hash?: string;
};
type InputFile = { file: File; path: string };
export async function droppedFiles(
  data: DataTransfer,
): Promise<{ files: InputFile[]; directories: string[] }> {
  const files: InputFile[] = [],
    directories: string[] = [];
  const walk = async (entry: any, parent: string) => {
    const path = parent + entry.name;
    if (entry.isFile) {
      const file = await new Promise<File>((resolve, reject) =>
        entry.file(resolve, reject),
      );
      files.push({ file, path });
    } else if (entry.isDirectory) {
      directories.push(path);
      const reader = entry.createReader();
      for (;;) {
        const rows: any[] = await new Promise((resolve, reject) =>
          reader.readEntries(resolve, reject),
        );
        if (!rows.length) break;
        for (const child of rows) await walk(child, path + "/");
      }
    }
  };
  const entries = Array.from(data.items)
    .filter((i) => i.kind === "file")
    .map((i) => (i as any).webkitGetAsEntry?.());
  if (entries.some(Boolean)) {
    for (const e of entries) {
      if (!e) throw new Error("无法读取拖入的目录，请使用上传文件夹。");
      await walk(e, "");
    }
  } else
    for (const file of Array.from(data.files))
      files.push({ file, path: file.name });
  if (!files.length && !directories.length)
    throw new Error("无法读取拖入内容，请使用上传文件或上传文件夹。");
  return { files, directories };
}
export function ProjectFiles({
  projectId,
  files,
  selected,
  locked,
  refresh,
  openFile,
  notify,
  versions,
}: {
  projectId: string;
  files: FileEntry[];
  selected: string;
  locked: boolean;
  refresh: () => Promise<void>;
  openFile: (path: string) => Promise<void>;
  notify: (text: string) => void;
  versions: () => void;
}) {
  const [directory, setDirectory] = useState(""),
    [items, setItems] = useState<Item[]>([]),
    [query, setQuery] = useState(""),
    [results, setResults] = useState<any[] | null>(null),
    [drag, setDrag] = useState(false),
    [working, setWorking] = useState(false),
    [zip, setZip] = useState<File | null>(null),
    [zipTarget, setZipTarget] = useState(""),
    [zipError, setZipError] = useState("");
  const [creation, setCreation] = useState<"file" | "directory" | null>(null),
    [name, setName] = useState("");
  const fileInput = useRef<HTMLInputElement>(null),
    folderInput = useRef<HTMLInputElement>(null),
    zipInput = useRef<HTMLInputElement>(null);
  const upload = async (rows: Item[], policy = "", dirs: string[] = []) => {
    setWorking(true);
    try {
      for (const path of dirs)
        await api(`/projects/${projectId}/directories`, { path });
      for (const item of rows) {
        setItems((old) =>
          old.map((x) =>
            x.id === item.id ? { ...x, state: "uploading", error: "" } : x,
          ),
        );
        try {
          const form = new FormData();
          form.append("file", item.file);
          form.append("path", item.path);
          form.append("baseHash", policy === "replace" ? item.hash || "" : "");
          if (policy) form.append("conflict", policy);
          const v = await api(`/projects/${projectId}/upload`, form);
          setItems((old) =>
            old.map((x) =>
              x.id === item.id
                ? { ...x, path: v.path, state: v.skipped ? "skipped" : "done" }
                : x,
            ),
          );
        } catch (e) {
          let hash = "";
          const conflict = (e as any).status === 409;
          if (conflict) {
            try {
              hash = (
                await api(
                  `/projects/${projectId}/file?view=1&path=${encodeURIComponent(item.path)}`,
                )
              ).hash;
            } catch {}
          }
          setItems((old) =>
            old.map((x) =>
              x.id === item.id
                ? {
                    ...x,
                    state: conflict ? "conflict" : "failed",
                    error: (e as Error).message,
                    hash,
                  }
                : x,
            ),
          );
          if ((e as any).status === 423) break;
        }
      }
      await refresh();
    } catch (e) {
      notify((e as Error).message);
      setItems((old) =>
        old.map((x) =>
          rows.some((r) => r.id === x.id) && x.state === "pending"
            ? { ...x, state: "failed", error: (e as Error).message }
            : x,
        ),
      );
    } finally {
      setWorking(false);
    }
  };
  const enqueue = (incoming: InputFile[], dirs: string[] = []) => {
    const prefix = directory ? directory + "/" : "";
    const rows: Item[] = incoming.map((f) => ({
      id: crypto.randomUUID(),
      file: f.file,
      path: prefix + f.path,
      state: "pending",
    }));
    setItems((old) => [...old, ...rows]);
    void upload(
      rows,
      "",
      dirs.map((d) => prefix + d),
    );
  };
  const pick = (list: FileList | null) => {
    if (list)
      enqueue(
        Array.from(list).map((file) => ({
          file,
          path: file.webkitRelativePath || file.name,
        })),
      );
  };
  return (
    <div
      className={`project-files ${drag ? "dragging" : ""}`}
      onDragOver={(e) => {
        e.preventDefault();
        if (!locked && !working) setDrag(true);
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node)) setDrag(false);
      }}
      onDrop={async (e) => {
        e.preventDefault();
        setDrag(false);
        if (locked || working) {
          notify("任务正在写入或上传尚未结束，请稍后再添加文件。");
          return;
        }
        try {
          const data = await droppedFiles(e.dataTransfer);
          enqueue(data.files, data.directories);
        } catch (err) {
          notify((err as Error).message);
        }
      }}
    >
      <form
        className="file-search"
        onSubmit={async (e) => {
          e.preventDefault();
          try {
            setResults(
              await api(
                `/projects/${projectId}/search?q=${encodeURIComponent(query)}`,
              ),
            );
          } catch (err) {
            notify((err as Error).message);
          }
        }}
      >
        <Search size={14} />
        <input
          aria-label="搜索项目资料"
          placeholder="搜索资料，回车查全文…"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setResults(null);
          }}
        />
      </form>
      <div className="file-actions">
        <ActionMenu
          label="添加项目文件"
          context={`${projectId}:${directory}:${locked}:${working}`}
          align="start"
          triggerContent={
            <>
              <Plus size={15} />
              添加
            </>
          }
          triggerClassName="file-add-trigger"
          panelClassName="add-menu"
        >
          <button
            role="menuitem"
            tabIndex={-1}
            disabled={locked || working}
            onClick={() => fileInput.current?.click()}
          >
            上传文件
          </button>
          <button
            role="menuitem"
            tabIndex={-1}
            disabled={locked || working}
            onClick={() => folderInput.current?.click()}
          >
            上传文件夹
          </button>
          <button
            role="menuitem"
            tabIndex={-1}
            disabled={locked || working}
            onClick={() => zipInput.current?.click()}
          >
            导入 ZIP
          </button>
          <button
            role="menuitem"
            tabIndex={-1}
            disabled={locked || working}
            onClick={() => {
              setCreation("file");
              setName("新文档.md");
            }}
          >
            新建文件
          </button>
          <button
            role="menuitem"
            tabIndex={-1}
            disabled={locked || working}
            onClick={() => {
              setCreation("directory");
              setName("新文件夹");
            }}
          >
            新建文件夹
          </button>
        </ActionMenu>
        <button
          aria-label="项目文件历史"
          title="项目文件历史"
          onClick={versions}
        >
          <History size={15} />
        </button>
        <a
          title="导出项目"
          aria-label="导出项目"
          href={endpoint(`/projects/${projectId}/export`)}
        >
          <Download size={15} />
        </a>
      </div>
      <input
        hidden
        ref={fileInput}
        aria-label="上传项目文件"
        type="file"
        multiple
        disabled={locked || working}
        onChange={(e) => {
          pick(e.target.files);
          e.target.value = "";
        }}
      />
      <input
        hidden
        ref={folderInput}
        aria-label="上传项目文件夹"
        type="file"
        multiple
        {...({ webkitdirectory: "" } as any)}
        disabled={locked || working}
        onChange={(e) => {
          pick(e.target.files);
          e.target.value = "";
        }}
      />
      <input
        hidden
        ref={zipInput}
        aria-label="导入项目 ZIP"
        type="file"
        accept=".zip,application/zip"
        disabled={locked || working}
        onChange={(e) => {
          setZip(e.target.files?.[0] || null);
          setZipTarget(directory);
          setZipError("");
          e.target.value = "";
        }}
      />
      <div className="upload-target" title={directory || "项目根目录"}>
        保存到：{directory || "项目根目录"}
      </div>
      {locked && (
        <p className="lock-note">任务正在写入，结束或停止后可上传。</p>
      )}
      {creation && (
        <form
          className="inline-create"
          onSubmit={async (e) => {
            e.preventDefault();
            try {
              const path = [directory, name].filter(Boolean).join("/");
              if (creation === "directory") {
                await api(`/projects/${projectId}/directories`, { path });
                setDirectory(path);
              } else {
                await api(
                  `/projects/${projectId}/file`,
                  { path, content: "", baseHash: "" },
                  "PUT",
                );
                await openFile(path);
              }
              setCreation(null);
              await refresh();
            } catch (err) {
              notify((err as Error).message);
            }
          }}
        >
          <label>
            {creation === "file" ? "新文件路径" : "新文件夹路径"}
            <input
              aria-label="新建路径"
              required
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          <button type="button" onClick={() => setCreation(null)}>
            取消
          </button>
          <button className="primary" disabled={locked}>
            创建
          </button>
        </form>
      )}
      {zip && (
        <form
          className="zip-import"
          onSubmit={async (e) => {
            e.preventDefault();
            setWorking(true);
            setZipError("");
            try {
              const form = new FormData();
              form.append("file", zip);
              form.append("targetDir", zipTarget);
              await api(`/projects/${projectId}/import`, form);
              await refresh();
              setZip(null);
              notify("ZIP 已导入，原有目录结构已保留");
            } catch (err) {
              setZipError((err as Error).message);
            } finally {
              setWorking(false);
            }
          }}
        >
          <strong>{zip.name}</strong>
          <label>
            解压到目录
            <input
              aria-label="ZIP 目标目录"
              placeholder="留空表示项目根目录"
              value={zipTarget}
              onChange={(e) => setZipTarget(e.target.value)}
            />
          </label>
          {zipError && <p role="alert">{zipError}。可修改目标目录后重试。</p>}
          <button type="button" disabled={working} onClick={() => setZip(null)}>
            取消
          </button>
          <button disabled={locked || working}>解压导入</button>
        </form>
      )}
      {items.length > 0 && (
        <details
          className="upload-results"
          open={items.some((x) => x.state !== "done" && x.state !== "skipped")}
        >
          <summary>
            上传结果 · {items.filter((x) => x.state === "done").length}/
            {items.length}
            <button
              disabled={working}
              onClick={(e) => {
                e.preventDefault();
                setItems((old) =>
                  old.filter(
                    (x) => x.state !== "done" && x.state !== "skipped",
                  ),
                );
              }}
            >
              清理
            </button>
          </summary>
          {items.map((item) => (
            <div key={item.id} className={`upload-item ${item.state}`}>
              <span title={item.path}>{item.path}</span>
              <small>
                {
                  {
                    pending: "等待上传",
                    uploading: "上传中",
                    done: "已保存",
                    skipped: "已跳过",
                    failed: "失败",
                    conflict: "同名文件",
                  }[item.state]
                }
              </small>
              {item.error && <p>{item.error}</p>}
              {item.state === "conflict" && (
                <div>
                  {[
                    ["skip", "跳过"],
                    ["rename", "保留两份"],
                    ["replace", "替换"],
                  ].map(([policy, label]) => (
                    <button
                      key={policy}
                      disabled={locked || working}
                      onClick={() => upload([item], policy)}
                    >
                      {label}
                    </button>
                  ))}
                </div>
              )}
            </div>
          ))}
          {items.some((x) => x.state === "failed" || x.state === "pending") && (
            <button
              disabled={locked || working}
              onClick={() =>
                upload(
                  items.filter(
                    (x) => x.state === "failed" || x.state === "pending",
                  ),
                )
              }
            >
              重试未完成项
            </button>
          )}
        </details>
      )}
      {results ? (
        <div className="file-tree">
          {results.map((r) => (
            <button
              className="search-hit"
              key={r.path}
              onClick={() => openFile(r.path).catch((e) => notify(e.message))}
            >
              <strong>{r.path}</strong>
              <small>{r.snippet}</small>
            </button>
          ))}
          {!results.length && <p className="panel-empty">没有找到匹配内容。</p>}
        </div>
      ) : (
        <FileTree
          files={files.filter((f) =>
            f.path.toLowerCase().includes(query.toLowerCase()),
          )}
          selected={selected}
          selectedDir={directory}
          onFile={(p) => openFile(p).catch((e) => notify(e.message))}
          onDirectory={setDirectory}
        />
      )}
      {drag && (
        <div className="drop-hint">
          拖入文件或文件夹，保存到 {directory || "根目录"}
        </div>
      )}
    </div>
  );
}
