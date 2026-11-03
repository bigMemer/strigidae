package workspace

import (
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/editor"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/terminaltext"
)

// :edit-pull-request changes the title and body in one editor session. The
// title and body are re-read from GitHub before the editor opens, so the edit
// starts from their current text; after the editor it asks y/N, and busy is
// checked again after the yes.

const (
	noticeCannotEditPullRequest = "you can't edit this pull request"
	noticeNoChanges             = "no changes"
	noticePullRequestUpdated    = "Pull request updated"
)

type (
	// pullRequestReadMessage is the title and body freshly read from GitHub.
	pullRequestReadMessage struct {
		origin   origin
		sequence int
		detail   github.PullRequestDetail
		err      error
	}
	// pullRequestTextMessage is what the editor returned for the text read.
	pullRequestTextMessage struct {
		origin   origin
		sequence int
		detail   github.PullRequestDetail // as read, before the editor
		text     string
		err      error
	}
	// pullRequestUpdatedMessage is the result of the update call.
	pullRequestUpdatedMessage struct {
		origin origin
		title  string
		body   string
		err    error
	}
)

// editPullRequest re-reads the pull request, then opens the editor on it.
func (view *View) editPullRequest() tea.Cmd {
	origin, client, timeouts := view.origin(), view.client, view.timeouts
	view.commentSequence++ // an editor started meanwhile supersedes this read
	sequence := view.commentSequence
	return func() tea.Msg {
		ctx, cancel := timeouts.LoadContext()
		defer cancel()
		detail, err := client.GetPullRequest(ctx, origin.repository, origin.number)
		return pullRequestReadMessage{origin: origin, sequence: sequence, detail: detail, err: err}
	}
}

// pullRequestRead refuses or opens the editor on the text that was read.
func (view *View) pullRequestRead(message pullRequestReadMessage) tea.Cmd {
	if message.err != nil {
		return userinterface.Error(terminaltext.SafeError(fmt.Errorf("read pull request: %w", message.err)))
	}
	if !message.detail.ViewerCanUpdate {
		return userinterface.Info(noticeCannotEditPullRequest)
	}
	if busy := view.guardState().busy(writePullRequest); busy != nil {
		return busy
	}
	origin, sequence, detail := message.origin, message.sequence, message.detail
	return view.edit(editor.FormatPullRequestText(detail.Title, detail.Body), func(text string, err error) tea.Msg {
		return pullRequestTextMessage{origin: origin, sequence: sequence, detail: detail, text: text, err: err}
	})
}

// pullRequestEdited checks the editor's text and asks before sending it.
func (view *View) pullRequestEdited(message pullRequestTextMessage) tea.Cmd {
	if message.err != nil {
		return userinterface.Error(terminaltext.SafeError(fmt.Errorf("edit pull request: %w", message.err)))
	}
	title, body, err := editor.ParsePullRequestText(message.text)
	switch {
	case errors.Is(err, editor.ErrEmptyTitle):
		return userinterface.Error(errors.New("empty title, nothing sent (did the editor fail?)"))
	case err != nil:
		return userinterface.Error(err)
	}
	// What was read goes through the same parsing, so a body GitHub stores
	// with CRLF or trailing blanks does not count as changed.
	oldTitle, oldBody, _ := editor.ParsePullRequestText(editor.FormatPullRequestText(message.detail.Title, message.detail.Body))
	if title == oldTitle && body == oldBody {
		return userinterface.Info(noticeNoChanges)
	}
	if busy := view.guardState().busy(writePullRequest); busy != nil {
		return busy
	}
	question := fmt.Sprintf("Update the title and body of #%d?", message.origin.number)
	return userinterface.Confirm(question, view.agreed(writePullRequest, view.sendPullRequest(message.detail.Identifier, title, body)))
}

// sendPullRequest is the command that runs on the yes.
func (view *View) sendPullRequest(identifier, title, body string) tea.Cmd {
	origin, client, timeouts := view.origin(), view.client, view.timeouts
	return func() tea.Msg {
		ctx, cancel := timeouts.RequestContext()
		defer cancel()
		err := userinterface.WriteError(client.UpdatePullRequest(ctx, github.UpdatePullRequestInput{
			Repository: origin.repository, Number: origin.number, PullRequestIdentifier: identifier, Title: title, Body: body,
		}))
		result := pullRequestUpdatedMessage{origin: origin, title: title, body: body, err: err}
		if err == nil {
			return result
		}
		return withShownError(result, terminaltext.SafeError(fmt.Errorf("update pull request: %w", err)))
	}
}

// pullRequestUpdated shows the new text at once and refreshes from GitHub.
func (view *View) pullRequestUpdated(message pullRequestUpdatedMessage) tea.Cmd {
	view.writeDone()
	if userinterface.IsWriteTimeout(message.err) {
		return view.reloadPullRequest() // the error is already shown
	}
	if message.err != nil {
		return nil // the error is already shown
	}
	if view.detail != nil {
		view.detail.Title, view.detail.Body = message.title, message.body
		view.overview.invalidate()
		view.layout()
	}
	return tea.Batch(userinterface.Info(noticePullRequestUpdated), view.reloadPullRequest())
}
