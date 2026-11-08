package secrets_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/saphan-ai/discover/internal/fixture"
	"github.com/saphan-ai/discover/internal/model"
	"github.com/saphan-ai/discover/internal/scan/secrets"
)

// want is one expected finding.
type want struct {
	line int
	typ  string
}

func got(fs []model.Finding) []want {
	var out []want
	for _, f := range fs {
		out = append(out, want{f.Source.Line, f.SecretType})
	}
	return out
}

func lines(ls ...string) []byte { return []byte(strings.Join(ls, "\n") + "\n") }

func TestScanNamesTheTypeAndTheLineOfEachSecret(t *testing.T) {
	aws, gh, npm, gl, pw := fixture.AWSKey(), fixture.GitHubToken(), fixture.NPMToken(), fixture.GitLabToken(), fixture.Password()
	key := fixture.KeyBody()
	cases := []struct {
		name    string
		content []byte
		want    []want
	}{
		{"private key block", lines("# keys", fixture.KeyHeader(), key[0].Text, key[1].Text, fixture.KeyFooter()),
			[]want{{2, "private-key"}}},
		{"aws access key", lines("[default]", "id = "+aws.Text), []want{{2, "aws-access-key"}}},
		{"github token", lines("echo start", "curl -H 'Authorization: token "+gh.Text+"' x"), []want{{2, "github-token"}}},
		{"npm token", lines("//registry.example.invalid/:_authToken=" + npm.Text), []want{{1, "npm-token"}}},
		{"gitlab token", lines("a", "b", "c = '"+gl.Text+"'"), []want{{3, "gitlab-token"}}},
		{"assigned secret, env style", lines("A=1", "DB_PASSWORD="+pw.Text), []want{{2, "assigned-secret"}}},
		{"assigned secret, quoted yaml", lines("db:", `  api_token: "`+pw.Text+`"`), []want{{2, "assigned-secret"}}},
		{"assigned secret, json", lines(`{"client_secret": "` + pw.Text + `"}`), []want{{1, "assigned-secret"}}},
		{"assigned secret, go", lines(`const appSecret = "` + pw.Text + `"`), []want{{1, "assigned-secret"}}},
		{"a token prefix wins over the assignment", lines("GITHUB_TOKEN=" + gh.Text), []want{{1, "github-token"}}},
		{"two secrets, two lines", lines("k="+aws.Text, "x", "DB_PASSWORD="+pw.Text),
			[]want{{1, "aws-access-key"}, {3, "assigned-secret"}}},
		{"one type once per line", lines(aws.Text + " " + aws.Text), []want{{1, "aws-access-key"}}},
		{"assigned secret, punctuation-free password", lines("A=1", "DB_PASSWORD="+fixture.PlainPassword().Text), []want{{2, "assigned-secret"}}},
		{"a documentation key does not hide a real key", lines("e.g. AKIAIOSFODNN7EXAMPLE or " + aws.Text), []want{{1, "aws-access-key"}}},
		{"a real key before the documentation key", lines(aws.Text + " then AKIAIOSFODNN7EXAMPLE"), []want{{1, "aws-access-key"}}},
		{"windows line endings", []byte("a\r\nDB_PASSWORD=" + pw.Text + "\r\n"), []want{{2, "assigned-secret"}}},
		{"no trailing newline", []byte("a\nDB_PASSWORD=" + pw.Text), []want{{2, "assigned-secret"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if g := got(secrets.Scan("f.txt", c.content)); fmt.Sprint(g) != fmt.Sprint(c.want) {
				t.Fatalf("findings = %v, want %v", g, c.want)
			}
		})
	}
}

// TestScanReadsAnUnquotedWordInAConfigFileAsAValue: in an environment or config file a word after = or : is a value.
func TestScanReadsAnUnquotedWordInAConfigFileAsAValue(t *testing.T) {
	pw := fixture.PlainPassword().Text
	for file, line := range map[string]string{
		".env":              "DB_PASSWORD=" + pw,
		"app/.env.local":    "DB_PASSWORD=" + pw,
		"app.properties":    "db.password=" + pw,
		"conf/app.ini":      "password = " + pw,
		"values.yaml":       "password: " + pw,
		"deploy/ci.yml":     "  api_token: " + pw,
		"config.toml":       "client_secret = " + pw,
		"settings/prod.cfg": "secret = " + pw,
	} {
		t.Run(file, func(t *testing.T) {
			if g := got(secrets.Scan(file, lines(line))); fmt.Sprint(g) != fmt.Sprint([]want{{1, "assigned-secret"}}) {
				t.Fatalf("findings = %v, want one assigned-secret", g)
			}
		})
	}
}

func TestScanWidensTheNamesOfASecret(t *testing.T) {
	sk, pw := fixture.SigningKey().Text, fixture.PlainPassword().Text
	for _, c := range []struct {
		name, file, line string
		hit              bool
	}{
		{"aws secret access key", "creds.ini", "aws_secret_access_key = " + sk, true},
		{"django secret key", "settings.py", "SECRET_KEY = '" + sk + "'", true},
		{"private key setting", "app.yaml", `private_key = "` + sk + `"`, true},
		{"private-key dash", "app.yaml", "private-key: " + sk, true},
		{"signing key", "app.go", `signingKey := "` + sk + `"`, true},
		{"access key", "app.env", "ACCESS_KEY=" + sk, true},
		{"passphrase", "app.yaml", "passphrase: " + pw, true},
		{"credentials", "app.yaml", "credentials: " + pw, true},
		{"credential", "app.yaml", "credential: " + pw, true},
		{"auth", "app.yaml", "auth: " + pw, true},
		{"id suffix is not a key", "app.env", "ACCESS_KEY_ID_TTL=" + pw, false},
		{"name that only contains key", "app.env", "SECRET_KEYWORD=" + pw, false},
		{"signing key reference", "app.go", "signing_key = cfg.Key", false},
		{"secret key call", "app.go", "secret_key = vault.Read(path)", false},
		{"auth reference", "app.go", "auth = cfg.Auth", false},
		{"author is not auth", "app.yaml", "author: " + pw, false},
		{"passphrase from env", "app.go", `passphrase = os.Getenv("PASS")`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := secrets.Scan(c.file, lines(c.line))
			if c.hit && fmt.Sprint(got(g)) != fmt.Sprint([]want{{1, "assigned-secret"}}) {
				t.Fatalf("findings = %v, want one assigned-secret", got(g))
			}
			if !c.hit && len(g) != 0 {
				t.Fatalf("reported: %v", got(g))
			}
		})
	}
}

func TestScanLeavesPlaceholdersAndReferencesAlone(t *testing.T) {
	for _, line := range []string{
		"DB_PASSWORD=",
		"DB_PASSWORD=${DB_PASSWORD}",
		"DB_PASSWORD=$DB_PASSWORD",
		`password = "changeme"`,
		`API_TOKEN="your-token-here"`,
		`API_TOKEN=<token>`,
		`client_secret: {{ .Values.secret }}`,
		`token = os.Getenv("API_TOKEN")`,
		`token := cfg.Token`,
		`const apiToken = process.env.API_TOKEN`,
		`password: xxxxxxxxxxxx`,
		`APP_SECRET=short`,
		`ACCESS_TOKEN_TTL=3600`,
		`secret_name = "db-credentials-prod"`,
		"AKIAIOSFODNN7EXAMPLE",
		"see the docs about a private key",
	} {
		t.Run(line, func(t *testing.T) {
			if g := secrets.Scan("f.txt", lines(line)); len(g) != 0 {
				t.Fatalf("a placeholder or reference was reported: %v", got(g))
			}
		})
	}
}

func TestScanReportsAValueAfterAPasswordFlag(t *testing.T) {
	assertFlagValue(t, "app --password "+fixture.Password().Text)
	if g := secrets.Scan("f.txt", lines("app --password changeme")); len(g) != 0 {
		t.Fatalf("a placeholder after --password was reported: %v", got(g))
	}
}

func TestScanReportsAValueAfterTheShortPasswordFlagOnLogin(t *testing.T) {
	pw := fixture.Password().Text
	assertFlagValue(t, "docker login -u u -p "+pw+" registry.example.invalid")
	if g := secrets.Scan("f.txt", lines("docker run -p "+pw)); len(g) != 0 {
		t.Fatalf("-p without login was reported: %v", got(g))
	}
}

func TestScanReportsAValueAfterAnAPIKeyFlag(t *testing.T) {
	assertFlagValue(t, "deploy --api-key "+fixture.Password().Text)
}

func TestScanReportsAValueAfterASigningKeyInExecForm(t *testing.T) {
	assertFlagValue(t, `CMD ["app", "--signing-key", "`+fixture.Password().Text+`"]`)
}

func assertFlagValue(t *testing.T, line string) {
	t.Helper()
	fs := secrets.Scan("f.txt", lines(line))
	if fmt.Sprint(got(fs)) != fmt.Sprint([]want{{1, "assigned-secret"}}) {
		t.Fatalf("findings = %v, want one assigned-secret", got(fs))
	}
	b, err := json.Marshal(fs)
	if err != nil {
		t.Fatal(err)
	}
	if hit := fixture.Leaks(string(b), fixture.Password()); len(hit) != 0 {
		t.Fatalf("the finding carries %v", hit)
	}
}

func TestScanReportsOnlyFileLineAndType(t *testing.T) {
	pw := fixture.Password()
	fs := secrets.Scan("dir/f.env", lines("DB_PASSWORD="+pw.Text))
	if len(fs) != 1 {
		t.Fatalf("findings = %d, want 1", len(fs))
	}
	f := fs[0]
	if f.Kind != "secret" || f.Source.File != "dir/f.env" || f.Source.Line != 1 || f.Source.Key != "" || f.Text != "" {
		t.Fatalf("finding = %+v", f)
	}
	b, err := json.Marshal(fs)
	if err != nil {
		t.Fatal(err)
	}
	if hit := fixture.Leaks(string(b), pw); len(hit) != 0 {
		t.Fatalf("the finding carries %v", hit)
	}
}

func TestTrackedEnvNamesEnvironmentFilesButNotTemplates(t *testing.T) {
	for path, want := range map[string]bool{
		".env":                 true,
		".env.local":           true,
		".env.production":      true,
		"app/.env":             true,
		"app/sub/.env.staging": true,
		".env.example":         false,
		".env.sample":          false,
		".env.template":        false,
		"app/.env.dist":        false,
		"env.go":               false,
		"app/.envrc":           false,
		"dotenv/readme.md":     false,
		"production.env":       false,
	} {
		if g := secrets.TrackedEnv(path); g != want {
			t.Errorf("TrackedEnv(%q) = %v, want %v", path, g, want)
		}
	}
}
