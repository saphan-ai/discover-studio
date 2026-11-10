// Package citecheck is the cite-or-drop filter. It keeps a model claim, open question or recommendation only when
// every citation it carries resolves against what the scan read: the file is in the read manifest, the line exists,
// and the cited line, or the one before or after it, shares a word with the text. What fails is dropped, never
// repaired, and counted.
package citecheck

import (
	"bytes"
	"strings"
	"unicode"

	"github.com/saphan-ai/discover-studio/internal/llm"
	"github.com/saphan-ai/discover-studio/internal/manifest"
	"github.com/saphan-ai/discover-studio/internal/model"
)

// maxFileBytes caps the size of a cited file the filter will read.
const maxFileBytes = 4 << 20

// minTokenLen is the shortest word that counts as a shared word.
const minTokenLen = 3

// stopWords are too common to show that a line supports a claim.
var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true, "this": true, "are": true, "was": true,
	"not": true, "but": true, "you": true, "all": true, "can": true, "has": true, "have": true, "its": true,
	"from": true, "into": true, "when": true, "which": true, "will": true, "use": true, "uses": true, "used": true,
}

// Result is what survived, and how much did not. Dropped counts claims, questions and recommendations together.
type Result struct {
	Claims          []model.Claim
	OpenQuestions   []model.OpenQuestion
	Recommendations []model.Recommendation
	Dropped         int
}

// Check filters an answer against the manifest. It reads each cited file at most once per call.
func Check(a llm.Answer, m *manifest.Manifest) Result {
	c := &checker{m: m, in: map[string]bool{}, lines: map[string][]string{}}
	for _, e := range m.Entries() {
		c.in[e.Path] = true
	}
	var r Result
	for _, cl := range a.Claims {
		if c.supports(cl.Text, cl.Citations) {
			r.Claims = append(r.Claims, cl)
		} else {
			r.Dropped++
		}
	}
	for _, q := range a.OpenQuestions {
		text := q.Topic + " " + q.Question + " " + strings.Join(q.Options, " ")
		if len(q.Options) >= 2 && len(q.Options) <= 3 && q.Topic != "" && q.Question != "" &&
			c.supports(text, []model.Source{q.Context}) {
			r.OpenQuestions = append(r.OpenQuestions, q)
		} else {
			r.Dropped++
		}
	}
	for _, rec := range a.Recommendations {
		if c.supports(rec.Text, rec.Citations) {
			r.Recommendations = append(r.Recommendations, rec)
		} else {
			r.Dropped++
		}
	}
	return r
}

// Narrative places the kept claims and the drop count into the report's narrative object.
func Narrative(modelName string, r Result) model.Narrative {
	claims := r.Claims
	if claims == nil {
		claims = []model.Claim{}
	}
	return model.Narrative{Model: modelName, Dropped: r.Dropped, Claims: claims}
}

type checker struct {
	m     *manifest.Manifest
	in    map[string]bool
	lines map[string][]string
}

// supports reports whether text is a non-empty statement whose every citation resolves.
func (c *checker) supports(text string, cites []model.Source) bool {
	if strings.TrimSpace(text) == "" || len(cites) == 0 {
		return false
	}
	words := tokens(text)
	for _, s := range cites {
		if !c.resolves(s, words) {
			return false
		}
	}
	return true
}

// resolves applies the three tests to one citation.
func (c *checker) resolves(s model.Source, words map[string]bool) bool {
	// A key names a place no step here resolves, so a citation that carries one is not resolvable.
	if s.Key != "" || !c.in[s.File] || s.Line < 1 {
		return false
	}
	lines, ok := c.fileLines(s.File)
	if !ok || s.Line > len(lines) {
		return false
	}
	for i := s.Line - 2; i <= s.Line; i++ {
		if i < 0 || i >= len(lines) {
			continue
		}
		for w := range tokens(lines[i]) {
			if words[w] {
				return true
			}
		}
	}
	return false
}

// fileLines returns the lines of a recorded file. A file that cannot be read again, or that changed since it was
// recorded, gives false.
func (c *checker) fileLines(file string) ([]string, bool) {
	if l, ok := c.lines[file]; ok {
		return l, l != nil
	}
	data, _, err := c.m.Read(file, maxFileBytes)
	if err != nil {
		c.lines[file] = nil
		return nil, false
	}
	data = bytes.TrimSuffix(data, []byte("\n"))
	l := strings.Split(string(data), "\n")
	if len(data) == 0 {
		l = []string{}
	}
	c.lines[file] = l
	return l, true
}

// tokens returns the lower-case words of s: runs of letters and digits, of at least minTokenLen, that are not stop
// words.
func tokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) >= minTokenLen && !stopWords[w] {
			out[w] = true
		}
	}
	return out
}
