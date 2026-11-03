package userinterface

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

// mappedModel is a root over a fake view with the mappings.
func mappedModel(mappings ...KeyMapping) (*Model, *fakeView) {
	view := newFake("list")
	model := New(view, theme.Default(), Options{Tick: instantTick, KeyMappings: mappings})
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	return model, view
}

// pressSequence types the keys and returns the last key's own command, without
// the spinner's.
func pressSequence(model *Model, texts ...string) tea.Cmd {
	var command tea.Cmd
	for _, text := range texts {
		command = model.update(keyFor(text))
	}
	return command
}

func TestMappedKeyRunsItsLine(test *testing.T) {
	model, view := mappedModel(KeyMapping{Sequence: "g a", Line: "qa"})
	command := pressSequence(model, "g", "a")
	if command == nil {
		test.Fatal("the mapping ran nothing")
	}
	if _, ok := command().(QuitMessage); !ok {
		test.Error(":qa did not run")
	}
	if len(view.keyMessages()) != 0 {
		test.Errorf("the view got %v", view.keyMessages())
	}
}

func TestMappedKeyUnknownCommand(test *testing.T) {
	model, _ := mappedModel(KeyMapping{Sequence: "Q", Line: "frobnicate"})
	message := pressSequence(model, "Q")()
	failure, ok := message.(ErrorMessage)
	if !ok || failure.Err.Error() != "not a command: frobnicate" {
		test.Errorf("message = %#v", message)
	}
}

// A key strig binds, globally or in the view on top, keeps its meaning.
func TestBuiltinKeysWinOverMappings(test *testing.T) {
	for _, testCase := range []struct {
		name     string
		sequence string
		keys     []string
	}{
		{"a global key", "j", []string{"j"}},
		{"a key of the view", "z q", []string{"z", "q"}},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			model, view := mappedModel(KeyMapping{Sequence: testCase.sequence, Line: "qa"})
			if command := pressSequence(model, testCase.keys...); command != nil {
				if _, ok := command().(QuitMessage); ok {
					test.Fatal("the mapping ran")
				}
			}
			got := view.keyMessages()
			if len(got) != 1 || got[0].Sequence != testCase.sequence {
				test.Errorf("the view got %v, want %q", got, testCase.sequence)
			}
		})
	}
}

func TestMappedKeysShowInTheHelp(test *testing.T) {
	model, _ := mappedModel(KeyMapping{Sequence: "g a", Line: "ask why"})
	if help := strings.Join(model.helpLines(), "\n"); !strings.Contains(help, ":ask why") {
		test.Errorf("help has no mapping:\n%s", help)
	}
}
