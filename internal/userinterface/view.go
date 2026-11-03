// Package userinterface is the app shell: the root Bubble Tea model, the view stack,
// navigation and status messages, the status bar, and the help overlay.
//
// Screens implement View and are composed by the root Model. Views never
// touch the stack; they return commands built by Push, Pop, Quit, Info and Error, and the root acts on the resulting messages.
package userinterface

import (
	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
)

// View is a screen held by the root model's view stack.
//
// The root forwards to the active (top) view:
//   - keys.Message for key presses and sequences that match a binding active in
//     the view (the global bindings plus anything in FullHelp). Test with
//     key.Matches(msg, binding).
//   - raw tea.KeyPressMsg for keys that match nothing, and for every key
//     while the view implements KeyCapturer and is capturing.
//   - tea.WindowSizeMsg with the area the view may draw in (the terminal
//     minus the status bar), on push and on every resize. Views below the top
//     of the stack get it too.
//   - ThemeMessage when the theme changes.
//   - every other message (command results, spinner and blink ticks, ...) is
//     broadcast to EVERY view in the stack, so a result is never lost because
//     another view was pushed meanwhile. Views must therefore ignore messages
//     that are not theirs: result messages carry an identity (repo, PR number,
//     request sequence number; see CLAUDE.md), and bubbles components already
//     filter their own ticks by ID.
//
// Embedded bubbles components (viewport, list, ...) are driven through their
// methods in response to keys.Message (viewport.ScrollDown, viewport.HalfPageDown, viewport.GotoTop,
// ...). Do not pass a keys.Message into a component's Update: those match only
// tea.KeyPressMsg and would silently do nothing.
//
// The root handles Help (?) and the command line (:) itself and never
// forwards them; Cancel (esc) is always forwarded. esc never navigates: it
// only cancels or clears state inside the view. Leaving a view is ":q".
//
// ShortHelp and FullHelp come from help.KeyMap. List only bindings specific
// to the view; the global ones are added by the root. FullHelp doubles as the
// registry of the view's multi-key sequences (bindings with keys like "g f"),
// so every binding the view reacts to must appear there, even if ShortHelp
// leaves it out.
type View interface {
	// Init returns the command to run when the view is pushed, or nil.
	Init() tea.Cmd
	// Update handles a message and returns the updated view. It must not do I/O.
	Update(message tea.Msg) (View, tea.Cmd)
	// Render draws the view in at most width columns and height rows. The
	// root clips anything beyond that.
	Render(width, height int) string
	// Title is the short name shown in the status bar breadcrumbs.
	Title() string

	help.KeyMap
}

// KeyCapturer is optionally implemented by views with a focused text input.
// While CapturesKeys returns true the root forwards every key as a raw
// tea.KeyPressMsg without interpreting it.
//
// There is no ctrl+c escape hatch, so a capturer MUST release capture when it
// receives esc. Every capturer is covered by TestCapturersReleaseOnEsc; add
// new ones to its table. The root's command line is the reference pattern.
type KeyCapturer interface {
	CapturesKeys() bool
}

// Loader is optionally implemented by views that load data. The root shows
// the status bar spinner while the active (top) view reports Loading, so the
// view owns its loading state and popping it ends its spinner.
type Loader interface {
	Loading() bool
}

// Commander is optionally implemented by views that own commands of their
// own (":approve" in the PR workspace). The root consults only the top view,
// and only when the static command table has no match, so a command never
// reaches two views the way a broadcast result would. Command returns the
// command to run and true when it recognizes name; false means "not available
// here" and the status bar says so. args is the rest of the line, trimmed.
type Commander interface {
	Command(name, arguments string) (command tea.Cmd, handled bool)
}

// CommandLister is optionally implemented by a Commander to say which commands
// it takes, in the order Tab on the command line offers them. Only the top view
// is asked, so a command another view owns is not offered. It must list the
// names Command handles.
type CommandLister interface {
	CommandNames() []string
}

// Resumer is optionally implemented by views that want to know they are
// visible again after the view above them was popped (for example to refresh).
type Resumer interface {
	Resumed() tea.Cmd
}
