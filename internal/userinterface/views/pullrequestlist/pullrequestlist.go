// Package pullrequestlist is the screen that lists a repository's open pull requests.
package pullrequestlist

import (
	"fmt"
	"io"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/cli/go-gh/v2/pkg/browser"

	"github.com/MikePors/strigidae/internal/clipboard"
	"github.com/MikePors/strigidae/internal/config"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/options"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/components/scroll"
	"github.com/MikePors/strigidae/internal/userinterface/components/search"
	"github.com/MikePors/strigidae/internal/userinterface/keys"
	"github.com/MikePors/strigidae/internal/userinterface/terminaltext"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
	"github.com/MikePors/strigidae/internal/userinterface/views/workspace"
)

const (
	// loadMoreMargin is how close to the end of the loaded rows the selection
	// gets before the next page is requested.
	loadMoreMargin = 5
)

// pullRequestsLoadedMessage is the result of a ListPullRequests or search call.
// The repository, tab and sequence identify the request: every view receives
// every command result, and a refresh supersedes any page still in flight.
type pullRequestsLoadedMessage struct {
	repository github.Repository
	tab        int
	sequence   int
	// more is true for a next-page fetch that is appended to the list, false
	// for a first-page fetch that replaces it.
	more bool
	page github.Page[github.PullRequest]
	err  error
}

// Options configures New. Zero values select the real behaviour.
type Options struct {
	// Now supplies the current time for relative ages.
	Now func() time.Time
	// Open opens a URL in the browser; it runs inside a tea.Cmd.
	Open func(url string) error
	// Timeouts are the deadlines of the GitHub calls; zero fields select the defaults.
	Timeouts userinterface.Timeouts
	// PullRequest, if not zero, is opened on start, over the list, as if it
	// had been chosen from it.
	PullRequest int
	// Questions, if set, lets the workspaces it opens ask about lines (:ask).
	Questions *workspace.Questions
	// Clipboard is the system clipboard tool the workspaces copy with besides
	// OSC 52; nil means OSC 52 only.
	Clipboard clipboard.Copier
}

// tab is one list of the repository's open pull requests with its own rows,
// cursor and search. The first tab is every author's; the others hold one
// author's pull requests, found by a search query.
type tab struct {
	name      string
	query     string // empty for the repository's own list
	author    string // who the tab is about, empty for the first tab
	requested bool   // the rows were asked for at least once

	pullRequests []github.PullRequest
	next         string // cursor of the next page, empty when all are loaded
	sequence     int    // latest request; older results are ignored
	loading      bool   // a first-page (re)load is in flight
	loadingMore  bool   // a next-page load is in flight
	err          error  // the last failed load, cleared by the next success
	moreFailed   bool   // the last next-page load failed; no auto-retry until R

	window scroll.Window // cursor and top row over pullRequests
	search search.State
}

// View lists one repository's open pull requests.
type View struct {
	client     github.Client
	repository github.Repository
	theme      theme.Theme
	globalKeys keys.Global
	now        func() time.Time
	open       func(url string) error
	timeouts   userinterface.Timeouts
	clipboard  clipboard.Copier
	// hunkMarks keeps the hunks marked reviewed in every workspace opened
	// from here, so they survive closing and reopening a pull request.
	hunkMarks *workspace.HunkMarks
	questions *workspace.Questions
	// openFirst is the pull request to open on start, or zero.
	openFirst int
	// analyses keeps the review analyses loaded in every workspace opened
	// from here.
	analyses *workspace.Analyses
	bindings bindings

	tabs          []tab
	active        int
	options       options.Values // the `:set` options, sent by the root
	width, height int
}

type bindings struct {
	nextTab, previousTab key.Binding
}

// actions names the list's own bindings, in the namespace of the global ones
// (keys.Global.Actions).
func (listBindings *bindings) actions() []keys.Action {
	return []keys.Action{
		{Name: "next-tab", Binding: &listBindings.nextTab},
		{Name: "previous-tab", Binding: &listBindings.previousTab},
	}
}

// ActionNames lists the action names of the pull request list, for the check
// that views don't reuse each other's names by accident.
func ActionNames() []string {
	listBindings := View{}.bindings
	var names []string
	for _, action := range listBindings.actions() {
		names = append(names, action.Name)
	}
	return names
}

var (
	_ userinterface.View     = (*View)(nil)
	_ userinterface.Loader   = (*View)(nil)
	_ userinterface.Prompter = (*View)(nil)
)

// New returns the PR list for repository. It starts loading when pushed (Init).
// The tabs are "All", then one per configured tab showing that tab's user's
// pull requests in this repository; the tabs' sections are not used here.
func New(client github.Client, repository github.Repository, configured []config.Tab, activeTheme theme.Theme, options Options) *View {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Open == nil {
		options.Open = openInBrowser
	}
	tabs := []tab{{name: "All"}}
	for _, current := range configured {
		// The config only allows a GitHub login or @me, so Clean changes nothing
		// there; it guards tabs built some other way.
		author := terminaltext.Clean(current.User)
		if author == "" {
			author = config.Me
		}
		tabs = append(tabs, tab{name: current.Name, author: author, query: authorQuery(repository, author)})
	}
	return &View{
		client:     client,
		repository: repository,
		theme:      activeTheme,
		globalKeys: keys.DefaultGlobal(),
		bindings: bindings{
			nextTab:     key.NewBinding(key.WithKeys("g t"), key.WithHelp("gt", "next tab")),
			previousTab: key.NewBinding(key.WithKeys("g T"), key.WithHelp("gT", "previous tab")),
		},
		now:       options.Now,
		open:      options.Open,
		timeouts:  options.Timeouts,
		clipboard: options.Clipboard,
		questions: options.Questions,
		openFirst: options.PullRequest,
		analyses:  workspace.NewAnalyses(),
		hunkMarks: workspace.NewHunkMarks(),
		tabs:      tabs,
	}
}

// authorQuery finds the open pull requests of one author in the repository,
// newest update first like the All tab. The sort is a qualifier, as in the
// dashboard's sections.
func authorQuery(repository github.Repository, author string) string {
	return fmt.Sprintf("is:pr is:open repo:%s author:%s sort:%s-%s", repository, author, config.SortUpdated, config.OrderDescending)
}

// current is the tab being shown.
func (view *View) current() *tab { return &view.tabs[view.active] }

// openInBrowser discards the launcher's output: it must not touch the screen
// the TUI owns. This covers a configured launcher; the default fallback writes
// to cli/browser's globals, which cmd/strig silences at startup.
func openInBrowser(url string) error {
	return browser.New("", io.Discard, io.Discard).Browse(url)
}

// Loading implements userinterface.Loader.
func (view *View) Loading() bool {
	current := view.current()
	return current.loading || current.loadingMore
}

// Title implements userinterface.View.
func (view *View) Title() string { return view.repositoryName() }

// repositoryName is the repository name made safe to draw (it can come from -R).
func (view *View) repositoryName() string { return terminaltext.Clean(view.repository.String()) }

// ShortHelp implements userinterface.View.
func (view *View) ShortHelp() []key.Binding {
	return []key.Binding{view.globalKeys.Select, view.globalKeys.Open, view.globalKeys.Refresh, view.bindings.nextTab}
}

// FullHelp implements userinterface.View. The search keys are global ones that
// only views with a list to search list.
func (view *View) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{view.bindings.nextTab, view.bindings.previousTab},
		{view.globalKeys.Search, view.globalKeys.NextMatch, view.globalKeys.PreviousMatch},
	}
}

// Init implements userinterface.View.
func (view *View) Init() tea.Cmd {
	if view.openFirst > 0 {
		return tea.Batch(view.show(view.active), view.openNumber(view.openFirst))
	}
	return view.show(view.active)
}

// show loads the tab's rows the first time it is shown.
func (view *View) show(index int) tea.Cmd {
	current := &view.tabs[index]
	if current.requested {
		return nil
	}
	return view.reload(index)
}

// reload requests a tab's first page. Rows already shown stay until the new
// ones arrive.
func (view *View) reload(index int) tea.Cmd {
	current := &view.tabs[index]
	current.requested = true
	current.sequence++
	current.loading, current.loadingMore, current.moreFailed = true, false, false
	// Intentional limit: the API caps a page at 100 rows, so past row 100 the
	// refreshed list is shorter than the old one and the selection falls back
	// to the same row index, which may be a different PR. The rest pages in
	// lazily as before.
	// Ask for as many rows as are loaded (up to the API maximum) so the rows
	// the user has paged through survive; zero means the default size.
	return view.fetch(index, github.PageRequest{Limit: min(len(current.pullRequests), github.MaximumPageSize)}, false)
}

// fetch loads one page of a tab: the repository's list for the first tab, a
// search for the others. The closure captures copies, so it shares no state
// with the view.
func (view *View) fetch(index int, pageRequest github.PageRequest, more bool) tea.Cmd {
	client, repository, timeouts := view.client, view.repository, view.timeouts
	query, sequence := view.tabs[index].query, view.tabs[index].sequence
	return func() tea.Msg {
		ctx, cancel := timeouts.LoadContext()
		defer cancel()
		var page github.Page[github.PullRequest]
		var err error
		if query == "" {
			page, err = client.ListPullRequests(ctx, repository, pageRequest)
		} else {
			page, err = client.SearchPullRequests(ctx, query, pageRequest)
		}
		return pullRequestsLoadedMessage{repository: repository, tab: index, sequence: sequence, more: more, page: page, err: err}
	}
}

// Update implements userinterface.View.
func (view *View) Update(message tea.Msg) (userinterface.View, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		view.width, view.height = message.Width, message.Height
		view.current().window.SetSize(view.listHeight())
		return view, nil
	case userinterface.OptionsMessage:
		view.options = message.Values
	case userinterface.ThemeMessage:
		view.theme = message.Theme
	case pullRequestsLoadedMessage:
		return view, view.loaded(message)
	case userinterface.PromptResultMessage:
		return view, view.current().search.Prompt(message, view.searchList())
	case keys.Message:
		return view, view.handleKey(message)
	}
	return view, nil
}

func (view *View) loaded(message pullRequestsLoadedMessage) tea.Cmd {
	if message.repository != view.repository || message.tab >= len(view.tabs) {
		return nil // meant for another view
	}
	current := &view.tabs[message.tab]
	if message.sequence != current.sequence {
		return nil // superseded by a refresh
	}
	if message.more {
		current.loadingMore = false
	} else {
		current.loading = false
	}
	if message.err != nil {
		current.err = message.err
		current.moreFailed = message.more // stop auto-paging until R

		failure := fmt.Errorf("load pull requests in %s: %w", view.repository, message.err)
		if message.tab != view.active {
			failure = fmt.Errorf("%s: %w", terminaltext.Clean(current.name), failure)
		}
		return userinterface.Error(terminaltext.SafeError(failure))
	}
	current.err = nil
	current.next = message.page.Next
	if message.more {
		current.pullRequests = append(current.pullRequests, message.page.Items...)
	} else {
		current.replace(message.page.Items)
	}
	current.window.SetRows(len(current.pullRequests))
	current.search.Refresh(view.searchListOf(current))
	if message.tab != view.active {
		return nil // paging resumes when it is shown
	}
	return view.maybeLoadMore()
}

// replace swaps in a fresh first page, keeping the selection on the same pull
// request when it is still listed.
func (current *tab) replace(items []github.PullRequest) {
	var selected int
	if pullRequest, ok := current.selected(); ok {
		selected = pullRequest.Number
	}
	current.pullRequests = items
	// If the selected PR is gone, stay on the same row (the window clamps it).
	current.window.SetRows(len(items))
	for i, pullRequest := range items {
		if selected != 0 && pullRequest.Number == selected {
			current.window.Restore(i, current.window.Top(), 0)
			break
		}
	}
}

func (current *tab) selected() (github.PullRequest, bool) {
	if current.window.Cursor() >= len(current.pullRequests) {
		return github.PullRequest{}, false
	}
	return current.pullRequests[current.window.Cursor()], true
}

func (view *View) handleKey(message keys.Message) tea.Cmd {
	current := view.current()
	switch {
	case key.Matches(message, view.bindings.nextTab):
		return view.switchTab(1)
	case key.Matches(message, view.bindings.previousTab):
		return view.switchTab(-1)
	case key.Matches(message, view.globalKeys.Down):
		current.window.Down(message.Repeat())
	case key.Matches(message, view.globalKeys.Up):
		current.window.Up(message.Repeat())
	case key.Matches(message, view.globalKeys.Top):
		current.window.GotoTop()
	case key.Matches(message, view.globalKeys.Bottom):
		if message.Count > 0 {
			current.window.GotoRow(message.Count - 1)
		} else {
			current.window.GotoBottom()
		}
	case key.Matches(message, view.globalKeys.HalfDown):
		message.Each(current.window.Cursor, current.window.HalfPageDown)
	case key.Matches(message, view.globalKeys.HalfUp):
		message.Each(current.window.Cursor, current.window.HalfPageUp)
	case key.Matches(message, view.globalKeys.PageDown):
		message.Each(current.window.Cursor, current.window.PageDown)
	case key.Matches(message, view.globalKeys.PageUp):
		message.Each(current.window.Cursor, current.window.PageUp)
	case key.Matches(message, view.globalKeys.ScrollCursorTop):
		current.window.ScrollCursorTop()
	case key.Matches(message, view.globalKeys.ScrollCursorMiddle):
		current.window.ScrollCursorMiddle()
	case key.Matches(message, view.globalKeys.ScrollCursorBottom):
		current.window.ScrollCursorBottom()
	case key.Matches(message, view.globalKeys.Search):
		return current.search.Start(view.searchList())
	case key.Matches(message, view.globalKeys.NextMatch):
		return current.search.Step(message, true, view.searchList(), view.options.WrapScan())
	case key.Matches(message, view.globalKeys.PreviousMatch):
		return current.search.Step(message, false, view.searchList(), view.options.WrapScan())
	case key.Matches(message, view.globalKeys.Cancel):
		current.search.Clear()
	case key.Matches(message, view.globalKeys.Select):
		return view.openSelected()
	case key.Matches(message, view.globalKeys.Open):
		return view.browseSelected()
	case key.Matches(message, view.globalKeys.Refresh):
		return view.reload(view.active)
	default:
		return nil
	}
	return view.maybeLoadMore()
}

// switchTab moves to the next (or previous) tab, wrapping around.
func (view *View) switchTab(step int) tea.Cmd {
	view.active = (view.active + step + len(view.tabs)) % len(view.tabs)
	view.current().window.SetSize(view.listHeight())
	if !view.current().requested {
		return view.show(view.active)
	}
	return view.maybeLoadMore()
}

// maybeLoadMore requests the next page when the selection is near the end of
// what is loaded.
func (view *View) maybeLoadMore() tea.Cmd {
	current := view.current()
	if current.next == "" || current.loading || current.moreFailed || current.loadingMore || current.window.Cursor() < len(current.pullRequests)-1-loadMoreMargin {
		return nil
	}
	current.loadingMore = true
	return view.fetch(view.active, github.PageRequest{After: current.next}, true)
}

// openSelected pushes the PR workspace for the selected pull request. It
// hands over the list's theme, clock and browser opener so the workspace looks
// and behaves like its parent (and tests stay deterministic).
func (view *View) openSelected() tea.Cmd {
	pullRequest, ok := view.current().selected()
	if !ok {
		return nil
	}
	return view.openNumber(pullRequest.Number)
}

// openNumber opens pull request number's workspace over the list.
func (view *View) openNumber(number int) tea.Cmd {
	return userinterface.Push(workspace.New(view.client, view.repository, number,
		workspace.WithTheme(view.theme),
		workspace.WithClock(view.now),
		workspace.WithOpener(view.open),
		workspace.WithHunkMarks(view.hunkMarks),
		workspace.WithWrap(view.options.Wrap()),
		workspace.WithTimeouts(view.timeouts),
		workspace.WithQuestions(view.questions),
		workspace.WithAnalyses(view.analyses),
		workspace.WithClipboard(view.clipboard),
	))
}

func (view *View) browseSelected() tea.Cmd {
	pullRequest, ok := view.current().selected()
	if !ok {
		return nil
	}
	open, url := view.open, pullRequest.URL
	return func() tea.Msg {
		if url == "" {
			return userinterface.ErrorMessage{Err: fmt.Errorf("pull request #%d has no URL", pullRequest.Number)}
		}
		if err := open(url); err != nil {
			return userinterface.ErrorMessage{Err: terminaltext.SafeError(fmt.Errorf("open #%d in browser: %w", pullRequest.Number, err))}
		}
		return userinterface.InfoMessage{Text: fmt.Sprintf("opened #%d in browser", pullRequest.Number)}
	}
}

func (view *View) listHeight() int { return max(0, view.height-2) } // minus the tab bar and the header row
