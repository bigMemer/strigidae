package gitobjects

import (
	"context"
	"fmt"
)

// Fake is an in-memory stand-in for Repository, for tests and demos. Files
// are keyed by commit, then path.
type Fake struct {
	Files map[string]map[string][]string
	// MergeBases maps "first second" to their merge base.
	MergeBases map[string]string
}

// HasCommit reports whether the fake has any file at sha.
func (fake *Fake) HasCommit(sha string) (bool, error) {
	_, ok := fake.Files[sha]
	return ok, nil
}

// Lines returns the file's lines, or ErrMissing.
func (fake *Fake) Lines(sha, path string) ([]string, error) {
	lines, ok := fake.Files[sha][path]
	if !ok {
		return nil, fmt.Errorf("read %s at %s: %w", path, sha, ErrMissing)
	}
	return lines, nil
}

// MergeBase returns the recorded merge base, or ErrNoMergeBase.
func (fake *Fake) MergeBase(_ context.Context, first, second string) (string, error) {
	base, ok := fake.MergeBases[first+" "+second]
	if !ok {
		return "", fmt.Errorf("merge base of %s and %s: %w", first, second, ErrNoMergeBase)
	}
	return base, nil
}
