package engine

import (
	"context"
	"workbench-native/internal/core"
)

type Attachment struct {
	ID    string `json:"attachmentId"`
	Name  string `json:"name"`
	Bytes int    `json:"bytes"`
	Text  string `json:"text,omitempty"`
}
type AttachmentHost interface {
	Attachments(context.Context, core.Principal, string) ([]Attachment, error)
}
