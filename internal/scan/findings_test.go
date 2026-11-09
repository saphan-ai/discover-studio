package scan_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/saphan-ai/discover-studio/internal/attest"
	"github.com/saphan-ai/discover-studio/internal/fixture"
	"github.com/saphan-ai/discover-studio/internal/model"
	"github.com/saphan-ai/discover-studio/internal/scan"
)

// planted is every secret value the fixture repository holds, so one list drives the plant and the hunt.
func planted() []fixture.Value {
	vs := []fixture.Value{fixture.AWSKey(), fixture.GitHubToken(), fixture.NPMToken(), fixture.GitLabToken(), fixture.Password()}
	return append(vs, fixture.KeyBody()...)
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// secretRepo builds a temporary repository whose secret values exist only at run time.
func secretRepo(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := fixture.KeyBody()
	write(t, root, "config/aws.txt", "[default]\nregion = x\nkey_id = "+fixture.AWSKey().Text+"\n")
	write(t, root, "ci/deploy.sh", "#!/bin/sh\ncurl -H 'Authorization: token "+fixture.GitHubToken().Text+"' u\n")
	write(t, root, ".npmrc", "//registry.example.invalid/:_authToken="+fixture.NPMToken().Text+"\n")
	write(t, root, "ci/pipeline.txt", "a\nb\nc\nd\ntrigger = "+fixture.GitLabToken().Text+"\n")
	write(t, root, "app.yaml", "name: app\ndb:\n  host: h\n  user: u\n  db_password: \""+fixture.Password().Text+"\"\n")
	write(t, root, "keys/id.pem", fixture.KeyHeader()+"\n"+key[0].Text+"\n"+key[1].Text+"\n"+fixture.KeyFooter()+"\n")
	write(t, root, "src/rotate.go", "package src\n// TODO rotate the key\nvar k = \""+fixture.AWSKey().Text+"\" // FIXME move it\n")
	write(t, root, ".env", "APP_SECRET="+fixture.Password().Text+"\n")
	write(t, root, ".env.example", "APP_SECRET=your-secret-here\n")
	write(t, root, "README.md", "# demo\nAPI_TOKEN=${API_TOKEN}\npassword = \"changeme\"\n")
	write(t, root, ".github/workflows/release.yml", strings.Join([]string{
		"jobs:",
		"  j:",
		"    steps:",
		`      - run: curl -H "Authorization: Bearer ` + fixture.GitHubToken().Text + `" u`,
		"      - run: |",
		"          deploy --api-key " + fixture.Password().Text,
	}, "\n")+"\n")
	write(t, root, ".gitlab-ci.yml", strings.Join([]string{
		"job:",
		"  script:",
		"    - docker login -u u -p " + fixture.GitLabToken().Text + " registry.example.invalid",
	}, "\n")+"\n")
	write(t, root, "Dockerfile", "FROM alpine\nCMD [\"app\", \"--signing-key\", \""+fixture.Password().Text+"\"]\n")
	write(t, root, "compose.yml", strings.Join([]string{
		"services:",
		"  app:",
		"    image: app",
		"    command: app --password " + fixture.Password().Text,
	}, "\n")+"\n")
	return root
}

// asGit commits the whole tree, .env included, as a git target.
func asGit(t *testing.T, root string) {
	t.Helper()
	fixture.Git(t, root, "init", "-q")
	fixture.Git(t, root, "add", "-A", "-f")
	fixture.Git(t, root, "commit", "-q", "-m", "fixture")
}

func triples(r scan.Result, kind string) []string {
	var out []string
	for _, f := range r.Repo.Findings {
		if f.Kind == kind {
			out = append(out, fmt.Sprintf("%s:%d:%s", f.Source.File, f.Source.Line, f.SecretType))
		}
	}
	sort.Strings(out)
	return out
}

// captured runs fn and returns what it wrote to stdout, stderr and the standard logger.
func captured(t *testing.T, fn func()) string {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr, oldLog := os.Stdout, os.Stderr, log.Writer()
	os.Stdout, os.Stderr = pw, pw
	log.SetOutput(pw)
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(pr)
		done <- string(b)
	}()
	func() {
		defer func() {
			os.Stdout, os.Stderr = oldOut, oldErr
			log.SetOutput(oldLog)
			pw.Close()
		}()
		fn()
	}()
	return <-done
}

var wantSecretsDir = []string{
	".env:1:assigned-secret",
	".github/workflows/release.yml:4:github-token",
	".github/workflows/release.yml:6:assigned-secret",
	".gitlab-ci.yml:3:gitlab-token",
	".npmrc:1:npm-token",
	"Dockerfile:2:assigned-secret",
	"app.yaml:5:assigned-secret",
	"ci/deploy.sh:2:github-token",
	"ci/pipeline.txt:5:gitlab-token",
	"compose.yml:4:assigned-secret",
	"config/aws.txt:3:aws-access-key",
	"keys/id.pem:1:private-key",
	"src/rotate.go:3:aws-access-key",
}

func TestSecretsAreNamedByFileLineAndTypeAndNeverQuoted(t *testing.T) {
	for _, mode := range []attest.Mode{attest.ModeDir, attest.ModeGit} {
		t.Run(string(mode), func(t *testing.T) {
			root := secretRepo(t)
			want := wantSecretsDir
			if mode == attest.ModeGit {
				asGit(t, root)
				want = append(append([]string(nil), want...), ".env:1:tracked-env-file")
				sort.Strings(want)
			}
			var res scan.Result
			var runErr error
			stray := captured(t, func() { res, runErr = scan.Run(root, mode, scan.Options{}) })
			if runErr != nil {
				t.Fatal(runErr)
			}
			if got := triples(res, "secret"); fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("secret findings:\n got %v\nwant %v", got, want)
			}
			b, err := json.Marshal(res)
			if err != nil {
				t.Fatal(err)
			}
			commands, err := json.Marshal(res.Repo.Commands)
			if err != nil {
				t.Fatal(err)
			}
			if hit := fixture.Leaks(string(b)+string(commands)+stray, planted()...); len(hit) != 0 {
				t.Fatalf("a secret value reached the output: %v", hit)
			}
		})
	}
}

func TestASecretInAFileAnotherScannerSetAsideIsStillNamed(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body := "db_password: " + fixture.Password().Text + "\n// TODO rotate\n"
	write(t, root, "yarn.lock", body)
	write(t, root, "package.json", "{not-json\n"+body)
	write(t, root, "notes.txt", body)
	res, err := scan.Run(root, attest.ModeDir, scan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"notes.txt:1:assigned-secret",
		"package.json:2:assigned-secret",
		"yarn.lock:1:assigned-secret",
	}
	got := triples(res, "secret")
	if fmt.Sprint(got) != fmt.Sprint(want) || res.TodoTotal != 3 {
		t.Fatalf("secrets %v TodoTotal %d, want %v and 3", got, res.TodoTotal, want)
	}
}

func TestAnEnvFileIsTrackedOnlyWhenGitTracksIt(t *testing.T) {
	root := secretRepo(t)
	write(t, root, ".gitignore", ".env\n")
	asGitExcept := func() {
		fixture.Git(t, root, "init", "-q")
		fixture.Git(t, root, "add", "-A")
		fixture.Git(t, root, "commit", "-q", "-m", "fixture")
	}
	asGitExcept()
	res, err := scan.Run(root, attest.ModeGit, scan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range triples(res, "secret") {
		if strings.HasSuffix(s, "tracked-env-file") {
			t.Fatalf("an ignored, untracked .env was reported as tracked: %v", s)
		}
	}
}

func TestSecretFindingsValidateAgainstTheSchema(t *testing.T) {
	root := secretRepo(t)
	asGit(t, root)
	res, err := scan.Run(root, attest.ModeGit, scan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	validate(t, res)
}

func validate(t *testing.T, res scan.Result) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtures, "schema", "valid", "two-repos.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rep model.Report
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatal(err)
	}
	rep.Repos = rep.Repos[:1]
	rep.Repos[0].Inventory = res.Repo.Inventory
	rep.Repos[0].Structure = res.Repo.Structure
	rep.Repos[0].Findings = res.Repo.Findings
	rep.Repos[0].Attestation.ReadManifest = res.ReadManifest
	out, err := model.Marshal(&rep)
	if err != nil {
		t.Fatalf("model.Marshal refuses the report: %v", err)
	}
	if err := model.Validate(out); err != nil {
		t.Fatalf("model.Validate: %v", err)
	}
}

func TestTodoListsThirtyAndCountsAll(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "a.go", "package a\n"+strings.Repeat("// TODO a\n", 20))
	write(t, root, "b.go", "package b\n"+strings.Repeat("// FIXME b\n", 25))
	res, err := scan.Run(root, attest.ModeDir, scan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.TodoTotal != 45 {
		t.Fatalf("TodoTotal = %d, want 45", res.TodoTotal)
	}
	got := triples(res, "todo")
	if len(got) != 30 {
		t.Fatalf("todo findings = %d, want 30", len(got))
	}
	var files []string
	for _, f := range res.Repo.Findings {
		if f.Kind == "todo" {
			files = append(files, fmt.Sprintf("%s:%d", f.Source.File, f.Source.Line))
		}
	}
	if files[0] != "a.go:2" || files[19] != "a.go:21" || files[20] != "b.go:2" || files[29] != "b.go:11" {
		t.Fatalf("todo findings are not the first 30 in file order: %v", files)
	}
	validate(t, res)
}

func TestTodoTotalIsZeroWhenThereAreNoMarkers(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "a.go", "package a\n")
	res, err := scan.Run(root, attest.ModeDir, scan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.TodoTotal != 0 || len(triples(res, "todo")) != 0 {
		t.Fatalf("TodoTotal = %d, findings = %v", res.TodoTotal, triples(res, "todo"))
	}
}

func TestAFileOverTheCapIsSkippedByNameAndNotScanned(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pw := fixture.Password()
	write(t, root, "big.env", "APP_SECRET="+pw.Text+"\n// TODO x\n"+strings.Repeat("a", 4096))
	write(t, root, "small.env", "APP_SECRET="+pw.Text+"\n")
	res, err := scan.Run(root, attest.ModeDir, scan.Options{MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if got := triples(res, "secret"); fmt.Sprint(got) != "[small.env:1:assigned-secret]" {
		t.Fatalf("findings = %v", got)
	}
	n := 0
	for _, s := range res.Skipped {
		if s.Path == "big.env" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("big.env is listed in Skipped %d times, want once", n)
	}
	if res.TodoTotal != 0 {
		t.Fatalf("TodoTotal = %d: a skipped file was read", res.TodoTotal)
	}
}

func TestEveryErrorAndSkipReasonIsFreeOfTheValues(t *testing.T) {
	root := secretRepo(t)
	write(t, root, "bin.dat", "\x00\x01"+fixture.AWSKey().Text)
	res, err := scan.Run(root, attest.ModeDir, scan.Options{MaxBytes: 64})
	var all strings.Builder
	if err != nil {
		all.WriteString(err.Error())
	}
	for _, s := range res.Skipped {
		all.WriteString(s.Path + " " + s.Reason + "\n")
	}
	if hit := fixture.Leaks(all.String(), planted()...); len(hit) != 0 {
		t.Fatalf("a value reached an error or a skip reason: %v", hit)
	}
	if _, err := scan.Run(filepath.Join(root, "no-such-dir"), attest.ModeDir, scan.Options{}); err != nil {
		if hit := fixture.Leaks(err.Error(), planted()...); len(hit) != 0 {
			t.Fatalf("a value reached an error: %v", hit)
		}
	}
}

func TestTheLeakCheckFindsAValueThatIsPlanted(t *testing.T) {
	pw := fixture.Password()
	for name, haystack := range map[string]string{
		"whole":   "x " + pw.Text + " y",
		"a piece": "x " + pw.Text[4:12] + " y",
	} {
		if hit := fixture.Leaks(haystack, pw); len(hit) != 1 {
			t.Errorf("%s: the leak check did not see a planted value", name)
		}
	}
	if hit := fixture.Leaks("nothing of it", pw); len(hit) != 0 {
		t.Errorf("a clean haystack reported %v", hit)
	}
}
