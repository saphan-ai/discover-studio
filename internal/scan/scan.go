// Package scan runs the scanners over one repository and gathers their sections into a model.Repo.
//
// The registry is Scanners. A later wagon adds one line to it, and its scanner fills its own section of State.Repo.
// Scanners run in the order listed, one after the other: a scanner may rely on what an earlier one left in State
// (the structure scanner reads the file list the inventory scanner produced).
//
// Nothing here writes to the target, and every file a scanner reads goes through State.Manifest.
package scan

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"

	"github.com/saphan-ai/discover/internal/attest"
	"github.com/saphan-ai/discover/internal/manifest"
	"github.com/saphan-ai/discover/internal/model"
	"github.com/saphan-ai/discover/internal/scan/commands"
	"github.com/saphan-ai/discover/internal/scan/inventory"
	"github.com/saphan-ai/discover/internal/scan/secrets"
	"github.com/saphan-ai/discover/internal/scan/stack"
	"github.com/saphan-ai/discover/internal/scan/structure"
	"github.com/saphan-ai/discover/internal/scan/todo"
)

// Options tune one run.
type Options struct {
	// MaxBytes is the size cap of one file read; zero means inventory.DefaultMaxBytes.
	MaxBytes int64
	// Attest carries the git settings.
	Attest attest.Options
}

// State is what a scanner reads and fills.
type State struct {
	// Root is the real, absolute path of the target.
	Root string
	Mode attest.Mode
	Opts Options
	// Manifest records every file read under Root.
	Manifest *manifest.Manifest
	// Files is every path the inventory listed, sorted. It is empty until the inventory scanner has run.
	Files []string
	// Skipped is every file a scanner did not read or did not count, with the reason.
	Skipped []inventory.Skipped
	// Repo is the report of the repository; each scanner fills its own section.
	Repo *model.Repo
	// TodoTotal is the number of TODO, FIXME, HACK and XXX markers found; Repo.Findings lists at most todo.Max.
	TodoTotal int
}

// Scanner fills one section of the report.
type Scanner struct {
	Name string
	Run  func(*State) error
}

// Scanners is the registry, in the order they run.
var Scanners = []Scanner{
	{"inventory", runInventory},
	{"structure", runStructure},
	{"stack", runStack},
	{"commands", runCommands},
	{"secrets", runSecrets},
	{"todo", runTodo},
}

func runInventory(s *State) error {
	res, err := inventory.Scan(s.Root, s.Mode, s.Manifest, inventory.Options{MaxBytes: s.Opts.MaxBytes, Attest: s.Opts.Attest})
	if err != nil {
		return err
	}
	s.Repo.Inventory = res.Inventory
	s.Files = res.Files
	s.Skipped = append(s.Skipped, res.Skipped...)
	return nil
}

func runStructure(s *State) error {
	s.Repo.Structure = structure.Scan(s.Files)
	return nil
}

func runStack(s *State) error {
	limit := s.Opts.MaxBytes
	if limit <= 0 {
		limit = inventory.DefaultMaxBytes
	}
	rows, skipped := stack.Scan(s.Files, func(p string) ([]byte, error) {
		b, _, err := s.Manifest.Read(p, limit)
		return b, err
	})
	s.Repo.Stack = rows
	s.Skipped = append(s.Skipped, skipped...)
	return nil
}

// runCommands cites the commands, entry points, CI and containers the files hold. It runs none of them. A file that is
// already in Skipped keeps its first reason.
func runCommands(s *State) error {
	limit := s.Opts.MaxBytes
	if limit <= 0 {
		limit = inventory.DefaultMaxBytes
	}
	res := commands.Scan(s.Files, func(p string) ([]byte, error) {
		b, _, err := s.Manifest.Read(p, limit)
		return b, err
	})
	s.Repo.EntryPoints, s.Repo.Commands, s.Repo.CI, s.Repo.Containers = res.EntryPoints, res.Commands, res.CI, res.Containers
	named := map[string]bool{}
	for _, k := range s.Skipped {
		named[k.Path] = true
	}
	for _, k := range res.Skipped {
		if !named[k.Path] {
			s.Skipped = append(s.Skipped, inventory.Skipped{Path: k.Path, Reason: k.Reason})
		}
	}
	return nil
}

// eachText calls fn with the content of every listed file a scanner may read. A file the inventory already skipped is
// not read again. A file over the size cap, or one that cannot be read, is added to Skipped once, by path and reason,
// and its error text (which could carry anything) is dropped.
func eachText(s *State, fn func(path string, content []byte)) {
	limit := s.Opts.MaxBytes
	if limit <= 0 {
		limit = inventory.DefaultMaxBytes
	}
	skipped := map[string]bool{}
	for _, k := range s.Skipped {
		skipped[k.Path] = true
	}
	for _, p := range s.Files {
		if skipped[p] {
			continue
		}
		b, _, err := s.Manifest.Read(p, limit)
		if err != nil {
			reason := inventory.ReasonUnreadable
			if errors.Is(err, manifest.ErrTooLarge) {
				reason = inventory.ReasonOverCap
			}
			s.Skipped = append(s.Skipped, inventory.Skipped{Path: p, Reason: reason})
			skipped[p] = true
			continue
		}
		fn(p, b)
	}
}

// runSecrets lists the secrets by file, line and type. In git mode it also lists the environment files git tracks.
func runSecrets(s *State) error {
	if s.Repo.Findings == nil {
		s.Repo.Findings = []model.Finding{}
	}
	if s.Mode == attest.ModeGit {
		tracked, err := attest.TrackedFiles(s.Root, s.Opts.Attest)
		if err != nil {
			return err
		}
		isTracked := make(map[string]bool, len(tracked))
		for _, p := range tracked {
			isTracked[p] = true
		}
		for _, p := range s.Files {
			if isTracked[p] && secrets.TrackedEnv(p) {
				s.Repo.Findings = append(s.Repo.Findings, secrets.TrackedEnvFinding(p))
			}
		}
	}
	eachText(s, func(p string, b []byte) {
		s.Repo.Findings = append(s.Repo.Findings, secrets.Scan(p, b)...)
	})
	sort.SliceStable(s.Repo.Findings, func(i, j int) bool {
		a, b := s.Repo.Findings[i].Source, s.Repo.Findings[j].Source
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	return nil
}

// runTodo lists the first todo.Max markers in file order and counts them all.
func runTodo(s *State) error {
	if s.Repo.Findings == nil {
		s.Repo.Findings = []model.Finding{}
	}
	listed := 0
	eachText(s, func(p string, b []byte) {
		for _, f := range todo.Scan(p, b) {
			s.TodoTotal++
			if listed < todo.Max {
				s.Repo.Findings = append(s.Repo.Findings, f)
				listed++
			}
		}
	})
	return nil
}

// Result is what one run found. Repo holds only the sections the registered scanners fill; the rest is left for the
// caller (the attestation and the other sections).
type Result struct {
	Repo         model.Repo
	Skipped      []inventory.Skipped
	ReadManifest model.ReadManifest
	// TodoTotal is the number of markers found; the model has no field for it (see State.TodoTotal).
	TodoTotal int
}

// Run scans root, which is an existing directory, in the given mode.
func Run(root string, mode attest.Mode, opts Options) (Result, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return Result{}, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", filepath.Base(abs), cause(err))
	}
	s := &State{Root: real, Mode: mode, Opts: opts, Manifest: manifest.New(real), Repo: &model.Repo{Name: filepath.Base(abs)}}
	for _, sc := range Scanners {
		if err := sc.Run(s); err != nil {
			return Result{}, fmt.Errorf("%s scanner: %s: %w", sc.Name, filepath.Base(abs), cause(err))
		}
	}
	rm := model.ReadManifest{Entries: []model.ManifestEntry{}}
	for _, e := range s.Manifest.Entries() {
		rm.Entries = append(rm.Entries, model.ManifestEntry{Path: e.Path, SHA256: e.SHA256, Bytes: int(e.Size)})
		rm.BytesRead += int(e.Size)
	}
	rm.FilesRead = len(rm.Entries)
	return Result{Repo: *s.Repo, Skipped: s.Skipped, ReadManifest: rm, TodoTotal: s.TodoTotal}, nil
}

// cause drops the path an operating system error carries, so an error never names the absolute root.
func cause(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
