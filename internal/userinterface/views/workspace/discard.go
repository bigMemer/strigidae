package workspace

import (
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/terminaltext"
)

// Discarding the pending review deletes it with all its draft comments, so it
// always asks y/N, naming how many drafts go with it. The review is re-read
// before the question and again after the yes; a review that is gone is not
// written to, and one that is not the one asked about is asked about again.

const (
	noticeNoPendingReview = "no pending review"
	noticeReviewDiscarded = "Pending review discarded"
)

// reviewDiscardedMessage is the result of discarding the pending review, or of
// a refusal before the write. noPending says there was nothing to discard.
type reviewDiscardedMessage struct {
	origin    origin
	noPending bool
	err       error
}

// reviewDiscard is one guarded discard of the viewer's pending review.
type reviewDiscard struct {
	origin   origin
	client   github.Client
	timeouts userinterface.Timeouts
}

// discardReview starts discarding the viewer's pending review.
func (view *View) discardReview() tea.Cmd {
	discard := reviewDiscard{origin: view.origin(), client: view.client, timeouts: view.timeouts}
	view.writeStarted()
	return func() tea.Msg { return discard.check(nil) }
}

// check re-reads the review state. asked is the review the user said yes to,
// or nil before the question: it then asks, and after the yes it runs again
// and deletes.
func (discard reviewDiscard) check(asked *github.PendingReview) tea.Msg {
	readContext, cancelRead := discard.timeouts.RequestContext()
	defer cancelRead()
	status, err := discard.client.GetReviewStatus(readContext, discard.origin.repository, discard.origin.number)
	if err != nil {
		return discard.finish(false, fmt.Errorf("check review state: %w", err))
	}
	pending := status.Pending
	if pending == nil {
		return discard.finish(true, nil)
	}
	if asked == nil || asked.Identifier != pending.Identifier || asked.CommentCount != pending.CommentCount {
		current := *pending
		again := func() tea.Msg { return discard.check(&current) }
		return commentCheckedMessage{origin: discard.origin, question: discardQuestion(current), onYes: agreed(discard.origin, writeDiscard, again)}
	}
	deleteContext, cancelDelete := discard.timeouts.RequestContext()
	defer cancelDelete()
	err = userinterface.WriteError(discard.client.DeleteReview(deleteContext, pending.Identifier))
	if errors.Is(err, github.ErrNotFound) {
		return discard.finish(true, nil) // deleted between the re-read and the write; a submitted review may fail with another error, shown as one
	}
	return discard.finish(false, err)
}

func discardQuestion(pending github.PendingReview) string {
	switch pending.CommentCount {
	case 0:
		return "Discard your pending review?"
	case 1:
		return "Discard your pending review and its 1 draft comment?"
	}
	return fmt.Sprintf("Discard your pending review and its %d draft comments?", pending.CommentCount)
}

// finish builds the result. A failure is also sent as an error to show, so it
// shows even if the view was left meanwhile.
func (discard reviewDiscard) finish(noPending bool, err error) tea.Msg {
	result := reviewDiscardedMessage{origin: discard.origin, noPending: noPending, err: err}
	if err == nil {
		return result
	}
	return withShownError(result, terminaltext.SafeError(fmt.Errorf("discard review: %w", err)))
}

// reviewDiscarded shows the outcome and refreshes what shows the drafts.
func (view *View) reviewDiscarded(message reviewDiscardedMessage) tea.Cmd {
	view.writeDone()
	switch {
	case userinterface.IsWriteTimeout(message.err):
		return view.reloadPendingComments() // the error is already shown
	case message.err != nil:
		return nil // the error is already shown
	case message.noPending:
		// The screen may still show drafts of a review that is gone.
		return tea.Batch(userinterface.Info(noticeNoPendingReview), view.reloadPendingComments())
	}
	return tea.Batch(userinterface.Info(noticeReviewDiscarded), view.reloadPendingComments())
}
