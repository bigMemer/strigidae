// Package gitrepository infers which GitHub repository a working directory belongs
// to by reading its git remotes. It has no UI dependencies; git is run through
// an injectable Runner so tests never need a real checkout.
package gitrepository

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"

	"github.com/MikePors/strigidae/internal/environment"
	"github.com/MikePors/strigidae/internal/github"
)

var (
	// ErrNotCheckout means the directory is not inside a git work tree (or git
	// is not installed).
	ErrNotCheckout = errors.New("not inside a git checkout")
	// ErrNoGitHubRemote means the checkout has no usable "upstream" or
	// "origin" remote.
	ErrNoGitHubRemote = errors.New("no GitHub remote named upstream or origin")
)

// remoteNames are the remotes to try, in order. "upstream" wins so that a fork
// checkout lists the parent repository's pull requests.
var remoteNames = []string{"upstream", "origin"}

// Remote is a repository on a specific host.
type Remote struct {
	// Host is the GitHub host, e.g. "github.com" or a GitHub Enterprise host.
	Host       string
	Repository github.Repository
}

// Runner runs git with args in dir and returns its trimmed standard output.
type Runner func(ctx context.Context, directory string, arguments ...string) (string, error)

// ExecRunner is the Runner that executes the real git binary.
func ExecRunner(ctx context.Context, directory string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", arguments...)
	command.Dir = directory
	command.Env = environment.Scrub(command.Environ())
	out, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(arguments, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// CurrentBranch is the branch checked out in directory, or "" on a detached
// head.
func CurrentBranch(ctx context.Context, run Runner, directory string) (string, error) {
	branch, err := run(ctx, directory, "branch", "--show-current")
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNotCheckout, err)
	}
	return branch, nil
}

// Infer finds the GitHub repository for the checkout containing directory.
//
// A remote whose host knownHost rejects is skipped, so a mirror on another
// forge falls through to the next remote. A nil knownHost accepts every host.
func Infer(ctx context.Context, run Runner, directory string, knownHost func(host string) bool) (Remote, error) {
	if _, err := run(ctx, directory, "rev-parse", "--show-toplevel"); err != nil {
		return Remote{}, fmt.Errorf("%w: %w", ErrNotCheckout, err)
	}
	var unknown []string
	for _, name := range remoteNames {
		raw, err := run(ctx, directory, "remote", "get-url", name)
		if err != nil {
			continue // remote not configured
		}
		remote, err := ParseURL(raw)
		if err != nil {
			continue
		}
		if knownHost != nil && !knownHost(remote.Host) {
			unknown = append(unknown, remote.Host)
			continue
		}
		return remote, nil
	}
	if len(unknown) > 0 {
		return Remote{}, &UnknownHostError{Hosts: unknown}
	}
	return Remote{}, ErrNoGitHubRemote
}

// UnknownHostError means the checkout has parseable remotes, but none on a
// host the caller recognises (for example an SSH alias or another forge).
type UnknownHostError struct {
	// Hosts are the hosts of the rejected remotes, in preference order.
	Hosts []string
}

// Error implements error.
func (unknownHostError *UnknownHostError) Error() string {
	return fmt.Sprintf("remote host %s is not a known GitHub host", unknownHostError.QuotedHosts())
}

// QuotedHosts returns the hosts individually quoted and comma-separated.
func (unknownHostError *UnknownHostError) QuotedHosts() string {
	queries := make([]string, len(unknownHostError.Hosts))
	for i, host := range unknownHostError.Hosts {
		queries[i] = strconv.Quote(host)
	}
	return strings.Join(queries, ", ")
}

// ParseURL parses a git remote URL into a host and repository. It accepts
// scp-like SSH (git@host:owner/repo.git), ssh://, git://, https:// and http://.
func ParseURL(raw string) (Remote, error) {
	raw = strings.TrimSpace(raw)
	host, path, err := splitURL(raw)
	if err != nil {
		return Remote{}, fmt.Errorf("parse remote %q: %w", raw, err)
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	repository, err := github.ParseRepository(path)
	if err != nil || host == "" {
		return Remote{}, fmt.Errorf("parse remote %q: want host and owner/repo", raw)
	}
	return Remote{Host: github.NormalizeHost(host), Repository: repository}, nil
}

func splitURL(raw string) (host, path string, err error) {
	if !strings.Contains(raw, "://") {
		// scp-like: [user@]host:path
		hostPart, path, ok := strings.Cut(raw, ":")
		if !ok || strings.Contains(hostPart, "/") {
			return "", "", errors.New("unrecognised URL")
		}
		if _, host, found := strings.Cut(hostPart, "@"); found {
			hostPart = host
		}
		return hostPart, path, nil
	}
	address, err := url.Parse(raw)
	if err != nil {
		return "", "", err
	}
	switch address.Scheme {
	case "ssh", "git", "https", "http":
		host = address.Hostname() // any port is a transport detail, not part of the host
	default:
		return "", "", fmt.Errorf("unsupported scheme %q", address.Scheme)
	}
	return host, address.Path, nil
}
