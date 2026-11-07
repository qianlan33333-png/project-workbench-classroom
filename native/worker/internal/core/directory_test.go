package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKnowledgeDirectoryIsBoundedAndConfined(t *testing.T) {
	root := t.TempDir()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(os.WriteFile(filepath.Join(root, "guide.md"), []byte("# 资料\n第一版"), 0600))
	must(os.Mkdir(filepath.Join(root, ".private"), 0700))
	must(os.WriteFile(filepath.Join(root, ".private", "secret.txt"), []byte("private"), 0600))
	must(os.WriteFile(filepath.Join(root, "data.bin"), []byte("binary"), 0600))
	rows, first, e := directoryRecords(context.Background(), root, ".")
	must(e)
	if len(rows) != 1 || rows[0].Fields["name"] != "guide.md" {
		t.Fatal("unexpected files", rows)
	}
	id := rows[0].ID
	must(os.WriteFile(filepath.Join(root, "guide.md"), []byte("# 资料\n第二版"), 0600))
	rows, second, e := directoryRecords(context.Background(), root, ".")
	must(e)
	if first == second || rows[0].ID != id {
		t.Fatal("unstable identity or missing change digest")
	}
	if _, _, e = directoryRecords(context.Background(), root, ".."); !errors.Is(e, ErrDenied) {
		t.Fatal("parent traversal", e)
	}
	outside := t.TempDir()
	must(os.WriteFile(filepath.Join(outside, "secret.md"), []byte("outside"), 0600))
	must(os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(root, "link.md")))
	if _, _, e = directoryRecords(context.Background(), root, "."); !errors.Is(e, ErrDenied) {
		t.Fatal("symlink accepted", e)
	}
	must(os.Remove(filepath.Join(root, "link.md")))
	must(os.WriteFile(filepath.Join(root, "large.md"), []byte(strings.Repeat("a", 256*1024+1)), 0600))
	if _, _, e = directoryRecords(context.Background(), root, "."); !errors.Is(e, ErrInvalid) {
		t.Fatal("oversize accepted", e)
	}
	must(os.Remove(filepath.Join(root, "large.md")))
	must(os.WriteFile(filepath.Join(root, "invalid.txt"), []byte{0xff}, 0600))
	if _, _, e = directoryRecords(context.Background(), root, "."); !errors.Is(e, ErrInvalid) {
		t.Fatal("invalid UTF8 accepted", e)
	}
}
