package userinterface

import (
	"errors"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/golden"
	"github.com/MikePors/strigidae/internal/userinterface/keys"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

// fakeView records what the root sends it.
type fakeView struct {
	title   string
	next    key.Binding // view-specific sequence "z q" (arbitrary; no real binding uses it)
	capture bool
	loading bool
	resumed int
	results int
	got     []tea.Msg
}

func newFake(title string) *fakeView {
	return &fakeView{
		title: title,
		next:  key.NewBinding(key.WithKeys("z q"), key.WithHelp("zq", "test sequence")),
	}
}

func (view *fakeView) Init() tea.Cmd { return nil }
func (view *fakeView) Loading() bool { return view.loading }
func (view *fakeView) Resumed() tea.Cmd {
	view.resumed++
	return nil
}

func (view *fakeView) Update(message tea.Msg) (View, tea.Cmd) {
	view.got = append(view.got, message)
	if key, ok := message.(tea.KeyPressMsg); ok && key.String() == "esc" {
		view.capture = false
	}
	if _, ok := message.(resultMessage); ok {
		view.results++
		view.loading = false
	}
	return view, nil
}
func (view *fakeView) Render(_, _ int) string    { return view.title + " body" }
func (view *fakeView) Title() string             { return view.title }
func (view *fakeView) ShortHelp() []key.Binding  { return []key.Binding{view.next} }
func (view *fakeView) FullHelp() [][]key.Binding { return [][]key.Binding{{view.next}} }
func (view *fakeView) CapturesKeys() bool        { return view.capture }
func (view *fakeView) last() tea.Msg             { return view.got[len(view.got)-1] }
func (view *fakeView) keyMessages() (out []keys.Message) {
	for _, message := range view.got {
		if key, ok := message.(keys.Message); ok {
			out = append(out, key)
		}
	}
	return out
}

// instantTick makes timers return their message immediately when the command
// is run, so tests need no clock or sleeping.
func instantTick(_ time.Duration, function func(time.Time) tea.Msg) tea.Cmd {
	return func() tea.Msg { return function(time.Time{}) }
}

// testViewCommands stands in for the names cmd/strig collects from the views.
var testViewCommands = []string{"approve", "merge", "review", "copy-filename", "resolve", "unresolve"}

func newModel(root View) *Model {
	model := New(root, theme.Default(), Options{Tick: instantTick, ViewCommands: testViewCommands})
	model.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	return model
}

func press(model *Model, text string) tea.Cmd {
	_, command := model.Update(keyFor(text))
	return command
}

func isQuit(command tea.Cmd) bool {
	if command == nil {
		return false
	}
	_, ok := command().(tea.QuitMsg)
	return ok
}

func run(model *Model, command tea.Cmd) {
	if command == nil {
		return
	}
	message := command()
	if _, ok := message.(tea.BatchMsg); ok {
		return
	}
	model.Update(message)
}

func TestKeyEncoding(test *testing.T) {
	// Guards the assumption press() relies on: key names match binding names.
	for _, text := range []string{"j", "G", "?", ":", "ctrl+c", "esc", "enter", "tab", "ctrl+o"} {
		key := keyFor(text)
		if key.String() != text {
			test.Errorf("key for %q stringifies as %q", text, key.String())
		}
	}
}

func keyFor(text string) tea.KeyPressMsg {
	switch text {
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "ctrl+o":
		return tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: []rune(text)[0], Text: text}
}

func TestCommandLine(test *testing.T) {
	typeLine := func(model *Model, line string) tea.Cmd {
		press(model, ":")
		for _, character := range line {
			press(model, string(character))
		}
		return press(model, "enter")
	}
	test.Run(": opens and captures keys", func(test *testing.T) {
		view := newFake("v")
		model := newModel(view)
		press(model, ":")
		if !model.commandLine.active {
			test.Fatal(": should open the command line")
		}
		press(model, "j")
		if len(view.keyMessages()) != 0 || model.commandLine.input.Value() != "j" {
			test.Errorf("keys leaked to view or were not captured; value = %q", model.commandLine.input.Value())
		}
	})
	test.Run(":q on root quits", func(test *testing.T) {
		model := newModel(newFake("root"))
		_, command := model.Update(typeLine(model, "q")())
		if !isQuit(command) {
			test.Error(":q on the root view should quit")
		}
	})
	test.Run(":q pops a pushed view", func(test *testing.T) {
		model := newModel(newFake("root"))
		run(model, Push(newFake("child")))
		run(model, typeLine(model, "quit"))
		if len(model.stack) != 1 {
			test.Errorf("depth = %d, want 1", len(model.stack))
		}
	})
	test.Run(":qa quits from anywhere", func(test *testing.T) {
		model := newModel(newFake("root"))
		run(model, Push(newFake("child")))
		command := typeLine(model, "qa")
		if command == nil {
			test.Fatal("expected a command")
		}
		if _, ok := command().(QuitMessage); !ok {
			test.Error(":qa should produce QuitMessage")
		}
	})
	test.Run("unknown command shows an error", func(test *testing.T) {
		model := newModel(newFake("root"))
		run(model, typeLine(model, "frobnicate now"))
		if model.status.text != "not a command: frobnicate" || model.status.kind != statusError {
			test.Errorf("status = %+v", model.status)
		}
	})
	test.Run("esc cancels", func(test *testing.T) {
		model := newModel(newFake("root"))
		press(model, ":")
		press(model, "q")
		press(model, "esc")
		if model.commandLine.active || len(model.stack) != 1 {
			test.Error("esc should cancel the command line without running it")
		}
	})
	test.Run("backspace on empty line cancels, otherwise deletes", func(test *testing.T) {
		model := newModel(newFake("root"))
		press(model, ":")
		press(model, "q")
		model.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
		if !model.commandLine.active || model.commandLine.input.Value() != "" {
			test.Fatal("first backspace should delete the character")
		}
		model.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
		if model.commandLine.active {
			test.Error("backspace on an empty line should cancel")
		}
	})
	test.Run("ctrl-c and q do nothing special", func(test *testing.T) {
		model := newModel(newFake("root"))
		if command := press(model, "ctrl+c"); isQuit(command) {
			test.Error("ctrl+c must not quit")
		}
		if command := press(model, "q"); isQuit(command) || len(model.stack) != 1 {
			test.Error("bare q must not quit or pop")
		}
	})
	test.Run("tab and ctrl+o are not intercepted", func(test *testing.T) {
		model := newModel(newFake("root"))
		press(model, "tab")
		press(model, "ctrl+o")
		if model.commandLine.active || model.showHelp || len(model.stack) != 1 {
			test.Error("reserved keys must not navigate")
		}
	})
}

func TestParseCommand(test *testing.T) {
	tests := []struct {
		line string
		want tea.Msg // nil: expect a "not a command" error for wantName
		name string
	}{
		{line: "q", want: PopMessage{}},
		{line: "quit", want: PopMessage{}},
		{line: "  q  ", want: PopMessage{}},
		{line: "qa", want: QuitMessage{}},
		{line: "qall", want: QuitMessage{}},
		{line: "quitall", want: QuitMessage{}},
		{line: "zzz arg", name: "zzz"},
		{line: "Q", name: "Q"},
	}
	for _, testCase := range tests {
		test.Run(testCase.line, func(test *testing.T) {
			got := parseCommand(testCase.line, nil, testViewCommands)()
			if testCase.want == nil {
				errorMessage, ok := got.(ErrorMessage)
				if !ok || errorMessage.Err.Error() != "not a command: "+testCase.name {
					test.Errorf("got %#v, want not-a-command error for %q", got, testCase.name)
				}
				return
			}
			if diff := cmp.Diff(testCase.want, got); diff != "" {
				test.Errorf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
	if parseCommand("   ", nil, testViewCommands) != nil {
		test.Error("blank line should do nothing")
	}
}

// TestCapturersReleaseOnEsc is the safety net for having no ctrl+c: every
// component that captures keys must release capture on esc.
func TestCapturersReleaseOnEsc(test *testing.T) {
	tests := []struct {
		name string
		// setup returns a root with the capturer engaged, and a func reporting
		// whether it still captures.
		setup func() (*Model, func() bool)
	}{
		{"command line", func() (*Model, func() bool) {
			model := newModel(newFake("v"))
			press(model, ":")
			press(model, "x")
			return model, model.commandLine.CapturesKeys
		}},
		{"view prompt", func() (*Model, func() bool) {
			model := newModel(newPrompting())
			press(model, "/")
			press(model, "x")
			return model, model.commandLine.CapturesKeys
		}},
		{"confirm prompt", func() (*Model, func() bool) {
			model := newModel(newFake("v"))
			run(model, Confirm("sure?", nil))
			return model, func() bool { return model.confirm.CapturesKeys() }
		}},
		{"view implementing KeyCapturer", func() (*Model, func() bool) {
			view := newFake("v")
			view.capture = true
			return newModel(view), view.CapturesKeys
		}},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			model, captures := testCase.setup()
			if !captures() {
				test.Fatal("setup did not engage capture")
			}
			press(model, "esc")
			if captures() {
				test.Error("capturer still holds keys after esc")
			}
		})
	}
}

func TestPushPop(test *testing.T) {
	root, child := newFake("root"), newFake("child")
	model := newModel(root)

	run(model, Push(child))
	if model.top() != View(child) || len(model.stack) != 2 {
		test.Fatalf("push failed: depth %d", len(model.stack))
	}
	if diff := cmp.Diff(tea.WindowSizeMsg{Width: 60, Height: 11}, child.got[0]); diff != "" {
		test.Errorf("pushed view should get content size (-want +got):\n%s", diff)
	}
	if got := model.bar().crumbs; got != "root › child" {
		test.Errorf("crumbs = %q", got)
	}

	run(model, Pop())
	if model.top() != View(root) || len(model.stack) != 1 {
		test.Errorf("pop failed: depth %d", len(model.stack))
	}

	run(model, Push(nil))
	if len(model.stack) != 1 {
		test.Error("pushing nil should be ignored")
	}
	_, command := model.Update(PopMessage{})
	if !isQuit(command) {
		test.Error("PopMessage on root should quit")
	}
}

func TestSequences(test *testing.T) {
	test.Run("global gg delivered once complete", func(test *testing.T) {
		view := newFake("v")
		model := newModel(view)
		if command := press(model, "g"); command == nil {
			test.Fatal("first g should schedule a timeout")
		}
		if model.matcher.Display() != "g" || model.bar().pending != "g" {
			test.Errorf("pending prefix = %q, want g", model.bar().pending)
		}
		if len(view.keyMessages()) != 0 {
			test.Error("view received keys before sequence completed")
		}
		press(model, "g")
		if diff := cmp.Diff([]keys.Message{{Sequence: "g g"}}, view.keyMessages()); diff != "" {
			test.Errorf("mismatch (-want +got):\n%s", diff)
		}
		if model.bar().pending != "" {
			test.Error("pending prefix should clear after match")
		}
	})
	test.Run("view-declared sequence", func(test *testing.T) {
		view := newFake("v")
		model := newModel(view)
		press(model, "z")
		press(model, "q")
		if diff := cmp.Diff([]keys.Message{{Sequence: "z q"}}, view.keyMessages()); diff != "" {
			test.Errorf("mismatch (-want +got):\n%s", diff)
		}
	})
	test.Run("timeout clears pending prefix", func(test *testing.T) {
		view := newFake("v")
		model := newModel(view)
		command := press(model, "g")
		run(model, command) // fires sequenceTimeoutMessage
		if model.matcher.Pending() {
			test.Error("timeout should clear the pending prefix")
		}
		if len(view.keyMessages()) != 0 {
			test.Error("incomplete sequence must not reach the view")
		}
	})
	test.Run("stale timeout is ignored", func(test *testing.T) {
		model := newModel(newFake("v"))
		stale := press(model, "g")
		press(model, "g")
		press(model, "z") // new pending prefix
		run(model, stale)
		if !model.matcher.Pending() {
			test.Error("stale timeout cleared a newer prefix")
		}
	})
	test.Run("unbound key goes to view raw", func(test *testing.T) {
		view := newFake("v")
		model := newModel(view)
		press(model, "x")
		if _, ok := view.last().(tea.KeyPressMsg); !ok {
			test.Errorf("last msg = %T, want tea.KeyPressMsg", view.last())
		}
	})
}

func TestCounts(test *testing.T) {
	test.Run("count reaches the view with the key", func(test *testing.T) {
		view := newFake("v")
		model := newModel(view)
		press(model, "1")
		press(model, "1")
		if got := model.bar().pending; got != "11" {
			test.Errorf("status bar pending = %q, want 11", got)
		}
		if len(view.keyMessages()) != 0 {
			test.Error("view received keys while the count was pending")
		}
		press(model, "j")
		if diff := cmp.Diff([]keys.Message{{Sequence: "j", Count: 11}}, view.keyMessages()); diff != "" {
			test.Errorf("mismatch (-want +got):\n%s", diff)
		}
		if model.bar().pending != "" {
			test.Error("pending count should clear after the key")
		}
	})
	test.Run("count with a sequence shows both", func(test *testing.T) {
		view := newFake("v")
		model := newModel(view)
		press(model, "3")
		press(model, "z")
		if got := model.bar().pending; got != "3z" {
			test.Errorf("status bar pending = %q, want 3z", got)
		}
		press(model, "q")
		if diff := cmp.Diff([]keys.Message{{Sequence: "z q", Count: 3}}, view.keyMessages()); diff != "" {
			test.Errorf("mismatch (-want +got):\n%s", diff)
		}
	})
	test.Run("timeout drops the count", func(test *testing.T) {
		view := newFake("v")
		model := newModel(view)
		run(model, press(model, "4"))
		if model.matcher.Pending() {
			test.Error("timeout should clear the count")
		}
		press(model, "j")
		if diff := cmp.Diff([]keys.Message{{Sequence: "j"}}, view.keyMessages()); diff != "" {
			test.Errorf("mismatch (-want +got):\n%s", diff)
		}
	})
	test.Run("unbound key drops the count", func(test *testing.T) {
		view := newFake("v")
		model := newModel(view)
		press(model, "4")
		press(model, "x")
		press(model, "j")
		if diff := cmp.Diff([]keys.Message{{Sequence: "j"}}, view.keyMessages()); diff != "" {
			test.Errorf("mismatch (-want +got):\n%s", diff)
		}
	})
	test.Run("digits are plain input while a view captures", func(test *testing.T) {
		view := newFake("v")
		view.capture = true
		model := newModel(view)
		press(model, "1")
		press(model, "1")
		if model.matcher.Pending() {
			test.Error("a captured digit started a count")
		}
		if len(view.keyMessages()) != 0 || len(view.got) != 3 { // size + 1 + 1
			test.Errorf("view got %d messages, want 2 raw digits after the size", len(view.got))
		}
	})
	test.Run("capture drops a count already typed", func(test *testing.T) {
		view := newFake("v")
		model := newModel(view)
		press(model, "5")
		view.capture = true
		press(model, "x")
		view.capture = false
		press(model, "j")
		if diff := cmp.Diff([]keys.Message{{Sequence: "j"}}, view.keyMessages()); diff != "" {
			test.Errorf("mismatch (-want +got):\n%s", diff)
		}
	})
	test.Run("digits are plain input on the command line", func(test *testing.T) {
		view := newFake("v")
		model := newModel(view)
		press(model, ":")
		press(model, "5")
		if model.matcher.Pending() {
			test.Error("a digit on the command line started a count")
		}
		if got := model.commandLine.input.Value(); got != "5" {
			test.Errorf("command line = %q, want 5", got)
		}
	})
	test.Run("count is not a binding in the help", func(test *testing.T) {
		for _, binding := range keys.DefaultGlobal().Bindings() {
			for _, pressed := range binding.Keys() {
				if pressed == "1" {
					test.Error("the count help entry must not be matchable")
				}
			}
		}
	})
}

func TestKeyCapture(test *testing.T) {
	view := newFake("v")
	view.capture = true
	model := newModel(view)
	press(model, ":")
	press(model, "?")
	if model.commandLine.active || model.showHelp {
		test.Error("captured keys must not open the command line or help")
	}
	if len(view.got) != 3 { // size + : + ?
		test.Errorf("view got %d msgs, want 3", len(view.got))
	}
}

func TestHelpToggle(test *testing.T) {
	view := newFake("v")
	model := newModel(view)

	press(model, "?")
	if !model.showHelp {
		test.Fatal("? should open help")
	}
	press(model, "j")
	if len(view.keyMessages()) != 0 {
		test.Error("keys must not reach the view while help is open")
	}
	for _, closer := range []string{"?", "esc"} {
		model.showHelp = true
		press(model, closer)
		if model.showHelp {
			test.Errorf("%s should close help", closer)
		}
	}
	if len(model.stack) != 1 {
		test.Error("closing help must not pop")
	}
}

func TestStatusMessages(test *testing.T) {
	model := newModel(newFake("v"))

	_, command := model.Update(InfoMessage{Text: "hello"})
	if model.status.text != "hello" || model.status.kind != statusInfo {
		test.Errorf("status = %+v", model.status)
	}
	stale := command()

	_, command = model.Update(ErrorMessage{Err: errors.New("boom")})
	if model.status.text != "boom" || model.status.kind != statusError {
		test.Errorf("status = %+v", model.status)
	}
	model.Update(stale) // the older info timer must not clear the newer error
	if model.status.text != "boom" {
		test.Error("stale clear removed a newer message")
	}
	model.Update(command())
	if model.status.text != "" || model.status.kind != statusNone {
		test.Errorf("message should auto-clear, got %+v", model.status)
	}
}

// resultMessage stands in for a command result addressed to a view.
type resultMessage struct{}

func TestSpinnerFollowsTopView(test *testing.T) {
	root, child := newFake("root"), newFake("child")
	root.loading = true
	model := newModel(root)

	if !model.spinning || model.bar().spinner == "" {
		test.Fatal("a loading top view should start the spinner")
	}
	run(model, Push(child))
	if model.bar().spinner != "" {
		test.Error("spinner must follow the top view, which is not loading")
	}
	run(model, Pop())
	if model.bar().spinner == "" {
		test.Error("spinner should return when the loading view is revealed again")
	}
	root.loading = false
	model.Update(resultMessage{})
	if model.bar().spinner != "" {
		test.Error("spinner should stop when the view stops loading")
	}
}

func TestPoppedViewEndsItsSpinner(test *testing.T) {
	root, child := newFake("root"), newFake("child")
	model := newModel(root)
	run(model, Push(child))
	child.loading = true
	model.Update(struct{}{}) // any message makes the root re-check
	if model.bar().spinner == "" {
		test.Fatal("child is loading; spinner expected")
	}
	run(model, Pop())
	if model.bar().spinner != "" {
		test.Error("popping the loading view must end its spinner")
	}
}

func TestResultReachesCoveredView(test *testing.T) {
	root, child := newFake("root"), newFake("child")
	root.loading = true
	model := newModel(root)
	run(model, Push(child)) // pushed while root's load is in flight

	model.Update(resultMessage{}) // the load finishes while child is on top
	if root.results != 1 || child.results != 1 {
		test.Fatalf("result delivery: root %d, child %d; want both 1 (broadcast)", root.results, child.results)
	}
	run(model, Pop())
	if root.loading || model.bar().spinner != "" {
		test.Error("after :q the lower view should show its result with no stuck spinner")
	}
}

func TestResumed(test *testing.T) {
	root, child := newFake("root"), newFake("child")
	model := newModel(root)
	run(model, Push(child))
	if root.resumed != 0 {
		test.Fatal("push must not resume")
	}
	run(model, Pop())
	if root.resumed != 1 || child.resumed != 0 {
		test.Errorf("resumed: root %d, child %d; want 1, 0", root.resumed, child.resumed)
	}
}

func TestSpinnerTicks(test *testing.T) {
	view := newFake("v")
	model := newModel(view)

	message := spinner.New().Tick() // a tick from a spinner the view owns
	before := len(view.got)
	model.Update(message)
	if len(view.got) != before+1 {
		test.Error("a view's own spinner tick must reach the view")
	}

	rootTick := model.spin.Tick()
	before = len(view.got)
	model.Update(rootTick)
	if len(view.got) != before {
		test.Error("the root spinner's tick must be consumed, not forwarded")
	}
}

func TestResizeAndTheme(test *testing.T) {
	root, child := newFake("root"), newFake("child")
	model := newModel(root)
	run(model, Push(child))

	model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	want := tea.WindowSizeMsg{Width: 100, Height: 29}
	for name, view := range map[string]*fakeView{"root": root, "child": child} {
		if diff := cmp.Diff(want, view.last()); diff != "" {
			test.Errorf("%s resize mismatch (-want +got):\n%s", name, diff)
		}
	}

	model.Update(tea.BackgroundColorMsg{})
	if _, ok := child.last().(ThemeMessage); !ok {
		test.Errorf("child last msg = %T, want ThemeMessage", child.last())
	}
}

func TestViewDimensions(test *testing.T) {
	model := newModel(newFake("v"))
	got := model.View()
	if !got.AltScreen {
		test.Error("view should use the alt screen")
	}
	golden.Assert(test, "model_view", got.Content, false)
}

func TestHelpGolden(test *testing.T) {
	model := newModel(newFake("v"))
	model.Update(tea.WindowSizeMsg{Width: 70, Height: 30})
	press(model, "?")
	golden.Assert(test, "help_overlay", model.View().Content, false)
}

func TestStatusBarGolden(test *testing.T) {
	activeTheme := theme.Default()
	tests := []struct {
		name  string
		bar   statusBar
		width int
	}{
		{"idle", statusBar{crumbs: "strig › screen 2", text: "? help  enter open"}, 60},
		{"pending_spinner", statusBar{crumbs: "strig", text: "? help", pending: "g", spinner: "⣾"}, 60},
		{"error", statusBar{crumbs: "strig", text: "load failed: rate limited", kind: statusError}, 60},
		{"info_narrow", statusBar{crumbs: "strig", text: "a rather long informational message", kind: statusInfo, pending: "g"}, 30},
		{"tiny", statusBar{crumbs: "a very long breadcrumb trail"}, 10},
		{"long_crumbs_error_pending", statusBar{
			crumbs:  "owner/repo \u203a #123 Fix the thing \u203a internal/ui/model.go",
			text:    "post failed: 422",
			kind:    statusError,
			pending: "g",
			spinner: "\u28fe",
		}, 40},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			got := testCase.bar.render(activeTheme, testCase.width)
			if width := lipgloss.Width(got); width != testCase.width {
				test.Errorf("width = %d, want %d", width, testCase.width)
			}
			golden.Assert(test, "statusbar_"+testCase.name, got, false)
		})
	}
}

func TestStatusBarStyled(test *testing.T) {
	activeTheme := theme.Default()
	got := statusBar{crumbs: "x", text: "oops", kind: statusError}.render(activeTheme, 20)
	if got == ansi.Strip(got) {
		test.Error("expected ANSI styling in the rendered bar")
	}
}

func TestCommandLineGolden(test *testing.T) {
	model := newModel(newFake("v"))
	press(model, ":")
	press(model, "q")
	press(model, "a")
	golden.Assert(test, "command_line", model.View().Content, false)
}

func TestCommandLineStaysOneRow(test *testing.T) {
	model := newModel(newFake("v"))
	model.Update(tea.WindowSizeMsg{Width: 20, Height: 6})
	press(model, ":")
	for range 60 {
		press(model, "x")
	}
	got := model.View().Content
	if count := lipgloss.Height(got); count != 6 {
		test.Errorf("frame height = %d, want 6", count)
	}
	for i, line := range strings.Split(got, "\n") {
		if width := lipgloss.Width(line); width > 20 {
			test.Errorf("line %d is %d columns wide, want <= 20", i, width)
		}
	}
	golden.Assert(test, "command_line_overflow", got, false)
}
