package workspace

import (
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/components/picker"
	"github.com/MikePors/strigidae/internal/userinterface/terminaltext"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

// reviewFlow is the state of submitting a review: the verdict chooser, the
// unsent summary, the request sequence and the in-flight flag. Its methods
// take everything they need from the workspace as a reviewEnvironment, and result
// messages carry the flow's own pointer as their identity.
type reviewFlow struct {
	chooser *picker.Model // non-nil while the verdict chooser is open
	// chooserVerdicts are the events of the chooser's rows, in order.
	chooserVerdicts []github.ReviewEvent
	sequence        int    // latest review request; older results are stale
	draft           string // unsent summary, kept until a review is submitted
	submitting      bool   // a submission is in flight
}

// reviewEnvironment is what the flow needs from the workspace at one moment.
type reviewEnvironment struct {
	client         github.Client
	timeouts       userinterface.Timeouts
	repository     github.Repository
	number         int
	theme          theme.Theme
	edit           EditFunction
	reload         func() tea.Cmd // reloads the workspace
	ownPullRequest bool           // the viewer authored the pull request
	diffHead       string         // head SHA the diff on screen was loaded for
	diffPending    bool           // a diff load is in flight
	guard          guardState     // what the guard table looks at (see guard.go)
}

// Results carry the identity of the flow, repo and number that asked. The
// ones that belong to one attempt (pending, edited, fresh) also carry the
// flow's sequence, so starting another review makes older results stale.
type (
	// reviewPendingMessage is the pending review fetched to pre-fill the editor.
	reviewPendingMessage struct {
		flow       *reviewFlow
		repository github.Repository
		number     int
		sequence   int
		event      github.ReviewEvent
		pending    *github.PendingReview
		err        error
	}
	reviewEditedMessage struct {
		flow       *reviewFlow
		repository github.Repository
		number     int
		sequence   int
		event      github.ReviewEvent
		body       string
		err        error
	}
	// reviewFreshMessage is the status fetched just before confirming.
	reviewFreshMessage struct {
		flow       *reviewFlow
		repository github.Repository
		number     int
		sequence   int
		event      github.ReviewEvent
		body       string // the edited summary
		status     github.ReviewStatus
		err        error
	}
	// reviewSendMessage is what the confirmation's "yes" produces; handling it
	// marks the submission as in flight before the call starts.
	reviewSendMessage struct {
		flow       *reviewFlow
		repository github.Repository
		number     int
		event      github.ReviewEvent
		body       string
		status     github.ReviewStatus
	}
	reviewSubmittedMessage struct {
		flow       *reviewFlow
		repository github.Repository
		number     int
		event      github.ReviewEvent
		body       string
		err        error
	}
	// refreshMessage asks the workspace to reload after its head moved.
	refreshMessage struct {
		flow       *reviewFlow
		repository github.Repository
		number     int
	}
)

func (view *View) reviewEnvironment() reviewEnvironment {
	return reviewEnvironment{
		client: view.client, timeouts: view.timeouts, repository: view.repository, number: view.number, theme: view.theme,
		edit: view.edit, reload: view.load,
		ownPullRequest: view.detail != nil && view.detail.ViewerDidAuthor,
		diffHead:       view.diffHead,
		diffPending:    view.diffPending,
		guard:          view.guardState(),
	}
}

// Command implements userinterface.Commander for every command in the
// registry (see Commands): the entry that has the name runs as a key would.
func (view *View) Command(name, arguments string) (tea.Cmd, bool) {
	chosen, ok := view.commandEntry(name)
	if !ok {
		return nil, false
	}
	if refusal := view.duringSelection(chosen); refusal != nil {
		return refusal, true
	}
	return view.invoke(chosen, invocation{arguments: arguments}), true
}

// mine reports whether a result belongs to this flow's latest attempt.
func (currentFlow *reviewFlow) mine(environment reviewEnvironment, flow *reviewFlow, repository github.Repository, number, sequence int) bool {
	return currentFlow.same(environment, flow, repository, number) && sequence == currentFlow.sequence
}

func (currentFlow *reviewFlow) same(environment reviewEnvironment, flow *reviewFlow, repository github.Repository, number int) bool {
	return flow == currentFlow && repository == environment.repository && number == environment.number
}

// update handles the flow's messages. handled is false for any other message.
func (currentFlow *reviewFlow) update(message tea.Msg, environment reviewEnvironment) (command tea.Cmd, handled bool) {
	switch message := message.(type) {
	case reviewPendingMessage:
		if !currentFlow.mine(environment, message.flow, message.repository, message.number, message.sequence) {
			return nil, true
		}
		return currentFlow.pendingFetched(message, environment), true
	case reviewEditedMessage:
		if !currentFlow.mine(environment, message.flow, message.repository, message.number, message.sequence) {
			return nil, true
		}
		return currentFlow.edited(message, environment), true
	case reviewFreshMessage:
		if !currentFlow.mine(environment, message.flow, message.repository, message.number, message.sequence) {
			return nil, true
		}
		return currentFlow.confirm(message, environment), true
	case reviewSendMessage:
		if !currentFlow.same(environment, message.flow, message.repository, message.number) {
			return nil, true
		}
		if busy := environment.guard.busy(writeReview); busy != nil { // something started since the yes was asked for
			return busy, true
		}
		currentFlow.submitting = true
		return tea.Batch(userinterface.Info("Submitting review…"), currentFlow.submit(environment, message.event, message.body, message.status)), true
	case reviewSubmittedMessage:
		if !currentFlow.same(environment, message.flow, message.repository, message.number) {
			return nil, true
		}
		return currentFlow.submitted(message, environment), true
	case refreshMessage:
		if !currentFlow.same(environment, message.flow, message.repository, message.number) {
			return nil, true
		}
		return environment.reload(), true
	}
	return nil, false
}

// openChooser shows the verdict chooser.
func (currentFlow *reviewFlow) openChooser(environment reviewEnvironment) tea.Cmd {
	if notice := environment.guard.admit(writeReview); notice != nil {
		return notice
	}
	currentFlow.chooserVerdicts = currentFlow.chooserVerdicts[:0]
	var items []string
	for _, verdict := range verdicts {
		if environment.ownPullRequest && verdict.event != github.ReviewEventComment {
			continue
		}
		currentFlow.chooserVerdicts = append(currentFlow.chooserVerdicts, verdict.event)
		items = append(items, verdict.label)
	}
	currentFlow.chooser = picker.New(environment.theme, "Submit review", items)
	if environment.ownPullRequest {
		currentFlow.chooser.SetNote(ownPullRequestText)
	}
	return nil
}

// chooserKey feeds a raw key to the open chooser.
func (currentFlow *reviewFlow) chooserKey(message tea.KeyPressMsg, environment reviewEnvironment) tea.Cmd {
	result := currentFlow.chooser.Update(message)
	switch result.Outcome {
	case picker.Cancelled:
		currentFlow.chooser = nil
	case picker.Chosen:
		currentFlow.chooser = nil
		return currentFlow.start(environment, currentFlow.chooserVerdicts[result.Index])
	case picker.Open:
	}
	return nil
}

// start fetches the pending review (whose body pre-fills the editor); the
// editor opens when it arrives.
func (currentFlow *reviewFlow) start(environment reviewEnvironment, event github.ReviewEvent) tea.Cmd {
	if notice := environment.guard.admit(writeReview); notice != nil {
		return notice
	}
	if environment.ownPullRequest && event != github.ReviewEventComment {
		return userinterface.Info(ownPullRequestText)
	}
	currentFlow.sequence++
	flow, sequence := currentFlow, currentFlow.sequence
	return func() tea.Msg {
		ctx, cancel := environment.timeouts.RequestContext()
		defer cancel()
		status, err := environment.client.GetReviewStatus(ctx, environment.repository, environment.number)
		return reviewPendingMessage{flow: flow, repository: environment.repository, number: environment.number, sequence: sequence, event: event, pending: status.Pending, err: err}
	}
}

// pendingFetched opens the editor on the saved draft, or the pending
// review's body.
func (currentFlow *reviewFlow) pendingFetched(message reviewPendingMessage, environment reviewEnvironment) tea.Cmd {
	if message.err != nil {
		return userinterface.Error(terminaltext.SafeError(fmt.Errorf("check review state: %w", message.err)))
	}
	initial := currentFlow.draft
	if initial == "" && message.pending != nil {
		initial = message.pending.Body
	}
	flow, sequence, event := currentFlow, currentFlow.sequence, message.event
	return environment.edit(initial, func(body string, err error) tea.Msg {
		return reviewEditedMessage{flow: flow, repository: environment.repository, number: environment.number, sequence: sequence, event: event, body: body, err: err}
	})
}

// edited keeps a non-empty body as the draft, then re-queries the state to
// confirm against. An empty body never overwrites an earlier draft, and is
// not stored: it is only sent where that is allowed.
func (currentFlow *reviewFlow) edited(message reviewEditedMessage, environment reviewEnvironment) tea.Cmd {
	body := strings.TrimRight(message.body, " \t\r\n")
	if body != "" {
		currentFlow.draft = body
	}
	if message.err != nil {
		return userinterface.Error(terminaltext.SafeError(fmt.Errorf("edit review: %w", message.err)))
	}
	flow, sequence, event := currentFlow, currentFlow.sequence, message.event
	return func() tea.Msg {
		ctx, cancel := environment.timeouts.RequestContext()
		defer cancel()
		status, err := environment.client.GetReviewStatus(ctx, environment.repository, environment.number)
		return reviewFreshMessage{flow: flow, repository: environment.repository, number: environment.number, sequence: sequence, event: event, body: body, status: status, err: err}
	}
}

// confirm applies the checks that need fresh state, then asks.
func (currentFlow *reviewFlow) confirm(message reviewFreshMessage, environment reviewEnvironment) tea.Cmd {
	if message.err != nil {
		return userinterface.Error(terminaltext.SafeError(fmt.Errorf("check review state: %w", message.err)))
	}
	status, body := message.status, message.body
	// The review applies to the commit whose diff is on screen. If that is
	// not the head now (or the diff is not settled), the user must refresh.
	flow := currentFlow
	if environment.diffPending || status.HeadSHA != environment.diffHead {
		refresh := func() tea.Msg {
			return refreshMessage{flow: flow, repository: environment.repository, number: environment.number}
		}
		return func() tea.Msg {
			return userinterface.ConfirmMessage{Text: headMovedText, Notice: true, OnDismiss: refresh}
		}
	}
	inline := 0
	if status.Pending != nil {
		inline = status.Pending.CommentCount
	}
	if message.event != github.ReviewEventApprove && strings.TrimSpace(body) == "" && inline == 0 {
		return userinterface.Error(errors.New("empty review, nothing sent (did the editor fail?)"))
	}
	send := func() tea.Msg {
		return reviewSendMessage{flow: flow, repository: environment.repository, number: environment.number, event: message.event, body: body, status: status}
	}
	return userinterface.Confirm(reviewQuestion(message.event, environment.repository, environment.number, status.Pending), send)
}

// submit is the command that runs when the user confirms. The commit is the
// head the diff on screen was loaded for, which confirm has just checked is
// still the head. On failure it also returns the error itself, so it shows
// even if the view was left meanwhile.
func (currentFlow *reviewFlow) submit(environment reviewEnvironment, event github.ReviewEvent, body string, status github.ReviewStatus) tea.Cmd {
	flow := currentFlow
	in := github.SubmitReviewInput{
		Repository: environment.repository, Number: environment.number, PullRequestIdentifier: status.PullRequestIdentifier,
		Event: event, Body: body, CommitObjectIdentifier: status.HeadSHA,
	}
	if status.Pending != nil {
		in.PendingReviewIdentifier = status.Pending.Identifier
	}
	return func() tea.Msg {
		ctx, cancel := environment.timeouts.RequestContext()
		defer cancel()
		err := userinterface.WriteError(environment.client.SubmitReview(ctx, in))
		result := reviewSubmittedMessage{flow: flow, repository: environment.repository, number: environment.number, event: event, body: body, err: err}
		if err == nil {
			return result
		}
		return withShownError(result, submitFailure(err, in.PendingReviewIdentifier != ""))
	}
}

func (currentFlow *reviewFlow) submitted(message reviewSubmittedMessage, environment reviewEnvironment) tea.Cmd {
	currentFlow.submitting = false
	if message.err != nil {
		// The draft stays for the next attempt. Whatever went wrong, the
		// review may or may not have landed, so show the current state.
		return environment.reload()
	}
	currentFlow.draft = ""
	return tea.Batch(userinterface.Info(submittedText(message.event, environment.number)), environment.reload())
}
