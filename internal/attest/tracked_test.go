package attest_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/saphan-ai/discover-studio/internal/attest"
	"github.com/saphan-ai/discover-studio/internal/fixture"
)

func TestTrackedFilesLeavesOutUntrackedFiles(t *testing.T) {
	dir := t.TempDir()
	fixture.GitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "later.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := attest.TrackedFiles(dir, attest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	all, err := attest.ListFiles(dir, attest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(all, "later.txt") || contains(got, "later.txt") {
		t.Fatalf("ListFiles has later.txt: %v; TrackedFiles has it: %v", contains(all, "later.txt"), contains(got, "later.txt"))
	}
	if !contains(got, "a.txt") || !contains(got, "sub/deep/e file.md") {
		t.Fatalf("tracked = %q", got)
	}
}

func TestTrackedFilesListsATrackedFileThatAGitignoreHides(t *testing.T) {
	dir := t.TempDir()
	fixture.GitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("x=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture.Git(t, dir, "add", "-f", ".env")
	fixture.Git(t, dir, "commit", "-q", "-m", "env")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := attest.TrackedFiles(dir, attest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(got, ".env") {
		t.Fatalf("tracked = %q", got)
	}
	if !reflect.DeepEqual(got, sorted(got)) {
		t.Fatalf("not sorted: %q", got)
	}
}

func TestTrackedFilesRefusesAPlainDirectoryByItsBaseName(t *testing.T) {
	dir := t.TempDir()
	_, err := attest.TrackedFiles(dir, attest.Options{})
	if err == nil {
		t.Fatal("a plain directory gave no error")
	}
	if strings.Contains(err.Error(), dir) {
		t.Fatalf("the error names the absolute root: %v", err)
	}
}

// pointAtAnotherRepository sets the three variables that would send a careless git command to another repository, and
// returns a file that only that repository tracks.
func pointAtAnotherRepository(t *testing.T) string {
	t.Helper()
	other := t.TempDir()
	fixture.GitRepo(t, other)
	if err := os.WriteFile(filepath.Join(other, "only-there.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture.Git(t, other, "add", "only-there.txt")
	fixture.Git(t, other, "commit", "-q", "-m", "other")
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index"))
	return "only-there.txt"
}

func TestTrackedFilesIgnoresTheCallersGitEnvironment(t *testing.T) {
	dir := t.TempDir()
	fixture.GitRepo(t, dir)
	foreign := pointAtAnotherRepository(t)
	got, err := attest.TrackedFiles(dir, attest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if contains(got, foreign) {
		t.Fatalf("TrackedFiles read the other repository: %q", got)
	}
	if !contains(got, "a.txt") {
		t.Fatalf("TrackedFiles lost the target's own files: %q", got)
	}
}

func TestTrackedFilesErrorNamesTheBaseNameUnderTheCallersGitEnvironment(t *testing.T) {
	parent := t.TempDir()
	missing := filepath.Join(parent, "no-such-target")
	pointAtAnotherRepository(t)
	_, err := attest.TrackedFiles(missing, attest.Options{})
	if err == nil {
		t.Fatal("a missing target gave no error")
	}
	if strings.Contains(err.Error(), parent) || !strings.Contains(err.Error(), "no-such-target") {
		t.Fatalf("the error must name the base name only: %v", err)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
