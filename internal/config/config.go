// Package config loads strig's YAML config file: tabs made of sections, each
// section a GitHub search query. It is a non-UI package; cmd/strig loads the
// config and passes it in.
package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/MikePors/strigidae/internal/options"
)

// Config is the validated, fully resolved configuration.
type Config struct {
	// Tabs are shown in order; there is always at least one.
	Tabs []Tab
	// Theme is the theme: value as written (a bundled theme's name or a theme
	// file path), or empty for the built-in theme.
	Theme string
	// Options holds the yes/no options the file sets; the rest are at their
	// defaults (see package options).
	Options options.Values
	// Timeouts are the GitHub call deadlines the file sets; a zero field
	// means the file left it out and the user interface's default applies.
	Timeouts Timeouts
	// Plugins are the commands the file defines, in file order.
	Plugins []Plugin
}

// Timeouts holds the optional deadline overrides of the timeouts: key.
type Timeouts struct {
	// Request bounds one small call or write.
	Request time.Duration
	// Load bounds a bigger read: a diff, details, threads, a pull request list.
	Load time.Duration
}

// Tab is a named group of sections, for example the viewer's own PRs or a
// teammate's.
type Tab struct {
	Name string
	// User is the GitHub login (or "@me") the sections were templated with.
	User     string
	Sections []Section
}

// Section is one PR list driven by a GitHub search query.
type Section struct {
	Name string
	// Query is the final search query, with the tab's user already
	// substituted for {user}.
	Query string
	// Limit is the most PRs to fetch, 1 to MaximumLimit.
	Limit int
	Sort  Sort
	Order Order
}

// Path returns the config file location: $XDG_CONFIG_HOME/strig/config.yml,
// falling back to ~/.config/strig/config.yml. getEnvironment and home are injected
// for tests.
func Path(getEnvironment func(string) string, home func() (string, error)) (string, error) {
	if directory := getEnvironment("XDG_CONFIG_HOME"); directory != "" && filepath.IsAbs(directory) {
		return filepath.Join(directory, "strig", "config.yml"), nil
	}
	homeDirectory, err := home()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(homeDirectory, ".config", "strig", "config.yml"), nil
}

// Load reads and validates the file at path. A missing file yields Default
// unless mustExist is set (the user named the file explicitly, so a typo
// shouldn't silently fall back). Problems in the file are returned as *Error.
func Load(path string, mustExist bool) (Config, error) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) && !mustExist {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	defer func() { _ = file.Close() }() // read-only
	// One byte over the limit is enough for Parse to refuse the file.
	data, err := io.ReadAll(io.LimitReader(file, MaximumFileSize+1))
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	return Parse(path, data)
}
