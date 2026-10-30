import { useCallback, useEffect, useRef, useState } from "react";
import { Terminal, Library, Plug, X } from "lucide-react";
import { api, type Capability } from "./api";
const kinds = { cli: "CLI", skill: "Skill", mcp: "MCP" };
const icons = { cli: Terminal, skill: Library, mcp: Plug };
export function CapabilityBadges({
  values,
  remove,
}: {
  values: Capability[];
  remove?: (c: Capability) => void;
}) {
  if (!values.length) return null;
  return (
    <div className="capability-chips">
      {values.map((c) =>
        remove ? (
          <button
            key={c.kind + ":" + c.id}
            type="button"
            aria-label={`移除 ${kinds[c.kind]} ${c.name}`}
            onClick={() => remove(c)}
          >
            <span>{kinds[c.kind]}</span>
            {c.name}
            <X size={12} />
          </button>
        ) : (
          <small key={c.kind + ":" + c.id}>
            {kinds[c.kind]} · {c.name}
          </small>
        ),
      )}
    </div>
  );
}
export function CapabilityComposer({
  projectId,
  sessionId,
  value,
  onChange,
  selected,
  onSelected,
  send,
}: {
  projectId: string;
  sessionId: string;
  value: string;
  onChange: (s: string) => void;
  selected: Capability[];
  onSelected: (c: Capability[]) => void;
  send: () => void;
}) {
  const input = useRef<HTMLTextAreaElement>(null);
  const [catalog, setCatalog] = useState<Capability[]>([]),
    [caret, setCaret] = useState(value.length),
    [focused, setFocused] = useState(false),
    [dismissed, setDismissed] = useState(false),
    [active, setActive] = useState(0),
    [error, setError] = useState(""),
    [loading, setLoading] = useState(false);
  const latestProject = useRef(projectId);
  latestProject.current = projectId;
  const load = useCallback(async () => {
    const project = projectId;
    setLoading(true);
    try {
      const rows = await api<Capability[]>(`/projects/${project}/capabilities`);
      if (latestProject.current === project) {
        setCatalog(rows);
        setError("");
      }
    } catch (e) {
      if (latestProject.current === project) setError((e as Error).message);
    } finally {
      if (latestProject.current === project) setLoading(false);
    }
  }, [projectId]);
  useEffect(() => {
    setCatalog([]);
    void load();
  }, [load]);
  useEffect(() => {
    setDismissed(false);
    setCaret(value.length);
  }, [sessionId]);
  const match = value.slice(0, caret).match(/(?:^|\s)\/([^\s/]*)$/u);
  const trigger =
    focused && !dismissed && match
      ? {
          start: caret - match[1].length - 1,
          end: caret,
          query: match[1].toLocaleLowerCase(),
        }
      : null;
  const choices = trigger
    ? catalog.filter(
        (c) =>
          `${kinds[c.kind]} ${c.name} ${c.command || ""} ${c.description || ""}`
            .toLocaleLowerCase()
            .includes(trigger.query) &&
          !selected.some((s) => s.kind === c.kind && s.id === c.id),
      )
    : [];
  const open = !!trigger;
  useEffect(() => {
    setActive(0);
  }, [trigger?.query, trigger?.start, catalog]);
  useEffect(() => {
    if (open) void load();
  }, [open, load]);
  useEffect(() => {
    if (open)
      document
        .getElementById(`capability-option-${active}`)
        ?.scrollIntoView({ block: "nearest" });
  }, [active, open]);
  const choose = (c: Capability) => {
    if (!trigger) return;
    if (selected.length >= 8) {
      setError("一条消息最多选择 8 项能力");
      return;
    }
    onSelected([...selected, c]);
    onChange(value.slice(0, trigger.start) + value.slice(trigger.end));
    setDismissed(true);
    const position = trigger.start;
    requestAnimationFrame(() => {
      input.current?.focus();
      input.current?.setSelectionRange(position, position);
      setCaret(position);
    });
  };
  return (
    <div className="composer-editor">
      <CapabilityBadges
        values={selected}
        remove={(c) =>
          onSelected(selected.filter((v) => v.kind !== c.kind || v.id !== c.id))
        }
      />
      <textarea
        ref={input}
        aria-label="发送消息"
        aria-autocomplete="list"
        aria-expanded={open}
        aria-controls={open ? "capability-menu" : undefined}
        aria-activedescendant={
          open && choices.length ? `capability-option-${active}` : undefined
        }
        placeholder="描述任务，输入 / 选择 CLI、Skill 或 MCP…"
        value={value}
        onFocus={() => setFocused(true)}
        onBlur={() => setFocused(false)}
        onClick={() => setDismissed(false)}
        onSelect={(e) => setCaret(e.currentTarget.selectionStart)}
        onChange={(e) => {
          onChange(e.target.value);
          setCaret(e.target.selectionStart);
          setDismissed(false);
        }}
        onKeyDown={(e) => {
          if (e.nativeEvent.isComposing) return;
          if (open) {
            if (e.key === "Escape") {
              e.preventDefault();
              setDismissed(true);
              return;
            }
            if (choices.length && ["ArrowDown", "ArrowUp"].includes(e.key)) {
              e.preventDefault();
              setActive(
                (i) =>
                  (i + (e.key === "ArrowDown" ? 1 : -1) + choices.length) %
                  choices.length,
              );
              return;
            }
            if (
              choices.length &&
              (e.key === "Enter" || e.key === "Tab") &&
              !e.shiftKey
            ) {
              e.preventDefault();
              choose(choices[Math.min(active, choices.length - 1)]);
              return;
            }
          }
          if (e.key === "Enter" && !e.shiftKey) {
            e.preventDefault();
            send();
          }
        }}
      />
      {open && (
        <div
          className="capability-menu"
          id="capability-menu"
          role="listbox"
          aria-label="选择本条消息的能力"
        >
          <div className="capability-menu-hint">
            选择本条消息使用的能力{" "}
            <small>↑↓ 选择 · Enter 确认 · Esc 关闭</small>
          </div>
          {error && <p role="alert">{error}</p>}
          {loading && !catalog.length ? (
            <p>正在加载能力…</p>
          ) : choices.length ? (
            choices.map((c, i) => {
              const Icon = icons[c.kind];
              return (
                <ReactOption
                  key={c.kind + ":" + c.id}
                  c={c}
                  i={i}
                  active={active}
                  choose={choose}
                  Icon={Icon}
                />
              );
            })
          ) : (
            <p>没有匹配的可用能力，可继续输入指令</p>
          )}
        </div>
      )}
    </div>
  );
}
function ReactOption({
  c,
  i,
  active,
  choose,
  Icon,
}: {
  c: Capability;
  i: number;
  active: number;
  choose: (c: Capability) => void;
  Icon: typeof Terminal;
}) {
  return (
    <button
      type="button"
      role="option"
      id={`capability-option-${i}`}
      aria-selected={active === i}
      tabIndex={-1}
      onMouseDown={(e) => e.preventDefault()}
      onClick={() => choose(c)}
    >
      <Icon size={16} />
      <strong>{c.name}</strong>
      <span>{c.description || c.command || "全局工具"}</span>
      <small>{kinds[c.kind]}</small>
    </button>
  );
}
