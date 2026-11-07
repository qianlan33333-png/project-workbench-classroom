package api

import (
	"strings"
	"testing"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

func TestAttachmentChunksPreserveWorstCaseText(t *testing.T) {
	for _, text := range []string{strings.Repeat("\\", 256*1024), strings.Repeat("\x01", 256*1024), strings.Repeat("文档😀", 20000), ""} {
		records := attachmentRecords(engine.Attachment{ID: "sha256:example", Name: "test.txt", Text: text})
		if e := core.ValidateRecords(records); e != nil {
			t.Fatal(e)
		}
		var joined strings.Builder
		for _, record := range records {
			joined.WriteString(record.Fields["text"].(string))
		}
		if joined.String() != text {
			t.Fatal("file content changed")
		}
	}
}
