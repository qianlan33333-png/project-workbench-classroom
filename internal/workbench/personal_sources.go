package workbench

import (
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

type sourceQuery struct {
	ProjectID string `json:"projectId"`
	Kind      string `json:"kind"`
	Key       string `json:"key"`
	Query     string `json:"query"`
	SessionID string `json:"sessionId"`
	Offset    int    `json:"offset"`
}

// Only project-owned published text, visible conversation turns and task descriptions
// are exposed. Neither SQL nor a path outside the selected project is caller supplied.
func (s *Store) personalSource(q sourceQuery, read bool) (any, error) {
	p, e := s.Project(q.ProjectID)
	if e != nil || p.Archived || p.ID == personalProjectID {
		return nil, ErrNotFound
	}
	if q.Offset < 0 || q.Offset > 4<<20 || len(q.Query) > 800 {
		return nil, errors.New("查询范围无效")
	}
	if !read {
		var sql string
		var args []any
		switch q.Kind {
		case "file":
			sql = "SELECT path,path FROM documents WHERE project_id=? AND (instr(lower(path),lower(?)) OR instr(lower(content),lower(?))) ORDER BY path"
			args = []any{p.ID, q.Query, q.Query}
		case "conversation":
			sql = `SELECT r.id,s.data->>'$.title' || ' · ' || substr(r.data->>'$.text',1,80) FROM runs r JOIN sessions s ON s.id=r.session_id
   WHERE r.project_id=? AND s.project_id=r.project_id AND coalesce(s.data->>'$.recycledAt',0)=0 AND coalesce(r.data->>'$.kind','')<>'archive'
   AND (?='' OR r.session_id=?) AND (instr(r.id,lower(?))=1 OR instr(lower(s.data->>'$.title'),lower(?)) OR instr(lower(r.data->>'$.text'),lower(?)) OR instr(lower(r.data->>'$.result'),lower(?))) ORDER BY r.created_at DESC,r.rowid DESC`
			args = []any{p.ID, q.SessionID, q.SessionID, strings.TrimPrefix(strings.TrimPrefix(q.Query, "#T"), "#t"), q.Query, q.Query, q.Query}
		case "task":
			sql = `SELECT id,data->>'$.title' FROM project_tasks WHERE project_id=? AND (instr(lower(data->>'$.title'),lower(?)) OR instr(lower(data->>'$.description'),lower(?))) ORDER BY data->>'$.updatedAt' DESC,id`
			args = []any{p.ID, q.Query, q.Query}
		default:
			return nil, errors.New("请选择 file、conversation 或 task")
		}
		rows, e := s.DB.Query(sql+" LIMIT 21 OFFSET ?", append(args, q.Offset)...)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		hits := []map[string]string{}
		for rows.Next() {
			var key, title string
			if e = rows.Scan(&key, &title); e != nil {
				return nil, e
			}
			hits = append(hits, map[string]string{"key": key, "title": title})
		}
		if e = rows.Err(); e != nil {
			return nil, e
		}
		return map[string]any{"project": p.Name, "projectId": p.ID, "kind": q.Kind, "hits": hits[:min(20, len(hits))], "hasMore": len(hits) > 20}, nil
	}
	var title, body string
	switch q.Kind {
	case "file":
		title = q.Key
		body, e = s.readPersonalText(p.ID, q.Key)
	case "conversation":
		r, er := s.Run(q.Key)
		if er != nil || r.ProjectID != p.ID || r.Kind == "archive" {
			return nil, ErrNotFound
		}
		session, er := s.Session(r.SessionID)
		if er != nil {
			return nil, er
		}
		title = session.Title + " · #T" + r.ID[:8]
		body = "用户：" + r.Text + "\n助手：" + r.Result + "\n状态：" + stateLabel(r.State)
	case "task":
		t, er := s.Task(q.Key)
		if er != nil || t.ProjectID != p.ID {
			return nil, ErrNotFound
		}
		title = t.Title
		body = t.Title + "\n" + t.Description + "\n状态：" + t.State + "\n安排日期：" + t.ScheduledDate + "\n截止日期：" + t.DueDate
	default:
		return nil, errors.New("未知资料类型")
	}
	if e != nil {
		return nil, e
	}
	result := sourceExcerpt(body, q.Offset)
	result["project"] = p.Name
	result["projectId"] = p.ID
	result["source"] = title
	result["key"] = q.Key
	result["kind"] = q.Kind
	return result, nil
}
func (s *Store) readPersonalText(project, path string) (string, error) {
	if e := validPath(path); e != nil {
		return "", e
	}
	root, e := s.openRoot(project)
	if e != nil {
		return "", e
	}
	defer root.Close()
	f, e := root.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return "", e
	}
	if !st.Mode().IsRegular() || st.Size() > maxEditableFile {
		return "", errors.New("仅支持 2 MiB 以内的文本原件")
	}
	b, e := io.ReadAll(io.LimitReader(f, maxEditableFile+1))
	if e != nil {
		return "", e
	}
	if len(b) > maxEditableFile || !utf8.Valid(b) || strings.IndexByte(string(b), 0) >= 0 {
		return "", errors.New("当前格式无法读取为文本")
	}
	return string(b), nil
}
func sourceExcerpt(body string, offset int) map[string]any {
	runes := []rune(body)
	start := min(offset, len(runes))
	end := min(start+6000, len(runes))
	return map[string]any{"text": string(runes[start:end]), "offset": start, "nextOffset": end, "hasMore": end < len(runes)}
}
