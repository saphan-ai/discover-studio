// Package secrets finds secrets by file, line and type. It never copies a value into anything it returns: a finding
// holds the file, the line and the type. The model has a shared Text field; its validator refuses a secret finding
// that carries text.
package secrets

import (
	"bytes"
	"path"
	"regexp"
	"strings"

	"github.com/saphan-ai/discover/internal/model"
)

// The closed set of secret types.
const (
	PrivateKey     = "private-key"
	AWSAccessKey   = "aws-access-key"
	GitHubToken    = "github-token"
	GitLabToken    = "gitlab-token"
	NPMToken       = "npm-token"
	PyPIToken      = "pypi-token"
	AssignedSecret = "assigned-secret"
	TrackedEnvFile = "tracked-env-file"
)

// minValueLen is the shortest assigned value that is reported: a shorter one is a word, a flag or a unit.
const minValueLen = 8

// detector is a secret format that is recognised by its own shape.
type detector struct {
	typ string
	re  *regexp.Regexp
}

// detectors are tried in this order on every line; each type is reported once per line.
var detectors = []detector{
	{PrivateKey, regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY(?: BLOCK)?-----`)},
	{AWSAccessKey, regexp.MustCompile(`\b(?:AKIA|ASIA|ABIA|ACCA)[A-Z2-7]{16}\b`)},
	{GitHubToken, regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,255}|github_pat_[A-Za-z0-9_]{22,255})\b`)},
	{GitLabToken, regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`)},
	{NPMToken, regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`)},
	{PyPIToken, regexp.MustCompile(`\bpypi-AgEIcHlwaS5vcmc[A-Za-z0-9_-]{50,}`)},
}

// assignment matches a name that ends in a secret word, then = or :, then the rest of the line. The name is group 1
// and the rest is group 2; only the rest is ever a value.
var assignment = regexp.MustCompile(`(?i)([a-z0-9_.-]*(?:(?:secret|private|access|signing)[_-]?key|api[_-]?key|passphrase|credentials?|auth|secret|token|password|passwd))["']?\s*(?::=|[:=])\s*(.+)$`)

// reference matches an unquoted value that only names something else, outside a config file: a dotted path such as
// cfg.Token. A bare word is a value there, as it is in a config file; a call is told apart by its parentheses.
var reference = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)+$`)

// configExts name the files whose unquoted values are values: a word after = or : there is not a reference.
var configExts = []string{".env", ".properties", ".ini", ".yaml", ".yml", ".toml", ".cfg", ".conf"}

// isConfig reports whether file is an environment or config file, by its base name.
func isConfig(file string) bool {
	base := strings.ToLower(path.Base(file))
	if strings.HasPrefix(base, ".env") {
		return true
	}
	for _, e := range configExts {
		if strings.HasSuffix(base, e) {
			return true
		}
	}
	return false
}

// placeholderWords mark a value that stands for a secret and is not one.
var placeholderWords = []string{
	"changeme", "change-me", "change_me", "placeholder", "example", "your-", "your_", "yourtoken", "dummy",
	"redacted", "sample", "fake", "todo", "xxxx", "****",
}

// templateSuffixes name an environment file that is a template and holds no real values.
var templateSuffixes = []string{".example", ".sample", ".template", ".dist", ".defaults", ".tpl"}

// Scan returns the secret findings of one file's content, in line order.
func Scan(file string, content []byte) []model.Finding {
	var out []model.Finding
	config := isConfig(file)
	for i, line := range bytes.Split(content, []byte("\n")) {
		for _, typ := range lineTypes(line, config) {
			sev := "high"
			if typ == AssignedSecret {
				sev = "medium"
			}
			out = append(out, model.Finding{Kind: "secret", Severity: sev, Source: model.Source{File: file, Line: i + 1}, SecretType: typ})
		}
	}
	return out
}

// lineTypes returns the secret types found on one line. An assignment is looked for only when no format matched, so
// a token behind a name is reported as its own type.
func lineTypes(line []byte, config bool) []string {
	var types []string
	for _, d := range detectors {
		if realMatch(d, line) {
			types = append(types, d.typ)
		}
	}
	if len(types) == 0 && assigned(line, config) {
		types = append(types, AssignedSecret)
	}
	return types
}

// realMatch reports whether the detector matches the line anywhere but in a documentation key. Every match is
// looked at, so an example key does not hide a real one on the same line.
func realMatch(d detector, line []byte) bool {
	for _, m := range d.re.FindAll(line, -1) {
		if d.typ != AWSAccessKey || !bytes.HasSuffix(m, []byte("EXAMPLE")) {
			return true
		}
	}
	return false
}

// assigned reports whether the line assigns a real-looking value to a secret name.
func assigned(line []byte, config bool) bool {
	m := assignment.FindSubmatch(line)
	if m == nil {
		return false
	}
	value, quoted := valueOf(strings.TrimSpace(string(m[2])))
	return realValue(value, quoted, config)
}

// valueOf cuts the value out of the text after the separator: up to the closing quote, or up to a space or a
// terminating comma or semicolon.
func valueOf(rest string) (value string, quoted bool) {
	if rest == "" {
		return "", false
	}
	if q := rest[0]; q == '"' || q == '\'' || q == '`' {
		inner := rest[1:]
		if end := strings.IndexByte(inner, q); end >= 0 {
			inner = inner[:end]
		}
		return inner, true
	}
	if end := strings.IndexAny(rest, " \t"); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimRight(rest, ",;"), false
}

// realValue says whether a value looks like a secret and not like a placeholder, a reference or a call.
func realValue(v string, quoted, config bool) bool {
	if len(v) < minValueLen || strings.ContainsAny(v[:1], "$<%{@*[") {
		return false
	}
	if strings.Contains(v, "${") || strings.Contains(v, "{{") || strings.Contains(v, "%(") {
		return false
	}
	if strings.Trim(v, v[:1]) == "" {
		return false
	}
	lower := strings.ToLower(v)
	for _, w := range placeholderWords {
		if strings.Contains(lower, w) {
			return false
		}
	}
	if quoted {
		return true
	}
	return !strings.ContainsAny(v, "()") && (config || !reference.MatchString(v))
}

// TrackedEnv reports whether path names an environment file (.env, .env.local) that is not a template. The caller
// says whether git tracks it.
func TrackedEnv(file string) bool {
	base := path.Base(file)
	if base == ".env" {
		return true
	}
	if !strings.HasPrefix(base, ".env.") {
		return false
	}
	for _, s := range templateSuffixes {
		if strings.HasSuffix(base, s) {
			return false
		}
	}
	return true
}

// TrackedEnvFinding is the finding for a tracked environment file; the file is not read.
func TrackedEnvFinding(file string) model.Finding {
	return model.Finding{Kind: "secret", Severity: "medium", Source: model.Source{File: file, Line: 1}, SecretType: TrackedEnvFile}
}
