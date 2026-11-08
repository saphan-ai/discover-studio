// Package todo lists the TODO, FIXME, HACK and XXX markers of a repository. A finding names the marker, the file and
// the line; the text of the line is never copied, so a comment cannot carry anything into the report.
package todo

import (
	"bytes"
	"regexp"

	"github.com/saphan-ai/discover/internal/model"
)

// Max is the number of todo findings one repository lists.
const Max = 30

var marker = regexp.MustCompile(`\b(TODO|FIXME|HACK|XXX)\b`)

// Scan returns the first marker on each line of one file's content, in line order. It returns every one; the limit
// is the caller's, who must still count them all.
func Scan(file string, content []byte) []model.Finding {
	var out []model.Finding
	for i, line := range bytes.Split(content, []byte("\n")) {
		if m := marker.Find(line); m != nil {
			out = append(out, model.Finding{Kind: "todo", Severity: "info", Source: model.Source{File: file, Line: i + 1}, Text: string(m)})
		}
	}
	return out
}
