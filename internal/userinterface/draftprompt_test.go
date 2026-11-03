package userinterface

import (
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/MikePors/strigidae/internal/golden"
)

// The questions are the workspace's for :ready and :draft (the workspace tests
// check their text); this shows them in the status line.
func TestDraftStatePromptsGolden(test *testing.T) {
	tests := []struct{ name, question string }{
		{"prompt_ready", "Mark #7 ready for review?"},
		{"prompt_draft", "Convert #7 to a draft?"},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			model := newModel(newFake("v"))
			askConfirm(model, testCase.question)
			golden.Assert(test, testCase.name, ansi.Strip(model.bottomRow()), false)
		})
	}
}
