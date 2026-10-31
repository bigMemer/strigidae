package workspace

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/diff"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/components/diffview"
	"github.com/MikePors/strigidae/internal/userinterface/keys"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

// DiffState is the workspace's diff and where loading it stands.
type DiffState struct {
	// Raw is the unified diff of the whole pull request. On a refresh it keeps
	// the previous text while Loading is set, and after a failed refresh. A
	// diff too large for GitHub to give whole is built file by file from the
	// file list; Raw is then a text of the file list's patches, which still
	// changes whenever the diff does.
	Raw string
	// Parsed is Raw parsed by internal/diff, done in the load command. It is nil
	// until a load gets as far as parsing. When ParseError is set it holds the
	// files parsed before the problem (for a diff built file by file, every
	// file, the one with the bad patch holding the hunks before it): look files
	// up in it as usual and treat a missing one as "no diff available".
	Parsed *diff.Diff
	// ParseError is why Raw could not be parsed completely. The workspace reports
	// it once on the status bar; Parsed is still usable.
	ParseError error
	// Err is why the diff could not be loaded at all, for example
	// github.ErrDiffTooLarge. Raw and Parsed are empty then. A failed refresh
	// does not set it: the previous diff is kept instead.
	Err error
	// Loading is set while a (re)load is in flight.
	Loading bool
}

// FilePane is the content of the right pane while a file is selected. The
// diff component plugs in here through WithFilePane.
//
// Contract notes for implementers:
//   - SetDiff may be called again with an identical Raw (once when a load
//     starts, with Loading set, once when it ends, and on every refresh). Cache
//     parsing by Raw. A failed refresh keeps the previous Raw and Err stays nil.
//   - SetFile is called when the selected file differs from the last one
//     given (a different path, or the same path with new metadata after a
//     refresh); an unchanged file is not repeated. A repeat call for the same
//     path means only the metadata changed: keep cursor and scroll, and
//     re-resolve the file against the newest SetDiff.
//   - ChangedFile.Path is the new path, or the old path for a deleted file:
//     look files up by new path and fall back to the old one.
//   - SetSize is called whenever the pane's inner size changes (and before the
//     first Render), so Update can use it for page scrolling.
//
// The workspace calls SetTheme, SetDiff, SetFile and SetSize outside of rendering,
// forwards every keys.Message it does not handle itself (movement, scrolling and
// the pane's own sequences) to Update while the right pane has focus, and
// calls Render with the pane's inner size. FullHelp must list every binding
// Update reacts to, so multi-key sequences are recognised.
type FilePane interface {
	// SetTheme is called on construction and whenever the theme changes.
	SetTheme(activeTheme theme.Theme)
	// SetWrap turns wrapping of long lines on or off (the wrap option). Panes
	// that never wrap ignore it.
	SetWrap(wrap bool)
	// SetHideResolved leaves resolved review threads out, or shows them again
	// (the hideresolved option). Panes without threads ignore it.
	SetHideResolved(hide bool)
	// SetSkipResolved makes the thread keys pass over resolved threads (the
	// skipresolved option). Panes without threads ignore it.
	SetSkipResolved(skip bool)
	// SetRelativeNumber shows or hides row numbers counted from the cursor
	// (the relativenumber option). Panes without a gutter ignore it.
	SetRelativeNumber(on bool)
	// SetDiff is called when a diff load starts and when it finishes.
	SetDiff(diffState DiffState)
	// SetThreads is called when the pull request's review threads load or
	// reload; the pane shows the ones on its file.
	SetThreads(threads []github.ReviewThread)
	// SetThreadExpanded folds the thread with the given identifier open or
	// closed, replacing whatever the user chose before; it is called after a
	// thread was resolved or unresolved, together with SetThreads.
	SetThreadExpanded(identifier string, expanded bool)
	// SetFile is called when the selected file changes, and again when the
	// same path comes back with different metadata (change type, counts)
	// after a refresh. A repeat call for the same path keeps cursor and scroll.
	SetFile(file github.ChangedFile)
	// SetSize gives the pane's inner size; it is called whenever it changes.
	SetSize(width, height int)
	// Context returns what is at the pane's cursor: the threads and comment
	// there, and the lines a new comment would go on. Panes without threads
	// or lines return the zero value.
	Context() diffview.Context
	// GotoLine moves the cursor to line number line of side, or the closest
	// shown line of that side. Panes without lines ignore it.
	GotoLine(side github.DiffSide, line int)
	// VisualStart begins a line selection at the cursor, reporting false when
	// it cannot (no diff line under the cursor).
	VisualStart() bool
	// VisualActive reports whether a line selection is in progress.
	VisualActive() bool
	// VisualMove extends the selection by delta lines; a refused move (outside
	// one hunk or one side) leaves it unchanged and returns the reason.
	VisualMove(delta int) error
	// VisualEnd leaves the line selection.
	VisualEnd()
	// Update handles a key while the right pane has focus.
	Update(message keys.Message) tea.Cmd
	// Render draws the pane in exactly width columns and at most height rows.
	Render(width, height int) string
	// FullHelp lists the pane's bindings for the help overlay and sequence matching.
	FullHelp() [][]key.Binding
	// SetHunkReviewed is called once with how to tell whether a hunk is
	// marked reviewed; panes that show hunks mark their headers.
	SetHunkReviewed(reviewed func(file *diff.File, hunk int) bool)
	// SetHunkHiding is called once with how to tell whether a hunk is hidden
	// and how far it has faded (0 to theme.FadeSteps); panes that show hunks
	// leave hidden ones out and draw fading ones fainter.
	SetHunkHiding(hidden func(file *diff.File, hunk int) bool, fade func(file *diff.File, hunk int) int)
	// HiddenHunksChanged says the hidden function's answers changed.
	HiddenHunksChanged()
	// SetThreadFade is called once with how to tell whether a review thread
	// is fading out and how far (0 to theme.FadeSteps); panes with threads
	// keep a fading resolved one shown under SetHideResolved and draw it
	// fainter.
	SetThreadFade(fade func(identifier string) (step int, fading bool))
	// ThreadFadesChanged says a thread stopped fading.
	ThreadFadesChanged()
	// StartSearch begins a "/" search in the pane; TakeSearchRequest then
	// reports true once, so the workspace opens the prompt. SearchPrompt takes
	// the prompt's results and SearchStep serves n / N. ClearSearch drops the
	// search. SetHighlightSearch is the hlsearch option. Panes without
	// searchable rows may do nothing.
	StartSearch() tea.Cmd
	TakeSearchRequest() bool
	SearchPrompt(result userinterface.PromptResultMessage) tea.Cmd
	SearchStep(message keys.Message, forward, wrapScan bool) tea.Cmd
	ClearSearch()
	SetHighlightSearch(on bool)
}
