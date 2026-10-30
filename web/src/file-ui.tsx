import React, { useEffect, useRef, useState } from "react";
import CodeMirror from "@uiw/react-codemirror";
import { markdown } from "@codemirror/lang-markdown";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Folder, FileText, ChevronDown, ChevronRight } from "lucide-react";
import type { FileEntry } from "./api";

export function RichText({
  text,
  onFile,
}: {
  text: string;
  onFile?: (p: string) => void;
}) {
  const transformed = text.replace(
    /!?\[\[([^\]|]+)(?:\|([^\]]+))?\]\]/g,
    (_, path, label) => {
      const [file, anchor] = path.split("#");
      const target = /\.[^/]+$/.test(file) ? file : file + ".md";
      return `[${label || path}](project:${encodeURIComponent(target + (anchor ? "#" + anchor : ""))})`;
    },
  );
  return (
    <div className="markdown">
      <Markdown
        remarkPlugins={[remarkGfm]}
        urlTransform={(url) =>
          url.startsWith("project:")
            ? url
            : /^(https?:|mailto:|\/|#)/.test(url)
              ? url
              : url.includes(":")
                ? ""
                : url
        }
        components={{
          a: ({ href, children }) => {
            const local = href?.startsWith("project:")
              ? decodeURIComponent(href.slice(8))
              : href && !/^https?:|mailto:|#/.test(href)
                ? href.replace(/^\/workspace\//, "")
                : "";
            return local && onFile ? (
              <button className="file-link" onClick={() => onFile(local)}>
                {children}
              </button>
            ) : (
              <a href={href} target="_blank" rel="noreferrer">
                {children}
              </a>
            );
          },
        }}
      >
        {transformed}
      </Markdown>
    </div>
  );
}

export type FileDocument = {
  path: string;
  content: string;
  hash: string;
  binary: boolean;
  tooLarge?: boolean;
  size?: number;
  editable?: boolean;
};
export function FileContent({
  file,
  preview,
  onChange,
  url,
  onFile,
  readOnly = false,
  onAsk,
}: {
  file: FileDocument;
  preview: boolean;
  onChange: (value: string) => void;
  url: string;
  readOnly?: boolean;
  onAsk?: () => void;
  onFile?: (path: string) => void;
}) {
  if (!preview && !file.binary && !file.tooLarge)
    return (
      <CodeMirror
        value={file.content}
        editable={!readOnly}
        readOnly={readOnly}
        onChange={onChange}
        extensions={/\.(md|markdown)$/i.test(file.path) ? [markdown()] : []}
        height="100%"
        basicSetup={{ lineNumbers: true, foldGutter: true }}
      />
    );
  return (
    <div className="document-preview">
      {/\.(png|jpe?g|gif|webp|avif)$/i.test(file.path) ? (
        <img alt={file.path} src={url} />
      ) : /\.pdf$/i.test(file.path) ? (
        <iframe title="PDF 预览" src={url} />
      ) : /\.(mp4|webm)$/i.test(file.path) ? (
        <video controls src={url} />
      ) : file.tooLarge ? (
        <div className="preview-message">
          文件已完整保存。超过 2MiB 的文件请下载查看，也可以让项目助手按需读取。
        </div>
      ) : file.binary ? (
        <div className="preview-message">
          原文件已保存。此格式暂不支持在线编辑，请下载查看。
        </div>
      ) : /\.(md|markdown)$/i.test(file.path) ? (
        <RichText text={file.content} onFile={onFile} />
      ) : (
        <pre>{file.content}</pre>
      )}
      {(file.tooLarge || file.binary) && onAsk && (
        <button className="button" onClick={onAsk}>
          让助手处理
        </button>
      )}
    </div>
  );
}
export function FileTree({
  files,
  selected,
  selectedDir,
  onFile,
  onDirectory,
  disabled = false,
}: {
  files: FileEntry[];
  selected: string;
  selectedDir: string;
  onFile: (path: string) => void;
  onDirectory: (path: string) => void;
  disabled?: boolean;
}) {
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  return (
    <div className="file-tree" role="tree" aria-label="文件目录">
      <button
        disabled={disabled}
        className={`tree-root ${selectedDir === "" ? "selected" : ""}`}
        onClick={() => onDirectory("")}
      >
        <Folder size={14} />
        根目录
      </button>
      {files
        .filter((f) => ![...collapsed].some((p) => f.path.startsWith(p + "/")))
        .map((f) => (
          <div
            role="treeitem"
            key={f.path}
            aria-expanded={f.directory ? !collapsed.has(f.path) : undefined}
            className="tree-row"
            style={{ paddingLeft: 8 + (f.path.split("/").length - 1) * 14 }}
          >
            {f.directory ? (
              <button
                disabled={disabled}
                className="tree-toggle"
                aria-label={`${collapsed.has(f.path) ? "展开" : "收起"} ${f.path}`}
                onClick={() =>
                  setCollapsed((old) => {
                    const n = new Set(old);
                    if (n.has(f.path)) n.delete(f.path);
                    else n.add(f.path);
                    return n;
                  })
                }
              >
                {collapsed.has(f.path) ? (
                  <ChevronRight size={13} />
                ) : (
                  <ChevronDown size={13} />
                )}
              </button>
            ) : (
              <span className="tree-indent" />
            )}
            <button
              disabled={disabled}
              title={f.path}
              className={
                (f.directory ? selectedDir === f.path : selected === f.path)
                  ? "selected"
                  : ""
              }
              onClick={() =>
                f.directory ? onDirectory(f.path) : onFile(f.path)
              }
            >
              {f.directory ? <Folder size={14} /> : <FileText size={14} />}
              <span>{f.path.split("/").at(-1)}</span>
            </button>
          </div>
        ))}
    </div>
  );
}
export function useUnsavedChanges(dirty: boolean, save: () => Promise<void>) {
  const [prompt, setPrompt] = useState(false),
    [saving, setSaving] = useState(false),
    [error, setError] = useState("");
  const resolver = useRef<((ok: boolean) => void) | null>(null);
  useEffect(() => {
    if (!dirty) return;
    const fn = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", fn);
    return () => window.removeEventListener("beforeunload", fn);
  }, [dirty]);
  useEffect(() => () => resolver.current?.(false), []);
  const finish = (ok: boolean) => {
    resolver.current?.(ok);
    resolver.current = null;
    setPrompt(false);
    setError("");
  };
  const guard = () =>
    !dirty
      ? Promise.resolve(true)
      : new Promise<boolean>((resolve) => {
          resolver.current?.(false);
          resolver.current = resolve;
          setError("");
          setPrompt(true);
        });
  const dialog = prompt ? (
    <div className="modal-backdrop" role="presentation">
      <section
        className="modal unsaved-dialog"
        role="alertdialog"
        aria-modal="true"
        aria-label="未保存的修改"
      >
        <h2>保留这次修改？</h2>
        <p>当前内容尚未保存，请选择如何处理。</p>
        {error && (
          <p role="alert" className="error">
            {error}
          </p>
        )}
        <div className="dialog-actions">
          <button disabled={saving} onClick={() => finish(false)}>
            取消
          </button>
          <button disabled={saving} onClick={() => finish(true)}>
            放弃修改
          </button>
          <button
            className="primary"
            disabled={saving}
            onClick={async () => {
              setSaving(true);
              try {
                await save();
                finish(true);
              } catch (e) {
                setError((e as Error).message);
              } finally {
                setSaving(false);
              }
            }}
          >
            {saving ? "保存中…" : "保存并继续"}
          </button>
        </div>
      </section>
    </div>
  ) : null;
  return { guard, dialog };
}
