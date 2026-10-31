package workspace

import (
	"fmt"
	"io/fs"
	"strings"
	"testing"
)

// analysisJSON is a small valid analysis of the test pull request: spans in
// the diff (client.go and retry.go on the new side, legacy.go on the old),
// and one outside every hunk.
func analysisJSON(repository string, number int, head string) string {
	return fmt.Sprintf(`{
  "schema_version": "prreview/analysis@1",
  "pr": {"repo": %q, "number": %d, "head_sha": %q, "merge_base": "base"},
  "hunks": {
    "call": {"path": "internal/sync/client.go", "side": "RIGHT", "start": 14, "end": 15, "label": "the new call"},
    "helper": {"path": "internal/sync/retry.go", "side": "RIGHT", "start": 1, "end": 14},
    "removed": {"path": "internal/sync/legacy.go", "side": "LEFT", "start": 1, "end": 4},
    "far": {"path": "internal/sync/client.go", "side": "RIGHT", "start": 200, "end": 210, "context_only": true}
  },
  "lenses": [
    {"id": "flow", "title": "General Flow", "purpose": "Follow the call path", "order": ["use", "define"], "segments": [
      {"id": "define", "title": "The helper", "risk": "high", "annotation": "Check the backoff is bounded.", "hunks": ["helper", "removed"], "order": ["removed", "helper"]},
      {"id": "use", "title": "Where it is called", "risk": "low", "hunks": ["call", "far"], "notes": {"call": "Wraps the sync."}}
    ]},
    {"id": "claims", "title": "Claims", "segments": [
      {"id": "retries", "title": "Sync retries", "hunks": ["call", "helper"]}
    ]}
  ]
}`, repository, number, head)
}

// files is an analysis reader over a map, recording what was read.
type files struct {
	content map[string]string
	read    []string
}

func (files *files) readFile(path string) ([]byte, error) {
	files.read = append(files.read, path)
	text, ok := files.content[path]
	if !ok {
		return nil, fmt.Errorf("open %s: %w", path, fs.ErrNotExist)
	}
	return []byte(text), nil
}

// withFiles makes the view read analyses from content.
func (inliner *inliner) withFiles(content map[string]string) *files {
	files := &files{content: content}
	inliner.view.readFile = files.readFile
	return files
}

func TestAnalysisLoadsWhenThePullRequestOpens(test *testing.T) {
	inliner := newInliner(test)
	inliner.withFiles(map[string]string{".strig/analysis/7.json": analysisJSON("acme/widgets", 7, headSHA)})
	inliner.run(inliner.view.autoloadAnalysis())
	if inliner.view.analysis() == nil {
		test.Fatal("no analysis attached")
	}
	if got := inliner.lastInfo(); got != "analysis: 2 lenses, 3 segments" {
		test.Errorf("notice = %q", got)
	}
}

func TestNoAnalysisFileIsNotAnError(test *testing.T) {
	inliner := newInliner(test)
	files := inliner.withFiles(nil)
	inliner.messages = nil
	inliner.run(inliner.view.autoloadAnalysis())
	if len(files.read) != 1 || len(inliner.messages) != 0 || inliner.view.analysis() != nil {
		test.Errorf("read %q, messages %v, analysis %v", files.read, inliner.messages, inliner.view.analysis())
	}
}

func TestAnalysisCommand(test *testing.T) {
	for _, testCase := range []struct {
		name      string
		arguments string
		content   map[string]string
		wantErr   string
		wantInfo  string
	}{
		{"named file", " review.json ", map[string]string{"review.json": analysisJSON("acme/widgets", 7, headSHA)}, "", "analysis: 2 lenses, 3 segments"},
		{"conventional file", "", map[string]string{".strig/analysis/7.json": analysisJSON("ACME/Widgets", 7, headSHA)}, "", "analysis: 2 lenses, 3 segments"},
		{"stale", "review.json", map[string]string{"review.json": analysisJSON("acme/widgets", 7, "old")}, "", "analysis: 2 lenses, 3 segments (stale: made for another head)"},
		{"missing file", "nope.json", nil, "analysis nope.json: open nope.json", ""},
		{"missing conventional file", "", nil, "analysis .strig/analysis/7.json", ""},
		{"invalid", "review.json", map[string]string{"review.json": `{"schema_version": "x"}`}, "analysis review.json: invalid analysis", ""},
		{"another pull request", "review.json", map[string]string{"review.json": analysisJSON("acme/widgets", 8, headSHA)}, "analysis review.json is for acme/widgets#8, not acme/widgets#7", ""},
		{"another repository", "review.json", map[string]string{"review.json": analysisJSON("acme/gadgets", 7, headSHA)}, "is for acme/gadgets#7", ""},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			inliner := newInliner(test)
			inliner.withFiles(testCase.content)
			inliner.messages = nil
			command, handled := inliner.view.Command("analysis", testCase.arguments)
			if !handled {
				test.Fatal(":analysis not handled")
			}
			inliner.run(command)
			if testCase.wantErr != "" {
				if err := inliner.lastErr(); err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					test.Errorf("error = %v, want one containing %q", err, testCase.wantErr)
				}
				if inliner.view.analysis() != nil {
					test.Error("an analysis was attached")
				}
				return
			}
			if got := inliner.lastInfo(); got != testCase.wantInfo {
				test.Errorf("notice = %q, want %q", got, testCase.wantInfo)
			}
		})
	}
}

func TestAnalysisTextIsSafe(test *testing.T) {
	inliner := newInliner(test)
	inliner.withFiles(map[string]string{"evil\x1b[2J.json": analysisJSON("acme/widgets\x1b]0;x\x07", 7, headSHA)})
	command, _ := inliner.view.Command("analysis", "evil\x1b[2J.json")
	inliner.run(command)
	err := inliner.lastErr()
	if err == nil {
		test.Fatal("no error for another repository")
	}
	for _, bad := range []string{"\x1b[2J", "\x1b]0;"} {
		if strings.Contains(err.Error(), bad) {
			test.Errorf("error %q contains %q", err.Error(), bad)
		}
	}
}

func TestAnalysesAreSharedAndReplaced(test *testing.T) {
	first := newInliner(test)
	first.withFiles(map[string]string{"a.json": analysisJSON("acme/widgets", 7, headSHA), "b.json": analysisJSON("acme/widgets", 7, "newer")})
	command, _ := first.view.Command("analysis", "a.json")
	first.run(command)

	second := newInliner(test)
	second.view.analyses = first.view.analyses
	files := second.withFiles(nil)
	second.run(second.view.autoloadAnalysis())
	if len(files.read) != 0 {
		test.Errorf("read %q again although it was loaded", files.read)
	}
	if loaded := second.view.analysis(); loaded == nil || loaded.path != "a.json" {
		test.Fatalf("shared analysis = %+v", loaded)
	}

	command, _ = first.view.Command("analysis", "b.json")
	first.run(command)
	if got := second.view.analysis().content.PullRequest.HeadSHA; got != "newer" {
		test.Errorf("after a reload the head is %q, want the new file's", got)
	}
}
