package workspace

import (
	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/components/diffview"
)

// level is the kind of thing an action works on. Each action has one. The
// resolver below handles the diff cursor's levels (lines, thread, comment);
// the others are declared in the registry and resolved by the action itself.
type level int

const (
	levelNone        level = iota // moves around; works on nothing
	levelPullRequest              // the pull request as a whole
	levelFile                     // the selected file
	levelHunk                     // the hunk under the diff cursor
	levelLines                    // the line or selection a new comment goes on
	levelThread                   // the comment thread under the cursor
	levelComment                  // one comment of an open thread
)

// resolved reports whether the resolver turns the level into a target.
func (level level) resolved() bool { return level >= levelLines }

// request is what an action asks the resolver for.
type request struct {
	level level
	// words finish the notices: "reply", "resolve it", "react to it". The
	// focus notice is "focus the diff to " + words; the several-threads one
	// ends in " to " + words; the lines one is "move onto a diff line to " +
	// words.
	words string
	// rule narrows a comment-level target to the comments the action may use;
	// nil allows every comment.
	rule *commentRule
}

// commentRule says which comments an action may use and what it says about
// the others.
type commentRule struct {
	// mine reports whether the comment is the viewer's.
	mine func(commentTarget) bool
	// allowed reports whether the viewer may use their own comment.
	allowed func(commentTarget) bool
	// notMine is said when the cursor is not on one of the viewer's comments.
	notMine string
	// cannot is said when it is the viewer's comment but not allowed.
	cannot string
	// unsupported, if set, refuses a comment of a kind the action cannot
	// change yet; it comes last, after the rights.
	unsupported func(commentTarget) tea.Cmd
}

// resolvedTarget is what the cursor context gives the action. Only the field
// of the requested level is set.
type resolvedTarget struct {
	lines   diffview.Range
	thread  github.ReviewThread
	comment commentTarget
}

// resolveTarget turns the diff cursor into the target the request names, or
// returns the command that tells why there is none. The caller has checked
// that the diff pane is on screen (diffHidden) where the action needs it.
func (view *View) resolveTarget(request request) (resolvedTarget, tea.Cmd) {
	var context diffview.Context
	if view.selection() > 0 { // the Overview has no diff cursor
		context = view.filePane.Context()
	}
	switch request.level {
	case levelLines:
		if !context.HasLines {
			return resolvedTarget{}, userinterface.Info("move onto a diff line to " + request.words)
		}
		return resolvedTarget{lines: context.Lines}, nil
	case levelThread:
		switch len(context.Threads) {
		case 0:
			return resolvedTarget{}, userinterface.Info("no comment thread here")
		case 1:
			return resolvedTarget{thread: context.Threads[0]}, nil
		}
		return resolvedTarget{}, userinterface.Info("several threads on this line: move onto one to " + request.words)
	}
	rule := request.rule
	var comment commentTarget
	if context.OnComment {
		comment = reviewCommentTarget(context.Comment, context.CommentThread)
	}
	switch {
	case !context.OnComment:
		if rule != nil {
			return resolvedTarget{}, userinterface.Info(rule.notMine)
		}
		return resolvedTarget{}, userinterface.Info("move onto a comment to " + request.words)
	case rule != nil && !rule.mine(comment):
		return resolvedTarget{}, userinterface.Info(rule.notMine)
	case rule != nil && !rule.allowed(comment):
		return resolvedTarget{}, userinterface.Info(rule.cannot)
	}
	if rule != nil && rule.unsupported != nil {
		if refusal := rule.unsupported(comment); refusal != nil {
			return resolvedTarget{}, refusal
		}
	}
	return resolvedTarget{comment: comment}, nil
}

// diffHidden returns the notice to show when the diff pane is not on screen (a
// narrow terminal showing the Files pane), or nil. The cursor of a pane the
// user cannot see is not something to act on.
func (view *View) diffHidden(words string) tea.Cmd {
	if _, rightWidth := view.paneWidths(); rightWidth == 0 {
		return userinterface.Info("focus the diff to " + words)
	}
	return nil
}
