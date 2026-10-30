import { useEffect, useState } from "react";
import { api, type Session } from "./api";
export function RecycleBin({ notify }: { notify: (s: string) => void }) {
  const [rows, setRows] = useState<Session[]>([]),
    [error, setError] = useState("");
  const load = () =>
    api<Session[]>("/recycle-bin")
      .then(setRows)
      .catch((e) => setError(e.message));
  useEffect(() => {
    void load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, []);
  const action = async (s: Session, remove = false) => {
    if (
      remove &&
      !confirm("彻底删除这段对话的原始记录？项目文件继续保留，此操作不能撤销。")
    )
      return;
    try {
      await api(
        `/recycle-bin/${s.id}${remove ? "" : "/restore"}`,
        remove ? undefined : {},
        remove ? "DELETE" : "POST",
      );
      notify(remove ? "原始对话已清除" : "原对话已找回，旧任务不会自动重跑");
      void load();
    } catch (e) {
      setError((e as Error).message);
    }
  };
  return (
    <section className="settings-section">
      <h3>对话回收站</h3>
      <p>归档与删除后的原始对话保留 15 天；项目文件继续保留。</p>
      {error && <p role="alert">{error}</p>}
      {!rows.length && <p>回收站为空</p>}
      {rows.map((s) => (
        <div className="setting-row" key={s.id}>
          <div>
            <strong>{s.title || "新对话"}</strong>
            <p>
              {s.recycleReason === "archive" ? "已提炼归档" : "已删除"} ·{" "}
              {Math.max(
                0,
                Math.ceil(((s.expiresAt || 0) - Date.now()) / 86400000),
              )}{" "}
              天后清除
            </p>
            {s.archivePath && <p>项目文档：{s.archivePath}</p>}
          </div>
          <div>
            <button
              disabled={(s.expiresAt || 0) <= Date.now()}
              onClick={() => void action(s)}
            >
              找回
            </button>
            <button onClick={() => void action(s, true)}>彻底删除</button>
          </div>
        </div>
      ))}
    </section>
  );
}
export function GlobalTools({ notify }: { notify: (s: string) => void }) {
  const [tools, setTools] = useState<any[]>([]),
    [jobs, setJobs] = useState<any[]>([]),
    [error, setError] = useState(""),
    [requestedJobs, setRequestedJobs] = useState<string[]>([]),
    [draft, setDraft] = useState<any>(null);
  const load = async () => {
    try {
      const [t, j] = await Promise.all([
        api<any[]>("/cli-tools"),
        api<any[]>("/tool-jobs"),
      ]);
      setTools(t);
      setJobs(j);
    } catch (e) {
      setError((e as Error).message);
    }
  };
  useEffect(() => {
    void load();
    const t = setInterval(load, 2500);
    return () => clearInterval(t);
  }, []);
  const start = async (id: string, action: string) => {
    try {
      const job = await api(`/cli-tools/${id}/${action}`, {});
      setRequestedJobs((old) => [...old, job.id]);
      void load();
      notify("操作已开始，登录链接和进度会显示在下方");
    } catch (e) {
      setError((e as Error).message);
    }
  };
  const save = async () => {
    try {
      await api("/cli-tools", draft);
      setDraft(null);
      void load();
    } catch (e) {
      setError((e as Error).message);
    }
  };
  return (
    <section className="settings-section">
      <h3>
        全局 CLI 工具{" "}
        <button
          onClick={() =>
            setDraft({
              id: "",
              name: "",
              command: "",
              version: "",
              url: "",
              sha256: "",
              enabled: true,
            })
          }
        >
          添加 CLI
        </button>
      </h3>
      <p>
        安装和登录一次，所有项目均可调用。CLI 与 MCP
        的业务输出仍保存在当前项目。
      </p>
      {error && <p role="alert">{error}</p>}
      {draft && (
        <div className="tool-form">
          {[
            ["id", "工具标识"],
            ["name", "名称"],
            ["command", "入口命令"],
            ["version", "版本"],
            ["url", "Linux tar.gz 发行包地址"],
            ["sha256", "SHA256（可选）"],
            ["origin", "CRM 服务地址（CRM CLI）"],
          ].map(([key, label]) => (
            <label key={key}>
              {label}
              <input
                value={draft[key] || ""}
                onChange={(e) => setDraft({ ...draft, [key]: e.target.value })}
              />
            </label>
          ))}
          <button onClick={() => void save()}>保存工具配置</button>
          <button onClick={() => setDraft(null)}>取消</button>
        </div>
      )}
      {tools.map((t) => (
        <div className="setting-row" key={t.id}>
          <div>
            <strong>{t.name}</strong>
            <p>
              {t.installed ? "已安装" : "未安装"} · {t.version} ·{" "}
              {t.enabled ? "全局启用" : "已停用"}
              {" · " +
                ((
                  {
                    ready: "已授权",
                    needs_login: "待登录",
                    expired: "授权已到期",
                  } as Record<string, string>
                )[t.authStatus] || "待检查登录")}
              {t.authScopeCount > 0 && ` · ${t.authScopeCount}项权限`}
              {t.authExpiresAt > 0 &&
                ` · 到期 ${new Date(t.authExpiresAt).toLocaleString("zh-CN")}`}
            </p>
            <code>{t.command}</code>
          </div>
          <div className="tool-actions">
            <button onClick={() => setDraft({ ...t })}>配置</button>
            <button onClick={() => void start(t.id, "install")}>
              {t.installed ? "重新安装" : "安装"}
            </button>
            {t.installed && (
              <>
                <button onClick={() => void start(t.id, "configure")}>
                  配置账号
                </button>
                <button onClick={() => void start(t.id, "auth")}>
                  登录授权
                </button>
                <button onClick={() => void start(t.id, "check")}>
                  检查登录
                </button>
                <button
                  onClick={() =>
                    void api("/cli-tools", { ...t, enabled: !t.enabled })
                      .then(load)
                      .catch((e) => setError(e.message))
                  }
                >
                  {t.enabled ? "停用" : "启用"}
                </button>
                <button
                  onClick={() => {
                    if (confirm("卸载工具并清除它的本地登录配置？"))
                      void api(`/cli-tools/${t.id}`, undefined, "DELETE")
                        .then(load)
                        .catch((e) => setError(e.message));
                  }}
                >
                  卸载
                </button>
              </>
            )}
          </div>
        </div>
      ))}
      {jobs
        .filter(
          (j) =>
            ["queued", "running"].includes(j.state) ||
            (requestedJobs.includes(j.id) &&
              ["failed", "interrupted"].includes(j.state)),
        )
        .map((j) => (
          <div className="tool-current-operation" key={j.id} role="status">
            <p>
              {tools.find((t) => t.id === j.toolId)?.name || j.toolId} ·{" "}
              {j.state === "queued"
                ? "等待空闲"
                : j.state === "running"
                  ? (
                      {
                        install: "正在安装",
                        check: "正在检查授权",
                        auth: "等待完成授权",
                        configure: "正在配置账号",
                      } as Record<string, string>
                    )[j.action] || "进行中"
                  : "操作未完成"}
            </p>
            {j.error && <p role="alert">{j.error}</p>}
            {Array.from(
              new Set<string>(
                (j.output || "").match(/https?:\/\/[^\s"<>]+/g) || [],
              ),
            ).map((link) => (
              <p key={link}>
                <a href={link} target="_blank" rel="noreferrer">
                  打开授权链接
                </a>
              </p>
            ))}
          </div>
        ))}
    </section>
  );
}
