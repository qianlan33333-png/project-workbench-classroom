package workbench

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Resolve references inside the pinned package without exposing host tools to the
// summarizer. References can themselves link to sibling documents.
func expertReferences(dir, skill string) (string, error) {
	root, e := os.OpenRoot(dir)
	if e != nil {
		return "", e
	}
	defer root.Close()
	var files []string
	e = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if p != "SKILL.md" && d.Type().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	if e != nil {
		return "", e
	}
	type doc struct{ name, body string }
	queue := []doc{{"SKILL.md", skill}}
	seen := map[string]bool{"SKILL.md": true}
	var result strings.Builder
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, name := range files {
			if seen[name] {
				continue
			}
			sibling, err := filepath.Rel(filepath.Dir(cur.name), name)
			if err != nil {
				return "", err
			}
			sibling = filepath.ToSlash(sibling)
			dirReference := filepath.ToSlash(filepath.Dir(sibling))
			// An explicit directory link in the method also includes its text resources.
			listedDir := dirReference != "." && (strings.Contains(cur.body, "`"+dirReference+"/`") || strings.Contains(cur.body, "("+dirReference+"/)"))
			if !listedDir && !strings.Contains(cur.body, name) && !strings.Contains(cur.body, sibling) {
				continue
			}
			b, e := root.ReadFile(name)
			if e != nil {
				return "", e
			}
			if !utf8.Valid(b) || strings.IndexByte(string(b), 0) >= 0 {
				return "", fmt.Errorf("专家引用 %s 不能作为文本读取", name)
			}
			seen[name] = true
			queue = append(queue, doc{name, string(b)})
			fmt.Fprintf(&result, "\n专家引用文件 %s：\n%s\n", name, b)
		}
	}
	return result.String(), nil
}
