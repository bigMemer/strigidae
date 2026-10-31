package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/analysis"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/terminaltext"
)

// Analyses remembers the review analyses loaded for pull requests while strig
// runs, so one loaded once is there when its pull request is opened again.
// Only the UI goroutine touches it.
type Analyses struct {
	loaded map[pullRequestKey]*loadedAnalysis
}

type pullRequestKey struct {
	repository github.Repository
	number     int
}

// loadedAnalysis is an analysis and the file it came from.
type loadedAnalysis struct {
	content analysis.Analysis
	path    string
}

// NewAnalyses returns an empty record of analyses.
func NewAnalyses() *Analyses {
	return &Analyses{loaded: map[pullRequestKey]*loadedAnalysis{}}
}

// WithAnalyses shares a record of loaded analyses between workspaces.
// Without it each workspace keeps its own.
func WithAnalyses(analyses *Analyses) Option {
	return func(view *View) { view.analyses = analyses }
}

// WithReadFile replaces how analysis files are read, which by default is
// os.ReadFile.
func WithReadFile(readFile func(path string) ([]byte, error)) Option {
	return func(view *View) { view.readFile = readFile }
}

// analysisPath is where an analysis for pull request number is looked for
// when it opens, relative to where strig runs.
func analysisPath(number int) string {
	return filepath.Join(".strig", "analysis", strconv.Itoa(number)+".json")
}

// analysisReadMessage is the result of reading an analysis file.
type analysisReadMessage struct {
	owner   *View
	path    string
	content analysis.Analysis
	err     error
}

// analysis returns the analysis loaded for this pull request, or nil.
func (view *View) analysis() *loadedAnalysis {
	return view.analyses.loaded[pullRequestKey{view.repository, view.number}]
}

// analysisStale reports whether the analysis was made for another head than
// the diff on screen: its line numbers may have moved.
func (view *View) analysisStale() bool {
	loaded := view.analysis()
	return loaded != nil && view.diffHead != "" && !strings.EqualFold(loaded.content.PullRequest.HeadSHA, view.diffHead)
}

// autoloadAnalysis reads the conventional analysis file when the pull request
// opens, unless one is loaded already. A missing file is not an error.
func (view *View) autoloadAnalysis() tea.Cmd {
	if view.analysis() != nil {
		return nil
	}
	return view.readAnalysis(analysisPath(view.number), false)
}

// analysisCommand is :analysis [path]; without a path it reads the
// conventional file.
func (view *View) analysisCommand(arguments string) tea.Cmd {
	path := strings.TrimSpace(arguments)
	if path == "" {
		path = analysisPath(view.number)
	}
	return view.readAnalysis(path, true)
}

// readAnalysis reads and parses path in a command. When asked for explicitly
// a missing file is reported; otherwise it is silently skipped.
func (view *View) readAnalysis(path string, explicit bool) tea.Cmd {
	readFile, owner := view.readFile, view
	return func() tea.Msg {
		data, err := readFile(path)
		if err != nil {
			if !explicit && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return analysisReadMessage{owner: owner, path: path, err: err}
		}
		content, err := analysis.Parse(data)
		return analysisReadMessage{owner: owner, path: path, content: content, err: err}
	}
}

// analysisRead attaches a read analysis to this pull request, refusing one
// made for another.
func (view *View) analysisRead(message analysisReadMessage) tea.Cmd {
	if message.err != nil {
		return userinterface.Error(terminaltext.SafeError(fmt.Errorf("analysis %s: %w", message.path, message.err)))
	}
	pullRequest := message.content.PullRequest
	if !strings.EqualFold(pullRequest.Repository, view.repository.String()) || pullRequest.Number != view.number {
		return userinterface.Error(errors.New(terminaltext.Clean(fmt.Sprintf(
			"analysis %s is for %s#%d, not %s#%d", message.path, pullRequest.Repository, pullRequest.Number, view.repository, view.number))))
	}
	view.analyses.loaded[pullRequestKey{view.repository, view.number}] = &loadedAnalysis{content: message.content, path: message.path}
	return userinterface.Info(view.analysisSummary())
}

// analysisSummary is the notice shown when an analysis loads.
func (view *View) analysisSummary() string {
	content := view.analysis().content
	segments := 0
	for _, lens := range content.Lenses {
		segments += len(lens.Segments)
	}
	summary := fmt.Sprintf("analysis: %s, %s", plural(len(content.Lenses), "lens", "lenses"), plural(segments, "segment", "segments"))
	if view.analysisStale() {
		summary += " (stale: made for another head)"
	}
	return summary
}

func plural(count int, one, many string) string {
	if count == 1 {
		return "1 " + one
	}
	return strconv.Itoa(count) + " " + many
}
