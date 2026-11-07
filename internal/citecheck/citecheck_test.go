package citecheck_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saphan-ai/discover/internal/citecheck"
	"github.com/saphan-ai/discover/internal/llm"
	"github.com/saphan-ai/discover/internal/manifest"
	"github.com/saphan-ai/discover/internal/model"
)

const readme = "# Orbit\nOrbit is a command line tool that plans satellite passes.\nIt reads a tle file and prints a schedule.\nLicense: Apache-2.0\n"
const mainGo = "package main\n\nfunc main() {\n\tschedule := loadPasses()\n\tprint(schedule)\n}\n"

// repo writes a small tree and records the files in a manifest, the way a scan that read them would.
func repo(t *testing.T) (string, *manifest.Manifest) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"README.md": readme, "cmd/orbit/main.go": mainGo, "unread.txt": "orbit schedule\n"} {
		p := filepath.Join(root, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := manifest.New(root)
	for _, f := range []string{"README.md", "cmd/orbit/main.go"} {
		if _, err := m.Record(f, 0); err != nil {
			t.Fatal(err)
		}
	}
	return root, m
}

func cite(file string, line int) model.Source { return model.Source{File: file, Line: line} }

func claim(text string, cs ...model.Source) model.Claim {
	return model.Claim{Text: text, Citations: cs}
}

func TestKeepsOnlyClaimsWhoseEveryCitationResolves(t *testing.T) {
	root, m := repo(t)
	cases := []struct {
		name string
		c    model.Claim
		keep bool
	}{
		{"cited on the line", claim("Orbit plans satellite passes.", cite("README.md", 2)), true},
		{"word on the line after", claim("Prints a schedule", cite("README.md", 2)), true},
		{"word on the line before", claim("Apache licensed project", cite("README.md", 3)), true},
		{"two good citations", claim("The orbit tool prints its schedule", cite("README.md", 2), cite("cmd/orbit/main.go", 4)), true},
		{"no citation", claim("Orbit is great"), false},
		{"line past the end of the file", claim("Orbit plans satellite passes.", cite("README.md", 5)), false},
		{"line zero", claim("Orbit plans satellite passes.", cite("README.md", 0)), false},
		{"a negative line", claim("Orbit plans satellite passes.", cite("README.md", -1)), false},
		{"a key and no line", claim("Orbit plans satellite passes.", model.Source{File: "README.md", Key: "name"}), false},
		{"file not in the manifest", claim("orbit schedule", cite("unread.txt", 1)), false},
		{"file that does not exist", claim("orbit schedule", cite("gone.md", 1)), false},
		{"a parent segment", claim("orbit schedule", cite("../unread.txt", 1)), false},
		{"an absolute path", claim("orbit schedule", cite(filepath.Join(root, "README.md"), 2)), false},
		{"no shared word", claim("Quantum entanglement router", cite("README.md", 2)), false},
		{"word two lines away only", claim("Apache licensed project", cite("README.md", 1)), false},
		{"only a stop word is shared", claim("The pasta", cite("README.md", 2)), false},
		{"one good citation and one bad", claim("Orbit plans satellite passes.", cite("README.md", 2), cite("README.md", 9)), false},
		{"one good citation and one unread file", claim("Orbit plans schedule", cite("README.md", 2), cite("unread.txt", 1)), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := citecheck.Check(llm.Answer{Claims: []model.Claim{c.c}}, m)
			wantKept, wantDropped := 0, 1
			if c.keep {
				wantKept, wantDropped = 1, 0
			}
			if len(res.Claims) != wantKept || res.Dropped != wantDropped {
				t.Errorf("kept %d dropped %d, want kept %d dropped %d", len(res.Claims), res.Dropped, wantKept, wantDropped)
			}
		})
	}
}

func TestADroppedClaimIsNeverRepaired(t *testing.T) {
	_, m := repo(t)
	bad := claim("Orbit plans satellite passes.", cite("README.md", 40))
	res := citecheck.Check(llm.Answer{Claims: []model.Claim{bad}}, m)
	if len(res.Claims) != 0 {
		t.Fatalf("kept %+v", res.Claims)
	}
	if bad.Citations[0].Line != 40 {
		t.Error("the input was changed")
	}
}

func TestAFileChangedAfterTheReadDropsTheClaim(t *testing.T) {
	root, m := repo(t)
	c := claim("Orbit plans satellite passes.", cite("README.md", 2))
	if res := citecheck.Check(llm.Answer{Claims: []model.Claim{c}}, m); len(res.Claims) != 1 {
		t.Fatalf("the unchanged file was not accepted")
	}
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# Orbit\nOrbit plans.\n"), 0o644)
	if res := citecheck.Check(llm.Answer{Claims: []model.Claim{c}}, m); len(res.Claims) != 0 || res.Dropped != 1 {
		t.Errorf("a changed file kept %d dropped %d", len(res.Claims), res.Dropped)
	}
}

func TestQuestionsAndRecommendationsAreHeldToTheSameRule(t *testing.T) {
	_, m := repo(t)
	ans := llm.Answer{
		OpenQuestions: []model.OpenQuestion{
			{Topic: "schedule", Question: "Which format should the schedule use?", Context: cite("README.md", 3), Options: []string{"csv", "json"}},
			{Topic: "schedule", Question: "Which format should the schedule use?", Context: cite("README.md", 30), Options: []string{"csv", "json"}},
			{Topic: "schedule", Question: "Which format should the schedule use?", Context: cite("README.md", 3), Options: []string{"csv"}},
			{Topic: "kitchen", Question: "Which pan?", Context: cite("README.md", 3), Options: []string{"a", "b"}},
		},
		Recommendations: []model.Recommendation{
			{Text: "Document the schedule format", Citations: []model.Source{cite("README.md", 3)}},
			{Text: "Document the schedule format", Citations: []model.Source{cite("nope.md", 3)}},
		},
	}
	res := citecheck.Check(ans, m)
	if len(res.OpenQuestions) != 1 || len(res.Recommendations) != 1 || res.Dropped != 4 {
		t.Errorf("kept %d questions, %d recommendations, dropped %d; want 1, 1, 4", len(res.OpenQuestions), len(res.Recommendations), res.Dropped)
	}
}

// stub is a local OpenAI-compatible endpoint that answers with a fixed content.
func stub(t *testing.T, content string) llm.Config {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}}})
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return llm.Config{BaseURL: srv.URL + "/v1", Model: "stub"}
}

func TestThreeCitedAndTwoUncitedClaimsKeepThreeAndRecordTwoDrops(t *testing.T) {
	root, m := repo(t)
	_ = root
	answer := `{"claims":[
	 {"text":"Orbit is a command line tool.","citations":[{"file":"README.md","line":2}]},
	 {"text":"It reads a tle file.","citations":[{"file":"README.md","line":3}]},
	 {"text":"main prints the schedule.","citations":[{"file":"cmd/orbit/main.go","line":5}]},
	 {"text":"Orbit is fast and loved by many."},
	 {"text":"It supports every satellite.","citations":[]}
	],"open_questions":[],"recommendations":[]}`
	// The answer's last two claims carry no citation; the parser must still hand them over so the filter can count them.
	ans, err := llm.Ask(context.Background(), stub(t, answer), []llm.File{{Path: "README.md", Content: []byte(readme)}})
	if err != nil {
		t.Fatal(err)
	}
	res := citecheck.Check(ans, m)
	if len(res.Claims) != 3 || res.Dropped != 2 {
		t.Fatalf("kept %d dropped %d, want 3 and 2", len(res.Claims), res.Dropped)
	}
	n := citecheck.Narrative("stub", res)
	if n.Dropped != 2 || n.Model != "stub" || len(n.Claims) != 3 {
		t.Errorf("narrative %+v", n)
	}
	// Negative control: with the two uncited claims given a good citation the drop count falls to zero. The field
	// mutated is claims[3].citations and claims[4].citations.
	fixed := `{"claims":[
	 {"text":"Orbit is a command line tool.","citations":[{"file":"README.md","line":2}]},
	 {"text":"It reads a tle file.","citations":[{"file":"README.md","line":3}]},
	 {"text":"main prints the schedule.","citations":[{"file":"cmd/orbit/main.go","line":5}]},
	 {"text":"Orbit plans passes.","citations":[{"file":"README.md","line":2}]},
	 {"text":"It prints a schedule.","citations":[{"file":"README.md","line":3}]}
	],"open_questions":[],"recommendations":[]}`
	ans, err = llm.Ask(context.Background(), stub(t, fixed), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res := citecheck.Check(ans, m); len(res.Claims) != 5 || res.Dropped != 0 {
		t.Errorf("control: kept %d dropped %d", len(res.Claims), res.Dropped)
	}
}

func TestTheKeptNarrativeValidatesAgainstTheSchema(t *testing.T) {
	_, m := repo(t)
	res := citecheck.Check(llm.Answer{Claims: []model.Claim{claim("Orbit plans satellite passes.", cite("README.md", 2))}}, m)
	n := citecheck.Narrative("stub", res)
	b, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"model":"stub","claims":[{"text":"Orbit plans satellite passes.","citations":[{"file":"README.md","line":2}]}]}` {
		t.Errorf("got %s", b)
	}
	// With nothing dropped the optional field stays out; with a drop it appears.
	res.Dropped = 3
	b, _ = json.Marshal(citecheck.Narrative("stub", res))
	if want := `"dropped":3`; !strings.Contains(string(b), want) {
		t.Errorf("%s lacks %s", b, want)
	}
}

// A model citation that carries a key names something no step here resolves, so its claim is dropped, not repaired.
func TestACitationThatCarriesAKeyIsNotResolvable(t *testing.T) {
	_, m := repo(t)
	withKey := model.Source{File: "README.md", Line: 2, Key: "made-up-key"}
	res := citecheck.Check(llm.Answer{
		Claims:          []model.Claim{claim("Orbit plans satellite passes.", withKey)},
		Recommendations: []model.Recommendation{{Text: "Orbit plans satellite passes.", Citations: []model.Source{withKey}}},
	}, m)
	if len(res.Claims) != 0 || len(res.Recommendations) != 0 || res.Dropped != 2 {
		t.Errorf("kept %d claims and %d recommendations, dropped %d; want 0, 0, 2", len(res.Claims), len(res.Recommendations), res.Dropped)
	}
	// Control: the same citation without the key is kept, so the drop above is the key's doing.
	res = citecheck.Check(llm.Answer{Claims: []model.Claim{claim("Orbit plans satellite passes.", cite("README.md", 2))}}, m)
	if len(res.Claims) != 1 || res.Dropped != 0 {
		t.Errorf("control: kept %d dropped %d, want 1 and 0", len(res.Claims), res.Dropped)
	}
}
