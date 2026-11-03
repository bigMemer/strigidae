package workspace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/components/diffview"
	"github.com/MikePors/strigidae/internal/userinterface/terminaltext"
)

// Inline comments are written on diff lines and collect in the viewer's
// pending review. Adding or editing a pending comment asks nothing (private
// drafts). Deleting any comment, and editing a published one, asks y/N.

// commentDeletedMessage is the result of deleting a comment (or of a refusal
// before the question). Like a post it is identified by its origin only.
type commentDeletedMessage struct {
	origin origin
	err    error
}

// commentCheckedMessage carries a question for the user, asked after a comment
// was re-read: onYes runs on yes.
type commentCheckedMessage struct {
	origin   origin
	question string
	onYes    tea.Cmd
}

// commentAgreedMessage is what a "yes" produces: the write that was asked about.
// It goes through the view so the write is counted as in flight and refused
// when a review or merge is.
type commentAgreedMessage struct {
	origin origin
	kind   writeKind // what the write is, for the guard
	run    tea.Cmd
}

// commentReadMessage is the result of re-reading a comment before the editor
// opens on it: tgt holds the fresh body, or err why it cannot be edited.
type commentReadMessage struct {
	origin   origin
	sequence int // the compose sequence it was asked under; a later compose supersedes it
	target   target
	err      error
}

// staleError is why a comment cannot be changed any more: what is on screen
// is out of date, so the view refreshes.
type staleError string

func (staleError staleError) Error() string { return string(staleError) }

const (
	// errCommentGone: the comment does not exist any more.
	errCommentGone = staleError("that comment is gone; refreshed")
	// errNotYours: the viewer has write access, so GitHub lets them change the
	// comment, but somebody else wrote it.
	errNotYours = staleError("that comment is not yours; refreshed")
	// errCannotChange: the viewer wrote the comment but may not change it now
	// (a locked conversation, an archived repository).
	errCannotChange = staleError("you can no longer change that comment; refreshed")
)

// isStale reports whether err says the comment on screen is out of date.
func isStale(err error) bool {
	var staleFailure staleError
	return errors.As(err, &staleFailure)
}

// change is what is done to a comment.
type change int

const (
	changeEdit change = iota
	changeDelete
)

// permits reports whether the viewer may make the change: they wrote the
// comment and GitHub allows it. Write access makes the can-flags true on other
// people's comments too, so authorship is checked as well.
func (change change) permits(didAuthor, canUpdate, canDelete bool) bool {
	if !didAuthor {
		return false
	}
	if change == changeDelete {
		return canDelete
	}
	return canUpdate
}

// freshComment re-reads comment identifier and returns it, or a staleError when it is
// missing, not the viewer's, or not changeable by them. The fresh state, not
// the one on screen, decides what to ask.
func freshComment(ctx context.Context, client github.Client, identifier string, change change) (github.ReviewComment, error) {
	comment, err := client.GetReviewComment(ctx, identifier)
	switch {
	case errors.Is(err, github.ErrNotFound):
		return comment, errCommentGone
	case err != nil:
		return comment, fmt.Errorf("check comment: %w", err)
	case !comment.ViewerDidAuthor:
		return comment, errNotYours
	case !change.permits(comment.ViewerDidAuthor, comment.ViewerCanUpdate, comment.ViewerCanDelete):
		return comment, errCannotChange
	}
	return comment, nil
}

// answer is what the user has agreed to: nothing yet, or a yes to a question
// asked about a pending (or published) comment.
type answer struct{ given, pending bool }

// yes is the answer to the question asked about a comment in that state.
func yes(pending bool) answer { return answer{given: true, pending: pending} }

// covers reports whether the answer was given for a comment in that state.
func (answer answer) covers(pending bool) bool { return answer.given && answer.pending == pending }

// commentWrite is one guarded write to a comment of the viewer's. It re-reads
// the comment first and asks before writing when ask has a question for the
// comment's state right now; the answer is good only for the state that was
// asked about. A comment that changed state while the question was open is
// asked about again, so nothing is written on a stale yes.
type commentWrite struct {
	origin     origin
	client     github.Client
	timeouts   userinterface.Timeouts
	identifier string
	change     change
	// ask returns the question for a comment that is pending or published, or
	// "" to write without asking.
	ask    func(pending bool) string
	write  func(ctx context.Context, client github.Client) error
	result func(err error) tea.Msg
}

// run re-reads the comment and writes, or asks first. given is what the user
// has already said yes to (the zero answer before any question).
func (write commentWrite) run(given answer) tea.Msg {
	readContext, cancelRead := write.timeouts.RequestContext()
	defer cancelRead()
	fresh, err := freshComment(readContext, write.client, write.identifier, write.change)
	if err != nil {
		return write.result(err)
	}
	if query := write.ask(fresh.Pending); query != "" && !given.covers(fresh.Pending) {
		pending := fresh.Pending
		again := func() tea.Msg { return write.run(yes(pending)) }
		return commentCheckedMessage{origin: write.origin, question: query, onYes: agreed(write.origin, writeComment, again)}
	}
	writeContext, cancelWrite := write.timeouts.RequestContext()
	defer cancelWrite()
	err = userinterface.WriteError(write.write(writeContext, write.client))
	if errors.Is(err, github.ErrNotFound) {
		err = errCommentGone // deleted between the re-read and the write
	}
	return write.result(err)
}

// agreed wraps the write to run on a yes, so the view sees the yes first.
func agreed(origin origin, kind writeKind, run tea.Cmd) tea.Cmd {
	return func() tea.Msg { return commentAgreedMessage{origin: origin, kind: kind, run: run} }
}

// agreed is agreed for this view's origin.
func (view *View) agreed(kind writeKind, run tea.Cmd) tea.Cmd {
	return agreed(view.origin(), kind, run)
}

// commentAgreed starts the write a yes was given for, unless the guard says
// something is busy now; the write then counts as in flight until its result
// arrives.
func (view *View) commentAgreed(message commentAgreedMessage) tea.Cmd {
	if busy := view.guardState().busy(message.kind); busy != nil {
		return busy
	}
	view.writeStarted()
	return message.run
}

// withShownError returns result together with an error to show, so the error
// shows even if the view was left meanwhile.
func withShownError(result tea.Msg, shown error) tea.Msg {
	return tea.BatchMsg{func() tea.Msg { return result }, func() tea.Msg { return userinterface.ErrorMessage{Err: shown} }}
}

// writeStarted and writeDone count the comment writes in flight; the count
// never goes below zero.
func (view *View) writeStarted() { view.writes++ }

func (view *View) writeDone() { view.writes = max(0, view.writes-1) }

// reloadPendingComments refreshes what shows pending comments: the threads in
// the diff, and the pull request (whose reviews list the pending review).
func (view *View) reloadPendingComments() tea.Cmd {
	return tea.Batch(view.reloadPullRequest(), view.reloadThreads())
}

// inlineComment starts a comment on the diff line under the cursor, or on the
// selected range. The comment remembers the commit of the diff its line
// numbers come from.
func (view *View) inlineComment(lineRange diffview.Range) tea.Cmd {
	view.filePane.VisualEnd()
	in := &github.AddThreadInput{Path: lineRange.Path, Line: lineRange.End, Side: lineRange.Side}
	if lineRange.Start != lineRange.End {
		in.StartLine, in.StartSide = lineRange.Start, lineRange.Side
	}
	return view.compose(target{kind: kindInline, inline: in, head: view.diffHead})
}

// startVisual enters visual-line mode on the diff line under the cursor.
func (view *View) startVisual() tea.Cmd {
	if !view.filePane.VisualStart() {
		return userinterface.Info("move onto a diff line to select lines")
	}
	view.focus = paneRight
	return userinterface.Info("-- VISUAL LINE --  j/k extend, c comment, esc cancel")
}

// visualKey handles a raw key while a line selection is in progress. The
// workspace captures keys then, and esc always leaves.
func (view *View) visualKey(message tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(message, view.bind.VisualExit):
		view.filePane.VisualEnd()
	case key.Matches(message, view.bind.VisualDown):
		return refusal(view.filePane.VisualMove(1))
	case key.Matches(message, view.bind.VisualUp):
		return refusal(view.filePane.VisualMove(-1))
	case key.Matches(message, view.bind.Comment):
		return view.invoke(view.entryNamed("new-comment"), invocation{})
	}
	return nil
}

func refusal(err error) tea.Cmd {
	if err == nil {
		return nil
	}
	return userinterface.Info(err.Error())
}

// commentRuleFor says which comments e and D (and the commands) may change
// and what to tell about the others: only the viewer's own, where GitHub lets
// them make the change, and only comments of a review thread.
func commentRuleFor(change change) *commentRule {
	words := "edit it"
	if change == changeDelete {
		words = "delete it"
	}
	return &commentRule{
		mine: func(target commentTarget) bool { return target.viewerDidAuthor },
		allowed: func(target commentTarget) bool {
			return change.permits(target.viewerDidAuthor, target.viewerCanUpdate, target.viewerCanDelete)
		},
		notMine:     "move onto a comment of yours to " + words,
		cannot:      "you can't " + strings.TrimSuffix(words, " it") + " this comment",
		unsupported: func(target commentTarget) tea.Cmd { return reviewCommentOnly(target, words) },
	}
}

// reviewCommentOnly refuses a target that is not a comment of a review thread:
// e and D work through the thread, and the other kinds have no actions yet.
func reviewCommentOnly(target commentTarget, words string) tea.Cmd {
	if target.kind == commentReview && target.thread != nil {
		return nil
	}
	return userinterface.Info("you can't " + strings.TrimSuffix(words, " it") + " this comment here")
}

// editComment re-reads the viewer's comment at the cursor, then opens the
// editor on its current body, so edits made elsewhere are not overwritten.
func (view *View) editComment(comment commentTarget) tea.Cmd {
	thread := *comment.thread
	origin, client, timeouts := view.origin(), view.client, view.timeouts
	currentTarget := target{kind: kindEdit, commentIdentifier: comment.identifier, threadIdentifier: thread.Identifier, label: threadLocation(thread), home: view.reachableHome(thread)}
	view.commentSequence++ // a compose started meanwhile supersedes this read
	sequence := view.commentSequence
	return func() tea.Msg {
		ctx, cancel := timeouts.RequestContext()
		defer cancel()
		fresh, err := freshComment(ctx, client, currentTarget.commentIdentifier, changeEdit)
		currentTarget.initial = fresh.Body
		return commentReadMessage{origin: origin, sequence: sequence, target: currentTarget, err: err}
	}
}

// reachableHome is the position of a thread as `c` would write a comment
// there, or nil when `c` cannot reach it: the thread has no line (outdated and
// file-level threads) or its lines are not in the diff on screen.
func (view *View) reachableHome(thread github.ReviewThread) *github.AddThreadInput {
	if thread.Line == 0 || !view.lineInDiff(thread.Path, thread.DiffSide, thread.Line) || (thread.StartLine > 0 && !view.lineInDiff(thread.Path, thread.StartDiffSide, thread.StartLine)) {
		return nil
	}
	return &github.AddThreadInput{Path: thread.Path, Side: thread.DiffSide, Line: thread.Line, StartLine: thread.StartLine, StartSide: thread.StartDiffSide}
}

// lineInDiff reports whether the diff on screen has line n of path on side.
func (view *View) lineInDiff(path string, side github.DiffSide, count int) bool {
	if view.diff.Parsed == nil {
		return false
	}
	file := view.diff.Parsed.File(path)
	if file == nil {
		return false
	}
	for _, hunk := range file.Hunks {
		for _, line := range hunk.Lines {
			if (side == github.SideLeft && line.Old == count) || (side != github.SideLeft && line.New == count) {
				return true
			}
		}
	}
	return false
}

// commentRead opens the editor on a freshly read comment, or refuses.
func (view *View) commentRead(message commentReadMessage) tea.Cmd {
	if message.err != nil {
		return view.refuse(message.err, "read comment")
	}
	if busy := view.guardState().busy(writeComment); busy != nil {
		return busy
	}
	return view.compose(message.target)
}

// refuse shows why a comment cannot be changed, and refreshes when what is on
// screen turned out to be stale.
func (view *View) refuse(err error, what string) tea.Cmd {
	command := userinterface.Error(commentFailure(err, what))
	if isStale(err) {
		return tea.Batch(command, view.reloadPendingComments())
	}
	return command
}

// deleteComment re-reads the comment at the cursor, then asks before deleting
// it. The question says whether it is pending or published.
func (view *View) deleteComment(comment commentTarget) tea.Cmd {
	origin, identifier, label := view.origin(), comment.identifier, terminaltext.Clean(threadLocation(*comment.thread))
	write := commentWrite{
		origin: origin, client: view.client, timeouts: view.timeouts, identifier: identifier, change: changeDelete,
		ask: func(pending bool) string {
			if pending {
				return "Delete pending comment on " + label + "?"
			}
			return fmt.Sprintf("Delete your published comment on #%d %s?", origin.number, label)
		},
		write: func(ctx context.Context, client github.Client) error {
			return client.DeleteReviewComment(ctx, identifier)
		},
		result: func(err error) tea.Msg {
			result := commentDeletedMessage{origin: origin, err: err}
			if err == nil {
				return result
			}
			return withShownError(result, commentFailure(err, "delete comment"))
		},
	}
	view.writeStarted()
	return func() tea.Msg { return write.run(answer{}) }
}

// commentFailure is the error to show for a failed comment call: the stale
// reason as it is, any other wrapped with what was being done.
func commentFailure(err error, what string) error {
	var staleFailure staleError
	if errors.As(err, &staleFailure) {
		return staleFailure
	}
	return terminaltext.SafeError(fmt.Errorf("%s: %w", what, err))
}

func (view *View) commentDeleted(message commentDeletedMessage) tea.Cmd {
	view.writeDone()
	if isStale(message.err) || userinterface.IsWriteTimeout(message.err) {
		return view.reloadPendingComments() // the error is already shown
	}
	if message.err != nil {
		return nil
	}
	return tea.Batch(userinterface.Info("Comment deleted"), view.reloadPendingComments())
}

// sendEdit updates a comment of the viewer's. Updating a pending comment runs
// without a prompt; updating a published one asks first. A failed or refused
// edit never throws the text away (see editFailure and staleEdit).
func (view *View) sendEdit(target target, body string) tea.Cmd {
	// What the editor returned is trimmed; so is what was offered to it.
	if body == strings.TrimRight(target.initial, " \t\r\n") {
		delete(view.drafts, target.draftKey())
		return userinterface.Info("comment unchanged")
	}
	origin, client, timeouts, label := view.origin(), view.client, view.timeouts, terminaltext.Clean(target.label)
	write := commentWrite{
		origin: origin, client: client, timeouts: timeouts, identifier: target.commentIdentifier, change: changeEdit,
		ask: func(pending bool) string {
			if pending {
				return ""
			}
			return fmt.Sprintf("Update your published comment on #%d %s?", origin.number, label)
		},
		write: func(ctx context.Context, client github.Client) error {
			_, err := client.UpdateReviewComment(ctx, target.commentIdentifier, body)
			return err
		},
		result: func(err error) tea.Msg {
			result := postedMessage{origin: origin, target: target, body: body, err: err}
			if err == nil {
				return result
			}
			if isStale(err) {
				result.threadLive = threadExists(client, timeouts, origin, target.threadIdentifier)
			}
			return withShownError(result, editFailure(err, target, result.threadLive))
		},
	}
	view.writeStarted()
	return func() tea.Msg { return write.run(answer{}) }
}

// threadExists reports whether a thread is still on the pull request. When the
// threads cannot be listed it says no, which keeps the text where `c` finds it.
func threadExists(client github.Client, timeouts userinterface.Timeouts, origin origin, threadIdentifier string) bool {
	ctx, cancel := timeouts.LoadContext()
	defer cancel()
	threads, err := loadThreads(ctx, client, origin.repository, origin.number)
	return err == nil && slices.ContainsFunc(threads, func(thread github.ReviewThread) bool { return thread.Identifier == threadIdentifier })
}

// homeKey is the draft a refused edit's text goes to: the reply draft of its
// thread while the thread exists, else the draft of a new comment on its lines
// when `c` can reach them, else the top-level draft.
func homeKey(message postedMessage) string {
	switch {
	case message.threadLive:
		return target{kind: kindReply, threadIdentifier: message.target.threadIdentifier}.draftKey()
	case message.target.home != nil:
		return target{kind: kindInline, inline: message.target.home}.draftKey()
	}
	return target{}.draftKey()
}

// editFailure is the error shown for a failed edit, saying where its text is
// kept. A refused edit (a stale comment) goes to the thread's reply draft, or
// when the thread is gone too to the draft for a new comment on its lines (or,
// when `c` cannot reach them, the top-level draft); any other failure leaves the text under the comment's own draft, which `e` finds.
func editFailure(err error, target target, threadLive bool) error {
	var staleFailure staleError
	if !errors.As(err, &staleFailure) {
		return commentFailure(fmt.Errorf("%w; the comment is kept as a draft", err), "update comment")
	}
	switch {
	case threadLive:
		return staleError(string(staleFailure) + "; your text is kept as a reply draft")
	case target.home != nil:
		return staleError(string(staleFailure) + "; your text is kept as a draft for a new comment on " + terminaltext.Clean(lineLabel(target.home)))
	}
	return staleError(string(staleFailure) + "; your text is kept as a draft for a top-level comment")
}

// lineLabel is "path:line" or "path:start-line" for a position.
func lineLabel(in *github.AddThreadInput) string {
	if in.StartLine > 0 && in.StartLine != in.Line {
		return fmt.Sprintf("%s:%d-%d", in.Path, in.StartLine, in.Line)
	}
	return fmt.Sprintf("%s:%d", in.Path, in.Line)
}

// joinDraft appends text to an existing draft, separated by a blank line,
// unless that draft already is the text.
func joinDraft(old, text string) string {
	if old == "" || old == text {
		return text
	}
	return old + "\n\n" + text
}

// staleEdit handles an edit refused because its comment is stale: it
// refreshes, and moves the text to its new home (see homeKey), joined with any
// draft already there. Whether the thread still exists was read after the
// refusal, in the command, because the refreshed list arrives too late and may
// be an older snapshot.
func (view *View) staleEdit(message postedMessage) tea.Cmd {
	if message.target.threadIdentifier != "" {
		key := homeKey(message)
		view.drafts[key] = joinDraft(view.drafts[key], message.body)
		delete(view.drafts, message.target.draftKey())
	}
	return view.reloadPendingComments()
}

// sendInline adds a new inline comment to the pending review (creating the
// review if there is none), anchored to the commit the lines were picked on.
func (view *View) sendInline(target target, body string) tea.Cmd {
	// A diff load in flight is no obstacle: the lines are anchored to target.head,
	// and addPendingThread refuses unless the server is still on it. (The caller
	// checked that the pull request is loaded.)
	origin, client, timeouts := view.origin(), view.client, view.timeouts
	in := *target.inline
	in.Repository, in.Number, in.Body = origin.repository, origin.number, body
	pullRequestIdentifier := view.detail.Identifier
	view.writeStarted()
	return func() tea.Msg {
		err := addPendingThread(timeouts, client, in, pullRequestIdentifier, target.head)
		result := postedMessage{origin: origin, target: target, body: body, err: err}
		if err == nil {
			return result
		}
		return withShownError(result, commentFailure(fmt.Errorf("%w; the comment is kept as a draft", err), "add pending comment"))
	}
}

// addPendingThread adds in to the viewer's pending review, creating it on
// head first when there is none. The comment's line numbers come from the diff
// of commit head, so it is refused when the pull request has moved on or the
// existing pending review is anchored to another (or an unknown) commit.
func addPendingThread(timeouts userinterface.Timeouts, client github.Client, in github.AddThreadInput, pullRequestIdentifier, head string) error {
	statusContext, cancelStatus := timeouts.RequestContext()
	defer cancelStatus()
	status, err := client.GetReviewStatus(statusContext, in.Repository, in.Number)
	if err != nil {
		return fmt.Errorf("check review state: %w", err)
	}
	if status.HeadSHA != head {
		return errors.New("head moved since you loaded the diff; refresh first")
	}
	pending := status.Pending
	created := false
	switch {
	case pending == nil:
		startContext, cancelStart := timeouts.RequestContext()
		defer cancelStart()
		pendingReview, err := client.StartReview(startContext, github.StartReviewInput{Repository: in.Repository, Number: in.Number, PullRequestIdentifier: pullRequestIdentifier, CommitObjectIdentifier: head})
		if err != nil {
			return fmt.Errorf("start review: %w", userinterface.WriteError(err))
		}
		pending, created = &pendingReview, true
	case pending.CommitObjectIdentifier == "":
		return errors.New("your pending review is on an unknown commit; submit or discard it on GitHub first")
	case pending.CommitObjectIdentifier != head:
		return errors.New("your pending review is on an older commit; submit it first, or delete it on GitHub")
	}
	in.PendingReviewIdentifier = pending.Identifier
	addContext, cancelAdd := timeouts.RequestContext()
	defer cancelAdd()
	_, err = client.AddReviewThread(addContext, in)
	// Only a refusal proves nothing was written. After a timeout or a network
	// error the comment may have gone through, so the review is left alone.
	if errors.Is(err, github.ErrCommentRejected) && created {
		discardEmptyReview(timeouts, client, pending.Identifier)
	}
	return userinterface.WriteError(err)
}

// discardEmptyReview deletes a pending review this call created and GitHub
// refused to put a comment in (ErrCommentRejected), so GitHub is not left
// showing an empty one. It is best
// effort: a failure is logged and the caller keeps the error that led here.
// It has a context of its own because the failed call may have used up its
// deadline.
func discardEmptyReview(timeouts userinterface.Timeouts, client github.Client, reviewIdentifier string) {
	ctx, cancel := timeouts.RequestContext()
	defer cancel()
	if err := client.DeleteReview(ctx, reviewIdentifier); err != nil {
		slog.Warn("delete empty pending review", "review", reviewIdentifier, "err", err)
	}
}
