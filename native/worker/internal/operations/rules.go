package operations

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

//go:embed rules.py hxc_original.py writing.py raw_transport.py source_*.py original/*.py
var scripts embed.FS

// Evaluate invokes only the embedded original pure rules. No caller-supplied
// command, path, shell, credential or provider is accepted.
func Evaluate(ctx context.Context, input any, output any) error {
	root, err := os.MkdirTemp("", "workbench-operations-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	err = fs.WalkDir(scripts, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(root, path), 0700)
		}
		data, err := scripts.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, path), data, 0600)
	})
	if err != nil {
		return err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "python3", filepath.Join(root, "rules.py"))
	command.Stdin = bytes.NewReader(body)
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	var out bytes.Buffer
	command.Stdout = &out
	if err = command.Run(); err != nil {
		return errors.New("original rules rejected input; inspect source completeness")
	}
	return json.Unmarshal(out.Bytes(), output)
}
