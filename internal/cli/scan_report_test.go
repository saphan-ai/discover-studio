package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saphan-ai/discover-studio/internal/attest"
	"github.com/saphan-ai/discover-studio/internal/manifest"
	"github.com/saphan-ai/discover-studio/internal/model"
	scanpkg "github.com/saphan-ai/discover-studio/internal/scan"
)

// goProject writes a small Go project with a Makefile into a fresh directory and returns it.
func goProject(t *testing.T) string {
	t.Helper()
	dir := temp(t)
	files := map[string]string{
		"go.mod":       "module example.com/demo\n\ngo 1.26\n",
		"main.go":      "package main\n\nfunc main() {}\n",
		"Makefile":     "build:\n\tgo build ./...\n\ntest:\n\tgo test ./...\n",
		"AGENTS.md":    "# rules\n",
		"README.md":    "# demo\n",
		"cmd/x/x.go":   "package main\n\nfunc main() {}\n",
		"docs/note.md": "TODO: write the docs\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestScanWritesReportAndJSON is the test this change turns green: before it, scan wrote only READ_MANIFEST.json and
// ATTESTATION.json, and the manifest was empty.
func TestScanWritesReportAndJSON(t *testing.T) {
	target := goProject(t)
	out := filepath.Join(temp(t), "out")
	code, stdout, stderr := run(t, "--out", out, target)
	if code != ExitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, name := range outputFiles {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("%s not written: %v", name, err)
		}
	}
	if !strings.Contains(stdout, "found: ") || !strings.Contains(stdout, "wrote: REPORT.md") {
		t.Errorf("stdout names neither the findings nor the files written:\n%s", stdout)
	}

	js, err := os.ReadFile(filepath.Join(out, jsonFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := model.Validate(js); err != nil {
		t.Fatalf("discover.json does not validate against its own schema: %v", err)
	}
	var r model.Report
	if err := json.Unmarshal(js, &r); err != nil {
		t.Fatal(err)
	}
	rp := r.Repos[0]
	if len(rp.Stack) == 0 {
		t.Error("the stack scanner found nothing in a go.mod project")
	}
	if len(rp.Commands) == 0 {
		t.Error("the commands scanner cited nothing from a Makefile")
	}
	for _, c := range rp.Commands {
		if c.Executed {
			t.Errorf("command %q is marked executed", c.Text)
		}
	}
	if rp.Attestation.ReadOnlyProof.Status != "EQUAL" || !r.Scan.ReadOnlyProofAllEqual {
		t.Errorf("read-only proof %q, all-equal %v", rp.Attestation.ReadOnlyProof.Status, r.Scan.ReadOnlyProofAllEqual)
	}
	if rp.Attestation.ReadManifest.FilesRead == 0 {
		t.Error("discover.json's read manifest is empty although the scanners read files")
	}
	prof := r.WorkspaceProfile.Repos[0]
	if len(prof.Commands.Build) == 0 || len(prof.Commands.Test) == 0 {
		t.Errorf("profile commands: build %d, test %d", len(prof.Commands.Build), len(prof.Commands.Test))
	}
	if len(prof.ConventionFiles) != 1 || prof.ConventionFiles[0] != "AGENTS.md" {
		t.Errorf("profile convention files %v, want [AGENTS.md]", prof.ConventionFiles)
	}

	md, err := os.ReadFile(filepath.Join(out, reportFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## 1. ", "## 10. ", "Read-only proof | EQUAL"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("REPORT.md lacks %q", want)
		}
	}

	mfst, err := os.ReadFile(filepath.Join(out, manifestFile))
	if err != nil {
		t.Fatal(err)
	}
	var mm struct {
		Entries []json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(mfst, &mm); err != nil {
		t.Fatal(err)
	}
	if len(mm.Entries) == 0 {
		t.Error("READ_MANIFEST.json is empty although the scanners read files")
	}

	// No output names the absolute root unless --record-root asks for it.
	for _, name := range outputFiles {
		b, _ := os.ReadFile(filepath.Join(out, name))
		if strings.Contains(string(b), target) {
			t.Errorf("%s names the absolute root", name)
		}
	}
}

// TestScannerFailureWritesNoReport is the negative control (field: the scanners hook). A scanner that fails stops the
// scan before any output file is written, so no half report is left behind.
func TestScannerFailureWritesNoReport(t *testing.T) {
	old := scanners
	scanners = func(string, attest.Mode, attest.Options, *manifest.Manifest) (scanpkg.Result, error) {
		return scanpkg.Result{}, errors.New("planted scanner failure")
	}
	t.Cleanup(func() { scanners = old })

	target := goProject(t)
	out := filepath.Join(temp(t), "out")
	code, _, stderr := run(t, "--out", out, target)
	if code != ExitUsage {
		t.Fatalf("exit %d, want %d; stderr %q", code, ExitUsage, stderr)
	}
	if !strings.Contains(stderr, "planted scanner failure") {
		t.Errorf("stderr does not name the failure: %q", stderr)
	}
	for _, name := range outputFiles {
		if _, err := os.Stat(filepath.Join(out, name)); err == nil {
			t.Errorf("%s written although the scan failed", name)
		}
	}
}
