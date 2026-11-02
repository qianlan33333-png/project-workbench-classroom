package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// External processes never share the personal model home, app socket, or a
// project's volume. Native development deliberately has no host exec fallback.
func (h *Harness) personalExecArgs(in RuntimeInput, executable string, argv []string, envFile string, cli *CLITool) ([]string, string, error) {
	if h.Config.RuntimeMode != "podman" || h.Config.Podman == "" || h.Config.RuntimeImage == "" {
		return nil, "", errors.New("个人助手的本机工具需要 Podman 隔离执行环境")
	}
	if in.Project.ID != personalProjectID || in.Routing {
		return nil, "", errors.New("本次执行没有个人外部工具权限")
	}
	name := "pw-ext-" + newID()
	a := []string{"run", "--rm", "--interactive", "--name", name, "--label", "project-workbench=true", "--label", "project-workbench-root=" + digest([]byte(h.Config.DataDir))[:16], "--userns=keep-id", "--security-opt=no-new-privileges", "--cap-drop=ALL", "--pids-limit=96", "--memory=256m", "--workdir", "/tmp", "--env", "HOME=/tmp", "--env", "PATH=/tools/bin:/opt/node/bin:/usr/local/bin:/usr/bin:/bin"}
	if cli != nil {
		t := *cli
		if !t.Enabled || !t.Installed || !toolIdentifier.MatchString(t.Command) || !toolIdentifier.MatchString(t.ID) || !toolIdentifier.MatchString(t.Version) {
			return nil, "", errors.New("CLI 未安装或已停用")
		}
		root := filepath.Join(h.Store.Root, "tools")
		for _, mount := range []struct{ src, dst string }{{filepath.Join("bin", t.Command), ":ro"}, {filepath.Join("packages", t.ID, t.Version), ":ro"}, {filepath.Join("state", t.ID), ":rw"}} {
			a = append(a, "--volume", filepath.Join(root, mount.src)+":/tools/"+mount.src+mount.dst)
		}
	} else if h.Config.RuntimeDir != "" {
		a = append(a, "--volume", h.Config.RuntimeDir+":/opt/workbench-runtime:ro")
	}
	if envFile != "" {
		a = append(a, "--env-file", envFile)
	}
	a = append(a, "--entrypoint", executable, "--", h.Config.RuntimeImage)
	a = append(a, argv...)
	return a, name, nil
}
func (h *Harness) cleanupExternal(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, h.Config.Podman, "rm", "--force", "--ignore", name).Run()
}

type limitedOutput struct {
	b       bytes.Buffer
	clipped bool
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	left := 65536 - w.b.Len()
	if len(p) > left {
		p = p[:left]
		w.clipped = true
	}
	_, _ = w.b.Write(p)
	return n, nil
}

func (h *Harness) runPersonalCLI(parent context.Context, in RuntimeInput, raw json.RawMessage) (any, error) {
	var q struct {
		ID   string   `json:"id"`
		Args []string `json:"args"`
	}
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, err
	}
	if len(q.Args) > 256 {
		return nil, errors.New("参数过多")
	}
	t, e := h.Store.CLITool(q.ID)
	if e != nil {
		return nil, e
	}
	a, name, e := h.personalExecArgs(in, "/tools/bin/"+t.Command, q.Args, "", &t)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.Config.Podman, a...)
	cmd.WaitDelay = 2 * time.Second
	var output limitedOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	e = cmd.Run()
	if ctx.Err() != nil {
		h.cleanupExternal(name)
		return nil, errors.New("CLI 超时或已停止；外部动作的结果未确认，请先核对")
	}
	out := map[string]any{"output": strings.ToValidUTF8(output.b.String(), "�"), "truncated": output.clipped, "exitCode": 0}
	if e != nil {
		var exit *exec.ExitError
		if !errors.As(e, &exit) {
			return nil, e
		}
		out["exitCode"] = exit.ExitCode()
	}
	return out, nil
}

// Relay a registered stdio MCP as a byte stream. The caller can choose only
// its ID; executable, arguments and environment are read from owner settings.
func (h *Harness) servePersonalMCP(lifetime context.Context, in RuntimeInput, w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" || r.Header.Get("Upgrade") != "pwb-stdio" || in.Project.ID != personalProjectID || in.Routing {
		http.Error(w, "not available", 403)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/global-mcp/")
	p, e := h.Store.Plugin(id)
	if e != nil || p.Transport != "stdio" || p.Enabled != nil && !*p.Enabled {
		http.Error(w, "MCP unavailable", 404)
		return
	}
	var cli *CLITool
	if ts, err := h.Store.CLITools(); err == nil {
		for _, t := range ts {
			if p.Command == t.Command || p.Command == "/tools/bin/"+t.Command {
				cli = &t
				break
			}
		}
	}
	executable := p.Command
	if cli != nil {
		executable = "/tools/bin/" + cli.Command
	}
	envFile, e := externalEnv(p.Env)
	if e != nil {
		http.Error(w, "invalid MCP environment", 400)
		return
	}
	if envFile != "" {
		defer os.Remove(envFile)
	}
	a, name, e := h.personalExecArgs(in, executable, p.Args, envFile, cli)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	hijack, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "stdio unavailable", 500)
		return
	}
	conn, rw, e := hijack.Hijack()
	if e != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Time{})
	ctx, cancel := context.WithCancel(lifetime)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.Config.Podman, a...)
	stdin, e := cmd.StdinPipe()
	if e != nil {
		return
	}
	defer stdin.Close()
	cmd.Stdout = conn
	cmd.WaitDelay = 2 * time.Second
	if _, e = fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: pwb-stdio\r\nConnection: Upgrade\r\n\r\n"); e != nil {
		return
	}
	if e = rw.Flush(); e != nil {
		return
	}
	if e = cmd.Start(); e != nil {
		return
	}
	go func() { _, _ = io.Copy(stdin, rw.Reader); _ = stdin.Close(); cancel() }()
	_ = cmd.Wait()
	if ctx.Err() != nil {
		h.cleanupExternal(name)
	}
}

// Never put provider secrets in podman's argv or its host process environment.
func externalEnv(values map[string]string) (string, error) {
	if len(values) == 0 {
		return "", nil
	}
	keys := make([]string, 0, len(values))
	for k, v := range values {
		if !toolIdentifier.MatchString(k) || strings.ContainsAny(v, "\r\n\x00") {
			return "", errors.New("invalid environment")
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	f, e := os.CreateTemp("", "pwb-mcp-env-*")
	if e != nil {
		return "", e
	}
	for _, k := range keys {
		if _, e = fmt.Fprintf(f, "%s=%s\n", k, values[k]); e != nil {
			break
		}
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		os.Remove(f.Name())
		return "", e
	}
	return f.Name(), nil
}
