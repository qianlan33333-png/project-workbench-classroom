package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

type SourceResult struct {
	Directory string `json:"directory"`
	Files     map[string]struct {
		SHA256 string `json:"sha256"`
		Count  int    `json:"count"`
	} `json:"files"`
	Report map[string]any `json:"report"`
}

// Collect invokes a fixed adapter. Only an owner-registered server environment
// supplies the private connection file; model arguments contain resource IDs.
func Collect(ctx context.Context, config, id string, independent bool) (SourceResult, error) {
	var result SourceResult
	err := invokeSource(ctx, "source_collect.py", config, id, &result, independent)
	if err == nil && (!filepath.IsAbs(result.Directory) || len(result.Files) == 0) {
		err = errors.New("original source result is invalid")
	}
	return result, err
}

func CollectProfileDirectory(ctx context.Context, config, id string) (SourceResult, error) {
	var result SourceResult
	err := invokeSource(ctx, "source_collect_directory.py", config, id, &result)
	if err == nil && (!filepath.IsAbs(result.Directory) || len(result.Files) != 1) {
		err = errors.New("original profile directory result is invalid")
	}
	return result, err
}

func invokeSource(ctx context.Context, script, config, id string, result any, independent ...bool) error {
	if !filepath.IsAbs(config) {
		return errors.New("original source connection not configured")
	}
	folder, e := os.MkdirTemp("", "workbench-source-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(folder)
	e = fs.WalkDir(scripts, ".", func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(folder, path), 0700)
		}
		data, e := scripts.ReadFile(path)
		if e != nil {
			return e
		}
		mode := os.FileMode(0600)
		if path == "source_ssh.py" {
			mode = 0700
		}
		return os.WriteFile(filepath.Join(folder, path), data, mode)
	})
	if e != nil {
		return e
	}
	args := []string{filepath.Join(folder, script), config, id}
	if len(independent) != 0 && independent[0] {
		args = append(args, "independent")
	}
	command := exec.Command("python3", args...)
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var output bytes.Buffer
	command.Stdout = &output
	if e = command.Start(); e != nil {
		return e
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	canceled := false
	select {
	case e = <-done:
	case <-ctx.Done():
		canceled = true
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
	}
	// Also drain a helper left behind after an early parent failure. Do not
	// release the project's fence while its source process group still runs.
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(-command.Process.Pid, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		} else {
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if canceled {
		<-done
		return ctx.Err()
	}
	if e != nil {
		return errors.New("original source collection failed; inspect the original task's source step")
	}
	if e = json.Unmarshal(output.Bytes(), result); e != nil {
		return errors.New("original source returned an incomplete result")
	}
	return nil
}

func CleanupCollected(ctx context.Context, config, id string) error {
	return invokeSource(ctx, "source_cleanup.py", config, id, &map[string]any{})
}
