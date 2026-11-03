package userinterface

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

// commandDefinition is one entry in the command registry.
type commandDefinition struct {
	names []string // the command and its aliases, without the leading ":"
	run   func(arguments string) tea.Cmd
}

// commandTable lists the commands the command line understands. Add new
// commands here; parsing and dispatch need no other change.
func commandTable() []commandDefinition {
	return []commandDefinition{
		{names: []string{"q", "quit"}, run: func(string) tea.Cmd { return Pop() }},
		{names: []string{"qa", "qall", "quitall"}, run: func(string) tea.Cmd { return Quit() }},
		{names: []string{"set"}, run: setCommand},
		{names: []string{"theme"}, run: themeCommand},
	}
}

// CommandNames lists the commands the shell itself handles, so the wiring can
// check that no view command shadows one.
func CommandNames() []string {
	var names []string
	for _, definition := range commandTable() {
		names = append(names, definition.names...)
	}
	return names
}

// parseCommand turns a command line (without the ":") into a command. An
// empty line does nothing. The static table is consulted first; then top, if
// it is a Commander and the name is in viewCommands (the names every view
// declares, from Options.ViewCommands). A name nobody handles yields an error
// message.
func parseCommand(line string, top View, viewCommands []string) tea.Cmd {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	name, arguments, _ := strings.Cut(line, " ")
	arguments = strings.TrimSpace(arguments)
	for _, definition := range commandTable() {
		for _, alias := range definition.names {
			if alias == name {
				return definition.run(arguments)
			}
		}
	}
	if slices.Contains(viewCommands, name) {
		if commander, ok := top.(Commander); ok {
			if command, handled := commander.Command(name, arguments); handled {
				return command
			}
		}
		return Error(fmt.Errorf("not available here: %s", name))
	}
	return Error(fmt.Errorf("not a command: %s", name))
}

// commandLine is the ":" prompt drawn in the status bar row. It is the
// reference pattern for key-capturing input: while active it reports
// CapturesKeys, handles esc by releasing capture, and the root forwards it
// every key.
type commandLine struct {
	input  textinput.Model
	active bool
	// prompt is the prefix of the view prompt the line is serving (see
	// prompt.go), or empty when it takes a ":" command.
	prompt string
	// completion is the open match list of Tab (see completion.go), or nil.
	completion *completion
}

var _ KeyCapturer = (*commandLine)(nil)

func newCommandLine(activeTheme theme.Theme) commandLine {
	in := textinput.New()
	in.Prompt = ":"
	currentCommandLine := commandLine{input: in}
	currentCommandLine.setTheme(activeTheme)
	return currentCommandLine
}

// setTheme styles the input from the theme's command-line styles, so the
// typed text is readable on light and dark backgrounds alike.
func (commandLine *commandLine) setTheme(activeTheme theme.Theme) {
	state := textinput.StyleState{
		Text:        activeTheme.CommandText,
		Prompt:      activeTheme.CommandPrompt,
		Placeholder: activeTheme.CommandHint,
		Suggestion:  activeTheme.CommandHint,
	}
	commandLine.input.SetStyles(textinput.Styles{
		Focused: state,
		Blurred: state,
		Cursor:  textinput.CursorStyle{Color: activeTheme.CommandText.GetForeground(), Shape: tea.CursorBlock, Blink: true},
	})
}

// CapturesKeys implements KeyCapturer.
func (commandLine *commandLine) CapturesKeys() bool { return commandLine.active }

func (commandLine *commandLine) open() tea.Cmd {
	commandLine.input.Reset()
	commandLine.completion = nil
	commandLine.active = true
	return commandLine.input.Focus()
}

func (commandLine *commandLine) close() {
	commandLine.active = false
	commandLine.completion = nil
	commandLine.prompt, commandLine.input.Prompt = "", ":"
	commandLine.input.Blur()
	commandLine.input.Reset()
}

// handleKey processes one key press. esc cancels, enter submits, and
// backspace on an empty line cancels, as in vim. When the line is submitted
// the text is returned with ok true.
func (commandLine *commandLine) handleKey(message tea.KeyPressMsg) (line string, ok bool, command tea.Cmd) {
	// Any key but Tab and shift+tab (the root takes those) accepts the match
	// shown and closes the list.
	commandLine.completion = nil
	switch message.String() {
	case "esc":
		commandLine.close()
	case "enter":
		line = commandLine.input.Value()
		commandLine.close()
		return line, true, nil
	case "backspace":
		if commandLine.input.Value() == "" {
			commandLine.close()
			return "", false, nil
		}
		fallthrough
	default:
		commandLine.input, command = commandLine.input.Update(message)
	}
	return "", false, command
}

// update forwards non-key messages (cursor blink) to the input.
func (commandLine *commandLine) update(message tea.Msg) tea.Cmd {
	var command tea.Cmd
	commandLine.input, command = commandLine.input.Update(message)
	return command
}
