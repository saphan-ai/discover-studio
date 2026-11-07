package todo_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/saphan-ai/discover/internal/scan/todo"
)

func TestScanFindsEachMarkerWithItsLine(t *testing.T) {
	content := strings.Join([]string{
		"package x",
		"// TODO: tidy",
		"func f() {} // FIXME later",
		"# HACK around the cache",
		"/* XXX */",
		"var todo = 1",
		"var TODOS = 2",
		"// TODO and FIXME on one line",
		"no marker here",
	}, "\n")
	fs := todo.Scan("a/b.go", []byte(content))
	var got []string
	for _, f := range fs {
		if f.Kind != "todo" || f.Source.File != "a/b.go" || f.Severity != "info" {
			t.Fatalf("finding = %+v", f)
		}
		got = append(got, fmt.Sprintf("%d:%s", f.Source.Line, f.Text))
	}
	want := []string{"2:TODO", "3:FIXME", "4:HACK", "5:XXX", "8:TODO"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("findings = %v, want %v", got, want)
	}
}

func TestScanNeverCopiesTheLineText(t *testing.T) {
	fs := todo.Scan("f", []byte("// TODO: rotate the credential before release\n"))
	if len(fs) != 1 || strings.Contains(fs[0].Text, "credential") {
		t.Fatalf("findings = %+v", fs)
	}
}

func TestScanReturnsEveryMarkerBeyondTheListLimit(t *testing.T) {
	content := strings.Repeat("// TODO x\n", 45)
	if n := len(todo.Scan("f", []byte(content))); n != 45 {
		t.Fatalf("markers = %d, want 45: the limit belongs to the caller, which must count them all", n)
	}
	if todo.Max != 30 {
		t.Fatalf("Max = %d, want 30", todo.Max)
	}
}
