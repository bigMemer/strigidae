package workspace

import (
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/diff"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/components/diffview"
	"github.com/MikePors/strigidae/internal/userinterface/keys"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

// horizontalScrollStep is how many columns `z h` / `z l` scroll.
const horizontalScrollStep = 4

// diffBindings are the diff pane's own keys; the movement keys come from
// keys.Global.
type diffBindings struct {
	NextHunk, PreviousHunk          key.Binding
	NextThread, PreviousThread      key.Binding
	ToggleThread                    key.Binding
	OpenThread, CloseThread         key.Binding
	ScrollLeft, ScrollRight         key.Binding
	HalfScrollLeft, HalfScrollRight key.Binding
}

func defaultDiffBindings() diffBindings {
	bind := func(pressed, helpKey, description string) key.Binding {
		return key.NewBinding(key.WithKeys(pressed), key.WithHelp(helpKey, description))
	}
	return diffBindings{
		NextHunk:        bind("]", "]", "next hunk"),
		PreviousHunk:    bind("[", "[", "previous hunk"),
		NextThread:      bind(")", ")", "next comment thread"),
		PreviousThread:  bind("(", "(", "previous comment thread"),
		ToggleThread:    bind("z a", "za", "toggle comment thread (also enter on a marked line)"),
		OpenThread:      bind("z o", "zo", "open comment thread"),
		CloseThread:     bind("z c", "zc", "close comment thread"),
		ScrollLeft:      bind("z h", "zh", "scroll left"),
		ScrollRight:     bind("z l", "zl", "scroll right"),
		HalfScrollLeft:  bind("z H", "zH", "scroll half a screen left"),
		HalfScrollRight: bind("z L", "zL", "scroll half a screen right"),
	}
}

// actions names the diff pane's own bindings, in the namespace of the global
// ones (keys.Global.Actions).
func (paneBindings *diffBindings) actions() []keys.Action {
	return []keys.Action{
		{Name: "next-hunk", Binding: &paneBindings.NextHunk},
		{Name: "previous-hunk", Binding: &paneBindings.PreviousHunk},
		{Name: "next-thread", Binding: &paneBindings.NextThread},
		{Name: "previous-thread", Binding: &paneBindings.PreviousThread},
		{Name: "toggle-thread", Binding: &paneBindings.ToggleThread},
		{Name: "open-thread", Binding: &paneBindings.OpenThread},
		{Name: "close-thread", Binding: &paneBindings.CloseThread},
		{Name: "scroll-left", Binding: &paneBindings.ScrollLeft},
		{Name: "scroll-right", Binding: &paneBindings.ScrollRight},
		{Name: "half-scroll-left", Binding: &paneBindings.HalfScrollLeft},
		{Name: "half-scroll-right", Binding: &paneBindings.HalfScrollRight},
	}
}

// diffPane adapts diffview.Model to FilePane: it resolves the selected file in
// the parsed diff and maps keys to diffview's methods.
type diffPane struct {
	diffView   *diffview.Model
	globalKeys keys.Global
	bind       diffBindings

	state   DiffState
	file    github.ChangedFile
	hasFile bool
}

var _ FilePane = (*diffPane)(nil)

func newDiffPane(activeTheme theme.Theme, now func() time.Time, wrap bool) *diffPane {
	return &diffPane{diffView: diffview.New(activeTheme, diffview.WithClock(now), diffview.WithWrap(wrap)), globalKeys: keys.DefaultGlobal(), bind: defaultDiffBindings()}
}

// SetWrap turns wrapping of long diff lines on or off.
func (pane *diffPane) SetWrap(wrap bool) { pane.diffView.SetWrap(wrap) }

// SetHideResolved leaves resolved review threads out of the diff, or shows
// them again.
func (pane *diffPane) SetHideResolved(hide bool) { pane.diffView.SetHideResolved(hide) }

// SetSkipResolved makes `)` and `(` pass over resolved threads.
func (pane *diffPane) SetSkipResolved(skip bool) {
	pane.diffView.SetSkipResolved(skip)
}

// SetRelativeNumber shows or hides the diff's relative-number column.
func (pane *diffPane) SetRelativeNumber(on bool) { pane.diffView.SetRelativeNumber(on) }

func (pane *diffPane) SetTheme(activeTheme theme.Theme) { pane.diffView.SetTheme(activeTheme) }

func (pane *diffPane) SetHunkReviewed(reviewed func(file *diff.File, hunk int) bool) {
	pane.diffView.SetHunkReviewed(reviewed)
}

func (pane *diffPane) SetHunkHiding(hidden func(file *diff.File, hunk int) bool, fade func(file *diff.File, hunk int) int) {
	pane.diffView.SetHunkHidden(hidden)
	pane.diffView.SetHunkFade(fade)
}

func (pane *diffPane) HiddenHunksChanged() { pane.diffView.HiddenHunksChanged() }

func (pane *diffPane) SetThreadFade(fade func(identifier string) (step int, fading bool)) {
	pane.diffView.SetThreadFade(fade)
}

func (pane *diffPane) ThreadFadesChanged() { pane.diffView.ThreadFadesChanged() }

func (pane *diffPane) SetThreadExpanded(identifier string, expanded bool) {
	pane.diffView.SetThreadExpanded(identifier, expanded)
}

func (pane *diffPane) SetThreads(threads []github.ReviewThread) { pane.diffView.SetThreads(threads) }

func (pane *diffPane) SetSize(width, height int) { pane.diffView.SetSize(width, height) }

func (pane *diffPane) SetDiff(diffState DiffState) {
	pane.state = diffState
	pane.show()
}

func (pane *diffPane) SetFile(file github.ChangedFile) {
	if !pane.hasFile || pane.file.Path != file.Path {
		// Another file, even one that shows only a message: the query
		// belonged to the last one and must not come back with it.
		pane.diffView.ClearSearch()
	}
	pane.file, pane.hasFile = file, true
	pane.show()
}

// show points the diff component at what the pane should display now.
// diffview ignores a repeated *diff.File and keeps its position when the same
// path is re-parsed, so calling this on every change is cheap and safe.
func (pane *diffPane) show() {
	switch {
	case pane.state.Err != nil:
		pane.diffView.SetDiffError(pane.state.Err)
	case pane.state.Parsed == nil:
		pane.diffView.SetMessage("Loading diff…")
	case !pane.hasFile:
		pane.diffView.SetFile(nil)
	default:
		file := pane.state.Parsed.File(pane.file.Path)
		if file == nil {
			message := "No diff available for this file."
			if pane.state.ParseError != nil {
				message += "\nThe diff could not be fully parsed: " + pane.state.ParseError.Error()
			}
			pane.diffView.SetMessage(message)
			return
		}
		pane.diffView.SetFile(file)
	}
}

func (pane *diffPane) Update(message keys.Message) tea.Cmd {
	diffView := pane.diffView
	switch {
	case key.Matches(message, pane.globalKeys.Down):
		diffView.CursorDown(message.Repeat())
	case key.Matches(message, pane.globalKeys.Up):
		diffView.CursorUp(message.Repeat())
	case key.Matches(message, pane.globalKeys.Top):
		diffView.GotoTop()
	case key.Matches(message, pane.globalKeys.Bottom):
		if message.Count > 0 {
			diffView.GotoLine(message.Count)
		} else {
			diffView.GotoBottom()
		}
	case key.Matches(message, pane.globalKeys.HalfDown):
		message.Each(diffView.CursorRow, diffView.HalfPageDown)
	case key.Matches(message, pane.globalKeys.HalfUp):
		message.Each(diffView.CursorRow, diffView.HalfPageUp)
	case key.Matches(message, pane.globalKeys.PageDown):
		message.Each(diffView.CursorRow, diffView.PageDown)
	case key.Matches(message, pane.globalKeys.PageUp):
		message.Each(diffView.CursorRow, diffView.PageUp)
	case key.Matches(message, pane.globalKeys.ScrollCursorTop):
		diffView.ScrollCursorTop()
	case key.Matches(message, pane.globalKeys.ScrollCursorMiddle):
		diffView.ScrollCursorMiddle()
	case key.Matches(message, pane.globalKeys.ScrollCursorBottom):
		diffView.ScrollCursorBottom()
	case key.Matches(message, pane.bind.NextHunk):
		message.Each(diffView.CursorRow, diffView.NextHunk)
	case key.Matches(message, pane.bind.PreviousHunk):
		message.Each(diffView.CursorRow, diffView.PreviousHunk)
	case key.Matches(message, pane.bind.NextThread):
		return pane.moveThread(message, true)
	case key.Matches(message, pane.bind.PreviousThread):
		return pane.moveThread(message, false)
	case key.Matches(message, pane.bind.ToggleThread), key.Matches(message, pane.globalKeys.Select):
		diffView.ToggleThread()
	case key.Matches(message, pane.bind.OpenThread):
		diffView.OpenThread()
	case key.Matches(message, pane.bind.CloseThread):
		diffView.CloseThread()
	case key.Matches(message, pane.bind.ScrollLeft):
		diffView.ScrollLeft(message.Repeat() * horizontalScrollStep)
	case key.Matches(message, pane.bind.ScrollRight):
		diffView.ScrollRight(message.Repeat() * horizontalScrollStep)
	case key.Matches(message, pane.bind.HalfScrollLeft):
		diffView.HalfScrollLeft()
	case key.Matches(message, pane.bind.HalfScrollRight):
		diffView.HalfScrollRight()
	}
	return nil
}

// moveThread applies a thread key. Under skipresolved a key with no
// unresolved thread to land on in its direction says so and leaves the cursor.
func (pane *diffPane) moveThread(message keys.Message, forward bool) tea.Cmd {
	if pane.diffView.NoThreadToVisit(forward) {
		return userinterface.Info("no unresolved threads")
	}
	if forward {
		message.Each(pane.diffView.CursorRow, pane.diffView.NextThread)
	} else {
		message.Each(pane.diffView.CursorRow, pane.diffView.PreviousThread)
	}
	return nil
}

func (pane *diffPane) StartSearch() tea.Cmd    { return pane.diffView.StartSearch() }
func (pane *diffPane) TakeSearchRequest() bool { return pane.diffView.TakeSearchRequest() }
func (pane *diffPane) SearchPrompt(result userinterface.PromptResultMessage) tea.Cmd {
	return pane.diffView.SearchPrompt(result)
}

func (pane *diffPane) SearchStep(message keys.Message, forward, wrapScan bool) tea.Cmd {
	return pane.diffView.SearchStep(message, forward, wrapScan)
}
func (pane *diffPane) ClearSearch()               { pane.diffView.ClearSearch() }
func (pane *diffPane) SetHighlightSearch(on bool) { pane.diffView.SetHighlightSearch(on) }

func (pane *diffPane) Context() diffview.Context { return pane.diffView.Context() }

func (pane *diffPane) GotoLine(side github.DiffSide, line int) {
	pane.diffView.GotoSideLine(side, line)
}

func (pane *diffPane) SetSpan(span diffview.Range) { pane.diffView.SetSpan(span) }

func (pane *diffPane) VisualStart() bool          { return pane.diffView.VisualStart() }
func (pane *diffPane) VisualActive() bool         { return pane.diffView.VisualActive() }
func (pane *diffPane) VisualMove(delta int) error { return pane.diffView.VisualMove(delta) }
func (pane *diffPane) VisualEnd()                 { pane.diffView.VisualEnd() }

func (pane *diffPane) Render(width, height int) string { return pane.diffView.Render(width, height) }

func (pane *diffPane) FullHelp() [][]key.Binding {
	bindings := pane.bind
	return [][]key.Binding{{bindings.NextHunk, bindings.PreviousHunk, bindings.NextThread, bindings.PreviousThread}, {bindings.ToggleThread, bindings.OpenThread, bindings.CloseThread}, {bindings.ScrollLeft, bindings.ScrollRight, bindings.HalfScrollLeft, bindings.HalfScrollRight}}
}
