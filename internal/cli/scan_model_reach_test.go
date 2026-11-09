package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/saphan-ai/discover-studio/internal/manifest"
)

// answerServer is a local endpoint that counts its requests and answers with modelAnswer.
func answerServer(t *testing.T) (*atomic.Int32, *httptest.Server) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(modelAnswer))
	}))
	t.Cleanup(srv.Close)
	return &n, srv
}

const (
	childRoot = "DISCOVER_CLI_CHILD_ROOT"
	childOut  = "DISCOVER_CLI_CHILD_OUT"
	childURL  = "DISCOVER_CLI_CHILD_URL"
)

// TestScanChild is the child of the proxy test: it runs two scans with the production transport, one to a name that
// only a proxy could reach and one to the named local endpoint. It does nothing outside that test.
func TestScanChild(t *testing.T) {
	root := os.Getenv(childRoot)
	if root == "" {
		t.Skip("runs only as the child of TestAProxyVariableDoesNotReachTheModelCall")
	}
	withReads(t)
	run(t, "--out", filepath.Join(os.Getenv(childOut), "o1"), "--model", "m1", "--model-url", "http://model.invalid/v1", root)
	if code, _, stderr := run(t, "--out", filepath.Join(os.Getenv(childOut), "o2"), "--model", "m1", "--model-url", os.Getenv(childURL)+"/v1", root); code != ExitOK {
		t.Fatalf("exit %d %q", code, stderr)
	}
}

// The standard library reads the proxy variables once per process, so the scan runs in a child that starts with them
// set. Nothing here injects a transport: this is the production default.
func TestAProxyVariableDoesNotReachTheModelCall(t *testing.T) {
	b, r := plainFixture(t)
	proxyHits, proxy := answerServer(t)
	endHits, end := answerServer(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestScanChild$", "-test.count=1")
	cmd.Env = append(os.Environ(), childRoot+"="+r, childOut+"="+b, childURL+"="+end.URL,
		"HTTP_PROXY="+proxy.URL, "http_proxy="+proxy.URL, "HTTPS_PROXY="+proxy.URL, "https_proxy="+proxy.URL, "NO_PROXY=", "no_proxy=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the child failed: %v\n%s", err, out)
	}
	if n := proxyHits.Load(); n != 0 {
		t.Errorf("the proxy received %d requests; the call must reach only the named endpoint", n)
	}
	// Control: the named endpoint, which the proxy variables do not cover, received its call in the same child.
	if n := endHits.Load(); n != 1 {
		t.Errorf("the named endpoint received %d requests, want 1", n)
	}
}

func TestARedirectFromTheNamedEndpointSendsTheDataNowhere(t *testing.T) {
	b, r := plainFixture(t)
	withReads(t)
	otherHits, other := answerServer(t)
	named := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, other.URL+"/v1/chat/completions", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(named.Close)
	code, _, stderr := run(t, "--out", filepath.Join(b, "out"), "--model", "m1", "--model-url", named.URL+"/v1", r)
	if n := otherHits.Load(); n != 0 {
		t.Errorf("the redirect target received %d requests", n)
	}
	if code == ExitOK || !strings.Contains(stderr, strconv.Itoa(http.StatusTemporaryRedirect)) {
		t.Errorf("the scan must fail naming the status 307: exit %d %q", code, stderr)
	}
	// Control: pointed at the target itself, the same scan makes one request there.
	if code, _, stderr := run(t, "--out", filepath.Join(b, "out2"), "--model", "m1", "--model-url", other.URL+"/v1", r); code != ExitOK || otherHits.Load() != 1 {
		t.Errorf("control: exit %d %q, target hits %d, want 1", code, stderr, otherHits.Load())
	}
}

func TestAnUnsetKeyVariableIsNamedOnStderr(t *testing.T) {
	b, r := plainFixture(t)
	withReads(t)
	withTransport(t, &dialCounter{body: modelAnswer})
	os.Unsetenv("DISCOVER_TEST_UNSET")
	code, _, stderr := run(t, "--out", filepath.Join(b, "out"), "--model", "m1", "--model-url", "http://127.0.0.1:9/v1", "--model-key-env", "DISCOVER_TEST_UNSET", r)
	if code != ExitOK {
		t.Errorf("exit %d %q: an unset key is not an error", code, stderr)
	}
	if !strings.Contains(stderr, "DISCOVER_TEST_UNSET") {
		t.Errorf("stderr does not name the variable: %q", stderr)
	}
	// Control: a variable that is set is not named as unset.
	t.Setenv("DISCOVER_TEST_SET", strings.Join([]string{"k", "e", "y", "v1"}, ""))
	_, _, stderr = run(t, "--out", filepath.Join(b, "out2"), "--model", "m1", "--model-url", "http://127.0.0.1:9/v1", "--model-key-env", "DISCOVER_TEST_SET", r)
	if strings.Contains(stderr, "DISCOVER_TEST_SET") {
		t.Errorf("a set variable is named as unset: %q", stderr)
	}
}

func TestAFileTheModelStepCannotReadIsCounted(t *testing.T) {
	b, r := plainFixture(t)
	big := make([]byte, maxModelFile+1)
	if err := os.WriteFile(filepath.Join(r, "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	withReads(t)
	old := analysers
	analysers = func(root string, m *manifest.Manifest) {
		old(root, m)
		if _, err := os.Stat(filepath.Join(root, "big.bin")); err != nil {
			return
		}
		if _, err := m.Record("big.bin", 0); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { analysers = old })
	withTransport(t, &dialCounter{body: modelAnswer})
	code, stdout, stderr := run(t, "--out", filepath.Join(b, "out"), "--model", "m1", "--model-url", "http://127.0.0.1:9/v1", r)
	if code != ExitOK {
		t.Fatalf("exit %d %q", code, stderr)
	}
	if !strings.Contains(stdout, "skipped: 1") {
		t.Errorf("the model line does not count the skipped file: %q", stdout)
	}
	// Control: with only readable files the count is zero.
	b2, r2 := plainFixture(t)
	_, stdout, _ = run(t, "--out", filepath.Join(b2, "out"), "--model", "m1", "--model-url", "http://127.0.0.1:9/v1", r2)
	if !strings.Contains(stdout, "skipped: 0") {
		t.Errorf("control: the model line does not show skipped: 0: %q", stdout)
	}
}
