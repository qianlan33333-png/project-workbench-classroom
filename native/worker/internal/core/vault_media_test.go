package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestVaultDirectoryMediaRejectsLinksOutsideItsRegisteredFolder(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "published")
	if err := os.Mkdir(inside, 0700); err != nil {
		t.Fatal(err)
	}
	file := []byte("%PDF-1.7 fixture")
	for _, folder := range []string{root, inside} {
		if err := os.WriteFile(filepath.Join(folder, "file.pdf"), file, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../file.pdf", filepath.Join(inside, "outside.pdf")); err != nil {
		t.Fatal(err)
	}
	got, err := readDirectoryMedia(root, "published", "file.pdf")
	if err != nil || string(got) != string(file) {
		t.Fatalf("read authorized attachment: %q %v", got, err)
	}
	if _, err = readDirectoryMedia(root, "published", "outside.pdf"); !errors.Is(err, ErrDenied) {
		t.Fatalf("outside attachment not denied: %v", err)
	}
}
