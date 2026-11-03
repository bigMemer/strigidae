package localthreads

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func sampleThreads() []Thread {
	return []Thread{
		{Identifier: "local-1", Head: "abc", Path: "a.go", Side: "RIGHT", Line: 5, StartLine: 3, Question: "why?", State: StateAnswered, Text: "because", Asked: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)},
		{Identifier: "local-2", Head: "abc", Path: "b.go", Side: "LEFT", Line: 1, StartLine: 1, Question: ":ask", State: StateWaiting, Asked: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)},
	}
}

func TestEncodeDecodeRoundTrip(test *testing.T) {
	for _, testCase := range []struct {
		name    string
		threads []Thread
	}{
		{"threads", sampleThreads()},
		{"none", []Thread{}},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			data, err := Encode(testCase.threads)
			if err != nil {
				test.Fatal(err)
			}
			file, err := Decode(data)
			if err != nil {
				test.Fatal(err)
			}
			if file.Version != Version {
				test.Errorf("version = %d", file.Version)
			}
			if diff := cmp.Diff(testCase.threads, file.Threads); diff != "" {
				test.Errorf("threads (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDecodeFailures(test *testing.T) {
	for _, testCase := range []struct {
		name string
		data string
		want error
	}{
		{"not json", "{nope", ErrCorrupt},
		{"empty", "", ErrCorrupt},
		{"unknown version", `{"version":2,"threads":[]}`, ErrUnknownVersion},
		{"no version", `{"threads":[]}`, ErrUnknownVersion},
		{"wrong shape", `{"version":1,"threads":"x"}`, ErrCorrupt},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			if _, err := Decode([]byte(testCase.data)); !errors.Is(err, testCase.want) {
				test.Errorf("err = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestPathParts(test *testing.T) {
	for _, testCase := range []struct {
		name                        string
		host, owner, repositoryName string
		number                      int
		want                        string
		wantNotPlain                bool
	}{
		{"plain", "github.com", "acme", "widgets", 7, "/state/strig/local-threads/github.com/acme/widgets/7.json", false},
		{"empty host", "", "acme", "widgets", 7, "", true},
		{"empty owner", "github.com", "", "widgets", 7, "", true},
		{"dot dot owner", "github.com", "..", "widgets", 7, "", true},
		{"dot name", "github.com", "acme", ".", 7, "", true},
		{"slash in host", "a/b", "acme", "widgets", 7, "", true},
		{"slash in name", "github.com", "acme", "../x", 7, "", true},
		{"backslash", "github.com", "acme\\x", "widgets", 7, "", true},
		{"nul", "github.com", "acme", "wid\x00gets", 7, "", true},
		{"mixed case", "github.com", "Acme", "Widgets", 7, "/state/strig/local-threads/github.com/acme/widgets/7.json", false},
		{"zero number", "github.com", "acme", "widgets", 0, "", true},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			got, err := Path("/state", testCase.host, testCase.owner, testCase.repositoryName, testCase.number)
			if (err != nil) != testCase.wantNotPlain {
				test.Fatalf("err = %v, want error %v", err, testCase.wantNotPlain)
			}
			if testCase.wantNotPlain && !errors.Is(err, ErrNotPlainName) {
				test.Errorf("err = %v, want ErrNotPlainName", err)
			}
			if got != testCase.want {
				test.Errorf("path = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestWriteThenRead(test *testing.T) {
	path, err := Path(test.TempDir(), "github.com", "acme", "widgets", 7)
	if err != nil {
		test.Fatal(err)
	}
	data, err := Encode(sampleThreads())
	if err != nil {
		test.Fatal(err)
	}
	if err := Write(path, data); err != nil {
		test.Fatal(err)
	}
	file, err := Read(path)
	if err != nil {
		test.Fatal(err)
	}
	if diff := cmp.Diff(sampleThreads(), file.Threads); diff != "" {
		test.Errorf("threads (-want +got):\n%s", diff)
	}
	info, err := os.Stat(path)
	if err != nil {
		test.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		test.Errorf("file mode = %v, want 0600", info.Mode().Perm())
	}
	directory, err := os.Stat(filepath.Dir(path))
	if err != nil {
		test.Fatal(err)
	}
	if directory.Mode().Perm() != 0o700 {
		test.Errorf("directory mode = %v, want 0700", directory.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		test.Fatal(err)
	}
	if len(entries) != 1 {
		test.Errorf("directory holds %d entries, want only the file (no temporary left)", len(entries))
	}
}

func TestReadFailures(test *testing.T) {
	for _, testCase := range []struct {
		name    string
		content []byte
		want    error
	}{
		{"corrupt", []byte("{nope"), ErrCorrupt},
		{"unknown version", []byte(`{"version":9}`), ErrUnknownVersion},
		{"too large", append([]byte(`{"version":1,"threads":[],"padding":"`), bytes.Repeat([]byte("x"), MaxFileSize)...), ErrTooLarge},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			path := filepath.Join(test.TempDir(), "7.json")
			if err := os.WriteFile(path, testCase.content, 0o600); err != nil {
				test.Fatal(err)
			}
			_, err := Read(path)
			if !errors.Is(err, testCase.want) {
				test.Errorf("err = %v, want %v", err, testCase.want)
			}
			if err != nil && strings.Contains(err.Error(), "nope") {
				test.Errorf("error holds file content: %v", err)
			}
		})
	}
}

func TestReadMissingFileIsEmpty(test *testing.T) {
	file, err := Read(filepath.Join(test.TempDir(), "none.json"))
	if err != nil || len(file.Threads) != 0 {
		test.Errorf("got %v, %v; want an empty file", file, err)
	}
}

func TestSetAside(test *testing.T) {
	path := filepath.Join(test.TempDir(), "7.json")
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		test.Fatal(err)
	}
	if err := SetAside(path); err != nil {
		test.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		test.Errorf("file is still there: %v", err)
	}
	kept, err := os.ReadFile(path + UnreadableSuffix)
	if err != nil || string(kept) != "{nope" {
		test.Errorf("unreadable copy = %q, %v", kept, err)
	}
	if err := SetAside(path); err != nil {
		test.Errorf("setting aside a missing file: %v", err)
	}
}

func TestSetAsideKeepsEarlierCopies(test *testing.T) {
	path := filepath.Join(test.TempDir(), "7.json")
	for _, content := range []string{"first", "second", "third"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			test.Fatal(err)
		}
		if err := SetAside(path); err != nil {
			test.Fatal(err)
		}
	}
	for suffix, want := range map[string]string{UnreadableSuffix: "first", UnreadableSuffix + ".2": "second", UnreadableSuffix + ".3": "third"} {
		got, err := os.ReadFile(path + suffix)
		if err != nil || string(got) != want {
			test.Errorf("%s = %q, %v; want %q", suffix, got, err, want)
		}
	}
}

func TestThreadSizesAddUpToTheFile(test *testing.T) {
	for _, count := range []int{0, 1, 2, 5} {
		threads := sampleThreads()
		for len(threads) < count {
			threads = append(threads, Thread{Identifier: "local-9", Question: "<q> & more", Text: "a \"quoted\" \u00e9 answer"})
		}
		threads = threads[:count]
		data, err := Encode(threads)
		if err != nil {
			test.Fatal(err)
		}
		want := FileOverhead
		for _, thread := range threads {
			want += ThreadSize(thread)
		}
		if count > 1 {
			want += count - 1
		}
		if len(data) != want {
			test.Errorf("%d threads: file is %d bytes, sizes add up to %d", count, len(data), want)
		}
	}
}
