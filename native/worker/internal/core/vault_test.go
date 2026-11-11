package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestVaultMarkdownDirectory(t *testing.T) {
	const note = "方法/运营说明.md"
	body := "---\ntags: [运营]\n---\n\n[[服务说明]]\n"
	notes, err := applyVaultChanges(nil, []VaultChange{{Path: note, Text: &body}})
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	ctx := context.Background()
	records, fingerprint, err := writeVaultVersion(ctx, parent, "version", notes)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(parent, "version", filepath.FromSlash(note)))
	if err != nil || string(got) != body || len(records) != 1 || records[0].Fields["name"] != note {
		t.Fatalf("Markdown not written exactly: %q, %v, %v", got, records, err)
	}
	if _, retry, err := writeVaultVersion(ctx, parent, "version", notes); err != nil || retry != fingerprint {
		t.Fatalf("same version retry changed: %q, %v", retry, err)
	}
	if _, _, err = writeVaultVersion(ctx, parent, "version", map[string]string{note: "不同正文"}); err == nil {
		t.Fatal("overwrote an existing version")
	}
	moved, err := applyVaultChanges(records, []VaultChange{{Path: note}, {Path: "新目录/说明.md", Text: &body}})
	if err != nil || len(moved) != 1 || moved["新目录/说明.md"] != body {
		t.Fatalf("move failed: %v, %v", moved, err)
	}
}

func TestVaultPathsAreConfined(t *testing.T) {
	text := ""
	for _, path := range []string{"../外部.md", "/绝对.md", ".obsidian/配置.md", "目录//笔记.md", "目录\\笔记.md", "文档.html"} {
		if _, err := applyVaultChanges(nil, []VaultChange{{Path: path, Text: &text}}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q unexpectedly accepted: %v", path, err)
		}
	}
	changes := []VaultChange{{Path: "Notes/One.md", Text: &text}, {Path: "notes/Two.md", Text: &text}}
	if _, err := applyVaultChanges(nil, changes); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ambiguous folder spelling accepted: %v", err)
	}
}
