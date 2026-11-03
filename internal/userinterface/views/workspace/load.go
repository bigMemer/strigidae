package workspace

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/diff"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/terminaltext"
)

// Result messages carry the identity of the request that produced them
// (repo, number and the view's request sequence), because every view in the
// stack receives every non-key message.
type (
	pullRequestLoadedMessage struct {
		owner      *View // the workspace that asked; compared, never dereferenced
		repository github.Repository
		number     int
		sequence   int
		detail     github.PullRequestDetail
		err        error
		// thenDiff makes the result start the diff load. The diff is fetched
		// after the detail so that the head recorded for it can only be older
		// than the diff, never newer.
		thenDiff bool
	}
	diffLoadedMessage struct {
		owner      *View
		repository github.Repository
		number     int
		sequence   int
		// head is the detail's head SHA this diff followed; empty when the
		// detail failed to load.
		head string
		raw  string
		// parsed is Raw parsed in the command, so Update stays cheap. It is
		// never nil when err is nil, and may be partial when parseErr is set.
		parsed   *diff.Diff
		parseErr error
		err      error // the GetDiff failure
		// perFile is set when GetDiff refused the diff as too large and it was
		// built from the file list instead (perFileDiff).
		perFile bool
		// fallbackErr is why the file list could not be loaded either; err
		// then still holds the too-large error.
		fallbackErr error
	}
	threadsLoadedMessage struct {
		owner      *View
		repository github.Repository
		number     int
		sequence   int
		// generation is the view's threadGeneration when the load started; a
		// later reload, or a resolution applied in place, supersedes it.
		generation int
		threads    []github.ReviewThread
		err        error
	}
)

// maximumThreadPages bounds the follow-up requests for review threads, so a
// misbehaving server cannot keep the command running forever.
const maximumThreadPages = 100

// loadThreads fetches every review thread, page by page. Any failure discards
// the partial result: a half-list would hide threads without saying so.
func loadThreads(ctx context.Context, client github.Client, repository github.Repository, number int) ([]github.ReviewThread, error) {
	var all []github.ReviewThread
	var pageRequest github.PageRequest
	for range maximumThreadPages {
		page, err := client.ListReviewThreads(ctx, repository, number, pageRequest)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if page.Next == "" {
			return all, nil
		}
		pageRequest.After = page.Next
	}
	return nil, fmt.Errorf("more than %d pages of review threads", maximumThreadPages)
}

// load starts a fresh load of the pull request and its diff.
func (view *View) load() tea.Cmd {
	view.sequence++
	view.diffPending = true
	view.diff.Loading = true
	view.filePane.SetDiff(view.diff)

	return tea.Batch(view.fetchPullRequest(true), view.reloadThreads())
}

// fetchDiff loads the diff once the detail has landed.
func (view *View) fetchDiff(message pullRequestLoadedMessage) tea.Cmd {
	client, repository, number, sequence, owner, timeouts := view.client, view.repository, view.number, message.sequence, view, view.timeouts
	head := ""
	if message.err == nil {
		head = message.detail.HeadSHA
	}
	return func() tea.Msg {
		ctx, cancel := timeouts.LoadContext() // covers the file-patch fallback too
		defer cancel()
		raw, err := client.GetDiff(ctx, repository, number)
		if errors.Is(err, github.ErrDiffTooLarge) {
			patches, listError := client.ListFilePatches(ctx, repository, number)
			if listError != nil {
				return diffLoadedMessage{owner: owner, repository: repository, number: number, sequence: sequence, err: err, fallbackErr: listError}
			}
			perFileRaw, parsed, parseError := perFileDiff(patches)
			return diffLoadedMessage{owner: owner, repository: repository, number: number, sequence: sequence, head: head, raw: perFileRaw, parsed: parsed, parseErr: parseError, perFile: true}
		}
		if err != nil {
			return diffLoadedMessage{owner: owner, repository: repository, number: number, sequence: sequence, err: err}
		}
		// diff.Parse returns a best-effort result with its error, so
		// files before a malformed hunk still show.
		parsed, parseError := diff.Parse(raw)
		return diffLoadedMessage{owner: owner, repository: repository, number: number, sequence: sequence, head: head, raw: raw, parsed: parsed, parseErr: parseError}
	}
}

// reloadPullRequest loads the pull request again under the current request sequence;
// after posting a comment it refreshes just the conversation.
func (view *View) reloadPullRequest() tea.Cmd { return view.fetchPullRequest(false) }

func (view *View) fetchPullRequest(thenDiff bool) tea.Cmd {
	view.pullRequestPending = true
	view.overview.invalidate()
	client, repository, number, sequence, owner, timeouts := view.client, view.repository, view.number, view.sequence, view, view.timeouts
	return func() tea.Msg {
		ctx, cancel := timeouts.LoadContext()
		defer cancel()
		detail, err := client.GetPullRequest(ctx, repository, number)
		return pullRequestLoadedMessage{owner: owner, repository: repository, number: number, sequence: sequence, detail: detail, err: err, thenDiff: thenDiff}
	}
}

// reloadThreads loads the review threads again under the current request
// sequence; after posting a reply it refreshes just the threads.
func (view *View) reloadThreads() tea.Cmd {
	view.threadPending, view.threadErr = true, nil
	view.threadGeneration++
	client, repository, number, sequence, owner, generation, timeouts := view.client, view.repository, view.number, view.sequence, view, view.threadGeneration, view.timeouts
	return func() tea.Msg {
		ctx, cancel := timeouts.LoadContext()
		defer cancel()
		threads, err := loadThreads(ctx, client, repository, number)
		return threadsLoadedMessage{owner: owner, repository: repository, number: number, sequence: sequence, generation: generation, threads: threads, err: err}
	}
}

// mine reports whether a result belongs to this workspace's latest request.
// The owner check matters because results go to every view in the stack: a
// closed workspace's commands may still be running when a new workspace for
// the same pull request starts counting its requests from 1 again.
func (view *View) mine(owner *View, repository github.Repository, number, sequence int) bool {
	return view.owns(origin{owner, repository, number}) && sequence == view.sequence
}

func (view *View) pullRequestLoaded(message pullRequestLoadedMessage) tea.Cmd {
	view.pullRequestPending = false
	if message.err != nil {
		if view.detail == nil {
			view.err, view.notFound = message.err, errors.Is(message.err, github.ErrNotFound)
		}
		return userinterface.Error(terminaltext.SafeError(message.err))
	}
	previous := view.selectedPath()
	following := view.current == view.cursorEntry() // the pane shows the cursor's file
	view.detail, view.err, view.notFound = &message.detail, nil, false
	view.reapplyViewed(message.sequence)
	view.current = 0
	for i, file := range message.detail.Files {
		if previous != "" && file.Path == previous {
			view.current = i + 1
		}
	}
	view.place(view.current) // a refresh must not scroll the Files list
	if following {
		view.current = view.cursorEntry() // the file may be hidden now
	}
	view.overview.invalidate()
	view.selectionChanged()
	view.search.Refresh(view.searchList()) // the paths may differ with the same rows
	view.layout()
	return nil
}

func (view *View) threadsLoaded(message threadsLoadedMessage) tea.Cmd {
	view.threadPending = false
	if message.err != nil {
		// Keep whatever threads were shown before; the error goes to the status
		// bar, and to the Overview while there is nothing else to show.
		view.threadErr = message.err
		view.overview.invalidate()
		return userinterface.Error(terminaltext.SafeError(fmt.Errorf("load review threads: %w", message.err)))
	}
	view.threads, view.threadLoaded = sortThreads(message.threads), true
	view.fades.endHolds(threadFadeKeyPrefix)
	view.endUnresolvedFades()
	view.filePane.SetThreads(view.threads)
	view.overview.invalidate()
	view.layout()
	return nil
}

func (view *View) diffLoaded(message diffLoadedMessage) tea.Cmd {
	view.diffPending = false
	// err is set only when no diff came back: a too-large diff is built file
	// by file, and err stays only if that failed too.
	if message.err != nil && view.diff.Parsed != nil {
		view.diff.Loading = false // keep the loaded diff; the error goes to the status bar
	} else {
		view.diff = DiffState{Raw: message.raw, Parsed: message.parsed, ParseError: message.parseErr, Err: message.err}
		view.diffHead = message.head
	}
	view.filePane.SetDiff(view.diff)
	if message.parseErr != nil {
		// Files that parsed still show; the problem is reported once here.
		return userinterface.Error(terminaltext.SafeError(fmt.Errorf("%s#%d: %w", view.repository, view.number, message.parseErr)))
	}
	if message.fallbackErr != nil {
		// The pane says the diff is too large; this says why it isn't shown file by file.
		return userinterface.Error(terminaltext.SafeError(fmt.Errorf("load the diff file by file: %w", message.fallbackErr)))
	}
	if message.perFile {
		return userinterface.Info(perFileNotice)
	}
	// A diff that is merely too large is shown in the diff pane, not as a
	// transient error.
	if message.err != nil && !errors.Is(message.err, github.ErrDiffTooLarge) {
		return userinterface.Error(terminaltext.SafeError(message.err))
	}
	return nil
}
