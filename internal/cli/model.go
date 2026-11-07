package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/saphan-ai/discover/internal/citecheck"
	"github.com/saphan-ai/discover/internal/llm"
	"github.com/saphan-ai/discover/internal/manifest"
)

// modelTransport is the transport of the model call; nil means the default. Tests put a counting one here.
var modelTransport http.RoundTripper

// analysers runs after the attestation's first pass and before the model step. It is where the scanners that
// record what they read into the manifest run; none is wired yet, and tests use it to put files there.
var analysers = func(root string, m *manifest.Manifest) {}

// modelTimeout bounds one model call.
const modelTimeout = 5 * time.Minute

// maxModelFile caps the size of one file read for the model; a larger one is not sent.
const maxModelFile = 256 << 10

// modelFlags are the flags of the optional model step.
type modelFlags struct {
	name   string
	url    string
	keyEnv string
	key    string
}

// on reports whether the model step was asked for. Only --model asks.
func (f modelFlags) on() bool { return f.name != "" }

// check returns a refusal sentence for a flag combination that cannot run, or "". It names flags, never a value
// of the key.
func (f modelFlags) check() string {
	if f.name == "" {
		if f.url != "" || f.keyEnv != "" {
			return "--model-url and --model-key-env need --model; the model step is off unless --model is given"
		}
		return ""
	}
	if f.url == "" {
		return "--model needs --model-url, the base URL of an OpenAI-compatible endpoint"
	}
	u, err := url.Parse(f.url)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "--model-url must be an http or https URL with a host"
	}
	return ""
}

// narration is what the model step produced after the filter.
type narration struct {
	result citecheck.Result
	// skipped counts the recorded files that could not be read for the model and were left out.
	skipped int
}

// narrate sends the files the scan read to the model, masked, and keeps what the filter lets through. With no file
// read there is nothing to describe and no call is made.
func narrate(f modelFlags, m *manifest.Manifest) (*narration, error) {
	var files []llm.File
	skipped := 0
	for _, e := range m.Entries() {
		data, _, err := m.Read(e.Path, maxModelFile)
		if err != nil {
			skipped++
			continue
		}
		files = append(files, llm.File{Path: e.Path, Content: data})
	}
	if len(files) == 0 {
		return &narration{skipped: skipped}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), modelTimeout)
	defer cancel()
	cfg := llm.Config{BaseURL: f.url, Model: f.name, Key: f.key, HTTP: llm.NewClient(modelTransport, modelTimeout)}
	ans, err := llm.Ask(ctx, cfg, files)
	if err != nil {
		return nil, fmt.Errorf("the model step failed: %w", err)
	}
	return &narration{result: citecheck.Check(ans, m), skipped: skipped}, nil
}
