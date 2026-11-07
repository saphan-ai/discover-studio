package attest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// TrackedFiles is git's list of the files in the index of a git target, one entry per path, sorted. Unlike ListFiles
// it leaves out the untracked files. A plain directory is an error. An error names the target by its base name.
func TrackedFiles(root string, opts Options) ([]string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, scrub(err, filepath.Base(abs), abs)
	}
	files, err := trackedFiles(real, opts)
	if err != nil {
		err = scrub(err, filepath.Base(abs), real, abs)
	}
	return files, err
}

func trackedFiles(real string, opts Options) ([]string, error) {
	if err := requireGit(real, opts); err != nil {
		return nil, err
	}
	out, err := runGit(real, opts, "ls-files", "-z", "--cached")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	files := []string{}
	for _, p := range nulList(out) {
		if !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
	}
	sort.Strings(files)
	return files, nil
}

// requireGit refuses a root that has no .git entry or that git does not accept as the top of a repository.
func requireGit(real string, opts Options) error {
	if _, err := os.Lstat(filepath.Join(real, ".git")); err != nil {
		return fmt.Errorf("%s: not a git repository: there is no .git entry at its root", filepath.Base(real))
	}
	return checkGit(real, opts)
}
