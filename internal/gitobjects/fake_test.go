package gitobjects

import (
	"context"
	"errors"
	"testing"
)

func TestFake(test *testing.T) {
	fake := &Fake{
		Files:      map[string]map[string][]string{"aaaa": {"a.go": {"package a"}}},
		MergeBases: map[string]string{"aaaa bbbb": "cccc"},
	}
	if ok, _ := fake.HasCommit("aaaa"); !ok {
		test.Error("HasCommit(aaaa) = false")
	}
	if ok, _ := fake.HasCommit("bbbb"); ok {
		test.Error("HasCommit(bbbb) = true")
	}
	if lines, err := fake.Lines("aaaa", "a.go"); err != nil || len(lines) != 1 {
		test.Errorf("Lines = %q, %v", lines, err)
	}
	if _, err := fake.Lines("aaaa", "b.go"); !errors.Is(err, ErrMissing) {
		test.Errorf("missing path: error = %v", err)
	}
	if base, err := fake.MergeBase(context.Background(), "aaaa", "bbbb"); err != nil || base != "cccc" {
		test.Errorf("MergeBase = %q, %v", base, err)
	}
	if _, err := fake.MergeBase(context.Background(), "bbbb", "aaaa"); !errors.Is(err, ErrNoMergeBase) {
		test.Errorf("unknown pair: error = %v", err)
	}
}
