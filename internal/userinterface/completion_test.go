package userinterface

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/golden"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
	"github.com/MikePors/strigidae/internal/userinterface/theme/scheme"
)

// listerView is a view that owns commands, as the workspace does.
type listerView struct {
	*fakeView
	names []string
}

func newLister(title string, names ...string) *listerView {
	return &listerView{fakeView: newFake(title), names: names}
}

func (view *listerView) Update(message tea.Msg) (View, tea.Cmd) {
	_, command := view.fakeView.Update(message)
	return view, command
}

func (view *listerView) CommandNames() []string { return view.names }

func (view *listerView) Command(name, _ string) (tea.Cmd, bool) {
	return nil, slices.Contains(view.names, name)
}

// completionKey builds the key a test names: a character, or a key by name.
func completionKey(name string) tea.KeyPressMsg {
	switch name {
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "space":
		return tea.KeyPressMsg{Code: ' ', Text: " "}
	}
	return tea.KeyPressMsg{Code: []rune(name)[0], Text: name}
}

// typeKeys presses ":" and then each key, where a key is a name from
// completionKey or, for anything else, a string typed character by character.
func typeKeys(model *Model, names ...string) {
	model.Update(keyFor(":"))
	for _, name := range names {
		switch name {
		case "tab", "shift+tab", "left", "backspace", "enter", "esc", "space":
			model.Update(completionKey(name))
		default:
			for _, character := range name {
				model.Update(completionKey(string(character)))
			}
		}
	}
}

// shown is the match list the model is showing, or nil.
func shown(model *Model) []string {
	if model.commandLine.completion == nil {
		return nil
	}
	return model.commandLine.completion.matches
}

func newCompletionModel(top View) *Model {
	model := New(top, theme.Default(), Options{Tick: instantTick})
	model.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	return model
}

func prefixed(prefix string, names []string) []string {
	var matches []string
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			matches = append(matches, name)
		}
	}
	return matches
}

func TestCompletion(test *testing.T) {
	shell := CommandNames()
	view := []string{"create", "copy-url", "edit-pull-request"}
	options := optionWords()
	tests := []struct {
		name      string
		keys      []string
		wantLine  string
		wantShown []string
	}{
		{"first tab fills the first match and lists all of them", []string{"tab"}, shell[0], append(slices.Clone(shell), view...)},
		{"tab moves to the next match", []string{"q", "tab", "tab"}, "quit", prefixed("q", shell)},
		{"tab wraps past the last match", []string{"c", "tab", "tab", "tab"}, "create", prefixed("c", append(slices.Clone(shell), view...))},
		{"shift+tab moves to the previous match", []string{"c", "tab", "tab", "shift+tab"}, "create", prefixed("c", append(slices.Clone(shell), view...))},
		{"shift+tab wraps before the first match", []string{"c", "tab", "shift+tab"}, "copy-url", prefixed("c", append(slices.Clone(shell), view...))},
		{"shift+tab first starts from the last match", []string{"c", "shift+tab"}, "copy-url", prefixed("c", append(slices.Clone(shell), view...))},
		{"a single match fills in with no list", []string{"th", "tab"}, "theme", nil},
		{"no match does nothing", []string{"zzz", "tab"}, "zzz", nil},
		{"matching is case-sensitive", []string{"Q", "tab"}, "Q", nil},
		{"typing accepts the match and closes the list", []string{"q", "tab", "tab", "x"}, "quitx", nil},
		{"backspace closes the list and edits", []string{"q", "tab", "tab", "backspace"}, "qui", nil},
		{"tab after backspace starts again", []string{"q", "tab", "tab", "backspace", "tab"}, "quit", prefixed("qui", shell)[:0:0]},
		{"esc closes the command line", []string{"q", "tab", "esc"}, "", nil},
		{"a view command completes", []string{"edit", "tab"}, "edit-pull-request", nil},
		{"set lists every option, then the no forms", []string{"set", "space", "tab"}, "set " + options[0], options},
		{"set completes an option name", []string{"set", "space", "hide", "tab", "tab"}, "set hidereviewed", prefixed("hide", options)},
		{"set completes the no forms", []string{"set", "space", "nowr", "tab"}, "set nowrapscan", prefixed("nowr", options)},
		{"set no alone offers only the no forms", []string{"set", "space", "no", "tab"}, "set nowrapscan", prefixed("no", options)},
		{"set completes a later word", []string{"set", "space", "wrap", "space", "nohl", "tab"}, "set wrap nohlsearch", nil},
		{"set leaves a query alone", []string{"set", "space", "wrap?", "tab"}, "set wrap?", nil},
		{"theme completes a name", []string{"theme", "space", "term", "tab"}, "theme terminal", nil},
		{"theme lists every name", []string{"theme", "space", "tab"}, "theme " + scheme.Names()[0], scheme.Names()},
		{"theme takes only one word", []string{"theme", "space", "a", "space", "tab"}, "theme a ", nil},
		{"another command's arguments are left alone", []string{"quit", "space", "tab"}, "quit ", nil},
		{"a command with no argument words is left alone", []string{"create", "space", "tab"}, "create ", nil},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			model := newCompletionModel(newLister("top", view...))
			typeKeys(model, testCase.keys...)
			if got := model.commandLine.input.Value(); got != testCase.wantLine {
				test.Errorf("line = %q, want %q", got, testCase.wantLine)
			}
			if strings.Contains(model.commandLine.input.Value(), "\t") {
				test.Error("a tab character was typed")
			}
			if testCase.name == "tab after backspace starts again" {
				return // the list is checked by the next line only
			}
			if diff := cmp.Diff(testCase.wantShown, shown(model)); diff != "" {
				test.Errorf("list (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCompletionEnterRunsTheLineAsShown(test *testing.T) {
	model := newCompletionModel(newLister("top"))
	typeKeys(model, "q", "tab", "tab")
	_, command := model.Update(completionKey("enter"))
	if model.commandLine.CapturesKeys() || shown(model) != nil {
		test.Error("enter left the command line or the list open")
	}
	if command == nil {
		test.Fatal("enter ran nothing")
	}
	_, quit := model.Update(command())
	if !isQuit(quit) {
		test.Fatal(":quit on the only view should quit")
	}
}

func TestCompletionEscCancelsTheCommandLine(test *testing.T) {
	model := newCompletionModel(newLister("top"))
	typeKeys(model, "q", "tab", "tab", "esc")
	if model.commandLine.CapturesKeys() || shown(model) != nil || model.commandLine.input.Value() != "" {
		test.Error("esc left the command line, its text or the list open")
	}
	if strings.Contains(ansi.Strip(model.View().Content), "quitall") {
		test.Error("the list is still drawn")
	}
}

func TestCompletionOnlyAtTheEndOfTheLine(test *testing.T) {
	model := newCompletionModel(newLister("top"))
	typeKeys(model, "se", "left", "tab")
	if got := model.commandLine.input.Value(); got != "se" || shown(model) != nil {
		test.Errorf("line = %q, list %v; want the line unchanged", got, shown(model))
	}
}

func TestCompletionOffersOnlyTheTopViewsCommands(test *testing.T) {
	below := newLister("below", "alpha")
	above := newLister("above", "beta")
	model := newCompletionModel(below)
	run(model, Push(above))

	typeKeys(model, "tab")
	if !slices.Contains(shown(model), "beta") || slices.Contains(shown(model), "alpha") {
		test.Errorf("with above on top the list is %v, want beta and not alpha", shown(model))
	}
	model.Update(completionKey("esc"))

	run(model, Pop())
	typeKeys(model, "tab")
	if !slices.Contains(shown(model), "alpha") || slices.Contains(shown(model), "beta") {
		test.Errorf("with below on top the list is %v, want alpha and not beta", shown(model))
	}
	model.Update(completionKey("esc"))

	run(model, Push(newFake("plain")))
	typeKeys(model, "tab")
	if diff := cmp.Diff(CommandNames(), shown(model)); diff != "" {
		test.Errorf("a view with no commands: list (-want +got):\n%s", diff)
	}
}

func TestCompletionNeverReachesTheView(test *testing.T) {
	view := newLister("top", "create")
	model := newCompletionModel(view)
	before := len(view.got)
	typeKeys(model, "c", "tab", "shift+tab", "tab")
	for _, message := range view.got[before:] {
		if _, isKey := message.(tea.KeyPressMsg); isKey {
			test.Errorf("a raw key reached the view: %v", message)
		}
		if pressed, isKey := message.(interface{ String() string }); isKey && strings.Contains(pressed.String(), "tab") {
			test.Errorf("a tab reached the view: %v", message)
		}
	}
	if !model.commandLine.CapturesKeys() {
		test.Error("tab released the command line")
	}
}

func TestCompletionLeavesPromptsAlone(test *testing.T) {
	view := newPrompting()
	model := newCompletionModel(view)
	model.Update(keyFor("/"))
	model.Update(completionKey("s"))
	model.Update(completionKey("tab"))
	if shown(model) != nil {
		test.Error("a prompt got a match list")
	}
	if got := model.commandLine.input.Value(); got != "s" {
		test.Errorf("prompt text = %q, want %q (no completion)", got, "s")
	}
}

func TestCompletionListIsDrawnAboveTheCommandLine(test *testing.T) {
	model := newCompletionModel(newLister("top", "create", "copy-url"))
	typeKeys(model, "c", "tab", "tab")
	rows := strings.Split(model.View().Content, "\n")
	if len(rows) != 12 {
		test.Fatalf("frame has %d rows, want 12", len(rows))
	}
	if got := ansi.Strip(rows[10]); !strings.HasPrefix(got, "copy-url") && !strings.Contains(got, "copy-url") {
		test.Errorf("row above the command line = %q, want the match list", got)
	}
	activeTheme := theme.Default()
	if !strings.Contains(rows[10], activeTheme.StatusContext.Render(model.commandLine.input.Value())) {
		test.Errorf("current match %q is not drawn with the highlight style", model.commandLine.input.Value())
	}
	if got := ansi.Strip(rows[11]); !strings.HasPrefix(got, ":"+model.commandLine.input.Value()) {
		test.Errorf("command line = %q", got)
	}
}

func TestMatchListGoldens(test *testing.T) {
	model := newCompletionModel(newLister("top", "create", "create-draft", "copy-url"))
	typeKeys(model, "c", "tab", "tab")
	golden.Assert(test, "completion_list", model.View().Content, false)

	narrow := newCompletionModel(newLister("top", "create", "create-draft", "copy-url"))
	narrow.Update(tea.WindowSizeMsg{Width: 20, Height: 6})
	typeKeys(narrow, "set", "space", "tab", "tab", "tab")
	golden.Assert(test, "completion_list_overflow", narrow.View().Content, false)
}

func TestRenderMatchesWindow(test *testing.T) {
	activeTheme := theme.Default()
	matches := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf"}
	tests := []struct {
		name  string
		index int
		width int
		want  string
	}{
		{"everything fits", 3, 60, "alpha  bravo  charlie  delta  echo  foxtrot  golf" + strings.Repeat(" ", 12)},
		{"the start of the list", 0, 20, "alpha  bravo …"},
		{"the middle scrolls both ways", 3, 20, "… delta  echo …"},
		{"the end of the list", 6, 20, "… foxtrot  golf"},
		{"just one fits", 2, 12, "… charlie …"},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			got := renderMatches(activeTheme, matches, testCase.index, testCase.width)
			if width := lipgloss.Width(got); width != testCase.width {
				test.Errorf("width = %d, want %d", width, testCase.width)
			}
			if plain := ansi.Strip(got); strings.TrimRight(plain, " ") != strings.TrimRight(testCase.want, " ") {
				test.Errorf("drawn %q, want %q", plain, testCase.want)
			}
			if strings.Contains(got, "\n") {
				test.Error("the list wrapped")
			}
		})
	}
}

func TestRenderMatchesKeepsTheCurrentOneVisible(test *testing.T) {
	activeTheme := theme.Default()
	matches := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf"}
	for width := 1; width <= 60; width++ {
		for index := range matches {
			got := renderMatches(activeTheme, matches, index, width)
			if lipgloss.Width(got) != width || strings.Contains(got, "\n") {
				test.Fatalf("width %d index %d: drew %q", width, index, ansi.Strip(got))
			}
			plain := ansi.Strip(got)
			current := matches[index]
			if width >= lipgloss.Width(current)+4 && !strings.Contains(plain, current) {
				test.Fatalf("width %d index %d: %q lacks the current match", width, index, plain)
			}
		}
	}
}

func TestRenderMatchesCutsACurrentMatchWiderThanTheLine(test *testing.T) {
	activeTheme := theme.Default()
	matches := []string{"short", "a-command-name-wider-than-the-line", "other"}
	got := renderMatches(activeTheme, matches, 1, 10)
	if width := lipgloss.Width(got); width != 10 {
		test.Errorf("width = %d, want 10", width)
	}
	if plain := ansi.Strip(got); plain != "a-command…" {
		test.Errorf("drawn %q, want the match cut with an ellipsis", plain)
	}
}
