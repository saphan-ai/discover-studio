package llm

import (
	"strings"
	"testing"
)

// The tag is lengthened until the text does not contain it. The digest is given, so the text can hold its 8-digit
// prefix, which a tag that does not lengthen would reuse.
func TestTheTagIsLengthenedUntilTheTextDoesNotContainIt(t *testing.T) {
	digest := "0123456789abcdef0123456789abcdef"
	text := "x tag-01234567 y tag-0123456789ab z\n"
	got := tagFrom(text, digest)
	if strings.Contains(text, got) {
		t.Errorf("the tag %q occurs in the text", got)
	}
	if got != "tag-0123456789abcdef" {
		t.Errorf("tag = %q, want the shortest prefix the text lacks", got)
	}
	// Control: a text without the prefix keeps the 8-digit tag.
	if got := tagFrom("plain\n", digest); got != "tag-01234567" {
		t.Errorf("control: tag = %q", got)
	}
}
