package cli

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/saphan-ai/discover-studio/internal/manifest"
)

// dialCounter is a transport that counts every call and answers with a fixed completion.
type dialCounter struct {
	calls atomic.Int32
	body  string
}

func (d *dialCounter) RoundTrip(r *http.Request) (*http.Response, error) {
	d.calls.Add(1)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(d.body)), Request: r}, nil
}

const modelAnswer = `{"choices":[{"message":{"role":"assistant","content":` +
	`"{\"claims\":[{\"text\":\"A tool named alpha.\",\"citations\":[{\"file\":\"a.txt\",\"line\":1}]},` +
	`{\"text\":\"Made up.\",\"citations\":[{\"file\":\"a.txt\",\"line\":9}]}],\"open_questions\":[],\"recommendations\":[]}"}}]}`

// withReads makes the scan record a.txt in the manifest, as an analyser that read it would.
func withReads(t *testing.T) {
	t.Helper()
	old := analysers
	analysers = func(root string, m *manifest.Manifest) {
		if _, err := m.Record("a.txt", 0); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { analysers = old })
}

func withTransport(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	old := modelTransport
	modelTransport = rt
	t.Cleanup(func() { modelTransport = old })
}

func TestNoModelFlagMeansNoDial(t *testing.T) {
	b, r := plainFixture(t)
	withReads(t)
	d := &dialCounter{body: modelAnswer}
	withTransport(t, d)
	if code, _, stderr := run(t, "--out", filepath.Join(b, "out"), r); code != ExitOK {
		t.Fatalf("exit %d %q", code, stderr)
	}
	if n := d.calls.Load(); n != 0 {
		t.Errorf("%d dials without --model", n)
	}
	// Negative control: the same run with --model dials, so the counter can see a call. The field changed is --model.
	code, stdout, stderr := run(t, "--out", filepath.Join(b, "out2"), "--model", "m1", "--model-url", "http://127.0.0.1:9/v1", r)
	if code != ExitOK {
		t.Fatalf("exit %d %q", code, stderr)
	}
	if n := d.calls.Load(); n != 1 {
		t.Errorf("%d dials with --model, want 1", n)
	}
	if !strings.Contains(stdout, "model: m1") || !strings.Contains(stdout, "kept: 1") || !strings.Contains(stdout, "dropped: 1") {
		t.Errorf("stdout names no model line with the counts: %q", stdout)
	}
}

func TestModelFlagsNeedAURLAndAreRefusedBeforeAnyRead(t *testing.T) {
	b, r := plainFixture(t)
	d := &dialCounter{body: modelAnswer}
	withTransport(t, d)
	out := filepath.Join(b, "out")
	cases := [][]string{
		{"--model", "m1"},
		{"--model", "m1", "--model-url", "not a url"},
		{"--model", "m1", "--model-url", "ftp://host/v1"},
		{"--model-url", "http://127.0.0.1:9/v1"},
		{"--model-key-env", "SOME_VAR"},
	}
	for _, c := range cases {
		code, _, stderr := run(t, append(append([]string{"--out", out}, c...), r)...)
		if code != ExitUsage || !strings.Contains(stderr, "--model") {
			t.Errorf("%v: exit %d %q", c, code, stderr)
		}
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a refused model flag still wrote output")
	}
	if d.calls.Load() != 0 {
		t.Error("a refused flag dialled")
	}
}

func TestTheKeyValueIsInNoOutput(t *testing.T) {
	b, r := plainFixture(t)
	withReads(t)
	key := strings.Join([]string{"sk-", "Wp3Z", "9qLm", "Tx7V"}, "")
	t.Setenv("DISCOVER_TEST_KEY", key)
	withTransport(t, &dialCounter{body: modelAnswer})
	out := filepath.Join(b, "out")
	code, stdout, stderr := run(t, "--out", out, "--model", "m1", "--model-url", "http://127.0.0.1:9/v1", "--model-key-env", "DISCOVER_TEST_KEY", r)
	if code != ExitOK {
		t.Fatalf("exit %d %q", code, stderr)
	}
	// collect gathers everything a run left in a directory, so the check below reads files the same way for the run
	// and for the control.
	collect := func(dir string) string {
		var all string
		filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				data, _ := os.ReadFile(p)
				all += string(data)
			}
			return nil
		})
		return all
	}
	if all := stdout + stderr + collect(out); strings.Contains(all, key) {
		t.Error("the key value is in an output")
	}
	// Negative control: the same collection finds the value in a file that holds it. The field changed is the
	// content of the file in the collected directory.
	planted := filepath.Join(b, "planted")
	if err := os.Mkdir(planted, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planted, "leak.txt"), []byte("x "+key+" y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(collect(planted), key) {
		t.Fatal("control broken: the collection does not find a key that is in a file")
	}
}

func TestAnUnsetKeyVariableIsNamedNotRead(t *testing.T) {
	b, r := plainFixture(t)
	withReads(t)
	withTransport(t, &dialCounter{body: modelAnswer})
	os.Unsetenv("DISCOVER_TEST_UNSET")
	// A local endpoint needs no key: an unset variable is not an error.
	if code, _, stderr := run(t, "--out", filepath.Join(b, "out"), "--model", "m1", "--model-url", "http://127.0.0.1:9/v1", "--model-key-env", "DISCOVER_TEST_UNSET", r); code != ExitOK {
		t.Errorf("exit %d %q", code, stderr)
	}
}
