package llm_test

// A value standing after a secret-named flag is masked before the model call, so the guarantee holds at the model
// boundary and not only inside the secrets scanner. Every value is built at run time from parts.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/saphan-ai/discover/internal/fixture"
	"github.com/saphan-ai/discover/internal/llm"
)

func flagValue(name string, parts ...string) fixture.Value {
	return fixture.Value{Name: name, Text: strings.Join(parts, "")}
}

func TestAFlagValueIsMaskedBeforeTheCall(t *testing.T) {
	apiKey := flagValue("api-key-flag", "Tz8q", "W3rN", "5vB1", "xLm7")
	bearer := flagValue("bearer", "Qa4n", "Rt7w", "Lp2z", "Kc9v")
	login := flagValue("login-p", "Mv6b", "Hy3s", "Jd8f")
	npm := flagValue("authtoken-space", "Pn5c", "Wq1x", "Ez7r", "Ub4k")
	jsonFlag := flagValue("json-flag", "Gs2m", "Ok8y", "Ia3t", "Fe6l")
	vals := []fixture.Value{apiKey, bearer, login, npm, jsonFlag}
	files := []llm.File{
		{Path: "deploy.sh", Content: []byte("#!/bin/sh\ntool deploy --api-key " + apiKey.Text + " --region eu\n" +
			"curl -H \"Authorization: Bearer " + bearer.Text + "\" http://localhost/v1\n" +
			"docker login -u ci -p " + login.Text + " registry.local\n")},
		{Path: "npmrc.txt", Content: []byte("//registry.local/:_authToken " + npm.Text + "\n")},
		{Path: "task.json", Content: []byte("{\"args\": [\"--signing-key\", \"" + jsonFlag.Text + "\"]}\n")},
	}
	rec, cfg := serve(t, envelope(emptyAnswer))
	if _, err := llm.Ask(context.Background(), cfg, files); err != nil {
		t.Fatal(err)
	}
	var req struct {
		Messages []struct{ Content string }
	}
	if err := json.Unmarshal([]byte(rec.bodies[0]), &req); err != nil {
		t.Fatal(err)
	}
	decoded := ""
	for _, m := range req.Messages {
		decoded += m.Content
	}
	for _, hay := range []string{rec.bodies[0], decoded} {
		if leaks := fixture.Leaks(hay, vals...); len(leaks) > 0 {
			t.Errorf("LEAK: a flag value reached the wire: %v", leaks)
		}
	}
	// The lines are masked, not dropped: the line numbers and the neighbouring text stay.
	for _, want := range []string{"1: #!/bin/sh", "2: [MASKED: assigned-secret]", "3: [MASKED: assigned-secret]",
		"4: [MASKED: assigned-secret]"} {
		if !strings.Contains(decoded, want) {
			t.Errorf("the block lacks %q", want)
		}
	}
	// Control inside the test: the same hunt finds a value that is in the text, so a pass is not a blind check.
	if leaks := fixture.Leaks("x "+apiKey.Text+" y", apiKey); len(leaks) != 1 {
		t.Fatal("control broken: the hunt does not see a planted value")
	}
}
