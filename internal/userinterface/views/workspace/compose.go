package workspace

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MikePors/strigidae/internal/editor"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/terminaltext"
)

// topLevelDraft is the draft key of the top-level comment; replies are keyed
// by thread ID.
const topLevelDraft = "top-level"

// EditFunction opens the editor on initial and, when it closes, delivers the
// result as the message done builds. The default runs $VISUAL or $EDITOR
// through tea.ExecProcess; tests inject their own.
type EditFunction func(initial string, done func(body string, err error) tea.Msg) tea.Cmd

// WithEditor replaces how comment bodies are edited.
func WithEditor(edit EditFunction) Option { return func(view *View) { view.edit = edit } }

// targetKind says what a comment being written is for.
type targetKind int

const (
	kindTopLevel targetKind = iota // a comment on the pull request
	kindReply                      // a reply to a review thread
	kindInline                     // a new inline comment in the pending review
	kindEdit                       // a change to one of the viewer's own comments
)

// target is what a comment is written for; kind says which fields apply.
type target struct {
	kind targetKind
	// threadIdentifier is the thread replied to (kindReply) or the one the edited
	// comment is in (kindEdit).
	threadIdentifier string
	label            string // "path:line" of the thread, for the questions about it
	// inline (kindInline) is the new comment's position; the repo, number and
	// pending review are filled in when it is sent. head is the commit of the
	// diff the lines were picked on.
	inline *github.AddThreadInput
	head   string
	// commentIdentifier (kindEdit) is the comment, pending or published, being
	// edited; initial is its body when the editor was opened, shown there
	// when no draft is kept.
	commentIdentifier string
	initial           string
	// home (kindEdit) is where the thread is in the diff: when the comment's
	// thread is gone, the text is kept as the draft of a new comment there.
	home *github.AddThreadInput
}

func (target target) draftKey() string {
	switch target.kind {
	case kindInline:
		in := target.inline
		return fmt.Sprintf("inline:%s:%s:%d-%d", in.Path, in.Side, in.StartLine, in.Line)
	case kindEdit:
		return "edit:" + target.commentIdentifier
	case kindReply:
		return target.threadIdentifier
	}
	return topLevelDraft
}

// Results of composing carry the same identity as load results. An edit
// result also carries the compose sequence, so starting a new edit makes older
// ones stale; a post result does not, because a later compose does not
// supersede a post.
type (
	editedMessage struct {
		owner      *View
		repository github.Repository
		number     int
		sequence   int
		target     target
		body       string
		err        error
	}
	postedMessage struct {
		origin origin
		target target
		body   string
		err    error
		// threadLive is, for an edit refused as stale, whether the comment's
		// thread still exists (it is gone with its only comment).
		threadLive bool
	}
)

// origin is the identity of the workspace and pull request a result belongs
// to: every view in the stack receives every result, so each ignores the ones
// that are not its own.
type origin struct {
	owner      *View // compared, never dereferenced
	repository github.Repository
	number     int
}

func (view *View) origin() origin {
	return origin{owner: view, repository: view.repository, number: view.number}
}

// owns reports whether a result with the given origin was asked for by this view.
func (view *View) owns(origin origin) bool { return origin == view.origin() }

func (view *View) composeMine(origin origin, sequence int) bool {
	return view.owns(origin) && sequence == view.commentSequence
}

// reply starts a reply to the thread under the diff cursor.
func (view *View) reply(thread github.ReviewThread) tea.Cmd {
	return view.compose(target{kind: kindReply, threadIdentifier: thread.Identifier, label: threadLocation(thread)})
}

// overviewComment starts a top-level comment on the pull request, whatever is
// selected or focused.
func (view *View) overviewComment() tea.Cmd {
	return view.compose(target{})
}

// compose opens the editor, pre-filled with the draft for target if there is one.
func (view *View) compose(target target) tea.Cmd {
	view.commentSequence++
	owner, repository, number, sequence := view, view.repository, view.number, view.commentSequence
	initial := view.drafts[target.draftKey()]
	if initial == "" {
		initial = target.initial
	}
	return view.edit(initial, func(body string, err error) tea.Msg {
		return editedMessage{owner: owner, repository: repository, number: number, sequence: sequence, target: target, body: body, err: err}
	})
}

// edited asks for confirmation once the editor closed with a body.
func (view *View) edited(message editedMessage) tea.Cmd {
	body := strings.TrimRight(message.body, " \t\r\n")
	if message.err != nil {
		// Whatever was typed is kept, so the next try starts from it.
		if strings.TrimSpace(body) != "" {
			view.drafts[message.target.draftKey()] = body
		}
		return userinterface.Error(terminaltext.SafeError(fmt.Errorf("edit comment: %w", message.err)))
	}
	if strings.TrimSpace(body) == "" {
		return userinterface.Error(errors.New("empty comment, nothing posted (did the editor fail?)"))
	}
	// Kept until the post succeeds, so declining or a failure loses nothing.
	view.drafts[message.target.draftKey()] = body
	// A pending comment is a private draft, so it is sent without asking;
	// sendEdit asks before changing a published one.
	switch message.target.kind {
	case kindInline, kindEdit:
		// The diff being reloaded does not matter now: the head check in the
		// send covers it. The text stays a draft if the send is refused.
		if busy := view.guardState().busy(writeComment); busy != nil {
			return busy
		}
		if message.target.kind == kindInline {
			return view.sendInline(message.target, body)
		}
		return view.sendEdit(message.target, body)
	}
	return userinterface.Confirm(view.question(message.target, body), view.agreed(postKind(message.target), view.post(message.target, body)))
}

// postKind is the write kind of posting a reply or a top-level comment.
func postKind(target target) writeKind {
	if target.kind == kindReply {
		return writeComment
	}
	return writeOverviewComment
}

// question is the confirmation text: where the text goes and its first line.
func (view *View) question(target target, body string) string {
	prefix := "Post comment?"
	if target.kind == kindReply {
		prefix = "Post reply to " + terminaltext.Clean(target.label) + "?"
	}
	first := firstLine(body)
	// Leave room for the breadcrumbs, the quotes and " y/N".
	room := max(10, view.width-ansi.StringWidth(prefix)-ansi.StringWidth(view.Title())-14)
	return prefix + ` "` + ansi.Truncate(first, room, "…") + `"`
}

func firstLine(body string) string {
	for line := range strings.SplitSeq(terminaltext.CleanBlock(body), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// post is the command that runs when the user confirms. On failure it also
// returns the error itself, so it shows even if the view was left meanwhile.
func (view *View) post(target target, body string) tea.Cmd {
	client, origin, timeouts := view.client, view.origin(), view.timeouts
	return func() tea.Msg {
		ctx, cancel := timeouts.RequestContext()
		defer cancel()
		var err error
		if target.kind == kindReply {
			_, err = client.ReplyToThread(ctx, target.threadIdentifier, body)
		} else {
			_, err = client.AddComment(ctx, origin.repository, origin.number, body)
		}
		err = userinterface.WriteError(err)
		result := postedMessage{origin: origin, target: target, body: body, err: err}
		if err == nil {
			return result
		}
		what := "comment"
		if target.kind == kindReply {
			what = "reply"
		}
		return withShownError(result, terminaltext.SafeError(fmt.Errorf("post %s: %w", what, err)))
	}
}

func (view *View) posted(message postedMessage) tea.Cmd {
	view.writeDone()
	if isStale(message.err) {
		return view.staleEdit(message)
	}
	if userinterface.IsWriteTimeout(message.err) {
		return view.reloadAfterPost(message.target.kind) // the draft stays; GitHub may have the comment
	}
	if message.err != nil {
		return nil // the draft stays; the command already reported the error
	}
	// A newer draft for the same target must survive.
	if view.drafts[message.target.draftKey()] == message.body {
		delete(view.drafts, message.target.draftKey())
	}
	return tea.Batch(userinterface.Info(postedNotice(message.target.kind)), view.reloadAfterPost(message.target.kind))
}

// postedNotice is the status text for a comment of the given kind that was saved.
func postedNotice(kind targetKind) string {
	switch kind {
	case kindReply:
		return "Reply posted"
	case kindInline:
		return "Pending comment added"
	case kindEdit:
		return "Comment updated"
	}
	return "Comment posted"
}

// reloadAfterPost refreshes what a comment of the given kind changes.
func (view *View) reloadAfterPost(kind targetKind) tea.Cmd {
	switch kind {
	case kindReply:
		return view.reloadThreads()
	case kindInline, kindEdit:
		return view.reloadPendingComments()
	}
	return view.reloadPullRequest()
}

// threadLocation is "path:line" for a thread.
func threadLocation(thread github.ReviewThread) string {
	line := thread.Line
	if line == 0 {
		line = thread.OriginalLine
	}
	if line == 0 {
		return thread.Path
	}
	return thread.Path + ":" + strconv.Itoa(line)
}

// ExecEditor is the default EditFunction (also used by the pull request
// list): a temporary file opened in the user's editor, with the TUI suspended
// meanwhile.
func ExecEditor(initial string, done func(string, error) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		session, err := editor.New(initial, os.Getenv)
		if err != nil {
			return done("", err)
		}
		return tea.ExecProcess(session.Command(), func(runErr error) tea.Msg {
			body, readErr := session.Result()
			return done(body, errors.Join(runErr, readErr))
		})()
	}
}
