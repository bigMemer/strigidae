package workspace

import (
	"fmt"
	"io"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/cli/go-gh/v2/pkg/browser"

	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/keys"
	"github.com/MikePors/strigidae/internal/userinterface/terminaltext"
)

// bindings are the keys specific to the workspace. Movement keys come from
// keys.Global.
type bindings struct {
	PaneLeft, PaneRight          key.Binding
	NextFile, PreviousFile       key.Binding
	FilePicker                   key.Binding
	NextSection, PreviousSection key.Binding
	Comment, OverviewComment     key.Binding
	Reply                        key.Binding
	Viewed                       key.Binding
	ReviewHunk                   key.Binding
	ResolveThread                key.Binding
	// Inline comments: V starts a line selection, e and D change your comment
	// at the cursor. The Visual* keys apply while a selection is open.
	Visual, EditComment, DeleteComment key.Binding
	VisualDown, VisualUp, VisualExit   key.Binding
}

func defaultBindings() bindings {
	bind := func(pressed, helpKey, description string) key.Binding {
		return key.NewBinding(key.WithKeys(pressed), key.WithHelp(helpKey, description))
	}
	return bindings{
		PaneLeft:        bind("h", "h", "focus files pane"),
		PaneRight:       bind("l", "l", "focus right pane"),
		NextFile:        bind("}", "}", "next file"),
		PreviousFile:    bind("{", "{", "previous file"),
		FilePicker:      bind("f", "f", "file picker"),
		NextSection:     bind("]", "]", "next section (overview)"),
		PreviousSection: bind("[", "[", "previous section (overview)"),
		Comment:         bind("c", "c", "new comment on the diff line / selection (lines only)"),
		OverviewComment: bind("C", "C", "new top-level comment on the pull request"),
		Reply:           bind("r", "r", "reply to the comment thread at the cursor (diff pane)"),
		ResolveThread:   bind("x", "x", "resolve or unresolve the comment thread at the cursor (diff pane; also :resolve, :unresolve)"),
		Viewed:          bind("v", "v", "toggle file viewed"),
		ReviewHunk:      bind("m", "m", "toggle hunk reviewed at the diff cursor (kept by strig until it quits)"),

		Visual:        bind("V", "V", "select diff lines for a multi-line comment"),
		EditComment:   bind("e", "e", "edit the comment of yours under the cursor (diff pane; also :edit-comment)"),
		DeleteComment: bind("D", "D", "delete the comment of yours under the cursor (diff pane; also :delete-comment)"),
		VisualDown:    key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j", "extend selection down")),
		VisualUp:      key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k", "extend selection up")),
		VisualExit:    key.NewBinding(key.WithKeys("esc", "V"), key.WithHelp("esc", "leave visual mode")),
	}
}

func openInBrowser(url string) error {
	return browser.New("", io.Discard, io.Discard).Browse(url)
}

func (view *View) handleKey(message keys.Message) tea.Cmd {
	if chosen, ok := view.keyEntry(message); ok {
		return view.invoke(chosen, invocation{message: message})
	}
	filesFocused := view.focus == paneFiles && !view.lensOpen()
	switch {
	case view.diffSearchFocused() && key.Matches(message, view.globalKeys.Search):
		view.searchPane = paneRight
		return view.filePane.StartSearch()
	case view.diffSearchFocused() && key.Matches(message, view.globalKeys.NextMatch):
		return view.filePane.SearchStep(message, true, view.options.WrapScan())
	case view.diffSearchFocused() && key.Matches(message, view.globalKeys.PreviousMatch):
		return view.filePane.SearchStep(message, false, view.options.WrapScan())
	case view.diffSearchFocused() && key.Matches(message, view.globalKeys.Cancel):
		view.filePane.ClearSearch()
	case filesFocused && key.Matches(message, view.globalKeys.Search):
		view.searchPane = paneFiles
		return view.search.Start(view.searchList())
	case filesFocused && key.Matches(message, view.globalKeys.NextMatch):
		return view.search.Step(message, true, view.searchList(), view.options.WrapScan())
	case filesFocused && key.Matches(message, view.globalKeys.PreviousMatch):
		return view.search.Step(message, false, view.searchList(), view.options.WrapScan())
	case filesFocused && key.Matches(message, view.globalKeys.Cancel):
		view.search.Clear()
	case key.Matches(message, view.globalKeys.Open):
		return view.openBrowser()
	case key.Matches(message, view.globalKeys.Refresh):
		return view.load()
	case view.selection() > 0 && view.diffNavigation(message):
		// Reserved globally: hunk and thread keys work from either pane.
		return view.filePane.Update(message)
	case view.focus == paneFiles && view.lensOpen():
		view.lensKey(message)
	case view.focus == paneFiles:
		view.filesKey(message)
	default:
		return view.rightKey(message)
	}
	view.layout()
	return nil
}

// diffSearchFocused reports whether the search keys act on the diff pane: it
// has focus and a file is shown.
func (view *View) diffSearchFocused() bool { return view.focus == paneRight && view.selection() > 0 }

// stepFile moves to the next or previous file. It never lands on Overview.
func (view *View) stepFile(delta int) {
	if view.selection() == 0 && delta < 0 {
		return
	}
	// Hidden files are skipped: count along the shown file entries.
	var before, after []int
	for _, entry := range view.rows[1:] {
		if entry < view.selection() {
			before = append(before, entry)
		} else if entry > view.selection() {
			after = append(after, entry)
		}
	}
	switch {
	case delta > 0 && len(after) > 0:
		view.selectEntry(after[min(delta, len(after))-1])
	case delta < 0 && len(before) > 0:
		view.selectEntry(before[len(before)-min(-delta, len(before))])
	}
}

func (view *View) openBrowser() tea.Cmd {
	if view.detail == nil || view.detail.URL == "" {
		return userinterface.Info("pull request not loaded yet")
	}
	return view.openAddress(view.detail.URL)
}

// openAddress opens url in the browser through the injected opener.
func (view *View) openAddress(url string) tea.Cmd {
	open := view.open
	return func() tea.Msg {
		if err := open(url); err != nil {
			return userinterface.ErrorMessage{Err: terminaltext.SafeError(fmt.Errorf("open %s in browser: %w", terminaltext.Clean(url), err))}
		}
		return nil
	}
}

// filesKey moves the selection in the Files pane.
func (view *View) filesKey(message keys.Message) {
	previous := view.window.Cursor()
	switch {
	case key.Matches(message, view.globalKeys.Down):
		view.window.Down(message.Repeat())
	case key.Matches(message, view.globalKeys.Up):
		view.window.Up(message.Repeat())
	case key.Matches(message, view.globalKeys.Top):
		view.window.GotoTop()
	case key.Matches(message, view.globalKeys.Bottom):
		if message.Count > 0 {
			view.window.GotoRow(message.Count - 1)
		} else {
			view.window.GotoBottom()
		}
	case key.Matches(message, view.globalKeys.HalfDown):
		message.Each(view.window.Cursor, view.window.HalfPageDown)
	case key.Matches(message, view.globalKeys.HalfUp):
		message.Each(view.window.Cursor, view.window.HalfPageUp)
	case key.Matches(message, view.globalKeys.PageDown):
		message.Each(view.window.Cursor, view.window.PageDown)
	case key.Matches(message, view.globalKeys.PageUp):
		message.Each(view.window.Cursor, view.window.PageUp)
	case key.Matches(message, view.globalKeys.ScrollCursorTop):
		view.window.ScrollCursorTop()
	case key.Matches(message, view.globalKeys.ScrollCursorMiddle):
		view.window.ScrollCursorMiddle()
	case key.Matches(message, view.globalKeys.ScrollCursorBottom):
		view.window.ScrollCursorBottom()
	case key.Matches(message, view.globalKeys.Select):
		view.focus = paneRight
	}
	if view.window.Cursor() != previous {
		view.cursorMoved()
	}
}

// rightKey scrolls the Overview, or hands the key to the file pane.
func (view *View) rightKey(message keys.Message) tea.Cmd {
	if view.selection() > 0 {
		return view.filePane.Update(message)
	}
	viewport := &view.overview.viewport
	switch {
	case key.Matches(message, view.globalKeys.Down):
		viewport.ScrollDown(message.Repeat())
	case key.Matches(message, view.globalKeys.Up):
		viewport.ScrollUp(message.Repeat())
	case key.Matches(message, view.globalKeys.Top):
		viewport.GotoTop()
	case key.Matches(message, view.globalKeys.Bottom):
		if message.Count > 0 {
			viewport.SetYOffset(message.Count - 1)
		} else {
			viewport.GotoBottom()
		}
	case key.Matches(message, view.globalKeys.HalfDown):
		message.Each(viewport.YOffset, viewport.HalfPageDown)
	case key.Matches(message, view.globalKeys.HalfUp):
		message.Each(viewport.YOffset, viewport.HalfPageUp)
	case key.Matches(message, view.globalKeys.PageDown):
		message.Each(viewport.YOffset, viewport.PageDown)
	case key.Matches(message, view.globalKeys.PageUp):
		message.Each(viewport.YOffset, viewport.PageUp)
	}
	return nil
}

// jumpSection moves the Overview to the next or previous section. ] and [ are
// sections only on the Overview; on a file they belong to the diff pane.
func (view *View) jumpSection(delta int) {
	if view.detail == nil {
		return
	}
	view.overview.jump(delta)
}

// diffNavigation reports whether message is a hunk or thread movement key.
func (view *View) diffNavigation(message keys.Message) bool {
	bindings := view.navigationBind
	return key.Matches(message, bindings.NextHunk, bindings.PreviousHunk, bindings.NextThread, bindings.PreviousThread)
}

// ActionNames lists the action names of the workspace and its diff pane, for
// the check that views don't reuse each other's names by accident.
func ActionNames() []string {
	workspaceBindings, paneBindings := defaultBindings(), defaultDiffBindings()
	var names []string
	for _, action := range append(workspaceBindings.actions(), paneBindings.actions()...) {
		names = append(names, action.Name)
	}
	return names
}
