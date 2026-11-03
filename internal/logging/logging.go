// Package logging sets up the file logger. The TUI owns stdout and stderr, so
// logs only ever go to a file under the XDG state directory.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// StateDirectory returns the directory strig keeps its state in: $XDG_STATE_HOME,
// falling back to ~/.local/state. getEnvironment and home are injected for tests.
func StateDirectory(getEnvironment func(string) string, home func() (string, error)) (string, error) {
	if directory := getEnvironment("XDG_STATE_HOME"); directory != "" && filepath.IsAbs(directory) {
		return directory, nil
	}
	homeDirectory, err := home()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(homeDirectory, ".local", "state"), nil
}

// Path returns the log file location: strig/strig.log under StateDirectory.
func Path(getEnvironment func(string) string, home func() (string, error)) (string, error) {
	directory, err := StateDirectory(getEnvironment, home)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "strig", "strig.log"), nil
}

// Open creates the log file (and its directory) and returns a logger writing
// to it. The caller must close the file.
func Open(path string) (*slog.Logger, *os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, fmt.Errorf("create log directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file: %w", err)
	}
	return slog.New(slog.NewTextHandler(file, nil)), file, nil
}

// Start opens the log file at Path(getEnvironment, home) and returns a logger writing
// to it and a func that closes the file. Logging is never fatal: if the file
// can't be opened, Start returns the error along with a logger that discards
// everything and a no-op close, so the logger and close are always usable.
func Start(getEnvironment func(string) string, home func() (string, error)) (*slog.Logger, func(), error) {
	path, err := Path(getEnvironment, home)
	if err == nil {
		var logger *slog.Logger
		var file *os.File
		if logger, file, err = Open(path); err == nil {
			return logger, func() { _ = file.Close() }, nil
		}
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil)), func() {}, err
}

// Install routes the standard library's default loggers to logger, so a
// dependency calling log.Printf or slog.Info can never write to stderr and
// corrupt the screen. slog.SetDefault also sends the log package through logger's
// handler, so those lines get a timestamp and level. This is the one place the
// program mutates the global loggers; pass a logger with an io.Discard
// handler to silence them. The other allowed global mutations are listed in
// the root CLAUDE.md.
func Install(logger *slog.Logger) {
	slog.SetDefault(logger)
}
