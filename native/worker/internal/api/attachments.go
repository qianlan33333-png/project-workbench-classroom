package api

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

func (a *Server) attachmentTools(s *mcp.Server, p core.Principal) {
	host, ok := a.Host.(engine.AttachmentHost)
	if !ok {
		return
	}
	mcp.AddTool(s, &mcp.Tool{Name: "attachment_list", Description: "List files actually uploaded by the owner in this native Session. Does not expose filesystem paths or other Sessions."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, struct {
		Files []engine.Attachment `json:"files"`
	}, error) {
		files, e := host.Attachments(ctx, p, "")
		return nil, struct {
			Files []engine.Attachment `json:"files"`
		}{files}, e
	})
	mcp.AddTool(s, &mcp.Tool{Name: "attachment_import", Description: "Register one owner-uploaded UTF-8 text file (up to 256KiB) as a resource in this project; returns a reference, not full text. Read with resource_read/resource_excerpt. Binary documents require an installed standard document connector; original native attachments remain intact."}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		ID string `json:"attachmentId"`
	}) (*mcp.CallToolResult, core.Ref, error) {
		files, e := host.Attachments(ctx, p, in.ID)
		if e != nil {
			return nil, core.Ref{}, e
		}
		if in.ID == "" || len(files) != 1 || files[0].ID != in.ID {
			return nil, core.Ref{}, core.ErrDenied
		}
		f := files[0]
		if len(f.Text) > 256*1024 {
			return nil, core.Ref{}, core.ErrInvalid
		}
		id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("attachment/"+p.Kind+"/"+p.ProjectID+"/"+f.ID)).String()
		ref := core.Ref{ResourceID: id, Version: "original", Section: "content"}
		owner := core.Principal{Kind: "owner", SessionID: "native-attachment-import"}
		if _, e = a.Store.Resource(ctx, id); core.IsMissing(e) {
			e = a.Store.PutResource(ctx, owner, core.Resource{ID: id, Name: f.Name, OwnerProject: p.ProjectID, Sections: []core.Section{{ID: "content", Name: "附件正文", Fields: []string{"name", "text", "attachmentId"}}}})
			if e != nil {
				if _, readErr := a.Store.Resource(ctx, id); readErr != nil {
					return nil, ref, e
				}
			}
		} else if e != nil {
			return nil, ref, e
		}
		if _, e = a.Store.Snapshot(ctx, id, "original"); core.IsMissing(e) {
			e = a.Store.BeginSnapshot(ctx, owner, core.Snapshot{ResourceID: id, Version: "original"})
			if e != nil {
				return nil, ref, e
			}
		}
		if e = core.Authorize(ctx, a.Store, p, ref); e != nil {
			// Authorize needs a published snapshot; resource ownership is fixed above.
			r, err := a.Store.Resource(ctx, id)
			if err != nil || r.OwnerProject != p.ProjectID {
				return nil, ref, core.ErrDenied
			}
		}
		snap, e := a.Store.Snapshot(ctx, id, "original")
		if e != nil {
			return nil, ref, e
		}
		if snap.State != "published" {
			records := attachmentRecords(f)
			e = a.Store.Stage(ctx, owner, id, "original", records)
			if e != nil {
				return nil, ref, e
			}
			_, e = a.Store.Publish(ctx, owner, id, "original", len(records))
			if e != nil {
				return nil, ref, e
			}
		}
		if e = core.Authorize(ctx, a.Store, p, ref); e != nil {
			return nil, ref, fmt.Errorf("%w: attachment resource", e)
		}
		return nil, ref, a.Store.RememberRead(ctx, p, []core.Ref{ref})
	})
}

// Bound serialized records even under JSON escaping. Keep every character and
// the immutable native file reference instead of truncating the document.
func attachmentRecords(f engine.Attachment) []core.Record {
	runes := []rune(f.Text)
	out := []core.Record{}
	for offset := 0; offset < len(runes) || offset == 0; offset += 4096 {
		end := min(offset+4096, len(runes))
		out = append(out, core.Record{ID: fmt.Sprintf("part-%06d", len(out)+1), Fields: map[string]any{"name": f.Name, "text": string(runes[offset:end]), "attachmentId": f.ID}})
		if end == len(runes) {
			break
		}
	}
	return out
}
