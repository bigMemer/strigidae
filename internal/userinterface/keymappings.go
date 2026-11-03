package userinterface

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/userinterface/keys"
)

// KeyMapping is a key sequence from the config that runs a command line.
type KeyMapping struct {
	// Sequence is the keys, separated by one space ("g a").
	Sequence string
	// Line is the command line, run as if typed after ":".
	Line string
}

// mappingBindings are the mappings as bindings, so the matcher resolves their
// sequences and the help lists them.
func (model *Model) mappingBindings() []key.Binding {
	bindings := make([]key.Binding, 0, len(model.mappings))
	for _, mapping := range model.mappings {
		bindings = append(bindings, key.NewBinding(key.WithKeys(mapping.Sequence), key.WithHelp(mapping.Sequence, ":"+mapping.Line)))
	}
	return bindings
}

// mappedCommand runs the line mapped to a resolved sequence. A sequence strig
// binds itself, globally or in the view on top, keeps that meaning there: a
// mapping never hides a built-in key, it only adds to the views where the
// key is free.
func (model *Model) mappedCommand(pressed keys.Message) (tea.Cmd, bool) {
	for _, binding := range model.builtinBindings() {
		if key.Matches(pressed, binding) {
			return nil, false
		}
	}
	for _, mapping := range model.mappings {
		if mapping.Sequence == pressed.Sequence {
			return parseCommand(mapping.Line, model.top(), model.viewCommands), true
		}
	}
	return nil, false
}
