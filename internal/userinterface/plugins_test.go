package userinterface

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/config"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/plugins"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

var (
	pullRequestSeven = PullRequestIdentity{Repository: github.Repository{Owner: "acme", Name: "widgets"}, Number: 7, Head: "abc"}
	// hostileText has an OSC title, a screen clear, a C1 control, a bidi
	// override and invalid UTF-8.
	hostileText = "hi\x1b]0;owned\x07\x1b[2J\u009b31m\u202etxt\xff"
)

// pullRequestView is a view showing a pull request, for the root to deliver
// plugin results to.
type pullRequestView struct {
	*fakeView
	identity PullRequestIdentity
	loaded   bool
	reloads  int
}

func newPullRequestView(identity PullRequestIdentity) *pullRequestView {
	return &pullRequestView{fakeView: newFake("#7"), identity: identity, loaded: true}
}

func (view *pullRequestView) Update(message tea.Msg) (View, tea.Cmd) {
	_, command := view.fakeView.Update(message)
	return view, command
}

func (view *pullRequestView) PullRequestIdentity() (PullRequestIdentity, bool) {
	return view.identity, view.loaded
}

func (view *pullRequestView) Reload() tea.Cmd {
	view.reloads++
	return nil
}

// fakeRunner answers every run at once with its canned result.
type fakeRunner struct {
	effects  []plugins.Effect
	err      error
	names    []string
	requests []plugins.Request
	contexts []context.Context
}

func (runner *fakeRunner) Run(ctx context.Context, name string, request plugins.Request) ([]plugins.Effect, error) {
	runner.names = append(runner.names, name)
	runner.requests = append(runner.requests, request)
	runner.contexts = append(runner.contexts, ctx)
	return runner.effects, runner.err
}

// pluginModel is a root model over a pull request view and a fake runner. The
// returned ticks hold the timers the model asked for.
func pluginModel(test *testing.T, runner PluginRunner, root View) (*Model, *[]time.Duration) {
	test.Helper()
	var asked []time.Duration
	model := New(root, theme.Default(), Options{
		Tick: func(duration time.Duration, function func(time.Time) tea.Msg) tea.Cmd {
			asked = append(asked, duration)
			return func() tea.Msg { return function(time.Time{}) }
		},
		ViewCommands:   testViewCommands,
		PluginRunner:   runner,
		PluginCommands: []PluginCommand{{Name: "ask", Target: config.TargetLines}},
	})
	model.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	return model, &asked
}

// step is Update, but returns the command the message itself produced and not
// the batch with the spinner, whose frames would sleep.
func step(model *Model, message tea.Msg) tea.Cmd {
	command := model.update(message)
	model.syncSpinner()
	return command
}

// startRun asks for a run and returns the command that performs it.
func startRun(model *Model, identity PullRequestIdentity) tea.Cmd {
	return step(model, RunPluginMessage{Name: "ask", Request: plugins.Request{Command: "ask"}, Identity: identity})
}

func TestPluginsMessageReachesTheViews(test *testing.T) {
	root := newPullRequestView(pullRequestSeven)
	model, _ := pluginModel(test, &fakeRunner{}, root)
	want := PluginsMessage{Commands: []PluginCommand{{Name: "ask", Target: config.TargetLines}}}
	if diff := cmp.Diff(want, root.got[0]); diff != "" {
		test.Errorf("first message (-want +got):\n%s", diff)
	}
	pushed := newPullRequestView(pullRequestSeven)
	step(model, PushMessage{View: pushed})
	found := false
	for _, message := range pushed.got {
		if diff := cmp.Diff(want, message); diff == "" {
			found = true
		}
	}
	if !found {
		test.Errorf("a pushed view was not told the plugin commands: %v", pushed.got)
	}
}

func TestPluginEffects(test *testing.T) {
	for _, testCase := range []struct {
		name     string
		effects  []plugins.Effect
		err      error
		wantText string
		wantKind statusKind
		reloads  int
	}{
		{"notice", []plugins.Effect{{Name: plugins.EffectNotice, Text: "Looks fine"}}, nil, "Looks fine", statusInfo, 0},
		{"error effect", []plugins.Effect{{Name: plugins.EffectError, Text: "no model"}}, nil, "no model", statusError, 0},
		{"answer shows as a notice", []plugins.Effect{{Name: plugins.EffectAnswer, Text: "Because."}}, nil, "Because.", statusInfo, 0},
		{"reload", []plugins.Effect{{Name: plugins.EffectReload}}, nil, "", statusNone, 1},
		{"effects in order, the last status stays", []plugins.Effect{{Name: plugins.EffectNotice, Text: "first"}, {Name: plugins.EffectReload}, {Name: plugins.EffectNotice, Text: "second"}}, nil, "second", statusInfo, 1},
		{"failed run shows as an error", nil, errors.New(`plugin "ask": timed out after 1m0s`), `plugin "ask": timed out after 1m0s`, statusError, 0},
		{"nothing printed", nil, nil, "", statusNone, 0},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			root := newPullRequestView(pullRequestSeven)
			model, _ := pluginModel(test, &fakeRunner{effects: testCase.effects, err: testCase.err}, root)
			command := startRun(model, pullRequestSeven)
			step(model, command()) // the timers that clear the status later are not run
			if model.status.text != testCase.wantText || model.status.kind != testCase.wantKind {
				test.Errorf("status = %q (%d), want %q (%d)", model.status.text, model.status.kind, testCase.wantText, testCase.wantKind)
			}
			if root.reloads != testCase.reloads {
				test.Errorf("reloads = %d, want %d", root.reloads, testCase.reloads)
			}
		})
	}
}

func unsafeRune(character rune) bool {
	return unicode.IsControl(character) || unicode.Is(unicode.Cf, character)
}

// Whatever a plugin prints, in an effect or in the text of the error of a
// failed run, is cleaned before the status bar.
func TestPluginTextIsCleanedBeforeTheStatusBar(test *testing.T) {
	for _, testCase := range []struct {
		name    string
		effects []plugins.Effect
		err     error
	}{
		{"notice", []plugins.Effect{{Name: plugins.EffectNotice, Text: hostileText}}, nil},
		{"error effect", []plugins.Effect{{Name: plugins.EffectError, Text: hostileText}}, nil},
		{"answer", []plugins.Effect{{Name: plugins.EffectAnswer, Text: hostileText}}, nil},
		{"error of a failed run", nil, errors.New(hostileText)},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			root := newPullRequestView(pullRequestSeven)
			model, _ := pluginModel(test, &fakeRunner{effects: testCase.effects, err: testCase.err}, root)
			step(model, startRun(model, pullRequestSeven)())
			if !strings.HasPrefix(model.status.text, "hi") || !strings.HasSuffix(model.status.text, "txt\ufffd") {
				test.Errorf("status = %q, want the readable text kept", model.status.text)
			}
			if index := strings.IndexFunc(model.status.text, unsafeRune); index >= 0 {
				test.Errorf("status %q holds an unsafe character at %d", model.status.text, index)
			}
			if rendered := ansi.Strip(model.bar().render(model.theme, 60)); strings.IndexFunc(rendered, unsafeRune) >= 0 {
				test.Errorf("rendered bar %q holds an unsafe character", rendered)
			}
		})
	}
}

// A result is shown only on the pull request it was asked from, at the head it
// was asked at, and only if that pull request is the top view.
func TestPluginResultGoesToTheRightPullRequest(test *testing.T) {
	other := PullRequestIdentity{Repository: pullRequestSeven.Repository, Number: 8, Head: "abc"}
	otherRepository := PullRequestIdentity{Repository: github.Repository{Owner: "acme", Name: "gadgets"}, Number: 7, Head: "abc"}
	newHead := PullRequestIdentity{Repository: pullRequestSeven.Repository, Number: 7, Head: "def"}
	for _, testCase := range []struct {
		name  string
		shown PullRequestIdentity // what the top view shows when the result arrives
		close bool                // the view is popped first
		want  bool
	}{
		{"same pull request", pullRequestSeven, false, true},
		{"another number", other, false, false},
		{"another repository, same number", otherRepository, false, false},
		{"the head moved on: the result still shows", newHead, false, true},
		{"the view was closed", pullRequestSeven, true, false},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			list := newFake("list")
			model, _ := pluginModel(test, &fakeRunner{effects: []plugins.Effect{{Name: plugins.EffectNotice, Text: "done"}, {Name: plugins.EffectReload}}}, list)
			opened := newPullRequestView(pullRequestSeven)
			step(model, PushMessage{View: opened})
			command := startRun(model, pullRequestSeven)
			result := command()
			var current *pullRequestView
			switch {
			case testCase.close:
				step(model, PopMessage{})
			default:
				current = opened
				opened.identity = testCase.shown
			}
			step(model, result)
			shown := model.status.text == "done"
			if shown != testCase.want {
				test.Errorf("status = %q, shown = %v, want %v", model.status.text, shown, testCase.want)
			}
			if current != nil && (current.reloads == 1) != testCase.want {
				test.Errorf("reloads = %d, want a reload only when the result was shown", current.reloads)
			}
			if len(model.pluginRuns) != 0 {
				test.Errorf("%d runs still counted after the result", len(model.pluginRuns))
			}
		})
	}
}

// A run belongs to the root, so popping the view that asked does not cancel it.
func TestClosingTheViewDoesNotCancelARun(test *testing.T) {
	runner := &fakeRunner{}
	model, _ := pluginModel(test, runner, newFake("list"))
	step(model, PushMessage{View: newPullRequestView(pullRequestSeven)})
	command := startRun(model, pullRequestSeven)
	step(model, PopMessage{})
	command()
	if len(runner.contexts) != 1 || runner.contexts[0].Err() != nil {
		test.Errorf("the run's context ended when the view closed: %v", runner.contexts)
	}
}

func TestPluginSpinner(test *testing.T) {
	root := newPullRequestView(pullRequestSeven)
	runner := &fakeRunner{}
	model, _ := pluginModel(test, runner, root)
	if model.spinning {
		test.Fatal("spinning before any run")
	}
	first := startRun(model, pullRequestSeven)
	second := startRun(model, pullRequestSeven) // queued behind the first in the real runner
	if !model.spinning || model.bar().spinner == "" {
		test.Fatal("no spinner while a run of this pull request is active")
	}
	step(model, first())
	if !model.spinning {
		test.Error("the spinner stopped while a queued run is still active")
	}
	step(model, second())
	if model.spinning || model.bar().spinner != "" {
		test.Error("the spinner is still on after the last run ended")
	}
}

// A new commit during a run changes neither the spinner nor where the result
// goes: both follow the same identity.
func TestSpinnerAndResultAgreeWhenTheHeadMoves(test *testing.T) {
	root := newPullRequestView(pullRequestSeven)
	model, _ := pluginModel(test, &fakeRunner{effects: []plugins.Effect{{Name: plugins.EffectNotice, Text: "done"}, {Name: plugins.EffectReload}}}, root)
	command := startRun(model, pullRequestSeven)
	root.identity.Head = "def"
	step(model, tea.WindowSizeMsg{Width: 60, Height: 12}) // any message makes the model re-evaluate the spinner
	if !model.spinning {
		test.Error("the spinner stopped when the head moved")
	}
	step(model, command())
	if model.status.text != "done" || root.reloads != 1 {
		test.Errorf("status %q, reloads %d: the result was dropped for a new head", model.status.text, root.reloads)
	}
}

func TestPluginSpinnerFollowsTheTopView(test *testing.T) {
	list := newFake("list")
	model, _ := pluginModel(test, &fakeRunner{}, list)
	step(model, PushMessage{View: newPullRequestView(pullRequestSeven)})
	command := startRun(model, pullRequestSeven)
	step(model, PopMessage{})
	if model.spinning {
		test.Error("spinning on the list for a run of a pull request that is not on screen")
	}
	other := newPullRequestView(PullRequestIdentity{Repository: pullRequestSeven.Repository, Number: 8})
	step(model, PushMessage{View: other})
	if model.spinning {
		test.Error("spinning on pull request 8 for a run of 7")
	}
	step(model, PopMessage{})
	command()
}

func TestPluginSpinnerNotWhileUnloaded(test *testing.T) {
	root := newPullRequestView(pullRequestSeven)
	root.loaded = false
	model, _ := pluginModel(test, &fakeRunner{}, root)
	startRun(model, pullRequestSeven)
	if model.spinning {
		test.Error("spinning for a view that shows no pull request yet")
	}
}

func TestRunWithoutARunnerDoesNothing(test *testing.T) {
	model := newModel(newPullRequestView(pullRequestSeven))
	if command := startRun(model, pullRequestSeven); command != nil || len(model.pluginRuns) != 0 {
		test.Error("a run started with no plugins configured")
	}
}

// Quitting with a run under way cancels it and waits, but only for the fixed
// time; quitting with none is immediate.
func TestQuitWaitsForRunsOnlyBriefly(test *testing.T) {
	for _, testCase := range []struct {
		name string
		quit func(model *Model) tea.Cmd
	}{
		{"quit all", func(model *Model) tea.Cmd { return step(model, QuitMessage{}) }},
		{"close the last view", func(model *Model) tea.Cmd { return step(model, PopMessage{}) }},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			runner := &fakeRunner{}
			model, asked := pluginModel(test, runner, newPullRequestView(pullRequestSeven))
			startRun(model, pullRequestSeven)
			startRun(model, pullRequestSeven)

			command := testCase.quit(model)
			if isQuit(command) {
				test.Fatal("quit at once with runs under way")
			}
			if model.pluginContext.Err() == nil {
				test.Error("the runs were not cancelled")
			}
			if diff := cmp.Diff([]time.Duration{DefaultPluginQuitWait}, *asked); diff != "" {
				test.Errorf("timers asked for (-want +got):\n%s", diff)
			}
			if DefaultPluginQuitWait != 2*time.Second {
				test.Errorf("the quit wait is %s, want 2s", DefaultPluginQuitWait)
			}
			if !isQuit(step(model, command())) {
				test.Error("the wait ending did not quit")
			}
		})
	}
}

func TestQuitEndsWhenTheLastRunEnds(test *testing.T) {
	runner := &fakeRunner{err: context.Canceled}
	model, _ := pluginModel(test, runner, newPullRequestView(pullRequestSeven))
	first, second := startRun(model, pullRequestSeven), startRun(model, pullRequestSeven)
	step(model, QuitMessage{})
	if isQuit(step(model, first())) {
		test.Error("quit while a run was still under way")
	}
	if !isQuit(step(model, second())) {
		test.Error("did not quit once the last run ended")
	}
}

func TestQuitWithoutRunsIsImmediate(test *testing.T) {
	model, asked := pluginModel(test, &fakeRunner{}, newPullRequestView(pullRequestSeven))
	if !isQuit(step(model, QuitMessage{})) || len(*asked) != 0 {
		test.Error("quit with no run did not quit at once")
	}
	// A finished run is not under way.
	command := startRun(model, pullRequestSeven)
	step(model, command())
	if !isQuit(step(model, QuitMessage{})) {
		test.Error("quit after a finished run did not quit at once")
	}
}

func TestQuitWaitIsInjectable(test *testing.T) {
	var asked []time.Duration
	model := New(newPullRequestView(pullRequestSeven), theme.Default(), Options{
		Tick: func(duration time.Duration, _ func(time.Time) tea.Msg) tea.Cmd {
			asked = append(asked, duration)
			return nil
		},
		PluginRunner:   &fakeRunner{},
		PluginCommands: []PluginCommand{{Name: "ask", Target: config.TargetLines}},
		PluginQuitWait: 5 * time.Millisecond,
	})
	startRun(model, pullRequestSeven)
	step(model, QuitMessage{})
	if diff := cmp.Diff([]time.Duration{5 * time.Millisecond}, asked); diff != "" {
		test.Errorf("timers asked for (-want +got):\n%s", diff)
	}
}

// Nothing starts and nothing is shown once quit began.
func TestNothingHappensAfterQuitBegan(test *testing.T) {
	runner := &fakeRunner{effects: []plugins.Effect{{Name: plugins.EffectNotice, Text: "late"}, {Name: plugins.EffectReload}}}
	root := newPullRequestView(pullRequestSeven)
	model, _ := pluginModel(test, runner, root)
	running := startRun(model, pullRequestSeven)
	step(model, QuitMessage{})

	if command := startRun(model, pullRequestSeven); command != nil {
		test.Error("a run was started during quit")
	}
	step(model, running())
	if model.status.text != "" || root.reloads != 0 {
		test.Errorf("a result was applied after quit began: status %q, reloads %d", model.status.text, root.reloads)
	}
	if len(runner.names) != 1 {
		test.Errorf("runner called %d times, want only the run that began before quit", len(runner.names))
	}
}

func TestSecondQuitDoesNotWaitAgain(test *testing.T) {
	model, _ := pluginModel(test, &fakeRunner{}, newPullRequestView(pullRequestSeven))
	startRun(model, pullRequestSeven)
	step(model, QuitMessage{})
	if !isQuit(step(model, QuitMessage{})) {
		test.Error("the second quit waited again")
	}
}

// The command line opens during a selection only for a view that asks for it.
type keepingView struct {
	*pullRequestView
	selecting bool
	typed     []string
}

func (view *keepingView) CapturesKeys() bool     { return view.selecting }
func (view *keepingView) KeepsCommandLine() bool { return view.selecting }
func (view *keepingView) Update(message tea.Msg) (View, tea.Cmd) {
	if pressed, ok := message.(tea.KeyPressMsg); ok {
		view.typed = append(view.typed, pressed.String())
	}
	_, command := view.pullRequestView.Update(message)
	return view, command
}

func TestCommandLineOpensDuringACapturingSelection(test *testing.T) {
	view := &keepingView{pullRequestView: newPullRequestView(pullRequestSeven), selecting: true}
	model, _ := pluginModel(test, &fakeRunner{}, view)
	press(model, ":")
	if !model.commandLine.active {
		test.Fatal(":did not open the command line during the selection")
	}
	press(model, "esc") // cancel: the view keeps its selection
	if model.commandLine.active || !view.selecting {
		test.Errorf("after esc: command line active = %v, selecting = %v", model.commandLine.active, view.selecting)
	}
	if len(view.typed) != 0 {
		test.Errorf("keys reached the view: %v", view.typed)
	}
	press(model, "j") // still captured: the view gets raw keys
	if diff := cmp.Diff([]string{"j"}, view.typed); diff != "" {
		test.Errorf("keys the view heard (-want +got):\n%s", diff)
	}
}

func TestCapturingViewWithoutTheKeeperStillGetsColon(test *testing.T) {
	view := newFake("capturing")
	view.capture = true
	model := newModel(view)
	press(model, ":")
	if model.commandLine.active {
		test.Error("the command line opened over a view that captures keys and did not ask for it")
	}
}

// stubbornRunner ignores the cancel and returns only when it is let go, like a
// runner that is stuck.
type stubbornRunner struct {
	started chan struct{}
	let     chan struct{}
}

func (runner *stubbornRunner) Run(context.Context, string, plugins.Request) ([]plugins.Effect, error) {
	close(runner.started)
	<-runner.let
	return nil, nil
}

// Whatever the runner does, quitting waits no longer than the quit wait.
func TestQuitBoundHoldsWhateverTheRunnerDoes(test *testing.T) {
	runner := &stubbornRunner{started: make(chan struct{}), let: make(chan struct{})}
	model, _ := pluginModel(test, runner, newPullRequestView(pullRequestSeven))
	results := make(chan tea.Msg, 1)
	run := startRun(model, pullRequestSeven)
	go func() { results <- run() }()
	<-runner.started

	waiting := step(model, QuitMessage{})
	if isQuit(waiting) {
		test.Fatal("quit at once with a run under way")
	}
	// The run is stuck. Only the end of the wait lets strig go.
	if !isQuit(step(model, waiting())) {
		test.Error("strig did not quit when the wait ended")
	}
	close(runner.let)
	<-results
}
