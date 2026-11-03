package workspace

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/config"
	"github.com/MikePors/strigidae/internal/diff"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/github/fake"
	"github.com/MikePors/strigidae/internal/plugins"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/components/diffview"
	"github.com/MikePors/strigidae/internal/userinterface/components/picker"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

// recordingRunner answers every run at once and keeps what it was asked.
type recordingRunner struct {
	effects  []plugins.Effect
	err      error
	names    []string
	requests []plugins.Request
}

func (runner *recordingRunner) Run(_ context.Context, name string, request plugins.Request) ([]plugins.Effect, error) {
	runner.names = append(runner.names, name)
	runner.requests = append(runner.requests, request)
	return runner.effects, runner.err
}

func noTimers(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }

var pluginCommands = []userinterface.PluginCommand{
	{Name: "ask", Target: config.TargetLines},
	{Name: "summarize", Target: config.TargetPullRequest},
}

// pluginSession is a loaded workspace under the real root, driven by key
// presses, with plugins configured.
type pluginSession struct {
	test    *testing.T
	inliner *inliner
	model   *userinterface.Model
	runner  PluginRunnerForTest
	infos   []string
	errors  []string
	quit    bool
}

// PluginRunnerForTest is what the session needs of a runner.
type PluginRunnerForTest = userinterface.PluginRunner

func newPluginSession(test *testing.T, runner PluginRunnerForTest) *pluginSession {
	test.Helper()
	inliner := newInliner(test)
	return startPluginSession(test, inliner, inliner.view, runner)
}

func startPluginSession(test *testing.T, inliner *inliner, root userinterface.View, runner PluginRunnerForTest, options ...func(*userinterface.Options)) *pluginSession {
	test.Helper()
	startup := userinterface.Options{
		Tick:           noTimers,
		ViewCommands:   append(Commands(), "ask", "summarize"),
		PluginRunner:   runner,
		PluginCommands: pluginCommands,
	}
	for _, option := range options {
		option(&startup)
	}
	session := &pluginSession{test: test, inliner: inliner, runner: runner}
	session.model = userinterface.New(root, theme.New(true), startup)
	session.model.Update(tea.WindowSizeMsg{Width: 110, Height: 41})
	return session
}

// send gives the model one message and returns the command it asked for.
func (session *pluginSession) send(message tea.Msg) tea.Cmd {
	_, command := session.model.Update(message)
	return command
}

// pump runs a command and feeds what it produced back to the model, until
// nothing is left. The spinner's next frame, which would wait, is not run.
func (session *pluginSession) pump(command tea.Cmd) {
	session.test.Helper()
	if command == nil {
		return
	}
	switch message := command().(type) {
	case nil:
	case tea.BatchMsg:
		for _, member := range message {
			session.pump(member)
		}
	case tea.QuitMsg:
		session.quit = true
	case spinner.TickMsg:
		session.send(message)
	case userinterface.InfoMessage:
		session.infos = append(session.infos, message.Text)
		session.pump(session.send(message))
	case userinterface.ErrorMessage:
		session.errors = append(session.errors, message.Err.Error())
		session.pump(session.send(message))
	default:
		session.pump(session.send(message))
	}
}

// key presses one key as the terminal reports it. Commands that keys return
// (cursor blinking) are not run; line does that for the one that matters.
func (session *pluginSession) key(text string) tea.Cmd {
	switch text {
	case "esc":
		return session.send(tea.KeyPressMsg{Code: tea.KeyEscape})
	case "enter":
		return session.send(tea.KeyPressMsg{Code: tea.KeyEnter})
	case " ":
		return session.send(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	}
	return session.send(tea.KeyPressMsg{Code: []rune(text)[0], Text: text})
}

// line types a command line and runs the result of enter.
func (session *pluginSession) line(text string) {
	session.test.Helper()
	session.key(":")
	for _, character := range text {
		session.key(string(character))
	}
	session.pump(session.key("enter"))
}

func (session *pluginSession) press(texts ...string) {
	for _, text := range texts {
		session.key(text)
	}
}

// status is the text of the status bar row.
func (session *pluginSession) status() string {
	lines := strings.Split(ansi.Strip(session.model.View().Content), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func (session *pluginSession) lastRequest() plugins.Request {
	session.test.Helper()
	recorder, ok := session.runner.(*recordingRunner)
	if !ok || len(recorder.requests) == 0 {
		session.test.Fatal("the plugin was not run")
	}
	return recorder.requests[len(recorder.requests)-1]
}

func (session *pluginSession) runs() int {
	recorder, ok := session.runner.(*recordingRunner)
	if !ok {
		return 0
	}
	return len(recorder.requests)
}

// The new-side lines of the first hunk of internal/sync/client.go, with their
// prefixes, as testdata/pull_request.diff has them.
var firstHunk = []string{
	` 	"context"`, // 0: new 10
	` 	"time"`,    // 1: new 11
	` )`,          // 2: new 12
	` `,           // 3: new 13
	`-func (c *Client) Sync(ctx context.Context) error {`, // 4: old 14
	"-\treturn c.retryForever(ctx, c.sync)",               // 5: old 15
	`+func (c *Client) Sync(ctx context.Context) error {`, // 6: new 14
	"+\treturn Retry(ctx, 5, func() error {",              // 7: new 15
	"+\t\treturn c.sync(ctx)",                             // 8: new 16
	"+\t})",                                               // 9: new 17
	` }`,                                                  // 10: new 18
	` `,                                                   // 11: new 19
}

func excerptOfHunk(first, last int) string { return strings.Join(firstHunk[first:last+1], "\n") }

func TestPluginRequest(test *testing.T) {
	for _, testCase := range []struct {
		name      string
		arrange   func(session *pluginSession)
		line      string
		want      plugins.Request
		wantTexts []string
	}{
		{
			name:    "cursor on the first line of a hunk: no lines before it",
			arrange: func(session *pluginSession) { session.inliner.moveTo(github.SideRight, 10) },
			line:    "ask",
			want: plugins.Request{
				Command: "ask",
				Target:  &plugins.Target{Path: clientPath, Side: "RIGHT", Line: 10, StartLine: 10, Excerpt: excerptOfHunk(0, 3)},
			},
		},
		{
			name:    "cursor on the last line of a hunk: no lines after it",
			arrange: func(session *pluginSession) { session.inliner.moveTo(github.SideRight, 19) },
			line:    "ask",
			want: plugins.Request{
				Command: "ask",
				Target:  &plugins.Target{Path: clientPath, Side: "RIGHT", Line: 19, StartLine: 19, Excerpt: excerptOfHunk(8, 11)},
			},
		},
		{
			name:    "an added line in the middle: three lines each side",
			arrange: func(session *pluginSession) { session.inliner.moveTo(github.SideRight, 15) },
			line:    "ask",
			want: plugins.Request{
				Command: "ask",
				Target:  &plugins.Target{Path: clientPath, Side: "RIGHT", Line: 15, StartLine: 15, Excerpt: excerptOfHunk(4, 10)},
			},
		},
		{
			name:    "a deleted line is on the left side",
			arrange: func(session *pluginSession) { session.inliner.moveTo(github.SideLeft, 15) },
			line:    "ask",
			want: plugins.Request{
				Command: "ask",
				Target:  &plugins.Target{Path: clientPath, Side: "LEFT", Line: 15, StartLine: 15, Excerpt: excerptOfHunk(2, 8)},
			},
		},
		{
			name: "a selection from the first line: the margin is cut at the hunk start",
			arrange: func(session *pluginSession) {
				session.inliner.moveTo(github.SideRight, 10)
				session.press("V", "j", "j")
			},
			line: "ask",
			want: plugins.Request{
				Command: "ask",
				Target:  &plugins.Target{Path: clientPath, Side: "RIGHT", Line: 12, StartLine: 10, Excerpt: excerptOfHunk(0, 5)},
			},
		},
		{
			name: "a selection to the last line: the margin is cut at the hunk end",
			arrange: func(session *pluginSession) {
				session.inliner.moveTo(github.SideRight, 17)
				session.press("V", "j", "j")
			},
			line: "ask",
			want: plugins.Request{
				Command: "ask",
				Target:  &plugins.Target{Path: clientPath, Side: "RIGHT", Line: 19, StartLine: 17, Excerpt: excerptOfHunk(6, 11)},
			},
		},
		{
			name: "a selection of added lines",
			arrange: func(session *pluginSession) {
				session.inliner.moveTo(github.SideRight, 14)
				session.press("V", "j", "j")
			},
			line: "ask",
			want: plugins.Request{
				Command: "ask",
				Target:  &plugins.Target{Path: clientPath, Side: "RIGHT", Line: 16, StartLine: 14, Excerpt: excerptOfHunk(3, 11)},
			},
		},
		{
			name: "a selection of deleted lines",
			arrange: func(session *pluginSession) {
				session.inliner.moveTo(github.SideLeft, 14)
				session.press("V", "j")
			},
			line: "ask",
			want: plugins.Request{
				Command: "ask",
				Target:  &plugins.Target{Path: clientPath, Side: "LEFT", Line: 15, StartLine: 14, Excerpt: excerptOfHunk(1, 8)},
			},
		},
		{
			name:    "arguments are the rest of the line",
			arrange: func(session *pluginSession) { session.inliner.moveTo(github.SideRight, 10) },
			line:    "ask why is this   <slow>?",
			want: plugins.Request{
				Command:   "ask",
				Arguments: "why is this   <slow>?",
				Target:    &plugins.Target{Path: clientPath, Side: "RIGHT", Line: 10, StartLine: 10, Excerpt: excerptOfHunk(0, 3)},
			},
		},
		{
			name:    "a pull request plugin has no target, from the diff",
			arrange: func(session *pluginSession) { session.inliner.moveTo(github.SideRight, 10) },
			line:    "summarize",
			want:    plugins.Request{Command: "summarize"},
		},
		{
			name:    "a pull request plugin takes arguments",
			arrange: func(*pluginSession) {},
			line:    "summarize in two lines",
			want:    plugins.Request{Command: "summarize", Arguments: "in two lines"},
		},
		{
			name:    "a pull request plugin works from the Files pane",
			arrange: func(session *pluginSession) { session.press("h") },
			line:    "summarize",
			want:    plugins.Request{Command: "summarize"},
		},
		{
			name:    "a pull request plugin works from the Overview",
			arrange: func(session *pluginSession) { session.press("h", "g g") },
			line:    "summarize",
			want:    plugins.Request{Command: "summarize"},
		},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			runner := &recordingRunner{}
			session := newPluginSession(test, runner)
			testCase.arrange(session)
			session.line(testCase.line)
			got := session.lastRequest()
			testCase.want.Repository = plugins.Repository{Host: "example.test", Owner: "acme", Name: "widgets"}
			testCase.want.Number = 7
			testCase.want.Head = headSHA
			if diff := cmp.Diff(testCase.want, got); diff != "" {
				test.Errorf("request (-want +got):\n%s", diff)
			}
		})
	}
}

// The "\ No newline at end of file" marker belongs to the line above it and is
// no line of the excerpt, nor does it count among the lines of margin.
func TestExcerptLeavesOutTheNoNewlineMarker(test *testing.T) {
	file := &diff.File{NewPath: "a.txt", Hunks: []diff.Hunk{{Lines: []diff.Line{
		{Kind: diff.Context, Old: 1, New: 1, Text: "one"},
		{Kind: diff.Delete, Old: 2, Text: "two"},
		{Kind: diff.NoNewline, Text: `\ No newline at end of file`},
		{Kind: diff.Add, New: 2, Text: "TWO"},
		{Kind: diff.NoNewline, Text: `\ No newline at end of file`},
	}}}}
	got, ok := excerptOf(file, diffview.Range{Path: "a.txt", Side: github.SideRight, Start: 2, End: 2})
	if want := " one\n-two\n+TWO"; !ok || got != want {
		test.Errorf("excerpt = %q (%v), want %q", got, ok, want)
	}
	got, ok = excerptOf(file, diffview.Range{Path: "a.txt", Side: github.SideLeft, Start: 2, End: 2})
	if want := " one\n-two\n+TWO"; !ok || got != want {
		test.Errorf("left excerpt = %q (%v), want %q", got, ok, want)
	}
}

// Lines the diff does not hold (it was replaced under the cursor) are refused,
// not sent as an empty excerpt.
func TestLinesPluginRefusedWhenTheLinesAreNotInTheDiff(test *testing.T) {
	session := newPluginSession(test, &recordingRunner{})
	session.inliner.moveTo(github.SideRight, 10)
	session.inliner.view.diff.Parsed = &diff.Diff{}
	session.line("ask")
	if diff := cmp.Diff([]string{"those lines are not in the diff; refresh and try again"}, session.infos); diff != "" {
		test.Errorf("notices (-want +got):\n%s", diff)
	}
	if session.runs() != 0 {
		test.Error("the plugin ran")
	}
}

// The selection is open when the command line opens, cancelling the line
// leaves it, and a plugin that runs gets exactly the selection.
func TestSelectionSurvivesTheCommandLine(test *testing.T) {
	runner := &recordingRunner{}
	session := newPluginSession(test, runner)
	session.inliner.moveTo(github.SideRight, 14)
	session.press("V", "j")
	if !session.inliner.view.CapturesKeys() {
		test.Fatal("setup: no selection")
	}
	session.key(":")
	session.key("a")
	session.key("esc")
	if !session.inliner.view.visualActive() {
		test.Fatal("cancelling the command line ended the selection")
	}
	session.key(":")
	session.key("backspace")
	session.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if !session.inliner.view.visualActive() {
		test.Fatal("backspace on an empty command line ended the selection")
	}
	session.press("j") // the selection still extends
	session.line("ask")
	target := session.lastRequest().Target
	if target == nil || target.StartLine != 14 || target.Line != 16 {
		test.Errorf("target = %+v, want the selection 14 to 16", target)
	}
	if session.inliner.view.visualActive() {
		test.Error("the selection stayed open after the plugin took it")
	}
}

// During a selection only a `lines` plugin runs: anything else is refused, so
// no chooser or picker can open under a live selection.
func TestOnlyLinesPluginsRunDuringASelection(test *testing.T) {
	for _, name := range []string{"review", "resolve", "approve", "merge", "summarize"} {
		test.Run(name, func(test *testing.T) {
			runner := &recordingRunner{}
			session := newPluginSession(test, runner)
			session.inliner.moveTo(github.SideRight, 17)
			session.press("V")
			session.line(name)
			if diff := cmp.Diff([]string{"end the selection to run :" + name}, session.infos); diff != "" {
				test.Errorf("notices (-want +got):\n%s", diff)
			}
			view := session.inliner.view
			if !view.visualActive() || view.review.chooser != nil || view.picker != nil || session.runs() != 0 {
				test.Errorf("selection %v, chooser %v, picker %v, runs %d", view.visualActive(), view.review.chooser != nil, view.picker != nil, session.runs())
			}
			if _, asked := session.inliner.lastConfirm(); asked {
				test.Error("a question was asked")
			}
		})
	}
}

// With no `lines` plugin, ":" in a selection does nothing, as before.
func TestColonDuringASelectionNeedsALinesPlugin(test *testing.T) {
	for _, testCase := range []struct {
		name     string
		commands []userinterface.PluginCommand
		opens    bool
	}{
		{"no plugins", nil, false},
		{"only a pull request plugin", []userinterface.PluginCommand{{Name: "summarize", Target: config.TargetPullRequest}}, false},
		{"a lines plugin", []userinterface.PluginCommand{{Name: "ask", Target: config.TargetLines}}, true},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			session := newPluginSession(test, &recordingRunner{})
			view := session.inliner.view
			view.pluginCommands = testCase.commands
			session.inliner.moveTo(github.SideRight, 14)
			session.press("V")
			if got := view.KeepsCommandLine(); got != testCase.opens {
				test.Fatalf("KeepsCommandLine = %v, want %v", got, testCase.opens)
			}
			session.key(":")
			if opened := session.status() == ":"; opened != testCase.opens {
				test.Errorf("command line open = %v (status %q), want %v", opened, session.status(), testCase.opens)
			}
		})
	}
}

// The command line stays closed over the pickers it was always closed over,
// even during a selection.
func TestColonStaysClosedOverPickers(test *testing.T) {
	session := newPluginSession(test, &recordingRunner{})
	view := session.inliner.view
	session.inliner.moveTo(github.SideRight, 14)
	session.press("V")
	if !view.KeepsCommandLine() {
		test.Fatal("setup: the command line should open over a plain selection")
	}
	view.openPicker()
	if view.KeepsCommandLine() {
		test.Error("the command line would open over the file picker")
	}
	view.picker = nil
	view.review.chooser = picker.New(view.theme, "Submit review", []string{"Approve"})
	if view.KeepsCommandLine() {
		test.Error("the command line would open over the verdict chooser")
	}
	view.review.chooser = nil
	view.filePane.VisualEnd()
	if view.KeepsCommandLine() {
		test.Error("the command line would open with no selection")
	}
}

func TestPluginRefusals(test *testing.T) {
	for _, testCase := range []struct {
		name  string
		setup func(session *pluginSession)
		line  string
		want  string
	}{
		{"lines plugin on the Overview", func(session *pluginSession) { session.press("h", "g g") }, "ask", "move onto a diff line to run :ask"},
		{"lines plugin on a hunk header", func(session *pluginSession) { session.inliner.diffView().GotoTop() }, "ask", "move onto a diff line to run :ask"},
		{"lines plugin with the diff off screen", func(session *pluginSession) {
			session.send(tea.WindowSizeMsg{Width: 30, Height: 20})
			session.press("h")
		}, "ask", "focus the diff to run :ask"},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			runner := &recordingRunner{}
			session := newPluginSession(test, runner)
			testCase.setup(session)
			session.infos = nil
			session.line(testCase.line)
			if diff := cmp.Diff([]string{testCase.want}, session.infos); diff != "" {
				test.Errorf("notices (-want +got):\n%s", diff)
			}
			if session.runs() != 0 {
				test.Error("the plugin ran")
			}
		})
	}
}

// Until the pull request (and for lines, its diff) has loaded, a plugin is
// refused with the notices the built-in commands give.
func TestPluginRefusedBeforeLoaded(test *testing.T) {
	for _, testCase := range []struct {
		name string
		line string
		want string
	}{
		{"lines plugin before anything loaded", "ask", "pull request not loaded yet"},
		{"pull request plugin before anything loaded", "summarize", "pull request not loaded yet"},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			harness := newHarness(test, 110, 40) // Init has not run: nothing is loaded
			runner := &recordingRunner{}
			session := startPluginSession(test, &inliner{&composer{harness: harness}}, harness.view, runner)
			session.line(testCase.line)
			if diff := cmp.Diff([]string{testCase.want}, session.infos); diff != "" {
				test.Errorf("notices (-want +got):\n%s", diff)
			}
			// The built-in guard says the same.
			session.infos = nil
			session.line("approve")
			if diff := cmp.Diff([]string{testCase.want}, session.infos); diff != "" {
				test.Errorf("built-in notices (-want +got):\n%s", diff)
			}
			if session.runs() != 0 {
				test.Error("the plugin ran")
			}
		})
	}
}

func TestLinesPluginRefusedWhileTheDiffLoads(test *testing.T) {
	session := newPluginSession(test, &recordingRunner{})
	session.inliner.moveTo(github.SideRight, 10)
	session.inliner.view.diffPending = true
	session.line("ask")
	if diff := cmp.Diff([]string{"diff not loaded"}, session.infos); diff != "" {
		test.Errorf("notices (-want +got):\n%s", diff)
	}
	session.infos = nil
	session.line("summarize") // the pull request is loaded; it needs no diff
	if session.runs() != 1 || len(session.infos) != 0 {
		test.Errorf("pull request plugin: runs = %d, notices = %v", session.runs(), session.infos)
	}
}

// A plugin is not blocked by the busy states that stop writes.
func TestPluginIgnoresWriteBusyStates(test *testing.T) {
	session := newPluginSession(test, &recordingRunner{})
	session.inliner.view.writes = 1
	session.inliner.view.merge.merging = true
	session.line("summarize")
	if session.runs() != 1 {
		test.Errorf("runs = %d, want the plugin to run", session.runs())
	}
}

func TestPluginEffectsReachTheStatusBar(test *testing.T) {
	hostile := "hi\x1b]0;owned\x07\x1b[2J\u009b31m\u202etxt\xff"
	for _, testCase := range []struct {
		name    string
		effects []plugins.Effect
		err     error
		want    string
	}{
		{"notice", []plugins.Effect{{Name: plugins.EffectNotice, Text: "Looks fine"}}, nil, "Looks fine"},
		{"error", []plugins.Effect{{Name: plugins.EffectError, Text: "no model"}}, nil, "no model"},
		{"answer shows as a notice", []plugins.Effect{{Name: plugins.EffectAnswer, Text: "Because."}}, nil, "Because."},
		{"a failed run", nil, errors.New(`plugin "summarize": timed out after 1m0s`), `plugin "summarize": timed out after 1m0s`},
		{"the last effect stays", []plugins.Effect{{Name: plugins.EffectNotice, Text: "one"}, {Name: plugins.EffectNotice, Text: "two"}}, nil, "two"},
		{"hostile notice", []plugins.Effect{{Name: plugins.EffectNotice, Text: hostile}}, nil, "hi ]0;owned  [2J 31m txt�"},
		{"hostile error effect", []plugins.Effect{{Name: plugins.EffectError, Text: hostile}}, nil, "hi ]0;owned  [2J 31m txt�"},
		{"hostile answer", []plugins.Effect{{Name: plugins.EffectAnswer, Text: hostile}}, nil, "hi ]0;owned  [2J 31m txt�"},
		{"hostile text of a failed run", nil, errors.New(hostile), "hi ]0;owned  [2J 31m txt�"},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			session := newPluginSession(test, &recordingRunner{effects: testCase.effects, err: testCase.err})
			session.line("summarize")
			if got := session.status(); !strings.HasSuffix(got, testCase.want) {
				test.Errorf("status = %q, want it to end with %q", got, testCase.want)
			}
			if got := ansi.Strip(session.model.View().Content); strings.IndexFunc(got, func(character rune) bool {
				return unicode.IsControl(character) && character != '\n'
			}) >= 0 {
				test.Errorf("screen holds a control character: %q", got)
			}
		})
	}
}

func TestPluginReloadRunsTheRefresh(test *testing.T) {
	session := newPluginSession(test, &recordingRunner{effects: []plugins.Effect{{Name: plugins.EffectReload}}})
	loads := func() int {
		count := 0
		for _, call := range session.inliner.client.Calls() {
			if call.Method == fake.MethodGetPullRequest {
				count++
			}
		}
		return count
	}
	before := loads()
	session.line("summarize")
	if got := loads() - before; got != 1 {
		test.Errorf("the reload effect loaded the pull request %d times, want once", got)
	}
}

// The reload effect is the refresh key: with a load in flight it starts a new
// one that supersedes it.
func TestPluginReloadSupersedesALoadInFlight(test *testing.T) {
	refresh := loaded(test, 110, 40)
	reloaded := loaded(test, 110, 40)
	refresh.view.load() // a load whose results have not arrived
	reloaded.view.load()
	refreshSequence, reloadedSequence := refresh.view.sequence, reloaded.view.sequence
	refresh.view.load()
	reloaded.view.Reload()
	if refresh.view.sequence-refreshSequence != reloaded.view.sequence-reloadedSequence || !reloaded.view.diffPending {
		test.Errorf("Reload moved the load sequence by %d, refresh by %d", reloaded.view.sequence-reloadedSequence, refresh.view.sequence-refreshSequence)
	}
}

func TestPluginsAreOnlyInTheWorkspace(test *testing.T) {
	session := startPluginSession(test, newInliner(test), &stubList{}, &recordingRunner{})
	session.line("ask")
	session.line("summarize")
	if diff := cmp.Diff([]string{"not available here: ask", "not available here: summarize"}, session.errors); diff != "" {
		test.Errorf("errors (-want +got):\n%s", diff)
	}
	if session.runs() != 0 {
		test.Error("a plugin ran outside the workspace")
	}
}

// stubList stands in for a list view under the workspace.
type stubList struct{}

func (list *stubList) Init() tea.Cmd { return nil }
func (list *stubList) Update(_ tea.Msg) (userinterface.View, tea.Cmd) {
	return list, nil
}
func (list *stubList) Render(int, int) string    { return "list" }
func (list *stubList) Title() string             { return "list" }
func (list *stubList) ShortHelp() []key.Binding  { return nil }
func (list *stubList) FullHelp() [][]key.Binding { return nil }

// newStackedSession is a workspace opened over a list, as strig opens it.
func newStackedSession(test *testing.T, runner PluginRunnerForTest, options ...func(*userinterface.Options)) *pluginSession {
	test.Helper()
	inliner := newInliner(test)
	session := startPluginSession(test, inliner, &stubList{}, runner, options...)
	session.pump(userinterface.Push(inliner.view))
	inliner.moveTo(github.SideRight, 10)
	return session
}

// held types a command line and returns the result of the run it starts,
// without delivering it, so the test chooses when it arrives.
func (session *pluginSession) held(text string) tea.Msg {
	session.test.Helper()
	session.key(":")
	for _, character := range text {
		session.key(string(character))
	}
	var result tea.Msg
	var collect func(command tea.Cmd)
	collect = func(command tea.Cmd) {
		if command == nil {
			return
		}
		switch message := command().(type) {
		case nil, spinner.TickMsg:
		case tea.BatchMsg:
			for _, member := range message {
				collect(member)
			}
		default:
			result = message
		}
	}
	asked := session.key("enter")()
	collect(session.send(asked))
	if result == nil {
		session.test.Fatal("the line did not start a run")
	}
	return result
}

func (session *pluginSession) loadCount(number int) int {
	count := 0
	for _, call := range session.inliner.client.Calls() {
		if call.Method == fake.MethodGetPullRequest && call.Number == number {
			count++
		}
	}
	return count
}

// A run belongs to the root: its result is shown when its pull request is
// still the one on screen, and dropped when the view was closed or another
// pull request is on top.
func TestPluginResultAfterTheViewChanged(test *testing.T) {
	effects := []plugins.Effect{{Name: plugins.EffectNotice, Text: "done"}, {Name: plugins.EffectReload}}
	for _, testCase := range []struct {
		name  string
		after func(session *pluginSession)
		want  bool
	}{
		{"still open", func(*pluginSession) {}, true},
		{"the view was closed", func(session *pluginSession) { session.line("q") }, false},
		{"another pull request is on top", func(session *pluginSession) {
			session.line("q")
			other := testDetail()
			other.Number = 8
			session.inliner.client.SetPullRequest(other)
			session.inliner.client.SetDiff(testRepository, 8, readDiff(test))
			session.pump(userinterface.Push(New(session.inliner.client, testRepository, 8)))
		}, false},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			session := newStackedSession(test, &recordingRunner{effects: effects})
			result := session.held("summarize")
			session.infos = nil
			testCase.after(session)
			loadsBefore := session.loadCount(7) + session.loadCount(8)
			session.pump(func() tea.Msg { return result })
			shown := session.status() == "done" || strings.HasSuffix(session.status(), "done")
			if shown != testCase.want {
				test.Errorf("status = %q, shown = %v, want %v", session.status(), shown, testCase.want)
			}
			if reloaded := session.loadCount(7) + session.loadCount(8) - loadsBefore; (reloaded > 0) != testCase.want {
				test.Errorf("the pull request was reloaded %d times, want a reload only when the result was shown", reloaded)
			}
		})
	}
}

// The status bar spinner is on from the moment the run starts to the moment
// its result arrives.
func TestPluginSpinnerOnTheStatusBar(test *testing.T) {
	session := newStackedSession(test, &recordingRunner{})
	frames := spinner.Dot.Frames
	spinning := func() bool {
		for _, frame := range frames {
			if strings.Contains(session.status(), strings.TrimSpace(frame)) {
				return true
			}
		}
		return false
	}
	if spinning() {
		test.Fatal("spinning before the run")
	}
	result := session.held("summarize")
	if !spinning() {
		test.Errorf("no spinner while the run is active: %q", session.status())
	}
	session.line("q") // closing the view: the run is not its own, no spinner on the list
	if spinning() {
		test.Errorf("spinner on a view that is not the run's: %q", session.status())
	}
	session.pump(func() tea.Msg { return result })
	if spinning() {
		test.Errorf("spinner after the result: %q", session.status())
	}
}

// Plugin commands are entries of the registry like the built-in ones.
func TestPluginEntriesHaveTheRegistryShape(test *testing.T) {
	view := newInliner(test).view
	view.pluginCommands = pluginCommands
	builtIn := map[string]bool{}
	for _, entry := range view.bind.entries() {
		builtIn[entry.Command] = true
	}
	found := map[string]actionEntry{}
	for _, entry := range view.entries() {
		if entry.Command != "" && !builtIn[entry.Command] {
			found[entry.Command] = entry
		}
	}
	for _, command := range pluginCommands {
		entry, ok := found[command.Name]
		if !ok || entry.run == nil || entry.Name == "" || entry.Binding != nil {
			test.Fatalf("no registry entry for :%s with a name, a run and no key: %+v", command.Name, entry)
		}
		if command.Target == config.TargetLines && (entry.level != levelLines || !entry.diffVisible || entry.guard != usePluginLines) {
			test.Errorf(":%s is not a lines entry: %+v", command.Name, entry)
		}
		if command.Target == config.TargetPullRequest && (entry.level != levelPullRequest || entry.diffVisible || entry.guard != usePluginPullRequest) {
			test.Errorf(":%s is not a pull-request entry: %+v", command.Name, entry)
		}
	}
	// A built-in name still reaches the built-in entry.
	view.pluginCommands = []userinterface.PluginCommand{{Name: "approve", Target: config.TargetPullRequest}}
	if entry, _ := view.commandEntry("approve"); entry.guard != writeReview {
		test.Errorf("a plugin named like a built-in took over :approve")
	}
}
