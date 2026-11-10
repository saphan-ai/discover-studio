package llm

import (
	"bytes"
	"sort"

	"github.com/saphan-ai/discover-studio/internal/scan/secrets"
)

// maskPlan says which lines of a file must not be sent, by line number from 1, and the secret types that put them
// there. It is the one place text is judged secret before it leaves; the leak control replaces this file.
//
// Three things are masked: every line secrets.Scan flags; every line of a private key block, from its header to its
// footer, because the body lines carry no marker of their own; and the whole of a tracked environment file.
func maskPlan(file string, content []byte) map[int][]string {
	plan := map[int][]string{}
	lines := bytes.Split(content, []byte("\n"))
	if secrets.TrackedEnv(file) {
		for i := range lines {
			plan[i+1] = []string{secrets.TrackedEnvFile}
		}
		return plan
	}
	for _, f := range secrets.Scan(file, content) {
		plan[f.Source.Line] = append(plan[f.Source.Line], f.SecretType)
	}
	var headers []int
	for n, types := range plan {
		if has(types, secrets.PrivateKey) {
			headers = append(headers, n)
		}
	}
	for _, n := range headers {
		for i := n; i < len(lines); i++ {
			if _, ok := plan[i+1]; !ok {
				plan[i+1] = []string{secrets.PrivateKey}
			}
			if bytes.Contains(lines[i], []byte("-----END")) {
				break
			}
		}
	}
	for n := range plan {
		sort.Strings(plan[n])
	}
	return plan
}

func has(types []string, t string) bool {
	for _, x := range types {
		if x == t {
			return true
		}
	}
	return false
}
