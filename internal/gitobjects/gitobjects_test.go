package gitobjects

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// clone is a throwaway repository with two commits on main and one on a
// branch with no shared history.
type clone struct {
	directory     string
	first, second string
	unrelated     string
}

// newClone builds the repository with git, isolated from the user's and the
// system's git config.
func newClone(test *testing.T) clone {
	test.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		test.Skip("git is not installed")
	}
	home := test.TempDir()
	test.Setenv("HOME", home)
	test.Setenv("XDG_CONFIG_HOME", home)
	test.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	test.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))
	test.Setenv("GIT_AUTHOR_NAME", "test")
	test.Setenv("GIT_AUTHOR_EMAIL", "test@example.invalid")
	test.Setenv("GIT_COMMITTER_NAME", "test")
	test.Setenv("GIT_COMMITTER_EMAIL", "test@example.invalid")

	directory := test.TempDir()
	git := func(arguments ...string) string {
		test.Helper()
		command := exec.Command("git", arguments...)
		command.Dir = directory
		output, err := command.CombinedOutput()
		if err != nil {
			test.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
		}
		return strings.TrimSpace(string(output))
	}
	write := func(path, content string) {
		test.Helper()
		full := filepath.Join(directory, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			test.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			test.Fatal(err)
		}
	}

	git("init", "-q", "-b", "main")
	write("a.go", "package a\n\nfunc A() {}\n")
	write("dir/with space.txt", "one\ntwo")
	write("-dash.txt", "dash\n")
	write("binary.bin", "a\x00b\n")
	write("empty.txt", "")
	git("add", "-A")
	git("commit", "-q", "-m", "first")
	first := git("rev-parse", "HEAD")
	write("a.go", "package a\n\nfunc A() {}\n\nfunc B() {}\n")
	git("commit", "-q", "-am", "second")
	second := git("rev-parse", "HEAD")
	git("checkout", "-q", "--orphan", "other")
	git("rm", "-q", "-rf", ".")
	write("b.go", "package b\n")
	git("add", "-A")
	git("commit", "-q", "-m", "unrelated")
	unrelated := git("rev-parse", "HEAD")
	git("checkout", "-q", "main")
	// An uncommitted edit, which reads by commit must not see.
	write("a.go", "changed in the worktree\n")
	return clone{directory: directory, first: first, second: second, unrelated: unrelated}
}

func open(test *testing.T, clone clone) *Repository {
	test.Helper()
	repository := Open(clone.directory, &bytes.Buffer{})
	test.Cleanup(func() {
		if err := repository.Close(); err != nil {
			test.Errorf("close: %v", err)
		}
	})
	return repository
}

func TestLines(test *testing.T) {
	clone := newClone(test)
	repository := open(test, clone)
	for _, testCase := range []struct {
		name    string
		sha     string
		path    string
		want    []string
		wantErr error
	}{
		{"at the first commit", clone.first, "a.go", []string{"package a", "", "func A() {}"}, nil},
		{"at the second commit, not the worktree", clone.second, "a.go", []string{"package a", "", "func A() {}", "", "func B() {}"}, nil},
		{"abbreviated SHA", clone.first[:10], "a.go", []string{"package a", "", "func A() {}"}, nil},
		{"space in the path, no final newline", clone.first, "dir/with space.txt", []string{"one", "two"}, nil},
		{"path starting with a dash", clone.first, "-dash.txt", []string{"dash"}, nil},
		{"empty file", clone.first, "empty.txt", nil, nil},
		{"missing path", clone.first, "nope.go", nil, ErrMissing},
		{"path added later", clone.unrelated, "a.go", nil, ErrMissing},
		{"missing commit", strings.Repeat("0", 40), "a.go", nil, ErrMissing},
		{"directory", clone.first, "dir", nil, ErrNotFile},
		{"binary", clone.first, "binary.bin", nil, ErrBinary},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			got, err := repository.Lines(testCase.sha, testCase.path)
			if !errors.Is(err, testCase.wantErr) {
				test.Fatalf("error = %v, want %v", err, testCase.wantErr)
			}
			if diff := cmp.Diff(testCase.want, got); diff != "" {
				test.Errorf("lines (-want +got):\n%s", diff)
			}
		})
	}
	// After all those, including refusals, the stream is still in step.
	if got, err := repository.Lines(clone.second, "a.go"); err != nil || len(got) != 5 {
		test.Errorf("read after the others = %q, %v", got, err)
	}
}

func TestLinesRefusesWhatIsNotACommitOrPath(test *testing.T) {
	clone := newClone(test)
	repository := open(test, clone)
	for _, testCase := range []struct{ sha, path string }{
		{"main", "a.go"},
		{"HEAD~1", "a.go"},
		{"@{-1}", "a.go"},
		{clone.first, ""},
		{clone.first, "a.go\n" + clone.second + ":a.go"},
		{clone.first, "a\x00.go"},
	} {
		if _, err := repository.Lines(testCase.sha, testCase.path); err == nil {
			test.Errorf("Lines(%q, %q) succeeded", testCase.sha, testCase.path)
		}
	}
}

func TestLinesRefusesLargeFiles(test *testing.T) {
	clone := newClone(test)
	big := filepath.Join(clone.directory, "big.txt")
	if err := os.WriteFile(big, bytes.Repeat([]byte("x\n"), MaximumFileSize/2+1), 0o644); err != nil {
		test.Fatal(err)
	}
	command := exec.Command("git", "hash-object", "-w", "big.txt")
	command.Dir = clone.directory
	if output, err := command.CombinedOutput(); err != nil {
		test.Fatalf("hash-object: %v\n%s", err, output)
	}
	// The blob is in the clone but on no commit: read it through a tree.
	tree := exec.Command("sh", "-c", `printf '100644 blob %s\tbig.txt\n' "$(git hash-object big.txt)" | git mktree`)
	tree.Dir = clone.directory
	treeOutput, err := tree.Output()
	if err != nil {
		test.Fatalf("mktree: %v", err)
	}
	commit := exec.Command("git", "commit-tree", "-m", "big", strings.TrimSpace(string(treeOutput)))
	commit.Dir = clone.directory
	commitOutput, err := commit.Output()
	if err != nil {
		test.Fatalf("commit-tree: %v", err)
	}
	repository := open(test, clone)
	if _, err := repository.Lines(strings.TrimSpace(string(commitOutput)), "big.txt"); !errors.Is(err, ErrTooLarge) {
		test.Errorf("error = %v, want ErrTooLarge", err)
	}
	if got, err := repository.Lines(clone.first, "-dash.txt"); err != nil || len(got) != 1 {
		test.Errorf("read after the refusal = %q, %v", got, err)
	}
}

func TestHasCommit(test *testing.T) {
	clone := newClone(test)
	repository := open(test, clone)
	for _, testCase := range []struct {
		name string
		sha  string
		want bool
	}{
		{"present", clone.first, true},
		{"abbreviated", clone.second[:8], true},
		{"missing", strings.Repeat("a", 40), false},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			got, err := repository.HasCommit(testCase.sha)
			if err != nil || got != testCase.want {
				test.Errorf("HasCommit = %v, %v, want %v", got, err, testCase.want)
			}
		})
	}
	tree := exec.Command("git", "rev-parse", clone.first+"^{tree}")
	tree.Dir = clone.directory
	output, err := tree.Output()
	if err != nil {
		test.Fatal(err)
	}
	if got, err := repository.HasCommit(strings.TrimSpace(string(output))); err != nil || got {
		test.Errorf("HasCommit(a tree) = %v, %v, want false", got, err)
	}
	if _, err := repository.HasCommit("main"); err == nil {
		test.Error("HasCommit(a branch name) succeeded")
	}
}

func TestMergeBase(test *testing.T) {
	clone := newClone(test)
	repository := open(test, clone)
	got, err := repository.MergeBase(context.Background(), clone.first, clone.second)
	if err != nil || got != clone.first {
		test.Errorf("MergeBase = %q, %v, want %q", got, err, clone.first)
	}
	if _, err := repository.MergeBase(context.Background(), clone.second, clone.unrelated); !errors.Is(err, ErrNoMergeBase) {
		test.Errorf("unrelated: error = %v, want ErrNoMergeBase", err)
	}
	if _, err := repository.MergeBase(context.Background(), "main", clone.first); err == nil {
		test.Error("MergeBase(a branch name) succeeded")
	}
}

func TestNotAClone(test *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		test.Skip("git is not installed")
	}
	test.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	repository := Open(test.TempDir(), &bytes.Buffer{})
	defer func() { _ = repository.Close() }()
	if _, err := repository.Lines(strings.Repeat("a", 40), "a.go"); err == nil {
		test.Error("Lines outside a clone succeeded")
	}
	if _, err := repository.Lines(strings.Repeat("a", 40), "a.go"); err == nil {
		test.Error("second Lines outside a clone succeeded")
	}
}

func TestCloseBeforeUse(test *testing.T) {
	if err := Open(test.TempDir(), &bytes.Buffer{}).Close(); err != nil {
		test.Error(err)
	}
}
