// Package localthreads reads and writes the file that keeps a pull request's
// local threads between runs of strig. It knows nothing of the user interface:
// the root model hands it plain data and calls it only from commands.
package localthreads

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/MikePors/strigidae/internal/github"
)

const (
	// Version is the file format this package reads and writes.
	Version = 1
	// MaxFileSize is the largest file that is read. A bigger one is treated
	// as unreadable.
	MaxFileSize = 4 << 20

	directoryMode = 0o700
	fileMode      = 0o600
	// UnreadableSuffix is appended to the name of a file that could not be
	// used, when it is set aside.
	UnreadableSuffix = ".unreadable"
)

// Errors that say why a file could not be used. None of them carries any of
// the file's content, so they are safe to log.
var (
	ErrTooLarge       = errors.New("file is too large")
	ErrCorrupt        = errors.New("file can't be decoded")
	ErrUnknownVersion = errors.New("file has an unknown version")
	ErrNotPlainName   = errors.New("path part is not a plain name")
)

// States a thread is kept in.
const (
	StateWaiting  = "waiting"
	StateAnswered = "answered"
	StateFailed   = "failed"
)

// Thread is one local thread as it is kept in the file. The text fields are
// the plugin's and have not been cleaned.
type Thread struct {
	Identifier string          `json:"identifier"`
	Head       string          `json:"head"`
	Path       string          `json:"path"`
	Side       github.DiffSide `json:"side"`
	Line       int             `json:"line"`
	StartLine  int             `json:"startLine"`
	Question   string          `json:"question"`
	State      string          `json:"state"`
	Text       string          `json:"text"`
	// Asked is when the question was asked. It decides which thread goes
	// when there are too many; it is not shown.
	Asked time.Time `json:"asked"`
}

// File is the content of one pull request's file.
type File struct {
	Version int      `json:"version"`
	Threads []Thread `json:"threads"`
}

// Encode returns the bytes of a file holding threads.
func Encode(threads []Thread) ([]byte, error) {
	if threads == nil {
		threads = []Thread{}
	}
	return marshal(File{Version: Version, Threads: threads})
}

// marshal is JSON without HTML escaping: answers hold code, and `<` and `&`
// would otherwise take six bytes each against the file size limit.
func marshal(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("encode local threads: %w", err)
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

// FileOverhead is the size of a file that holds no threads. A file of n
// threads is FileOverhead + the sum of their ThreadSize + (n-1) separators.
const FileOverhead = len(`{"version":1,"threads":[]}`)

// ThreadSize is how many bytes a thread takes in the file, without the
// separator after it.
func ThreadSize(thread Thread) int {
	data, err := marshal(thread)
	if err != nil {
		return 0
	}
	return len(data)
}

// Decode reads a file's bytes. It fails with ErrUnknownVersion for a version
// other than Version and ErrCorrupt for anything that isn't a file.
func Decode(data []byte) (File, error) {
	var header struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return File{}, ErrCorrupt
	}
	if header.Version != Version {
		return File{}, ErrUnknownVersion
	}
	var file File
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&file); err != nil {
		return File{}, ErrCorrupt
	}
	return file, nil
}

// Path returns the file for a pull request:
// <stateDirectory>/strig/local-threads/<host>/<owner>/<name>/<number>.json.
// Each part must be a plain name, because they come from a remote or a
// request and must not leave the directory.
func Path(stateDirectory, host, owner, name string, number int) (string, error) {
	if number <= 0 {
		return "", fmt.Errorf("pull request number %d: %w", number, ErrNotPlainName)
	}
	for _, part := range []string{host, owner, name} {
		if !plainName(part) {
			return "", ErrNotPlainName
		}
	}
	// GitHub names are case-insensitive, so both spellings share a file.
	owner, name = strings.ToLower(owner), strings.ToLower(name)
	return filepath.Join(stateDirectory, "strig", "local-threads", host, owner, name, strconv.Itoa(number)+".json"), nil
}

func plainName(part string) bool {
	return part != "" && part != "." && part != ".." && !strings.ContainsAny(part, "/\\\x00")
}

// Read loads the file at path. A file that doesn't exist is an empty one. A
// file over MaxFileSize fails with ErrTooLarge.
func Read(path string) (File, error) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return File{Version: Version}, nil
	}
	if err != nil {
		return File{}, fmt.Errorf("open local threads: %w", err)
	}
	defer func() { _ = file.Close() }() // read only
	info, err := file.Stat()
	if err != nil {
		return File{}, fmt.Errorf("stat local threads: %w", err)
	}
	if info.Size() > MaxFileSize {
		return File{}, ErrTooLarge
	}
	// The limit applies to the reader too, in case the file grows meanwhile.
	data, err := io.ReadAll(io.LimitReader(file, MaxFileSize+1))
	if err != nil {
		return File{}, fmt.Errorf("read local threads: %w", err)
	}
	if len(data) > MaxFileSize {
		return File{}, ErrTooLarge
	}
	return Decode(data)
}

// Write replaces the file at path with data: it writes a temporary file in the
// same directory and renames it over path, so a reader sees the old file or
// the new one and never half of one.
func Write(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, directoryMode); err != nil {
		return fmt.Errorf("create local threads directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".write-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	name := temporary.Name()
	_, err = temporary.Write(data)
	if err == nil {
		err = temporary.Chmod(fileMode)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name) // best effort: the write already failed
		return fmt.Errorf("write local threads: %w", err)
	}
	syncDirectory(directory)
	return nil
}

// syncDirectory makes the rename durable. It is best effort: the new file is
// already in place, and not every file system can sync a directory.
func syncDirectory(directory string) {
	handle, err := os.Open(directory)
	if err != nil {
		return
	}
	_ = handle.Sync()
	_ = handle.Close()
}

// SetAside renames an unusable file to path + UnreadableSuffix so the next
// write doesn't overwrite it. An earlier set-aside copy is kept: the name is
// then path + UnreadableSuffix + ".2", ".3" and so on. A file that is already
// gone is not an error.
func SetAside(path string) error {
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	target := path + UnreadableSuffix
	for attempt := 2; ; attempt++ {
		if _, err := os.Lstat(target); errors.Is(err, fs.ErrNotExist) {
			break
		}
		target = path + UnreadableSuffix + "." + strconv.Itoa(attempt)
	}
	if err := os.Rename(path, target); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("set aside unreadable local threads: %w", err)
	}
	return nil
}
