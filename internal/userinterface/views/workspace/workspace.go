// Package workspace is the PR workspace: one screen per pull request made of
// tiled panes. The left Files pane lists an Overview entry and the changed
// files; the right pane shows the PR description and conversation, or the
// selected file's diff.
package workspace

import (
	"fmt"
	"os"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/clipboard"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/options"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/components/markdown"
	"github.com/MikePors/strigidae/internal/userinterface/components/picker"
	"github.com/MikePors/strigidae/internal/userinterface/components/scroll"
	"github.com/MikePors/strigidae/internal/userinterface/components/search"
	"github.com/MikePors/strigidae/internal/userinterface/keys"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

// pane identifies which pane has focus.
type pane int

const (
	paneFiles pane = iota
	paneRight
)

// Option configures a View.
type Option func(*View)

// WithClock injects the clock used for relative times.
func WithClock(now func() time.Time) Option { return func(view *View) { view.now = now } }

// WithTheme sets the initial theme. Without it the default dark theme is used
// until the root sends a userinterface.ThemeMessage; a view that pushes a workspace should
// pass its own current theme.
func WithTheme(activeTheme theme.Theme) Option { return func(view *View) { view.theme = activeTheme } }

// WithOpener replaces how `o` opens the pull request URL, which by default is
// the user's browser.
func WithOpener(open func(url string) error) Option { return func(view *View) { view.open = open } }

// WithTimeouts sets the deadlines of the GitHub calls; zero fields select the defaults.
func WithTimeouts(timeouts userinterface.Timeouts) Option {
	return func(view *View) { view.timeouts = timeouts }
}

// WithClipboard sets the system clipboard tool that copy commands use besides
// OSC 52. Without it only OSC 52 is sent.
func WithClipboard(copier clipboard.Copier) Option {
	return func(view *View) { view.clipboard = copier }
}

// Clipboard returns the clipboard tool the view copies with, nil for OSC 52 only.
func (view *View) Clipboard() clipboard.Copier { return view.clipboard }

// WithWrap makes the diff pane wrap long lines instead of cutting them off.
func WithWrap(wrap bool) Option { return func(view *View) { view.wrap = wrap } }

// Wrap reports whether the diff pane wraps long lines.
func (view *View) Wrap() bool { return view.wrap }

// WithTick replaces how the fade of a viewed file is timed, which by default
// is tea.Tick.
func WithTick(tick userinterface.TickFunction) Option { return func(view *View) { view.tick = tick } }

// WithFilePane replaces the right pane's content for a selected file; this is
// the hook for the diff component.
func WithFilePane(pane FilePane) Option { return func(view *View) { view.filePane = pane } }

// View is the PR workspace screen.
type View struct {
	client     github.Client
	timeouts   userinterface.Timeouts
	repository github.Repository
	number     int

	now              func() time.Time
	tick             userinterface.TickFunction
	open             func(url string) error
	theme            theme.Theme
	globalKeys       keys.Global
	bind             bindings
	navigationBind   diffBindings // hunk/thread keys, routed to the file pane from either focus
	markdownRenderer *markdown.Renderer
	filePane         FilePane
	edit             EditFunction
	clipboard        clipboard.Copier // nil: OSC 52 only

	sequence           int // latest load request; older results are stale
	pullRequestPending bool
	diffPending        bool
	threadPending      bool
	threadGeneration   int                   // latest thread load or in-place change; older thread loads are stale
	threadLoaded       bool                  // a thread load has succeeded
	threadErr          error                 // why the last thread load failed; cleared when a load starts
	threads            []github.ReviewThread // sorted by file and line
	detail             *github.PullRequestDetail
	diff               DiffState
	diffHead           string // head SHA the diff on screen was loaded for; "" if unknown
	err                error  // load failure while there is nothing to show
	notFound           bool

	commentSequence int // latest compose request; older results are stale
	// writes counts the comment writes in flight, from the start of a write
	// until its result (or its question) arrives; reviews and merges wait for
	// it, and no second write starts meanwhile.
	writes int
	drafts map[string]string // unposted bodies, by thread ID or topLevelDraft

	viewedState map[string]*viewedToggle // by path; see viewedToggle

	wrap             bool       // the diff pane wraps long lines
	hunkMarks        *HunkMarks // hunks marked reviewed; shared with other workspaces by WithHunkMarks
	questions        *Questions // questions asked with :ask; nil when asking is off
	analyses         *Analyses  // review analyses loaded; shared with other workspaces by WithAnalyses
	readFile         func(path string) ([]byte, error)
	lens             *lensPane        // the lens shown in place of the Files list; nil when none is open
	hunkFingerprints fingerprintCache // fingerprints of the last file asked about

	window  scroll.Window  // Files pane: over rows
	rows    []int          // the entry each Files pane row shows; see hideviewed.go
	current int            // the entry the right pane shows
	fades   fader          // viewed files about to leave the Files pane
	search  search.State   // over the same rows; Overview never matches
	options options.Values // the `:set` options, sent by the root
	focus   pane
	// searchPane is the pane the search being typed belongs to; the prompt's
	// results go to it.
	searchPane pane
	overview   overview
	picker     *picker.Model // non-nil while the file picker is open
	review     reviewFlow    // submitting a review (see review.go)
	merge      mergeFlow     // merging the pull request (see merge.go)
	// pickerItems are the paths the picker was built from; a choice is
	// resolved by path because a refresh may replace the file list meanwhile.
	pickerItems []string
	pickerPaths []string // raw paths, parallel to pickerItems
	// pickerChecks is non-nil while the picker lists checks (:checks) rather
	// than files; it is the sorted list the picker's rows came from.
	pickerChecks []github.Check
	// pickerReaction is non-nil while the picker lists the 8 reactions (:react).
	pickerReaction *reactionTarget

	paneFile   github.ChangedFile // last value given to filePane.SetFile
	paneWidth  int                // size last given to filePane.SetSize
	paneHeight int

	width, height int
}

var (
	_ userinterface.View        = (*View)(nil)
	_ userinterface.Loader      = (*View)(nil)
	_ userinterface.KeyCapturer = (*View)(nil)
	_ userinterface.Commander   = (*View)(nil)
	_ userinterface.Prompter    = (*View)(nil)
)

// New returns the workspace for pull request number in repository.
func New(client github.Client, repository github.Repository, number int, options ...Option) *View {
	view := &View{
		client:         client,
		repository:     repository,
		number:         number,
		now:            time.Now,
		tick:           tea.Tick,
		rows:           []int{0},
		open:           openInBrowser,
		theme:          theme.Default(),
		globalKeys:     keys.DefaultGlobal(),
		bind:           defaultBindings(),
		navigationBind: defaultDiffBindings(),
		edit:           execEditor,
		drafts:         map[string]string{},
		viewedState:    map[string]*viewedToggle{},
		readFile:       os.ReadFile,
	}
	for _, option := range options {
		option(view)
	}
	if view.hunkMarks == nil {
		view.hunkMarks = NewHunkMarks()
	}
	if view.analyses == nil {
		view.analyses = NewAnalyses()
	}
	view.markdownRenderer = markdown.New(view.theme)
	view.overview = newOverview()
	if view.filePane == nil {
		view.filePane = newDiffPane(view.theme, func() time.Time { return view.now() }, view.wrap)
	}
	view.filePane.SetTheme(view.theme)
	view.filePane.SetHighlightSearch(view.options.HighlightSearch())
	view.filePane.SetHunkReviewed(view.hunkReviewed)
	view.filePane.SetHunkHiding(view.hunkHidden, view.hunkFade)
	view.filePane.SetThreadFade(view.threadFade)
	return view
}

// Title implements userinterface.View.
func (view *View) Title() string { return fmt.Sprintf("#%d", view.number) }

// Loading implements userinterface.Loader.
func (view *View) Loading() bool {
	return view.pullRequestPending || view.diffPending || view.threadPending || view.review.submitting || view.merge.merging
}

// CapturesKeys implements userinterface.KeyCapturer: the file picker, the verdict
// chooser and a visual-line selection own the keyboard while they are open.
func (view *View) CapturesKeys() bool {
	return view.picker != nil || view.review.chooser != nil || view.visualActive()
}

// visualActive reports whether a line selection is open.
func (view *View) visualActive() bool { return view.filePane.VisualActive() }

// Init implements userinterface.View.
func (view *View) Init() tea.Cmd { return tea.Batch(view.load(), view.autoloadAnalysis()) }

// Resumed implements userinterface.Resumer. Nothing to do yet: the workspace refreshes
// only on R.
func (view *View) Resumed() tea.Cmd { return nil }

// ShortHelp implements userinterface.View.
func (view *View) ShortHelp() []key.Binding {
	return []key.Binding{view.bind.PaneLeft, view.bind.PaneRight, view.bind.FilePicker, view.globalKeys.Open}
}

// FullHelp implements userinterface.View. It also registers every multi-key sequence the
// workspace and its file pane react to.
func (view *View) FullHelp() [][]key.Binding {
	out := [][]key.Binding{
		{view.bind.PaneLeft, view.bind.PaneRight, view.bind.NextFile, view.bind.PreviousFile, view.bind.FilePicker},
		{view.bind.NextSection, view.bind.PreviousSection, view.bind.Comment, view.bind.OverviewComment, view.bind.Reply, view.bind.Viewed, view.bind.ReviewHunk},
		{view.bind.Visual, view.bind.EditComment, view.bind.DeleteComment, view.bind.ResolveThread},
		view.searchHelp(),
	}
	return append(out, view.filePane.FullHelp()...)
}

// Update implements userinterface.View.
func (view *View) Update(message tea.Msg) (userinterface.View, tea.Cmd) {
	if command, ok := view.review.update(message, view.reviewEnvironment()); ok {
		return view, command
	}
	if command, ok := view.merge.update(message, view.reviewEnvironment()); ok {
		return view, command
	}
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		view.width, view.height = message.Width, message.Height
		view.layout()
	case userinterface.OptionsMessage:
		view.options = message.Values
		view.wrap = message.Values.Wrap()
		view.filePane.SetWrap(view.wrap)
		view.filePane.SetHideResolved(message.Values.HideResolved())
		view.filePane.SetSkipResolved(message.Values.SkipResolved())
		view.filePane.SetRelativeNumber(message.Values.RelativeNumber())
		view.filePane.SetHighlightSearch(message.Values.HighlightSearch())
		view.optionsChanged()
	case userinterface.ThemeMessage:
		view.theme = message.Theme
		view.markdownRenderer = markdown.New(view.theme)
		view.overview.invalidate()
		view.filePane.SetTheme(view.theme)
		view.layout()
	case pullRequestLoadedMessage:
		if !view.mine(message.owner, message.repository, message.number, message.sequence) {
			return view, nil
		}
		command := view.pullRequestLoaded(message)
		if message.thenDiff {
			command = tea.Batch(command, view.fetchDiff(message))
		}
		return view, command
	case diffLoadedMessage:
		if !view.mine(message.owner, message.repository, message.number, message.sequence) {
			return view, nil
		}
		return view, view.diffLoaded(message)
	case analysisReadMessage:
		if message.owner != view {
			return view, nil
		}
		return view, view.analysisRead(message)
	case answerMessage:
		return view, view.answered(message)
	case askSentMessage:
		view.askFailed(message)
		return view, nil
	case threadsLoadedMessage:
		if !view.mine(message.owner, message.repository, message.number, message.sequence) || message.generation != view.threadGeneration {
			return view, nil
		}
		return view, view.threadsLoaded(message)
	case fadeMessage:
		if message.owner == view {
			return view, view.fadeTicked(message)
		}
	case viewedMessage:
		if view.viewedMine(message) {
			return view, view.viewedDone(message)
		}
	case editedMessage:
		if !view.composeMine(origin{message.owner, message.repository, message.number}, message.sequence) {
			return view, nil
		}
		return view, view.edited(message)
	case postedMessage:
		// A post is never superseded by a later compose, so it is identified
		// by owner, repo and number only.
		if !view.owns(message.origin) {
			return view, nil
		}
		return view, view.posted(message)
	case commentDeletedMessage:
		if !view.owns(message.origin) {
			return view, nil
		}
		return view, view.commentDeleted(message)
	case threadResolvedMessage:
		if !view.owns(message.origin) {
			return view, nil
		}
		return view, view.threadResolved(message)
	case reactionToggledMessage:
		if !view.owns(message.origin) {
			return view, nil
		}
		return view, view.reactionToggled(message)
	case reviewDiscardedMessage:
		if !view.owns(message.origin) {
			return view, nil
		}
		return view, view.reviewDiscarded(message)
	case pullRequestReadMessage:
		if !view.composeMine(message.origin, message.sequence) {
			return view, nil
		}
		return view, view.pullRequestRead(message)
	case pullRequestTextMessage:
		if !view.composeMine(message.origin, message.sequence) {
			return view, nil
		}
		return view, view.pullRequestEdited(message)
	case pullRequestUpdatedMessage:
		if !view.owns(message.origin) {
			return view, nil
		}
		return view, view.pullRequestUpdated(message)
	case commentCheckedMessage:
		if !view.owns(message.origin) {
			return view, nil
		}
		view.writeDone() // the write waits for the answer
		return view, userinterface.Confirm(message.question, message.onYes)
	case commentAgreedMessage:
		if !view.owns(message.origin) {
			return view, nil
		}
		return view, view.commentAgreed(message)
	case commentReadMessage:
		if !view.owns(message.origin) || message.sequence != view.commentSequence { // another editor started meanwhile
			return view, nil
		}
		return view, view.commentRead(message)
	case userinterface.PromptResultMessage:
		if view.searchPane == paneRight {
			return view, view.filePane.SearchPrompt(message)
		}
		return view, view.search.Prompt(message, view.searchList())
	case keys.Message:
		return view, view.handleKey(message)
	case tea.KeyPressMsg:
		if view.visualActive() {
			return view, view.visualKey(message)
		}
		if view.review.chooser != nil {
			return view, view.review.chooserKey(message, view.reviewEnvironment())
		}
		if view.picker != nil {
			return view, view.pickerKey(message)
		}
	}
	return view, nil
}
