// Package gitobjects reads files as they are at a given commit from the local
// clone, by object rather than from the worktree: uncommitted edits or another
// checked-out branch must not shift line numbers. It has no UI dependencies.
//
// Lines are read through one long-lived `git cat-file --batch`, started on
// first use. Requests go to its stdin, so a path from an untrusted file never
// becomes a command-line argument.
package gitobjects

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/MikePors/strigidae/internal/environment"
)

// MaximumFileSize is the largest file Lines returns.
const MaximumFileSize = 4 << 20

var (
	// ErrMissing means the commit, or the path at that commit, is not in the
	// clone.
	ErrMissing = errors.New("not in the local clone")
	// ErrBinary means the file is not text.
	ErrBinary = errors.New("binary file")
	// ErrTooLarge means the file is over MaximumFileSize.
	ErrTooLarge = errors.New("file too large")
	// ErrNotFile means the path names something other than a file, such as a
	// directory.
	ErrNotFile = errors.New("not a file")
	// ErrNoMergeBase means the two commits share no history.
	ErrNoMergeBase = errors.New("no merge base")
)

// objectName is what a commit must look like: a full or abbreviated SHA.
// Anything else (a branch name, `@{-1}`, `HEAD~2`) is refused, so a file can
// only name commits, never revision expressions.
var objectName = regexp.MustCompile(`^[0-9a-fA-F]{4,64}$`)

// Repository reads objects from one clone. It is safe for concurrent use.
type Repository struct {
	directory string
	log       io.Writer

	mutex   sync.Mutex
	batch   *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	started bool
	err     error // why the batch process could not start or broke
}

// Open returns a Repository for the clone containing directory. Nothing runs
// until the first read. git's stderr goes to log.
func Open(directory string, log io.Writer) *Repository {
	return &Repository{directory: directory, log: log}
}

// HasCommit reports whether the clone has the commit.
func (repository *Repository) HasCommit(sha string) (bool, error) {
	if err := checkObjectName(sha); err != nil {
		return false, err
	}
	kind, _, err := repository.read(sha + "^{commit}")
	if errors.Is(err, ErrMissing) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return kind == "commit", nil
}

// Lines returns the lines of the file at path as it is at commit sha, without
// their line endings.
func (repository *Repository) Lines(sha, path string) ([]string, error) {
	if err := checkObjectName(sha); err != nil {
		return nil, err
	}
	if path == "" || strings.ContainsAny(path, "\n\x00") {
		return nil, fmt.Errorf("read %q: not a usable path", path)
	}
	kind, content, err := repository.read(sha + ":" + path)
	if err != nil {
		return nil, fmt.Errorf("read %s at %s: %w", path, sha, err)
	}
	if kind != "blob" {
		return nil, fmt.Errorf("read %s at %s: %w", path, sha, ErrNotFile)
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return nil, fmt.Errorf("read %s at %s: %w", path, sha, ErrBinary)
	}
	text := strings.TrimSuffix(string(content), "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

// MergeBase returns the best common ancestor of two commits.
func (repository *Repository) MergeBase(ctx context.Context, first, second string) (string, error) {
	for _, sha := range []string{first, second} {
		if err := checkObjectName(sha); err != nil {
			return "", err
		}
	}
	command := repository.command(ctx, "merge-base", first, second)
	output, err := command.Output()
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ExitCode() == 1 && len(output) == 0 {
		return "", fmt.Errorf("merge base of %s and %s: %w", first, second, ErrNoMergeBase)
	}
	if err != nil {
		return "", fmt.Errorf("merge base of %s and %s: %w", first, second, err)
	}
	return strings.TrimSpace(string(output)), nil
}

// Close stops the batch process, if it started.
func (repository *Repository) Close() error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.batch == nil {
		return nil
	}
	closeErr := repository.stdin.Close()
	waitErr := repository.batch.Wait()
	repository.batch = nil
	repository.err = errors.New("closed")
	return errors.Join(closeErr, waitErr)
}

func checkObjectName(sha string) error {
	if !objectName.MatchString(sha) {
		return fmt.Errorf("%q is not a commit SHA", sha)
	}
	return nil
}

func (repository *Repository) command(ctx context.Context, arguments ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, "git", arguments...)
	command.Dir = repository.directory
	command.Env = environment.Scrub(command.Environ())
	command.Stderr = repository.log
	return command
}

// read asks the batch process for one object and returns its type and
// content.
func (repository *Repository) read(request string) (kind string, content []byte, err error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if err := repository.start(); err != nil {
		return "", nil, err
	}
	if _, err := io.WriteString(repository.stdin, request+"\n"); err != nil {
		return "", nil, repository.broken(err)
	}
	header, err := repository.stdout.ReadString('\n')
	if err != nil {
		return "", nil, repository.broken(err)
	}
	// A refusal echoes the request, which may hold spaces; a found object's
	// header ends in its size, so the suffix alone tells them apart.
	trimmed := strings.TrimSuffix(header, "\n")
	if strings.HasSuffix(trimmed, " missing") || strings.HasSuffix(trimmed, " ambiguous") {
		return "", nil, ErrMissing
	}
	fields := strings.Fields(trimmed)
	if len(fields) != 3 {
		return "", nil, repository.broken(fmt.Errorf("unexpected reply %q", header))
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil {
		return "", nil, repository.broken(fmt.Errorf("unexpected reply %q", header))
	}
	// The content and its trailing newline are read even when refused, so
	// the next reply starts where it should.
	if size > MaximumFileSize {
		if _, err := repository.stdout.Discard(size + 1); err != nil {
			return "", nil, repository.broken(err)
		}
		return fields[1], nil, ErrTooLarge
	}
	content = make([]byte, size+1)
	if _, err := io.ReadFull(repository.stdout, content); err != nil {
		return "", nil, repository.broken(err)
	}
	return fields[1], content[:size], nil
}

// start runs the batch process on first use. A failure is remembered: every
// later read reports it rather than retrying.
func (repository *Repository) start() error {
	if repository.started {
		return repository.err
	}
	repository.started = true
	batch := repository.command(context.Background(), "cat-file", "--batch")
	stdin, err := batch.StdinPipe()
	if err != nil {
		repository.err = fmt.Errorf("start git cat-file: %w", err)
		return repository.err
	}
	stdout, err := batch.StdoutPipe()
	if err != nil {
		repository.err = fmt.Errorf("start git cat-file: %w", err)
		return repository.err
	}
	if err := batch.Start(); err != nil {
		repository.err = fmt.Errorf("start git cat-file: %w", err)
		return repository.err
	}
	repository.batch, repository.stdin, repository.stdout = batch, stdin, bufio.NewReader(stdout)
	return nil
}

// broken records that the batch process can no longer be trusted to be in
// step, so every later read fails rather than returning another file's lines.
func (repository *Repository) broken(err error) error {
	repository.err = fmt.Errorf("git cat-file: %w", err)
	return repository.err
}
