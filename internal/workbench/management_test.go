package workbench

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestExpertManagementSnapshotsAndReferences(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "专家验收")
	ex, e := s.CreateExpert("原专家", "原说明", "旧方法")
	if e != nil {
		t.Fatal(e)
	}
	original, e := s.ReadExpertFile(ex.ID, ex.Digest, "SKILL.md")
	if e != nil {
		t.Fatal(e)
	}
	withUnknown := bytes.Replace(original, []byte("description:"), []byte("custom-field:\n  nested: 保留我\ndescription:"), 1)
	ex, e = s.ChangeExpert(ex.ID, ExpertChanges{BaseDigest: ex.Digest, Files: []ExpertFileChange{{Op: "write", Path: "SKILL.md", Content: string(withUnknown)}}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	p.ExpertIDs = []string{ex.ID}
	p.DefaultExpertID = ex.ID
	p, e = s.UpdateProject(p.ID, p)
	if e != nil {
		t.Fatal(e)
	}
	ss, e := s.CreateSession(p.ID, "专题", "topic", ex.ID)
	if e != nil {
		t.Fatal(e)
	}
	sc, e := s.SaveSchedule(Schedule{ProjectID: p.ID, ExpertID: ex.ID, Name: "绑定任务", Instruction: "执行", Cron: "0 9 * * *", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	m := submitRoute(t, s, Incoming{Source: "web", EventID: newID(), SessionID: ss.ID, Text: "使用专家"})
	run, e := s.Run(m.RunID)
	if e != nil {
		t.Fatal(e)
	}
	fixed := ex.Digest
	newer, e := s.ChangeExpert(ex.ID, ExpertChanges{BaseDigest: fixed, Metadata: map[string]string{"name": "新专家", "description": "新说明"}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	data, e := s.ReadExpertFile(ex.ID, newer.Digest, "SKILL.md")
	if e != nil || !strings.Contains(string(data), "nested: 保留我") {
		t.Fatal(string(data), e)
	}
	old, _ := s.ExpertVersion(ex.ID, fixed)
	if old.Name != "原专家" {
		t.Fatal(old)
	}
	if _, e = s.ChangeExpert(ex.ID, ExpertChanges{BaseDigest: fixed, Metadata: map[string]string{"name": "过期"}}, nil); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	newer, e = s.ChangeExpert(ex.ID, ExpertChanges{BaseDigest: newer.Digest, Files: []ExpertFileChange{{Op: "write", Path: "参考/附件.bin", Upload: "file0"}, {Op: "mkdir", Path: "空目录"}}}, map[string][]byte{"file0": {0, 255, 3}})
	if e != nil {
		t.Fatal(e)
	}
	entries, _ := s.ExpertFiles(ex.ID, newer.Digest)
	found := false
	for _, f := range entries {
		if f.Path == "空目录" && f.Directory {
			found = true
		}
	}
	if !found {
		t.Fatal("empty directory missing")
	}
	copy, e := s.CopyExpert(ex.ID)
	if e != nil || copy.SkillName == newer.SkillName {
		t.Fatal(copy, e)
	}
	var archive bytes.Buffer
	if e = s.ExportExpert(ex.ID, newer.Digest, &archive); e != nil {
		t.Fatal(e)
	}
	z, e := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if e != nil {
		t.Fatal(e)
	}
	var binary []byte
	for _, f := range z.File {
		if f.Name == "参考/附件.bin" {
			r, _ := f.Open()
			binary, _ = io.ReadAll(r)
			r.Close()
		}
	}
	if !bytes.Equal(binary, []byte{0, 255, 3}) {
		t.Fatal(binary)
	}
	if e = s.DeleteExpert(ex.ID, newer.Digest); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Expert(ex.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	p, _ = s.Project(p.ID)
	ss, _ = s.Session(ss.ID)
	sc, _ = s.Schedule(sc.ID)
	if len(p.ExpertIDs) != 0 || p.DefaultExpertID != "" || ss.ExpertID != "general" || sc.Enabled || sc.ExpertID != "" || sc.PausedReason == "" {
		t.Fatal(p, ss, sc)
	}
	after, _ := s.Run(run.ID)
	if after.ExpertDigest != fixed || after.ExpertName != "原专家" {
		t.Fatal(after)
	}
	// Runtime config must load the immutable directory without a current catalogue row.
	h := &Harness{Store: s, Config: Config{DataDir: s.Root, RuntimeMode: "native", HarnessCommand: []string{"/usr/bin/true"}}}
	home := filepath.Join(s.Root, "runtime", "fixed-test")
	os.MkdirAll(home, 0700)
	_, _, _, e = h.prepare(RuntimeInput{Project: p, Session: ss, Run: run})
	if e != nil {
		t.Fatal("deleted expert broke queued run", e)
	}
}

func TestExpertChangesAreAtomicAndRejectInvalidEntry(t *testing.T) {
	s := testStore(t)
	ex, e := s.CreateExpert("方法", "说明", "正文")
	if e != nil {
		t.Fatal(e)
	}
	for _, op := range []ExpertFileChange{{Op: "delete", Path: "SKILL.md"}, {Op: "rename", Path: "SKILL.md", To: "other.md"}, {Op: "write", Path: "../escape", Content: "bad"}, {Op: "write", Path: "SKILL.md", Content: "missing yaml"}} {
		if _, e = s.ChangeExpert(ex.ID, ExpertChanges{BaseDigest: ex.Digest, Files: []ExpertFileChange{{Op: "write", Path: "must-not-exist.md", Content: "partial"}, op}}, nil); e == nil {
			t.Fatal("invalid change succeeded", op)
		}
		current, _ := s.Expert(ex.ID)
		if current.Digest != ex.Digest {
			t.Fatal("partial version published")
		}
		if _, e = s.ReadExpertFile(ex.ID, ex.Digest, "must-not-exist.md"); !os.IsNotExist(e) {
			t.Fatal(e)
		}
	}
	p := testProject(t, s, "移除专家")
	p.ExpertIDs = []string{ex.ID}
	p.DefaultExpertID = ex.ID
	p, _ = s.UpdateProject(p.ID, p)
	ss, _ := s.CreateSession(p.ID, "专题", "topic", ex.ID)
	sc, _ := s.SaveSchedule(Schedule{ProjectID: p.ID, ExpertID: ex.ID, Name: "绑定", Instruction: "执行", Cron: "0 9 * * *", Enabled: true})
	p.ExpertIDs = []string{}
	p.DefaultExpertID = ""
	if _, e = s.UpdateProject(p.ID, p); e != nil {
		t.Fatal(e)
	}
	ss, _ = s.Session(ss.ID)
	sc, _ = s.Schedule(sc.ID)
	if ss.ExpertID != "general" || sc.Enabled || sc.PausedReason == "" {
		t.Fatal(ss, sc)
	}
}

func TestFileUploadDirectoriesConflictAndViewLimit(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "文件验收")
	if e := s.CreateDirectory(p.ID, "资料/空目录"); e != nil {
		t.Fatal(e)
	}
	first, _, e := s.UploadFile(p.ID, "资料/中文.MD", []byte("中文品牌资料"), "", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.UploadFile(p.ID, first.Path, []byte("覆盖"), "", ""); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, skip, e := s.UploadFile(p.ID, first.Path, []byte("跳过"), "", "skip"); !skip || e != nil {
		t.Fatal(e)
	}
	renamed, _, e := s.UploadFile(p.ID, first.Path, []byte("副本"), "", "rename")
	if e != nil || renamed.Path != "资料/中文 (2).MD" {
		t.Fatal(renamed, e)
	}
	if _, _, e = s.UploadFile(p.ID, first.Path, []byte("替换"), first.After, "replace"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveFile(p.ID, "说明.markdown", []byte("Markdown 扩展品牌"), ""); e != nil {
		t.Fatal(e)
	}
	if rows, e := s.Search(p.ID, "扩展品牌"); e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	large := bytes.Repeat([]byte("a"), maxEditableFile+1)
	s.SaveFile(p.ID, "large.txt", large, "")
	hash, _ := PasswordHash("management-test-password")
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	(&API{Store: s, Config: Config{PasswordHash: hash}}).Mount(router)
	req := httptest.NewRequest("GET", "/api/v1/projects/"+p.ID+"/file?path=large.txt&view=1", nil)
	req.SetBasicAuth("owner", "management-test-password")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	var body map[string]any
	json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != 200 || body["tooLarge"] != true || body["editable"] != false || body["content"] != "" {
		t.Fatal(response.Code, body)
	}
	var zipData bytes.Buffer
	z := zip.NewWriter(&zipData)
	f, _ := z.Create("vault/.obsidian/config.json")
	f.Write([]byte("{}"))
	z.Close()
	if e = s.ImportZipTo(p.ID, bytes.NewReader(zipData.Bytes()), int64(zipData.Len()), "导入"); e != nil {
		t.Fatal(e)
	}
	if b, _, e := s.ReadFile(p.ID, "导入/vault/.obsidian/config.json"); e != nil || string(b) != "{}" {
		t.Fatal(e)
	}
	if e = s.ImportZipTo(p.ID, bytes.NewReader(zipData.Bytes()), int64(zipData.Len()), "导入"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}

func TestExpertMultipartAPI(t *testing.T) {
	s := testStore(t)
	ex, _ := s.CreateExpert("上传专家", "说明", "方法")
	hash, _ := PasswordHash("management-test-password")
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	(&API{Store: s, Config: Config{PasswordHash: hash}}).Mount(r)
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	form.WriteField("changes", encode(ExpertChanges{BaseDigest: ex.Digest, Files: []ExpertFileChange{{Op: "write", Path: "附件/a.bin", Upload: "file0"}}}))
	f, _ := form.CreateFormFile("file0", "a.bin")
	f.Write([]byte{0, 255, 1})
	form.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/experts/"+ex.ID+"/changes", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.SetBasicAuth("owner", "management-test-password")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	var updated Expert
	json.Unmarshal(res.Body.Bytes(), &updated)
	b, e := s.ReadExpertFile(ex.ID, updated.Digest, "附件/a.bin")
	if e != nil || !bytes.Equal(b, []byte{0, 255, 1}) {
		t.Fatal(b, e)
	}
}
