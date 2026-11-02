import { useEffect, useRef, useState } from "react";
import { Check } from "lucide-react";
import { api, endpoint, type Expert, type Project } from "./api";

type Connection = {
  id: string;
  label: string;
  enabled: boolean;
  accounts: string[];
};
type Binding = {
  id?: string;
  version?: number;
  projectId: string;
  connectionId: string;
  accountId: string;
  expertId: string;
  enabled: boolean;
  timeoutSeconds: number;
  notifyChatId: string;
  contactIds?: string[];
  knowledgePaths?: string[];
};
type Choice = { connection: Connection; accountId: string; binding?: Binding };

export function GatewayProjectSettings({
  project,
  experts,
  expanded = false,
}: {
  expanded?: boolean;
  project: Project;
  experts: Expert[];
}) {
  const [choices, setChoices] = useState<Choice[]>([]);
  const [selection, setSelection] = useState(0);
  const [loaded, setLoaded] = useState(false);
  const [serial, setSerial] = useState(0);
  const [error, setError] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    setLoaded(false);
    setError("");
    Promise.all([
      api<Connection[]>(
        "/gateway/connections",
        undefined,
        undefined,
        controller.signal,
      ),
      api<Binding[]>(
        "/gateway/bindings",
        undefined,
        undefined,
        controller.signal,
      ),
    ])
      .then(([connections, bindings]) => {
        if (controller.signal.aborted) return;
        const available = (connections ?? []).flatMap((connection) =>
          (connection.accounts ?? []).flatMap((accountId) => {
            const binding = (bindings ?? []).find(
              (b) =>
                b.connectionId === connection.id && b.accountId === accountId,
            );
            return (!binding && connection.enabled) ||
              binding?.projectId === project.id
              ? [{ connection, accountId, binding }]
              : [];
          }),
        );
        setChoices(available);
        setSelection((index) =>
          serial && available[index]
            ? index
            : Math.max(
                0,
                available.findIndex((c) => c.binding),
              ),
        );
        setLoaded(true);
      })
      .catch((e) => {
        if (!controller.signal.aborted) setError(e.message);
      });
    return () => controller.abort();
  }, [project.id, serial]);
  const choice = choices[selection];
  return (
    <details className="project-metadata gateway-settings" open={expanded}>
      <summary>自动化应答</summary>
      {error ? (
        <p role="alert">
          {error}{" "}
          <button type="button" onClick={() => setSerial((s) => s + 1)}>
            重新读取
          </button>
        </p>
      ) : !loaded ? (
        <p role="status">正在读取接入配置…</p>
      ) : !choice ? (
        <p>
          暂无可绑定的企微账号。请先配置网关连接，或检查托管账号的项目归属。
        </p>
      ) : (
        <>
          <label>
            企微连接与托管账号
            <select
              value={selection}
              onChange={(e) => setSelection(Number(e.target.value))}
            >
              {choices.map((c, i) => (
                <option
                  key={JSON.stringify([c.connection.id, c.accountId])}
                  value={i}
                >
                  {c.connection.label} · {c.accountId}
                  {c.binding ? "（已绑定）" : ""}
                </option>
              ))}
            </select>
          </label>
          <BindingForm
            key={JSON.stringify([
              choice.connection.id,
              choice.accountId,
              serial,
            ])}
            choice={choice}
            project={project}
            experts={experts}
            onSave={() => setSerial((s) => s + 1)}
          />
        </>
      )}
    </details>
  );
}

function BindingForm({
  choice,
  project,
  experts,
  onSave,
}: {
  choice: Choice;
  project: Project;
  experts: Expert[];
  onSave: () => void;
}) {
  const original = choice.binding;
  const [enabled, setEnabled] = useState(!!original?.enabled);
  const [expertId, setExpertId] = useState(
    original?.expertId || project.defaultExpertId || "general",
  );
  const [chatId, setChatId] = useState(original?.notifyChatId || "");
  const [all, setAll] = useState(!!original && !original.contactIds?.length);
  const [contacts, setContacts] = useState(
    (original?.contactIds ?? []).join("\n"),
  );
  const [paths, setPaths] = useState(
    (original?.knowledgePaths ?? []).join("\n"),
  );
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const lines = (value: string) => [
    ...new Set(
      value
        .split(/\r?\n/)
        .map((s) => s.trim())
        .filter(Boolean),
    ),
  ];
  const hook = new URL(
    endpoint("/gateway/hooks/" + encodeURIComponent(choice.connection.id)),
    location.origin,
  ).href;
  return (
    <form
      onSubmit={async (e) => {
        e.preventDefault();
        if (busy) return;
        if (!all && !lines(contacts).length) {
          setNotice("请填写至少一位试点客户，或明确选择全部私聊客户。");
          return;
        }
        setBusy(true);
        setNotice("");
        try {
          await api("/gateway/bindings", {
            ...original,
            projectId: project.id,
            connectionId: choice.connection.id,
            accountId: choice.accountId,
            enabled,
            expertId,
            notifyChatId: chatId.trim(),
            contactIds: all ? [] : lines(contacts),
            knowledgePaths: lines(paths),
            timeoutSeconds: original?.timeoutSeconds || 120,
          });
          onSave();
        } catch (e) {
          setNotice((e as Error).message);
        } finally {
          setBusy(false);
        }
      }}
    >
      <fieldset disabled={busy}>
        <label>
          推送接收地址
          <input readOnly value={hook} onFocus={(e) => e.target.select()} />
        </label>
        <label>
          应答专家
          <select
            value={expertId}
            onChange={(e) => setExpertId(e.target.value)}
          >
            <option value="general">通用助手</option>
            {experts
              .filter((e) => project.expertIds.includes(e.id))
              .map((e) => (
                <option key={e.id} value={e.id}>
                  {e.name}
                </option>
              ))}
          </select>
        </label>
        <label>
          人工接管通知会话
          <input
            required
            value={chatId}
            onChange={(e) => setChatId(e.target.value)}
            placeholder="飞书会话 ID"
          />
        </label>
        <label>
          客户范围
          <select
            value={all ? "all" : "selected"}
            onChange={(e) => setAll(e.target.value === "all")}
          >
            <option value="selected">指定试点客户</option>
            <option value="all">全部私聊客户</option>
          </select>
        </label>
        {!all && (
          <label>
            试点客户 ID（每行一个）
            <textarea
              required
              rows={2}
              value={contacts}
              onChange={(e) => setContacts(e.target.value)}
            />
          </label>
        )}
        <label>
          允许客户使用的知识文件（每行一个项目内路径）
          <textarea
            rows={2}
            value={paths}
            onChange={(e) => setPaths(e.target.value)}
          />
        </label>
        <label className="gateway-check">
          <input
            type="checkbox"
            checked={enabled}
            disabled={!choice.connection.enabled}
            onChange={(e) => setEnabled(e.target.checked)}
          />
          启用此账号的自动应答
        </label>
        {!choice.connection.enabled && <p>当前企微连接已停用。</p>}
        <button type="submit">{busy ? "正在保存…" : "保存应答配置"}</button>
      </fieldset>
      {notice && <p role="alert">{notice}</p>}
    </form>
  );
}

type CustomerSession = {
  id: string;
  mode: "human" | "auto";
  version: number;
  bindingVersion: number;
};
export function GatewayResume({
  id,
  notify,
}: {
  id: string;
  notify: (s: string) => void;
}) {
  const [state, setState] = useState<CustomerSession>();
  const [busy, setBusy] = useState(false);
  const epoch = useRef(0);
  const missing = useRef(false);
  const path = "/gateway/sessions/" + encodeURIComponent(id);
  async function read() {
    const stamp = ++epoch.current;
    try {
      const next = await api<CustomerSession>(path);
      if (epoch.current === stamp && next.id === id) setState(next);
    } catch (e) {
      if (epoch.current === stamp) {
        missing.current = (e as { status?: number }).status === 404;
        setState(undefined);
      }
    }
  }
  useEffect(() => {
    const update = () => {
      if (!document.hidden && !missing.current) void read();
    };
    update();
    window.addEventListener("focus", update);
    const timer = window.setInterval(update, 5000);
    return () => {
      epoch.current++;
      clearInterval(timer);
      window.removeEventListener("focus", update);
    };
  }, [id]);
  if (state?.id !== id || state.mode !== "human") return null;
  return (
    <button
      className="gateway-restore"
      disabled={busy}
      title="从下一条客户消息开始自动回复"
      onClick={async () => {
        if (busy) return;
        setBusy(true);
        try {
          await api(path + "/resume", {
            version: state.version,
            bindingVersion: state.bindingVersion,
          });
          notify("已恢复 AI，将从客户的下一条新消息开始应答。");
        } catch (e) {
          notify((e as Error).message);
        } finally {
          await read();
          setBusy(false);
        }
      }}
    >
      <span>AI 已暂停</span>
      <Check size={14} aria-hidden="true" />
      恢复
    </button>
  );
}
