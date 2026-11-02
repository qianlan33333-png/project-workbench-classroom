package workbench

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

type CLITool struct {
	AuthStatus     string `json:"authStatus,omitempty"`
	AuthCheckedAt  int64  `json:"authCheckedAt,omitempty"`
	AuthExpiresAt  int64  `json:"authExpiresAt,omitempty"`
	AuthScopeCount int    `json:"authScopeCount,omitempty"`
	ID             string `json:"id"`
	Name           string `json:"name"`
	Command        string `json:"command"`
	Version        string `json:"version"`
	URL            string `json:"url"`
	SHA256         string `json:"sha256"`
	Origin         string `json:"origin,omitempty"`
	Enabled        bool   `json:"enabled"`
	Installed      bool   `json:"installed"`
}
type ToolJob struct {
	ID         string   `json:"id"`
	ToolID     string   `json:"toolId"`
	Action     string   `json:"action"`
	Args       []string `json:"args"`
	State      string   `json:"state"`
	Output     string   `json:"output"`
	Error      string   `json:"error,omitempty"`
	CreatedAt  int64    `json:"createdAt"`
	FinishedAt int64    `json:"finishedAt"`
}

var toolInstallMu sync.Mutex

func toolPresets() []CLITool {
	return []CLITool{
		{ID: "lark", Name: "飞书 CLI", Command: "lark-cli", Version: "1.0.98", URL: "https://github.com/larksuite/cli/releases/download/v1.0.98/lark-cli-1.0.98-linux-amd64.tar.gz", SHA256: "4a12dcb7a0aab25986d8bcd046cb45b08278531a9aac3a12b53cf3d94738fe80", Enabled: true},
		{ID: "aicrm", Name: "CRM CLI", Command: "aicrm-cli", Version: "0.6.1", URL: "https://github.com/qianlan33333-png/aicrm-cli/releases/download/v0.6.1/aicrm-cli_0.6.1_linux_amd64.tar.gz", SHA256: "9cab143d39d9a01ef3c7b3eaf21e43738aadfa7ecf3a3c73942f767707c0470e", Enabled: true},
	}
}
func (s *Store) CLITools() ([]CLITool, error) {
	vs, e := listJSON[CLITool](s.DB, "SELECT data FROM cli_tools ORDER BY id")
	if e != nil {
		return nil, e
	}
	for _, preset := range toolPresets() {
		found := false
		for _, v := range vs {
			if v.ID == preset.ID {
				found = true
			}
		}
		if !found {
			vs = append(vs, preset)
		}
	}
	for i := range vs {
		_, e := os.Stat(s.toolBinary(vs[i]))
		vs[i].Installed = e == nil
		if vs[i].AuthExpiresAt > 0 && vs[i].AuthExpiresAt <= now() {
			vs[i].AuthStatus = "expired"
		}
	}
	return vs, nil
}
func (s *Store) CLITool(id string) (CLITool, error) {
	vs, e := s.CLITools()
	if e != nil {
		return CLITool{}, e
	}
	for _, v := range vs {
		if v.ID == id {
			return v, nil
		}
	}
	return CLITool{}, ErrNotFound
}
func (s *Store) toolBinary(v CLITool) string {
	return filepath.Join(s.Root, "tools", "packages", v.ID, v.Version, v.Command)
}

var toolIdentifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}$`)

func (s *Store) SaveCLITool(v CLITool) (CLITool, error) {
	if !toolIdentifier.MatchString(v.ID) || !toolIdentifier.MatchString(v.Command) || !toolIdentifier.MatchString(v.Version) || strings.TrimSpace(v.Name) == "" {
		return v, errors.New("请填写工具名称、稳定标识、入口命令与版本")
	}
	u, e := url.Parse(v.URL)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return v, errors.New("安装地址必须是 HTTPS Linux tar.gz 发行包")
	}
	if v.Origin != "" {
		u, e = url.Parse(v.Origin)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return v, errors.New("CRM 地址必须是 HTTPS Origin")
		}
		v.Origin = u.Scheme + "://" + u.Host
	}
	var duplicate int
	if e = s.DB.QueryRow("SELECT count(*) FROM cli_tools WHERE id<>? AND json_extract(data,'$.command')=?", v.ID, v.Command).Scan(&duplicate); e != nil {
		return v, e
	}
	if duplicate > 0 {
		return v, errors.New("入口命令已被另一工具使用，请更换命令名称")
	}
	old, oldErr := getJSON[CLITool](s.DB, "SELECT data FROM cli_tools WHERE id=?", v.ID)
	v.AuthStatus = ""
	v.AuthCheckedAt = 0
	v.AuthExpiresAt = 0
	v.AuthScopeCount = 0
	if oldErr == nil {
		v.AuthStatus = old.AuthStatus
		v.AuthCheckedAt = old.AuthCheckedAt
		v.AuthExpiresAt = old.AuthExpiresAt
		v.AuthScopeCount = old.AuthScopeCount
		if old.Version == v.Version && (old.URL != v.URL || old.Command != v.Command) {
			if _, err := os.Stat(s.toolBinary(old)); err == nil {
				return v, errors.New("已安装版本不能原地替换来源或入口，请使用新版本号")
			}
		}
	}
	_, e = s.DB.Exec("INSERT INTO cli_tools VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data", v.ID, encode(v))
	if e != nil {
		return v, e
	}
	if _, e = os.Stat(s.toolBinary(v)); e == nil {
		e = s.writeToolWrapper(v)
	}
	return v, e
}
func (s *Store) writeToolWrapper(v CLITool) error {
	dir := filepath.Join(s.Root, "tools", "bin")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	entry := filepath.Join(dir, v.Command)
	if !v.Enabled {
		return os.RemoveAll(entry)
	}
	// The same wrapper works on the host and in the task container. No secrets
	// appear in its body, only explicit paths to each CLI's persistent state.
	b := "#!/bin/sh\nset -eu\nbase=$(CDPATH= cd -- \"$(dirname -- \"$0\")/..\" && pwd)\n"
	if v.ID == "lark" {
		b += "export LARKSUITE_CLI_CONFIG_DIR=\"$base/state/lark/config\"\nexport LARKSUITE_CLI_DATA_DIR=\"$base/state/lark/data\"\n"
	}
	if v.ID == "aicrm" {
		b += "export AICRM_CLI_CONFIG_DIR=\"$base/state/aicrm/config\"\nexport AICRM_CLI_CREDENTIAL_FILE=\"$base/state/aicrm/credential.json\"\n"
	}
	b += "exec \"$base/packages/" + v.ID + "/" + v.Version + "/" + v.Command + "\" \"$@\"\n"
	if e := os.MkdirAll(filepath.Join(s.Root, "tools", "state", v.ID), 0700); e != nil {
		return e
	}
	tmp := entry + ".tmp"
	if e := os.WriteFile(tmp, []byte(b), 0755); e != nil {
		return e
	}
	return os.Rename(tmp, entry)
}
func (s *Store) RemoveCLITool(id string) error {
	return s.removeCLITool(id, "")
}
func (s *Store) removeCLITool(id, requestingRun string) error {
	toolInstallMu.Lock()
	defer toolInstallMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	v, e := s.CLITool(id)
	if e != nil {
		return e
	}
	// Existing task mounts may use the installed binary. Disable now, retain
	// version files until no business execution is using the environment.
	if e = s.checkNoRunningToolsExcept(requestingRun); e != nil {
		return e
	}
	for _, p := range []string{filepath.Join(s.Root, "tools", "bin", v.Command), filepath.Join(s.Root, "tools", "packages", id), filepath.Join(s.Root, "tools", "state", id)} {
		if e = os.RemoveAll(p); e != nil {
			return e
		}
	}
	_, e = s.DB.Exec("DELETE FROM cli_tools WHERE id=?", id)
	return e
}
func (s *Store) CheckNoRunningTools() error {
	return s.checkNoRunningToolsExcept("")
}
func (s *Store) checkNoRunningToolsExcept(requestingRun string) error {
	var n int
	e := s.DB.QueryRow("SELECT count(*) FROM workbench_run_leases WHERE run_id<>?", requestingRun).Scan(&n)
	if e != nil {
		return e
	}
	if n > 0 {
		return ErrLocked
	}
	return nil
}
func (s *Store) ToolJobs() ([]ToolJob, error) {
	return listJSON[ToolJob](s.DB, "SELECT data FROM tool_jobs ORDER BY created_at DESC LIMIT 30")
}
func (s *Store) SaveToolJob(j ToolJob) error {
	_, e := s.DB.Exec("UPDATE tool_jobs SET state=?,data=? WHERE id=?", j.State, encode(j), j.ID)
	if e == nil {
		e = s.Emit("", "", "tool.updated", j)
	}
	return e
}
func (a *API) startToolJob(id, action string, args []string) (ToolJob, error) {
	v, e := a.Store.CLITool(id)
	if e != nil {
		return ToolJob{}, e
	}
	if !v.Enabled {
		return ToolJob{}, errors.New("工具已停用，请先启用再操作")
	}
	if action != "install" && action != "auth" && action != "check" && action != "configure" {
		return ToolJob{}, errors.New("不支持的工具操作")
	}
	j := ToolJob{ID: newID(), ToolID: id, Action: action, Args: args, State: "queued", CreatedAt: now()}
	// One operation per CLI; a second click observes the existing task.
	a.toolMu.Lock()
	defer a.toolMu.Unlock()
	existing, _ := listJSON[ToolJob](a.Store.DB, "SELECT data FROM tool_jobs WHERE tool_id=? AND state IN ('queued','running') LIMIT 1", id)
	if len(existing) > 0 {
		return existing[0], nil
	}
	if _, e = a.Store.DB.Exec("INSERT INTO tool_jobs VALUES(?,?,?,?,?)", j.ID, id, j.State, j.CreatedAt, encode(j)); e != nil {
		return j, e
	}
	go a.runToolJob(v, j)
	return j, nil
}
func (a *API) runToolJob(v CLITool, j ToolJob) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	go func() {
		select {
		case <-a.Shutdown:
			cancel()
		case <-ctx.Done():
		}
	}()
	finish := func(e error) {
		j.FinishedAt = now()
		j.State = "completed"
		if e != nil {
			j.State = "failed"
			j.Error = safeRuntimeError(e.Error())
		}
		_ = a.Store.SaveToolJob(j)
	}
	if j.Action == "install" {
		toolInstallMu.Lock()
		defer toolInstallMu.Unlock()
		for {
			a.Store.mu.Lock()
			requestingRun := ""
			if len(j.Args) == 2 && j.Args[0] == "requesting-run" {
				requestingRun = j.Args[1]
			}
			// A chat may explicitly install a tool while awaiting the operation.
			// Waiting for that same run to finish would deadlock its tool call.
			e := a.Store.checkNoRunningToolsExcept(requestingRun)
			if e == nil {
				e = a.Store.SetSetting("tool_install_running", j.ID)
			}
			a.Store.mu.Unlock()
			if e == nil {
				break
			}
			select {
			case <-ctx.Done():
				finish(ctx.Err())
				return
			case <-time.After(time.Second):
			}
		}
		defer a.Store.DB.Exec("DELETE FROM settings WHERE key='tool_install_running'")
		j.State = "running"
		_ = a.Store.SaveToolJob(j)
		e := a.Store.installCLITool(ctx, v)
		finish(e)
		return
	}
	j.State = "running"
	_ = a.Store.SaveToolJob(j)
	if !v.Installed {
		finish(errors.New("请先安装工具"))
		return
	}
	if e := a.Store.writeToolWrapper(v); e != nil {
		finish(e)
		return
	}
	command := filepath.Join(a.Store.Root, "tools", "bin", v.Command)
	args := []string{}
	credentialCandidate := filepath.Join(a.Store.Root, "tools", "state", "aicrm", "credential-"+j.ID+".json")
	switch v.ID {
	case "lark":
		if j.Action == "configure" {
			args = []string{"config", "init", "--new"}
		} else if j.Action == "auth" {
			args = []string{"auth", "login", "--domain", "all", "--scope", "im:message.send_as_user"}
		} else {
			args = []string{"auth", "status"}
		}
	case "aicrm":
		if j.Action == "configure" {
			if v.Origin == "" {
				finish(errors.New("请先填写 CRM 地址"))
				return
			}
			args = []string{"profile", "add", "workbench", v.Origin}
		} else if j.Action == "auth" {
			args = []string{"auth", "login", "--wecom", "--no-browser", "--secret-output", credentialCandidate}
		} else {
			args = []string{"auth", "whoami"}
		}
	default:
		if j.Action != "check" {
			finish(errors.New("此工具请通过对话按官方步骤配置授权"))
			return
		}
		args = []string{"--version"}
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = filepath.Join(a.Store.Root, "tools", "state", v.ID)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	output := &toolOutput{update: func(text string) { j.Output = text; _ = a.Store.SaveToolJob(j) }}
	cmd.Stdout = output
	cmd.Stderr = output
	e := cmd.Run()
	if e == nil && v.ID == "aicrm" && j.Action == "auth" {
		e = os.Rename(credentialCandidate, filepath.Join(a.Store.Root, "tools", "state", "aicrm", "credential.json"))
	}
	if e != nil {
		_ = os.Remove(credentialCandidate)
	}
	if e == nil && v.ID == "aicrm" && j.Action == "configure" {
		next := exec.CommandContext(ctx, command, "profile", "use", "workbench")
		next.Dir = cmd.Dir
		next.Stdout = output
		next.Stderr = output
		e = next.Run()
	}
	if (j.Action == "auth" || j.Action == "check") && (v.ID == "lark" || v.ID == "aicrm") {
		statusArgs := []string{"auth", "status"}
		if v.ID == "aicrm" {
			statusArgs = []string{"auth", "whoami"}
		}
		checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		check := exec.CommandContext(checkCtx, command, statusArgs...)
		check.Dir = cmd.Dir
		raw, checkErr := check.Output()
		cancel()
		var data struct {
			Authenticated bool   `json:"authenticated"`
			ActorID       int64  `json:"actor_id"`
			ExpiresAt     string `json:"expires_at"`
			Identities    struct {
				User struct {
					Available bool   `json:"available"`
					Scope     string `json:"scope"`
				} `json:"user"`
			} `json:"identities"`
		}
		parsed := json.Unmarshal(raw, &data)
		status := "needs_login"
		scopes := 0
		expires := int64(0)
		if checkErr == nil && parsed == nil && (data.Authenticated || data.ActorID > 0 || data.Identities.User.Available) {
			status = "ready"
			scopes = len(strings.Fields(data.Identities.User.Scope))
			if tm, err := time.Parse(time.RFC3339Nano, data.ExpiresAt); err == nil {
				expires = tm.UnixMilli()
			}
		}
		_, _ = a.Store.DB.Exec("UPDATE cli_tools SET data=json_set(data,'$.authStatus',?,'$.authCheckedAt',?,'$.authExpiresAt',?,'$.authScopeCount',?) WHERE id=?", status, now(), expires, scopes, v.ID)
		if status != "ready" && e == nil {
			e = errors.New("工具尚未登录或授权已失效，请完成登录")
		}
	}
	finish(e)
}

type toolOutput struct {
	mu     sync.Mutex
	data   []byte
	update func(string)
}

var secretLine = regexp.MustCompile(`(?i)(access_token|refresh_token|app_secret|client_secret|bearer|password|token)\s*["']?\s*[:=]\s*["']?[^\s,"'}]+`)

func (o *toolOutput) Write(b []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.data = append(o.data, b...)
	if len(o.data) > 32000 {
		o.data = o.data[len(o.data)-32000:]
	}
	o.update(secretLine.ReplaceAllString(string(o.data), "$1=[REDACTED]"))
	return len(b), nil
}
func (s *Store) installCLITool(ctx context.Context, v CLITool) error {
	if runtime.GOOS != "linux" {
		return errors.New("工具发行包安装仅在 Linux 云端运行")
	}
	if _, e := os.Stat(s.toolBinary(v)); e == nil {
		_, e = s.SaveCLITool(v)
		return e
	}
	for _, preset := range toolPresets() {
		if v.ID == preset.ID && v.Version == preset.Version && v.URL == preset.URL && v.SHA256 == "" {
			v.SHA256 = preset.SHA256
		}
	}
	tmp, e := os.CreateTemp(filepath.Join(s.Root, "uploads"), "cli-*.tar.gz")
	if e != nil {
		return e
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	source, e := s.toolArchive(ctx, v)
	if e != nil {
		return e
	}
	defer source.Close()
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(source, 128<<20+1))
	if e != nil {
		return e
	}
	if n > 128<<20 {
		return errors.New("工具包超过 128MiB")
	}
	got := hex.EncodeToString(h.Sum(nil))
	if v.SHA256 != "" && !strings.EqualFold(got, v.SHA256) {
		return errors.New("工具包摘要不匹配")
	}
	v.SHA256 = got
	if _, e = tmp.Seek(0, 0); e != nil {
		return e
	}
	gz, e := gzip.NewReader(tmp)
	if e != nil {
		return e
	}
	defer gz.Close()
	dest := filepath.Dir(s.toolBinary(v))
	if e = os.MkdirAll(dest, 0700); e != nil {
		return e
	}
	tr := tar.NewReader(io.LimitReader(gz, 256<<20))
	found := false
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		hdr, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != v.Command {
			continue
		}
		if hdr.Size > 128<<20 {
			return errors.New("工具入口超过 128MiB")
		}
		p := s.toolBinary(v) + ".tmp"
		f, e := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
		if e != nil {
			return e
		}
		_, e = io.Copy(f, io.LimitReader(tr, 128<<20+1))
		if e == nil {
			e = f.Sync()
		}
		f.Close()
		if e != nil {
			return e
		}
		if e = os.Rename(p, s.toolBinary(v)); e != nil {
			return e
		}
		found = true
		break
	}
	if !found {
		return errors.New("发行包里没有指定的入口命令")
	}
	_, e = s.SaveCLITool(v)
	return e
}

func (s *Store) toolArchive(ctx context.Context, v CLITool) (io.ReadCloser, error) {
	// Release-bundled, digest-addressed archives make the two initial tools
	// installable even when the cloud host cannot reach GitHub release assets.
	if len(v.SHA256) == 64 {
		if _, e := hex.DecodeString(v.SHA256); e != nil {
			return nil, errors.New("工具包 SHA256 无效")
		}
		f, e := os.Open(filepath.Join(s.Root, "tools", "downloads", strings.ToLower(v.SHA256)+".tar.gz"))
		if e == nil {
			return f, nil // The caller always verifies the actual bytes.
		}
		if !errors.Is(e, os.ErrNotExist) {
			return nil, e
		}
	}
	req, e := http.NewRequestWithContext(ctx, "GET", v.URL, nil)
	if e != nil {
		return nil, e
	}
	client := http.Client{Timeout: 2 * time.Minute}
	res, e := client.Do(req)
	if e != nil {
		var u *url.Error
		if errors.As(e, &u) {
			return nil, fmt.Errorf("工具包下载失败：%w", u.Err)
		}
		return nil, e
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("工具包下载返回 %d", res.StatusCode)
	}
	return res.Body, nil
}
