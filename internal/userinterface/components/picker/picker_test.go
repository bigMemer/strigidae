package picker

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

func typeText(model *Model, text string) {
	for _, character := range text {
		model.Update(tea.KeyPressMsg{Code: character, Text: string(character)})
	}
}

func TestFilterAndChoose(test *testing.T) {
	model := New(theme.Default(), "Files", []string{"a/b.go", "c/d.go", "README.md"})
	if model.Matches() != 3 {
		test.Fatalf("matches = %d", model.Matches())
	}
	typeText(model, "DGO") // case-insensitive subsequence
	if model.Matches() != 1 {
		test.Fatalf("matches for %q = %d", model.Query(), model.Matches())
	}
	result := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Outcome != Chosen || result.Index != 1 {
		test.Errorf("result = %+v, want chosen index 1", result)
	}
}

func TestSelectStartsOnTheItem(test *testing.T) {
	model := New(theme.Default(), "Branches", []string{"a", "b", "c"})
	model.Select(2)
	if result := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Index != 2 {
		test.Errorf("chosen = %d, want 2", result.Index)
	}
	model.Select(7) // out of range: stays
	if result := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Index != 2 {
		test.Errorf("chosen after an out-of-range select = %d, want 2", result.Index)
	}
}

func TestCursorAndEsc(test *testing.T) {
	model := New(theme.Default(), "Files", []string{"x", "y", "z"})
	model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	model.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // clamps
	if result := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Index != 2 {
		test.Errorf("index = %d, want 2", result.Index)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if result := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Index != 1 {
		test.Errorf("index = %d, want 1", result.Index)
	}
	if result := model.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); result.Outcome != Cancelled {
		test.Errorf("esc outcome = %v", result.Outcome)
	}
}

func TestRenderFitsAndHandlesNoRoom(test *testing.T) {
	model := New(theme.Default(), "Files", []string{"one", "two"})
	out := ansi.Strip(model.Render(40, 10))
	if out == "" {
		test.Fatal("expected a box")
	}
	if got := model.Render(5, 3); got != "" {
		test.Errorf("no room should render nothing, got %q", got)
	}
	typeText(model, "zzz")
	if ansi.Strip(model.Render(40, 10)) == out {
		test.Error("filter should change the rendering")
	}
}
