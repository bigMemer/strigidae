package workspace

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/terminaltext"
)

// :ready and :draft move the pull request out of draft and back. The loaded
// pull request is checked first, then re-read from GitHub so a stale draft
// flag never sends the wrong mutation, then y/N, and busy is checked again
// after the yes.

const (
	noticeNotADraft     = "this pull request is not a draft"
	noticeAlreadyDraft  = "this pull request is already a draft"
	noticePullRequestIs = "this pull request is " // followed by closed or merged
)

type (
	// draftReadMessage is the pull request freshly read before asking.
	draftReadMessage struct {
		origin   origin
		sequence int
		toDraft  bool
		detail   github.PullRequestDetail
		err      error
	}
	// draftChangedMessage is the result of the mutation, or of the re-read
	// after the yes: refusal is set when nothing was sent because the state
	// had changed.
	draftChangedMessage struct {
		refusal string
		origin  origin
		toDraft bool
		err     error
	}
)

// draftRefusal is the notice for a pull request the change does not apply to,
// or "" when it applies.
func draftRefusal(detail github.PullRequest, toDraft bool) string {
	switch {
	case detail.State == github.PullRequestStateMerged:
		return noticePullRequestIs + "merged"
	case detail.State == github.PullRequestStateClosed:
		return noticePullRequestIs + "closed"
	case toDraft && detail.Draft:
		return noticeAlreadyDraft
	case !toDraft && !detail.Draft:
		return noticeNotADraft
	}
	return ""
}

// changeDraftState checks the loaded pull request, then re-reads it.
func (view *View) changeDraftState(toDraft bool) tea.Cmd {
	origin, client, timeouts := view.origin(), view.client, view.timeouts
	view.commentSequence++ // a newer request supersedes this read
	sequence := view.commentSequence
	return func() tea.Msg {
		ctx, cancel := timeouts.LoadContext()
		defer cancel()
		detail, err := client.GetPullRequest(ctx, origin.repository, origin.number)
		return draftReadMessage{origin: origin, sequence: sequence, toDraft: toDraft, detail: detail, err: err}
	}
}

// draftRead refuses on what GitHub has now, or asks.
func (view *View) draftRead(message draftReadMessage) tea.Cmd {
	if message.err != nil {
		return userinterface.Error(terminaltext.SafeError(fmt.Errorf("read pull request: %w", message.err)))
	}
	if refusal := draftRefusal(message.detail.PullRequest, message.toDraft); refusal != "" {
		return tea.Batch(userinterface.Info(refusal), view.reloadPullRequest()) // the screen was stale
	}
	if busy := view.guardState().busy(writeDraftState); busy != nil {
		return busy
	}
	input := github.DraftStateInput{Repository: message.origin.repository, Number: message.origin.number, PullRequestIdentifier: message.detail.Identifier}
	question := fmt.Sprintf("Mark #%d ready for review?", message.origin.number)
	if message.toDraft {
		question = fmt.Sprintf("Convert #%d to a draft?", message.origin.number)
	}
	return userinterface.Confirm(question, view.agreed(writeDraftState, view.sendDraftState(message.toDraft, input)))
}

// sendDraftState is the command that runs on the yes.
func (view *View) sendDraftState(toDraft bool, input github.DraftStateInput) tea.Cmd {
	origin, client, timeouts := view.origin(), view.client, view.timeouts
	return func() tea.Msg {
		// Read again after the yes: the state may have changed while the
		// question was open, and a stale yes never writes.
		loadContext, cancelLoad := timeouts.LoadContext()
		defer cancelLoad()
		fresh, readErr := client.GetPullRequest(loadContext, origin.repository, origin.number)
		if readErr != nil {
			return withShownError(draftChangedMessage{origin: origin, toDraft: toDraft, err: readErr}, terminaltext.SafeError(fmt.Errorf("read pull request: %w", readErr)))
		}
		if refusal := draftRefusal(fresh.PullRequest, toDraft); refusal != "" {
			return draftChangedMessage{origin: origin, toDraft: toDraft, refusal: refusal}
		}
		input.PullRequestIdentifier = fresh.Identifier
		ctx, cancel := timeouts.RequestContext()
		defer cancel()
		what := "mark ready for review"
		call := client.MarkReadyForReview
		if toDraft {
			what, call = "convert to draft", client.ConvertToDraft
		}
		err := userinterface.WriteError(call(ctx, input))
		result := draftChangedMessage{origin: origin, toDraft: toDraft, err: err}
		if err == nil {
			return result
		}
		return withShownError(result, terminaltext.SafeError(fmt.Errorf("%s: %w", what, err)))
	}
}

// draftChanged reloads, so the header shows the new state.
func (view *View) draftChanged(message draftChangedMessage) tea.Cmd {
	view.writeDone()
	if message.refusal != "" {
		return tea.Batch(userinterface.Info(message.refusal), view.reloadPullRequest()) // the screen was stale
	}
	if userinterface.IsWriteTimeout(message.err) {
		return view.reloadPullRequest() // the error is already shown
	}
	if message.err != nil {
		return nil // the error is already shown
	}
	notice := fmt.Sprintf("#%d is ready for review", message.origin.number)
	if message.toDraft {
		notice = fmt.Sprintf("#%d is now a draft", message.origin.number)
	}
	return tea.Batch(userinterface.Info(notice), view.reloadPullRequest())
}
