package cli

import (
	"fmt"
	"sort"
	"time"

	"github.com/saphan-ai/discover-studio/internal/attest"
	"github.com/saphan-ai/discover-studio/internal/manifest"
	"github.com/saphan-ai/discover-studio/internal/model"
	"github.com/saphan-ai/discover-studio/internal/report"
	scanpkg "github.com/saphan-ai/discover-studio/internal/scan"
)

// Names of the report files scan writes into --out beside the attestation.
const (
	reportFile = "REPORT.md"
	jsonFile   = "discover.json"
)

// scanners runs the registered scanners over the target and records every file they read into m. Tests replace it.
var scanners = func(root string, mode attest.Mode, opts attest.Options, m *manifest.Manifest) (scanpkg.Result, error) {
	return scanpkg.RunInto(root, mode, scanpkg.Options{Attest: opts}, m)
}

// conventionFiles are the top-level files a workspace profile names when the repository carries them.
var conventionFiles = []string{"AGENTS.md", "CLAUDE.md", "CONTRIBUTING.md", ".editorconfig"}

// buildReport assembles the discover/v1 model of one target from its attestation, the read-only proof and what the
// scanners found. It reads nothing itself.
func buildReport(before, after attest.Attestation, diffs []attest.Diff, res scanpkg.Result, m *manifest.Manifest,
	started, finished time.Time) model.Report {
	status := "EQUAL"
	if len(diffs) > 0 {
		status = "NOT-EQUAL"
	}
	repo := res.Repo
	repo.Attestation = model.Attestation{
		Root:          before.Root,
		Mode:          string(before.Mode),
		Tree:          modelTree(before.Tree),
		ReadOnlyProof: model.ReadOnlyProof{Status: status, Before: before.Tree.Digest, After: after.Tree.Digest},
		ReadManifest:  readManifest(m),
	}
	if g := before.Git; g != nil {
		// The attestation counts dirty paths; the model lists them. The count is printed and the list is left empty
		// rather than invented.
		repo.Attestation.Git = &model.GitState{Branch: g.Branch, Detached: g.Detached, Head: g.Head, Clean: g.Clean,
			DirtyPaths: []string{}}
	}
	if u := before.Untracked; u != nil {
		t := modelTree(*u)
		repo.Attestation.Untracked = &t
	}
	fillEmpty(&repo)

	vcs := "none"
	if before.Mode == attest.ModeGit {
		vcs = "git"
	}
	return model.Report{
		SchemaVersion: "discover/v1",
		Tool:          model.Tool{Name: "discover", Version: version, Commit: commit},
		Scan: model.Scan{
			Started:               started.UTC().Format(time.RFC3339),
			Finished:              finished.UTC().Format(time.RFC3339),
			Targets:               []model.Target{{Path: before.Root, Mode: string(before.Mode)}},
			ReadOnlyProofAllEqual: status == "EQUAL",
		},
		Repos:            []model.Repo{repo},
		WorkspaceProfile: model.WorkspaceProfile{Repos: []model.ProfileRepo{profile(repo, vcs)}},
	}
}

// renderReport returns REPORT.md and discover.json for r. discover.json is validated against the schema before it is
// returned, so a scan never writes a document its own schema refuses.
func renderReport(r model.Report) (md, js []byte, err error) {
	js, err = model.Marshal(&r)
	if err != nil {
		return nil, nil, err
	}
	md, err = report.Render(r, 0)
	if err != nil {
		return nil, nil, err
	}
	return md, js, nil
}

func modelTree(t attest.Tree) model.Tree {
	return model.Tree{Files: t.Files, Bytes: int(t.Bytes), Links: t.Links, Submodules: t.Submodules,
		Unreadable: t.Unreadable, Digest: t.Digest}
}

func readManifest(m *manifest.Manifest) model.ReadManifest {
	rm := model.ReadManifest{Entries: []model.ManifestEntry{}}
	for _, e := range m.Entries() {
		rm.Entries = append(rm.Entries, model.ManifestEntry{Path: e.Path, SHA256: e.SHA256, Bytes: int(e.Size)})
		rm.BytesRead += int(e.Size)
	}
	rm.FilesRead = len(rm.Entries)
	return rm
}

// profile is the workspace profile of one repository: what Studio imports at setup. Every command in it is one the
// commands scanner cited; none is run.
func profile(repo model.Repo, vcs string) model.ProfileRepo {
	p := model.ProfileRepo{Name: repo.Name, RelativePath: ".", VCS: vcs, Languages: []string{},
		Commands:        model.ProfileCommands{Build: []model.ProfileCommand{}, Test: []model.ProfileCommand{}, Run: []model.ProfileCommand{}},
		ConventionFiles: []string{}, CI: repo.CI}
	for _, l := range repo.Inventory.Languages {
		p.Languages = append(p.Languages, l.Name)
	}
	for _, c := range repo.Commands {
		pc := model.ProfileCommand{Text: c.Text, Source: c.Source}
		switch c.Kind {
		case "build":
			p.Commands.Build = append(p.Commands.Build, pc)
		case "test":
			p.Commands.Test = append(p.Commands.Test, pc)
		case "run":
			p.Commands.Run = append(p.Commands.Run, pc)
		}
	}
	top := map[string]bool{}
	for _, e := range repo.Inventory.TopLevel {
		if e.Kind == "file" {
			top[e.Name] = true
		}
	}
	for _, f := range conventionFiles {
		if top[f] {
			p.ConventionFiles = append(p.ConventionFiles, f)
		}
	}
	sort.Strings(p.ConventionFiles)
	return p
}

// fillEmpty turns every absent list into an empty one, so discover.json says "none found" as [] and never as null.
func fillEmpty(r *model.Repo) {
	if r.Stack == nil {
		r.Stack = []model.StackRow{}
	}
	if r.EntryPoints == nil {
		r.EntryPoints = []model.EntryPoint{}
	}
	if r.Commands == nil {
		r.Commands = []model.Command{}
	}
	if r.CI == nil {
		r.CI = []model.CI{}
	}
	if r.Containers == nil {
		r.Containers = []model.Container{}
	}
	if r.Wiring == nil {
		r.Wiring = []model.Wiring{}
	}
	if r.Findings == nil {
		r.Findings = []model.Finding{}
	}
	if r.UI == nil {
		r.UI = []model.UIItem{}
	}
	if r.Narrative.Claims == nil {
		r.Narrative.Claims = []model.Claim{}
	}
	if r.OpenQuestions == nil {
		r.OpenQuestions = []model.OpenQuestion{}
	}
	if r.Recommendations == nil {
		r.Recommendations = []model.Recommendation{}
	}
	if r.Absent == nil {
		r.Absent = []model.Absent{}
	}
}

// summaryLine is the one line scan prints about what the scanners found.
func summaryLine(r model.Repo, todoTotal int) string {
	return fmt.Sprintf("found: %d stack rows  %d commands  %d entry points  %d CI files  %d findings  %d TODO markers",
		len(r.Stack), len(r.Commands), len(r.EntryPoints), len(r.CI), len(r.Findings), todoTotal)
}
