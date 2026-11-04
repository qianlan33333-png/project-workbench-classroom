package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unicode/utf8"
)

type attachmentText struct {
	Text    string `json:"text"`
	Kind    string `json:"kind"`
	Warning string `json:"warning,omitempty"`
	Error   string `json:"error,omitempty"`
}

// This tool can read only a persisted attachment of the current personal run.
// The extraction helper receives a copy of that file, never a caller path,
// account credential, project directory, or general command to execute.
func (h *Harness) readMessageAttachment(ctx context.Context, in RuntimeInput, raw json.RawMessage) (any, error) {
	var q struct{ Index, Offset int }
	if e := json.Unmarshal(raw, &q); e != nil {
		return nil, e
	}
	actual, e := h.Store.Run(in.Run.ID)
	if e != nil || actual.ProjectID != personalProjectID || in.Project.ID != personalProjectID || actual.SessionID != in.Session.ID || !contains([]string{"queued", "running"}, actual.State) || q.Index < 0 || q.Index >= len(actual.Attachments) || q.Offset < 0 || q.Offset > 4<<20 {
		return nil, ErrNotFound
	}
	path := actual.Attachments[q.Index]
	if e = validPath(path); e != nil {
		return nil, e
	}
	root, e := h.Store.openRoot(personalProjectID)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	f, e := root.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() || st.Size() > 32<<20 {
		return nil, errors.New("读取文件最多32MiB，请缩小或拆分后重试")
	}
	b, e := io.ReadAll(io.LimitReader(f, 32<<20+1))
	if e != nil {
		return nil, e
	}
	if len(b) > 32<<20 {
		return nil, errors.New("附件读取超限")
	}
	h.attachmentMu.Lock()
	defer h.attachmentMu.Unlock()
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	scratch := filepath.Join(h.Config.DataDir, "runtime", "attachment-scratch", actual.ID)
	if e = os.MkdirAll(scratch, 0700); e != nil {
		return nil, e
	}
	cache := filepath.Join(scratch, digest(append([]byte(path+"\x00"), b...))+".json")
	var result attachmentText
	if data, er := os.ReadFile(cache); er == nil && json.Unmarshal(data, &result) == nil && result.Error == "" && utf8.ValidString(result.Text) && len(result.Text) <= 2<<20 {
		return attachmentExcerpt(result, path, q.Offset), nil
	}
	temp, e := os.MkdirTemp(scratch, "input-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(temp)
	input := filepath.Join(temp, "file")
	if e = os.WriteFile(input, b, 0600); e != nil {
		return nil, e
	}
	reader := filepath.Join(h.Config.RuntimeDir, "attachment-reader.py")
	if _, e = os.Stat(reader); e != nil {
		return nil, errors.New("附件读取组件尚未就绪")
	}
	child, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := []string{"python3", reader, input, filepath.Base(path)}
	name := "pw-" + actual.ID + "-attachment"
	if h.Config.RuntimeMode == "podman" {
		args = []string{h.Config.Podman, "run", "--rm", "--name", name, "--label", "project-workbench=true", "--label", "project-workbench-root=" + digest([]byte(h.Config.DataDir))[:16], "--network=none", "--read-only", "--userns=keep-id", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--memory=256m", "--cpus=1", "--pids-limit=64", "--tmpfs", "/tmp:rw,noexec,size=64m", "--env", "HOME=/tmp", "--env", "LANG=C.UTF-8", "--volume", input + ":/input:ro", "--volume", h.Config.RuntimeDir + ":/reader:ro", h.Config.RuntimeImage, "python3", "/reader/attachment-reader.py", "/input", filepath.Base(path)}
	}
	cmd := exec.CommandContext(child, args[0], args[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = temp
	if h.Config.RuntimeMode != "podman" {
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8", "HOME=" + temp, "TMPDIR=" + temp}
	}
	out := &limitedOutput{maxBytes: 8 << 20}
	errout := &limitedOutput{maxBytes: 4096}
	cmd.Stdout = out
	cmd.Stderr = errout
	commandErr := cmd.Run()
	if cmd.Process != nil {
		// Reap helpers even if the launcher exited before its descendants.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	if h.Config.RuntimeMode == "podman" {
		clean, stop := context.WithTimeout(context.Background(), 10*time.Second)
		cleanupErr := exec.CommandContext(clean, h.Config.Podman, "rm", "--force", "--ignore", name).Run()
		stop()
		if cleanupErr != nil {
			return nil, errors.New("附件读取进程未能收拢，请停止本轮后核对")
		}
	}
	if child.Err() != nil {
		return nil, errors.New("附件读取已取消或超时")
	}
	if e = json.Unmarshal(out.b.Bytes(), &result); e != nil {
		return nil, errors.New("无法读取附件，请检查格式或换成文本重试")
	}
	if result.Error != "" {
		return nil, fmt.Errorf("%s", result.Error)
	}
	if commandErr != nil || !utf8.ValidString(result.Text) || len(result.Text) > 2<<20 {
		return nil, errors.New("附件读取失败或内容超限")
	}
	if e = os.WriteFile(cache, []byte(encode(result)), 0600); e != nil {
		return nil, e
	}
	return attachmentExcerpt(result, path, q.Offset), nil
}
func attachmentExcerpt(v attachmentText, source string, offset int) map[string]any {
	result := sourceExcerpt(v.Text, offset)
	result["source"] = source
	result["kind"] = v.Kind
	if v.Warning != "" {
		result["warning"] = v.Warning
	}
	return result
}
