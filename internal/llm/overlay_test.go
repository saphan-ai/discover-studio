package llm_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The leak check must be able to see a leak. This runs the same check in a build where the masking file is replaced,
// through go's -overlay flag, by one that masks nothing. The check must fail there and name the leak; it passes in
// the real build, which is TestNoSecretInAnyRequestBody itself.
func TestTheLeakCheckSeesALeakWhenMaskingIsBypassed(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test in a child process")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command on PATH")
	}
	pkg, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	overlay := map[string]map[string]string{"Replace": {
		filepath.Join(pkg, "mask.go"): filepath.Join(pkg, "testdata", "bypass", "mask.go.txt"),
	}}
	data, _ := json.Marshal(overlay)
	file := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		cmd := exec.Command(goBin, append([]string{"test", "-count=1", "-run", "^TestNoSecretInAnyRequestBody$"}, args...)...)
		cmd.Dir = pkg
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run("-overlay", file, ".")
	if err == nil {
		t.Fatalf("the leak check passed with the masking bypassed:\n%s", out)
	}
	if !strings.Contains(out, "LEAK:") {
		t.Errorf("the check failed, but not by finding a leak:\n%s", out)
	}
	// Negative control for this test: without the overlay the same command passes.
	if out, err := run("."); err != nil {
		t.Errorf("the check fails in the real build:\n%s", out)
	}
}
