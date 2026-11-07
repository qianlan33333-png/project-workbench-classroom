package core

import (
	"strings"
	"testing"
)

func TestSectionProjectionOnlySeesAllowedFields(t *testing.T) {
	s := Section{ID: "profile", Fields: []string{"needs"}, Select: map[string]string{"judgment": "/needs/current", "evidence": "/needs/evidence/0", "secret": "/secret", "missing": "/needs/missing"}}
	got, e := projectFields(s, map[string]any{"needs": map[string]any{"current": "supported fact", "evidence": []any{"source one", strings.Repeat("x", 30000)}}, "secret": "must not leak"})
	if e != nil {
		t.Fatal(e)
	}
	if got["secret"] != nil || got["missing"] != nil || got["evidence"] != "source one" {
		t.Fatalf("incorrect projection: %+v", got)
	}
	if _, e = PrepareInput(Method{RequiredFields: []string{"judgment"}}, []Record{{ID: "one", Fields: got}}); e != nil {
		t.Fatal(e)
	}
	if _, e = PrepareInput(Method{RequiredFields: []string{"secret"}}, []Record{{ID: "one", Fields: got}}); e == nil {
		t.Fatal("missing required evidence accepted")
	}
}
