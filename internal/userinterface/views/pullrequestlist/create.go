package pullrequestlist

import (
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MikePors/strigidae/internal/editor"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/components/picker"
	"github.com/MikePors/strigidae/internal/userinterface/terminaltext"
)

// :create and :create-draft open a pull request in three steps: pick the head
// branch, pick the base branch, write the title and body in $EDITOR. :create
// asks y/N before it publishes; a draft is created at once. Every step carries
// the flow's sequence number, so a result that arrives after the flow was
// cancelled or restarted is ignored.

const (
	commandCreate      = "create"
	commandCreateDraft = "create-draft"

	// maximumBranchPages bounds how many pages of branches are loaded.
	maximumBranchPages = 10
)

// createFlow is one run of :create or :create-draft.
type createFlow struct {
	sequence int
	draft    bool
	loading  bool // the branches are being loaded
	branches []github.Branch
	head     string // chosen head, empty while the head picker is open
	picker   *picker.Model
	// choices are the branch names the open picker lists, in its order.
	choices []string
}

type (
	// branchesLoadedMessage is the repository's branches.
	branchesLoadedMessage struct {
		owner    *View
		sequence int
		branches []github.Branch
		err      error
	}
	// createTextMessage is what the editor returned.
	createTextMessage struct {
		owner    *View
		sequence int
		head     string
		base     string
		draft    bool
		text     string
		err      error
	}
	// pullRequestCreatedMessage is the result of the create call.
	pullRequestCreatedMessage struct {
		owner       *View
		pullRequest github.PullRequest
		err         error
	}
)

// Commands lists the command names of the pull request list, for the shell to
// tell a command another view owns from a word that is no command at all.
func Commands() []string { return []string{commandCreate, commandCreateDraft} }

// CommandNames implements userinterface.CommandLister.
func (view *View) CommandNames() []string { return Commands() }

// Command implements userinterface.Commander.
func (view *View) Command(name, _ string) (tea.Cmd, bool) {
	switch name {
	case commandCreate:
		return view.startCreate(false), true
	case commandCreateDraft:
		return view.startCreate(true), true
	}
	return nil, false
}

// CapturesKeys implements userinterface.KeyCapturer: an open branch picker
// owns the keyboard.
func (view *View) CapturesKeys() bool { return view.creating != nil && view.creating.picker != nil }

// startCreate loads the branches; the head picker opens when they arrive. A
// second command while one is running replaces it.
func (view *View) startCreate(draft bool) tea.Cmd {
	view.createSequence++ // only ever increases, so a late result is never taken for a newer flow's
	sequence := view.createSequence
	view.creating = &createFlow{sequence: sequence, draft: draft, loading: true}
	owner, client, repository, timeouts := view, view.client, view.repository, view.timeouts
	return func() tea.Msg {
		ctx, cancel := timeouts.LoadContext()
		defer cancel()
		var branches []github.Branch
		request := github.PageRequest{Limit: github.MaximumPageSize}
		for range maximumBranchPages {
			page, err := client.ListBranches(ctx, repository, request)
			if err != nil {
				return branchesLoadedMessage{owner: owner, sequence: sequence, err: err}
			}
			branches = append(branches, page.Items...)
			if !page.HasMore() {
				break
			}
			request.After = page.Next
		}
		return branchesLoadedMessage{owner: owner, sequence: sequence, branches: branches}
	}
}

// mine reports whether a result belongs to the flow that is running.
func (view *View) mine(owner *View, sequence int) bool {
	return owner == view && view.creating != nil && view.creating.sequence == sequence
}

// branchesLoaded opens the head picker.
func (view *View) branchesLoaded(message branchesLoadedMessage) tea.Cmd {
	if !view.mine(message.owner, message.sequence) || !view.creating.loading {
		return nil
	}
	flow := view.creating
	flow.loading = false
	if message.err != nil {
		view.creating = nil
		return userinterface.Error(terminaltext.SafeError(fmt.Errorf("list branches: %w", message.err)))
	}
	if len(message.branches) == 0 {
		view.creating = nil
		return userinterface.Info("no branches in " + view.repositoryName())
	}
	flow.branches = message.branches
	names := make([]string, len(message.branches))
	for i, branch := range message.branches {
		names[i] = branch.Name
	}
	preselected := -1
	for i, name := range names {
		if view.currentBranch != "" && name == view.currentBranch {
			preselected = i
		}
	}
	view.openBranchPicker(names, "Head branch", preselected)
	return nil
}

// openBranchPicker shows a picker over names with the cursor on preselected
// (-1 for none).
func (view *View) openBranchPicker(names []string, title string, preselected int) {
	flow := view.creating
	shown := make([]string, len(names))
	for i, name := range names {
		shown[i] = terminaltext.Clean(name)
	}
	flow.choices = names
	flow.picker = picker.New(view.theme, title, shown)
	if preselected >= 0 {
		flow.picker.Select(preselected)
	}
}

// createKey feeds a key to the open picker.
func (view *View) createKey(message tea.KeyPressMsg) tea.Cmd {
	flow := view.creating
	result := flow.picker.Update(message)
	switch result.Outcome {
	case picker.Cancelled:
		view.creating = nil
	case picker.Chosen:
		return view.branchChosen(flow.choices[result.Index])
	case picker.Open:
	}
	return nil
}

// branchChosen moves from the head picker to the base picker, then to the editor.
func (view *View) branchChosen(name string) tea.Cmd {
	flow := view.creating
	if flow.head == "" {
		flow.head = name
		var others []string
		defaultAt := -1
		for _, branch := range flow.branches {
			if branch.Name == name {
				continue
			}
			if branch.Default {
				defaultAt = len(others)
			}
			others = append(others, branch.Name)
		}
		if len(others) == 0 {
			view.creating = nil
			return userinterface.Info("no other branch to merge into")
		}
		view.openBranchPicker(others, "Base branch", defaultAt)
		return nil
	}
	flow.picker = nil
	owner, sequence, head, draft := view, flow.sequence, flow.head, flow.draft
	return view.edit("", func(text string, err error) tea.Msg {
		return createTextMessage{owner: owner, sequence: sequence, head: head, base: name, draft: draft, text: text, err: err}
	})
}

// createEdited checks the editor's text, asks before publishing, and creates.
func (view *View) createEdited(message createTextMessage) tea.Cmd {
	if !view.mine(message.owner, message.sequence) {
		return nil
	}
	view.creating = nil
	if message.err != nil {
		return userinterface.Error(terminaltext.SafeError(fmt.Errorf("edit pull request: %w", message.err)))
	}
	title, body, err := editor.ParsePullRequestText(message.text)
	switch {
	case errors.Is(err, editor.ErrEmptyTitle):
		return userinterface.Error(errors.New("empty title, nothing created (did the editor fail?)"))
	case err != nil:
		return userinterface.Error(err)
	}
	input := github.CreatePullRequestInput{Repository: view.repository, Head: message.head, Base: message.base, Title: title, Body: body, Draft: message.draft}
	if input.Draft {
		return view.create(input)
	}
	// Each part is cleaned on its own: the title and branch names come from the
	// editor and from GitHub.
	question := fmt.Sprintf("Create pull request \"%s\" from %s into %s?", terminaltext.Clean(title), terminaltext.Clean(message.head), terminaltext.Clean(message.base))
	return userinterface.Confirm(question, view.create(input))
}

// create is the command that runs the create call.
func (view *View) create(input github.CreatePullRequestInput) tea.Cmd {
	owner, client, timeouts := view, view.client, view.timeouts
	return func() tea.Msg {
		ctx, cancel := timeouts.RequestContext()
		defer cancel()
		pullRequest, err := client.CreatePullRequest(ctx, input)
		return pullRequestCreatedMessage{owner: owner, pullRequest: pullRequest, err: userinterface.WriteError(err)}
	}
}

// pullRequestCreated refreshes the lists and opens the new pull request.
func (view *View) pullRequestCreated(message pullRequestCreatedMessage) tea.Cmd {
	if message.owner != view {
		return nil
	}
	var shown tea.Cmd
	if message.err != nil {
		shown = userinterface.Error(terminaltext.SafeError(fmt.Errorf("create pull request: %w", message.err)))
		if !userinterface.IsWriteTimeout(message.err) {
			return shown
		}
	}
	for i := range view.tabs {
		if i != view.active {
			view.tabs[i].requested = false // reloaded when shown
		}
	}
	if message.err != nil {
		// The pull request may exist, but there is none to open: the list shows
		// what GitHub has.
		return tea.Batch(shown, view.reload(view.active))
	}
	return tea.Batch(
		userinterface.Info(fmt.Sprintf("Created #%d", message.pullRequest.Number)),
		view.reload(view.active),
		view.openPullRequest(message.pullRequest.Number),
	)
}

// overlayPicker draws the open branch picker over the list.
func (view *View) overlayPicker(screen string, width, height int) string {
	if view.creating == nil || view.creating.picker == nil {
		return screen
	}
	box := view.creating.picker.Render(width, height-1)
	if box == "" {
		return screen
	}
	rows := strings.Split(screen, "\n")
	for len(rows) < height { // a short or empty list leaves rows the picker must still draw over
		rows = append(rows, "")
	}
	boxLines := strings.Split(box, "\n")
	boxWidth := ansi.StringWidth(boxLines[0])
	column := max(0, (width-boxWidth)/2)
	for i, line := range boxLines {
		row := 1 + i // below the tab bar
		if row >= len(rows) {
			break
		}
		base := rows[row]
		if gap := width - ansi.StringWidth(base); gap > 0 {
			base += strings.Repeat(" ", gap)
		}
		rows[row] = ansi.Cut(base, 0, column) + line + ansi.Cut(base, column+boxWidth, width)
	}
	return strings.Join(rows, "\n")
}
