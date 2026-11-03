package workspace

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/config"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/github/fake"
	"github.com/MikePors/strigidae/internal/golden"
	"github.com/MikePors/strigidae/internal/plugins"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/components/diffview"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

// threadCommands are plugins that draw their answers as local threads, and one
// that does not.
var threadCommands = []userinterface.PluginCommand{
	{Name: "ask", Target: config.TargetLines, Show: config.ShowThread},
	{Name: "plain", Target: config.TargetLines},
	{Name: "summarize", Target: config.TargetPullRequest},
}

func withThreadCommands(startup *userinterface.Options) {
	startup.PluginCommands = threadCommands
	startup.ViewCommands = append(startup.ViewCommands, "plain")
}

func (session *pluginSession) screen() string { return ansi.Strip(session.model.View().Content) }

func newThreadSession(test *testing.T, runner *recordingRunner) *pluginSession {
	test.Helper()
	inliner := newInliner(test)
	session := startPluginSession(test, inliner, inliner.view, runner, withThreadCommands)
	inliner.moveTo(github.SideRight, 15)
	return session
}

// pumped presses one key and runs what it returns.
func (session *pluginSession) pumped(text string) {
	session.test.Helper()
	session.pump(session.key(text))
}

// answer is the effect of an answering plugin.
func answerEffects(text string) []plugins.Effect {
	return []plugins.Effect{{Name: plugins.EffectAnswer, Text: text}}
}

// ask runs :ask and delivers the result at once.
func (session *pluginSession) ask() {
	session.test.Helper()
	session.line("ask why?")
}

// localThreads are the local threads the view draws.
func (session *pluginSession) localThreads() []github.ReviewThread {
	var local []github.ReviewThread
	for _, thread := range session.inliner.view.paneThreads() {
		if thread.Local.IsLocal() {
			local = append(local, thread)
		}
	}
	return local
}

// toLocalComment puts the cursor on a row of one of the two comments of a
// local thread, which have no identifiers of their own: told apart by author.
func (session *pluginSession) toLocalComment(identifier string, index int) {
	session.test.Helper()
	author := []string{"you", "answer (local, not on GitHub)"}[index]
	session.inliner.cursorTo("a comment of "+identifier, func(diffView *diffview.Model) bool {
		context := diffView.Context()
		return context.OnComment && context.CommentThread.Identifier == identifier && context.Comment.Author == author
	})
}

func withoutStatusBar(screen string) string {
	lines := strings.Split(screen, "\n")
	return strings.Join(lines[:len(lines)-1], "\n")
}

func (session *pluginSession) calls() int { return len(session.inliner.client.Calls()) }

func TestLocalThreadStates(test *testing.T) {
	timeout := fmt.Errorf("plugin %q: %w after 1m0s", "ask", plugins.ErrTimeout)
	for _, testCase := range []struct {
		name    string
		effects []plugins.Effect
		err     error
		deliver bool
		golden  string
		want    github.LocalState
		wantAll []string
	}{
		{"waiting", nil, nil, false, "local_thread_waiting", github.LocalWaiting, []string{"waiting for an answer…"}},
		{"answered", answerEffects("Because the retry is **bounded** now."), nil, true, "local_thread_answered", github.LocalAnswered, []string{"Because the retry is bounded now."}},
		{"failed", []plugins.Effect{{Name: plugins.EffectError, Text: "no model configured"}}, nil, true, "local_thread_failed", github.LocalFailed, []string{"no answer: no model configured"}},
		{"timed out", nil, timeout, true, "local_thread_timed_out", github.LocalFailed, []string{`no answer: plugin "ask": timed out after 1m0s`}},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			session := newThreadSession(test, &recordingRunner{effects: testCase.effects, err: testCase.err})
			result := session.held("ask why is this bounded?")
			if testCase.deliver {
				session.pump(func() tea.Msg { return result })
			}
			threads := session.localThreads()
			if len(threads) != 1 || threads[0].Local != testCase.want {
				test.Fatalf("local threads = %+v, want one in state %d", threads, testCase.want)
			}
			screen := session.screen()
			for _, want := range append([]string{"you", "why is this bounded?", "answer (local, not on GitHub)"}, testCase.wantAll...) {
				if !strings.Contains(screen, want) {
					test.Errorf("screen lacks %q", want)
				}
			}
			if testCase.deliver && strings.Contains(screen, "waiting for an answer") {
				test.Error("the waiting line stayed after the result")
			}
			golden.Assert(test, testCase.golden, screen, false)
		})
	}
}

func TestLocalThreadOutdatedGolden(test *testing.T) {
	session := newThreadSession(test, &recordingRunner{effects: answerEffects("Because.")})
	session.ask()
	session.inliner.setHead("0123456789012345678901234567890123456789")
	session.inliner.toThread(session.localThreads()[0].Identifier)
	session.inliner.diffView().CursorDown(5) // into the answer, so the pane scrolls to it
	if got := session.localThreads(); len(got) != 1 || !got[0].IsOutdated {
		test.Fatalf("threads = %+v, want one outdated", got)
	}
	screen := session.screen()
	if !strings.Contains(screen, "[outdated]") {
		test.Errorf("no [outdated] on screen")
	}
	golden.Assert(test, "local_thread_outdated", screen, false)
}

// Outdated is worked out against the head of the diff on screen each time:
// another head makes it outdated, and the original head makes it current again.
func TestLocalThreadOutdatedFollowsTheHeadOfTheDiff(test *testing.T) {
	session := newThreadSession(test, &recordingRunner{effects: answerEffects("Because.")})
	session.ask()
	identifier := session.localThreads()[0].Identifier
	other := "0123456789012345678901234567890123456789"
	for _, step := range []struct {
		head     string
		outdated bool
		line     int
	}{
		{other, true, 0},
		{headSHA, false, 15},
		{other, true, 0},
	} {
		session.inliner.setHead(step.head)
		got := session.localThreads()
		if len(got) != 1 || got[0].Identifier != identifier {
			test.Fatalf("head %s: threads = %+v", step.head, got)
		}
		if got[0].IsOutdated != step.outdated || got[0].Line != step.line {
			test.Errorf("head %s: outdated = %v line = %d, want %v and %d", step.head, got[0].IsOutdated, got[0].Line, step.outdated, step.line)
		}
		if got[0].OriginalLine != 15 && step.outdated {
			test.Errorf("original line = %d, want 15", got[0].OriginalLine)
		}
	}
}

// A thread asked on another head shows, outdated, whichever head the plugin
// was run on later.
func TestLocalThreadAskedOnAnotherHeadIsOutdated(test *testing.T) {
	session := newThreadSession(test, &recordingRunner{effects: answerEffects("Because.")})
	other := "0123456789012345678901234567890123456789"
	session.inliner.setHead(other)
	session.inliner.moveTo(github.SideRight, 15)
	session.ask()
	if got := session.localThreads(); len(got) != 1 || got[0].IsOutdated {
		test.Fatalf("threads = %+v, want one current", got)
	}
	session.inliner.setHead(headSHA)
	if got := session.localThreads(); len(got) != 1 || !got[0].IsOutdated {
		test.Fatalf("threads = %+v, want one outdated", got)
	}
}

func TestLocalThreadQuestion(test *testing.T) {
	for _, testCase := range []struct {
		line string
		want string
	}{
		{"ask why is this slow?", "why is this slow?"},
		{"ask", ":ask"},
	} {
		test.Run(testCase.line, func(test *testing.T) {
			session := newThreadSession(test, &recordingRunner{effects: answerEffects("ok")})
			session.line(testCase.line)
			threads := session.localThreads()
			if len(threads) != 1 || threads[0].Comments[0].Body != testCase.want || threads[0].Comments[0].Author != "you" {
				test.Fatalf("threads = %+v, want the question %q by you", threads, testCase.want)
			}
		})
	}
}

// Without show the plugin is as before: the answer is a notice and no thread
// appears.
func TestPluginWithoutShowDrawsNoThread(test *testing.T) {
	session := newThreadSession(test, &recordingRunner{effects: answerEffects("Because.")})
	session.line("plain why?")
	if got := session.localThreads(); len(got) != 0 {
		test.Errorf("threads = %+v, want none", got)
	}
	if got := session.status(); !strings.HasSuffix(got, "Because.") {
		test.Errorf("status = %q, want the answer as a notice", got)
	}
}

// The thread is drawn when the run is requested, behind another run of the
// same plugin too, and each answer fills its own thread, whatever the order.
func TestLocalThreadsOfQueuedRunsAndOfTheSameLines(test *testing.T) {
	runner := &recordingRunner{}
	session := newThreadSession(test, runner)
	first := session.held("ask first question")
	second := session.held("ask second question")
	threads := session.localThreads()
	if len(threads) != 2 {
		test.Fatalf("threads = %d, want both at once", len(threads))
	}
	if threads[0].Line != 15 || threads[1].Line != 15 || threads[0].Identifier == threads[1].Identifier {
		test.Errorf("threads = %+v, want two on line 15", threads)
	}
	if got := strings.Count(session.screen(), "waiting for an answer"); got != 2 {
		test.Errorf("waiting lines = %d, want 2", got)
	}
	// The second answers first.
	session.pump(func() tea.Msg { return second })
	threads = session.localThreads()
	if threads[0].Local != github.LocalWaiting || threads[1].Local != github.LocalFailed && threads[1].Local != github.LocalAnswered {
		test.Errorf("states = %d and %d, want the second filled and the first waiting", threads[0].Local, threads[1].Local)
	}
	session.pump(func() tea.Msg { return first })
	for _, thread := range session.localThreads() {
		if thread.Local == github.LocalWaiting {
			test.Errorf("a thread is still waiting: %+v", thread)
		}
	}
}

func TestSeveralAnswersJoinWithABlankLine(test *testing.T) {
	session := newThreadSession(test, &recordingRunner{effects: []plugins.Effect{
		{Name: plugins.EffectAnswer, Text: "First."},
		{Name: plugins.EffectNotice, Text: "side note"},
		{Name: plugins.EffectAnswer, Text: "Second."},
	}})
	session.ask()
	got := session.localThreads()[0].Comments[1].Body
	if want := "First.\n\nSecond."; got != want {
		test.Errorf("answer = %q, want %q", got, want)
	}
	if status := session.status(); !strings.HasSuffix(status, "side note") {
		test.Errorf("status = %q, want the notice as in a plain run", status)
	}
}

func TestLocalThreadOtherEffects(test *testing.T) {
	session := newThreadSession(test, &recordingRunner{effects: []plugins.Effect{
		{Name: plugins.EffectAnswer, Text: "Because."},
		{Name: plugins.EffectReload},
	}})
	before := session.inliner.methods("GetPullRequest")
	session.ask()
	if got := len(session.inliner.methods("GetPullRequest")) - len(before); got != 1 {
		test.Errorf("reloads = %d, want 1", got)
	}
	if got := session.localThreads(); len(got) != 1 || got[0].Local != github.LocalAnswered {
		test.Errorf("threads = %+v, want the answered thread to survive the reload", got)
	}
}

// An error effect beside an answer: the answer is shown; with no answer the
// error is the reason.
func TestLocalThreadAnswerAndErrorEffects(test *testing.T) {
	for _, testCase := range []struct {
		name    string
		effects []plugins.Effect
		want    github.LocalState
		body    string
	}{
		{"an answer wins", []plugins.Effect{{Name: plugins.EffectError, Text: "bad"}, {Name: plugins.EffectAnswer, Text: "Fine."}}, github.LocalAnswered, "Fine."},
		{"the first error", []plugins.Effect{{Name: plugins.EffectError, Text: "one"}, {Name: plugins.EffectError, Text: "two"}}, github.LocalFailed, "no answer: one"},
		{"only a notice", []plugins.Effect{{Name: plugins.EffectNotice, Text: "hm"}}, github.LocalFailed, "no answer: the plugin gave no answer"},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			session := newThreadSession(test, &recordingRunner{effects: testCase.effects})
			session.ask()
			got := session.localThreads()[0]
			if got.Local != testCase.want || got.Comments[1].Body != testCase.body {
				test.Errorf("thread = %d %q, want %d %q", got.Local, got.Comments[1].Body, testCase.want, testCase.body)
			}
		})
	}
}

// A result for a local thread goes to the store whatever is on screen: an
// answer that arrives while the pull request is closed is there on reopen. A
// plain notice of the same run is dropped, as in a run with no thread.
func TestLocalThreadAnswerArrivesWhileThePullRequestIsClosed(test *testing.T) {
	session := newStackedSession(test, &recordingRunner{effects: append(answerEffects("Because."), plugins.Effect{Name: plugins.EffectNotice, Text: "done"})}, withThreadCommands)
	result := session.held("ask why?")
	session.line("q") // the workspace closes; the list is on top
	session.infos = nil
	session.pump(func() tea.Msg { return result })
	if strings.Contains(session.status(), "done") {
		test.Errorf("the notice was shown on a list: %q", session.status())
	}
	reopened := New(session.inliner.client, testRepository, 7)
	session.pump(userinterface.Push(reopened))
	var found []github.ReviewThread
	for _, thread := range reopened.paneThreads() {
		if thread.Local.IsLocal() {
			found = append(found, thread)
		}
	}
	if len(found) != 1 || found[0].Local != github.LocalAnswered || found[0].Comments[1].Body != "Because." {
		test.Fatalf("reopened threads = %+v, want the answered one", found)
	}
	// Another pull request has none.
	other := testDetail()
	other.Number = 8
	session.inliner.client.SetPullRequest(other)
	session.inliner.client.SetDiff(testRepository, 8, readDiff(test))
	elsewhere := New(session.inliner.client, testRepository, 8)
	session.pump(userinterface.Push(elsewhere))
	for _, thread := range elsewhere.paneThreads() {
		if thread.Local.IsLocal() {
			test.Errorf("pull request 8 shows a local thread of pull request 7: %+v", thread)
		}
	}
}

// A run that began before the view closed still has its thread, waiting, when
// the view reopens.
func TestLocalThreadWaitsAcrossReopening(test *testing.T) {
	session := newStackedSession(test, &recordingRunner{effects: answerEffects("Because.")}, withThreadCommands)
	result := session.held("ask why?")
	session.line("q")
	reopened := New(session.inliner.client, testRepository, 7)
	session.pump(userinterface.Push(reopened))
	if got := reopened.localThreads; len(got) != 1 || got[0].State != github.LocalWaiting {
		test.Fatalf("local threads = %+v, want one waiting", got)
	}
	session.pump(func() tea.Msg { return result })
	if got := reopened.localThreads; len(got) != 1 || got[0].State != github.LocalAnswered {
		test.Fatalf("local threads = %+v, want one answered", got)
	}
}

// The refusal is exact for each action, from a key and from its command, on
// the thread and on each of its comments, and nothing is sent to GitHub.
func TestLocalThreadRefusesEveryWrite(test *testing.T) {
	type action struct {
		name     string
		keys     []string // key presses; empty for a command
		command  string
		verb     string
		onThread bool // the thread is enough; otherwise a comment is needed
	}
	actions := []action{
		{"r", []string{"r"}, "", "reply", true},
		{"x", []string{"x"}, "", "resolve", true},
		{":resolve", nil, "resolve", "resolve", true},
		{":unresolve", nil, "unresolve", "resolve", true},
		{"e", []string{"e"}, "", "edit", false},
		{":edit-comment", nil, "edit-comment", "edit", false},
		{"D", []string{"D"}, "", "delete", false},
		{":delete-comment", nil, "delete-comment", "delete", false},
		{":react", nil, "react +1", "react to", false},
		{":react without a name", nil, "react", "react to", false},
	}
	for _, actionCase := range actions {
		for _, commentIndex := range []int{0, 1} {
			test.Run(fmt.Sprintf("%s on comment %d", actionCase.name, commentIndex), func(test *testing.T) {
				session := newThreadSession(test, &recordingRunner{effects: answerEffects("Because.")})
				session.ask()
				identifier := session.localThreads()[0].Identifier
				session.toLocalComment(identifier, commentIndex)
				session.infos, session.errors = nil, nil
				before := session.calls()
				if len(actionCase.keys) > 0 {
					for _, text := range actionCase.keys {
						session.pumped(text)
					}
				} else {
					session.line(actionCase.command)
				}
				want := "this answer is local to strig, not on GitHub: nothing to " + actionCase.verb
				if diff := cmp.Diff([]string{want}, session.infos); diff != "" {
					test.Errorf("notices (-want +got):\n%s", diff)
				}
				if len(session.errors) != 0 {
					test.Errorf("errors = %v", session.errors)
				}
				if after := session.calls(); after != before {
					test.Errorf("the client was called %d times, want none: %v", after-before, session.inliner.client.Calls()[before:])
				}
				if len(session.inliner.openedOn) != 0 {
					test.Errorf("the editor opened on %q", session.inliner.openedOn)
				}
				if _, asked := session.inliner.lastConfirm(); asked {
					test.Error("a question was asked")
				}
			})
		}
	}
}

// With the cursor on the diff line, not on the thread, the thread-level
// actions still find the one local thread and refuse it.
func TestLocalThreadRefusedFromItsLine(test *testing.T) {
	session := newThreadSession(test, &recordingRunner{effects: answerEffects("Because.")})
	session.ask()
	session.inliner.moveTo(github.SideRight, 15)
	session.infos = nil
	session.pumped("x")
	session.pumped("r")
	want := []string{
		"this answer is local to strig, not on GitHub: nothing to resolve",
		"this answer is local to strig, not on GitHub: nothing to reply",
	}
	if diff := cmp.Diff(want, session.infos); diff != "" {
		test.Errorf("notices (-want +got):\n%s", diff)
	}
}

// A GitHub thread on the same file is not refused as local.
func TestLocalThreadLeavesGitHubThreadsWritable(test *testing.T) {
	session := newThreadSession(test, &recordingRunner{effects: answerEffects("Because.")})
	session.ask()
	session.inliner.toThread("t-open")
	session.infos = nil
	session.pumped("x")
	for _, info := range session.infos {
		if strings.Contains(info, "local to strig") {
			test.Errorf("a GitHub thread was refused as local: %q", info)
		}
	}
	if got := session.inliner.methods(fake.MethodListReviewThreads); len(got) == 0 {
		test.Error("x on a GitHub thread did not start its re-read")
	}
}

// A local thread is never part of what the Overview, the counts and the
// writes see.
func TestLocalThreadIsNotAGitHubThread(test *testing.T) {
	session := newThreadSession(test, &recordingRunner{effects: answerEffects("Because.")})
	view := session.inliner.view
	before := len(view.threads)
	session.press("h")
	session.press("g")
	session.press("g")
	overviewBefore := withoutStatusBar(session.screen())
	session.press("l")
	session.inliner.moveTo(github.SideRight, 15)
	session.ask()
	if got := len(view.threads); got != before {
		test.Errorf("GitHub threads = %d, want %d", got, before)
	}
	session.press("h")
	session.press("g")
	session.press("g")
	if diff := cmp.Diff(overviewBefore, withoutStatusBar(session.screen())); diff != "" {
		test.Errorf("the Overview changed (-before +after):\n%s", diff)
	}
}

// Folding, the thread keys, the options and search treat a local thread as any
// other.
func TestLocalThreadActsLikeAnyThread(test *testing.T) {
	session := newThreadSession(test, &recordingRunner{effects: answerEffects("Because retries are bounded.")})
	session.ask()
	identifier := session.localThreads()[0].Identifier

	reachable := func() bool {
		session.inliner.diffView().GotoTop()
		for range 6 {
			session.pumped(")")
			if threads := session.inliner.diffView().Context().Threads; len(threads) == 1 && threads[0].Identifier == identifier {
				return true
			}
		}
		return false
	}

	// ) lands on it and ( leaves it.
	if !reachable() {
		test.Fatal(") never reached the local thread")
	}
	session.pumped("(")
	if threads := session.inliner.diffView().Context().Threads; len(threads) == 1 && threads[0].Identifier == identifier {
		test.Error("( did not leave the local thread")
	}

	// za folds it.
	if !reachable() || !strings.Contains(session.screen(), "Because retries are bounded.") {
		test.Fatal("the answer is not shown")
	}
	session.pumped("z")
	session.pumped("a")
	if screen := session.screen(); strings.Contains(screen, "Because retries are bounded.") || !strings.Contains(screen, "▸ you 2 comments") {
		test.Error("za did not fold the local thread")
	}
	session.pumped("z")
	session.pumped("a")

	// hideresolved and skipresolved leave it alone: it is never resolved.
	session.line("set hideresolved skipresolved")
	if !strings.Contains(session.screen(), "Because retries are bounded.") {
		test.Error("hideresolved hid a local thread")
	}
	if !reachable() {
		test.Error(") with skipresolved skipped the local thread")
	}

	// Search finds its text in the thread's rows.
	session.inliner.diffView().GotoTop()
	session.press("/")
	for _, character := range "bounded" {
		session.key(string(character))
	}
	session.pump(session.key("enter"))
	if context := session.inliner.diffView().Context(); !context.OnComment || context.CommentThread.Identifier != identifier {
		test.Errorf("search did not land in the local thread: %+v", context.Threads)
	}
}

// What the plugin sends is cleaned before it is drawn: the answer through the
// Markdown path, a failure as plain text.
func TestLocalThreadHostileText(test *testing.T) {
	hostile := "hi\x1b]0;owned\x07\x1b[2J\u009b31m\u202etxt\xff"
	for _, testCase := range []struct {
		name    string
		effects []plugins.Effect
		err     error
		line    string
		want    string
	}{
		{"answer", answerEffects(hostile), nil, "ask q", "txt"},
		{"error effect", []plugins.Effect{{Name: plugins.EffectError, Text: hostile}}, nil, "ask q", "no answer: hi"},
		{"failed run", nil, errors.New(hostile), "ask q", "no answer: hi"},
		{"question", answerEffects("ok"), nil, "ask " + hostile, "txt"},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			session := newThreadSession(test, &recordingRunner{effects: testCase.effects, err: testCase.err})
			session.line(testCase.line)
			raw := session.model.View().Content
			screen := ansi.Strip(raw)
			if !strings.Contains(screen, testCase.want) {
				test.Errorf("screen lacks %q", testCase.want)
			}
			if strings.IndexFunc(screen, func(character rune) bool { return unicode.IsControl(character) && character != '\n' }) >= 0 {
				test.Errorf("screen holds a control character")
			}
			for _, bad := range []string{"owned\x07", "\x1b]0", "\x1b[2J", "\u202e", "\u009b"} {
				if strings.Contains(raw, bad) {
					test.Errorf("raw output holds %q", bad)
				}
			}
		})
	}
}

// The question is what was typed, as plain text: nothing in it is Markdown.
func TestLocalThreadQuestionIsPlainText(test *testing.T) {
	for _, question := range []string{"why is List<T> used?", "what does a*b*c mean", "# heading?", "use `Retry` and _this_"} {
		test.Run(question, func(test *testing.T) {
			session := newThreadSession(test, &recordingRunner{effects: answerEffects("ok")})
			session.line("ask " + question)
			if screen := session.screen(); !strings.Contains(screen, "│   "+question) {
				test.Errorf("the question is not on screen as typed: %q", question)
			}
		})
	}
}

// An error printed beside an answer is not lost: the thread shows the answer
// and the status bar the error, cleaned.
func TestLocalThreadErrorBesideAnAnswerReachesTheStatusBar(test *testing.T) {
	session := newThreadSession(test, &recordingRunner{effects: []plugins.Effect{
		{Name: plugins.EffectAnswer, Text: "Fine."},
		{Name: plugins.EffectError, Text: "rate limited\x1b[2J"},
	}})
	session.ask()
	if got := session.status(); !strings.HasSuffix(got, "rate limited [2J") {
		test.Errorf("status = %q, want the error", got)
	}
	if got := session.localThreads()[0]; got.Local != github.LocalAnswered {
		test.Errorf("thread state = %d, want answered", got.Local)
	}
	// With no answer the thread has the error, and the status bar does not.
	other := newThreadSession(test, &recordingRunner{effects: []plugins.Effect{{Name: plugins.EffectError, Text: "no model"}}})
	other.ask()
	if got := other.status(); strings.Contains(got, "no model") {
		test.Errorf("status = %q, the error is in the thread", got)
	}
}

// The waiting line is muted and the failure is in the error style.
func TestLocalThreadStyles(test *testing.T) {
	for _, testCase := range []struct {
		name    string
		effects []plugins.Effect
		deliver bool
		text    string
		style   func(activeTheme theme.Theme) lipgloss.Style
	}{
		{"waiting", nil, false, "waiting for an answer…", func(activeTheme theme.Theme) lipgloss.Style { return activeTheme.Muted }},
		{"failed", []plugins.Effect{{Name: plugins.EffectError, Text: "no model"}}, true, "no answer: no model", func(activeTheme theme.Theme) lipgloss.Style { return activeTheme.ErrorText }},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			session := newThreadSession(test, &recordingRunner{effects: testCase.effects})
			result := session.held("ask why?")
			if testCase.deliver {
				session.pump(func() tea.Msg { return result })
			}
			want := testCase.style(session.inliner.view.theme).Render(testCase.text)
			if !strings.Contains(session.model.View().Content, want) {
				test.Errorf("the line is not drawn with its style: want %q", want)
			}
		})
	}
}
