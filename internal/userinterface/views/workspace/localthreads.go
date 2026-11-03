package workspace

import (
	"slices"
	"strings"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface"
)

// Authors of the two comments of a local thread.
const (
	localQuestionAuthor = "you"
	localAnswerAuthor   = "answer (local, not on GitHub)"
	localWaitingText    = "waiting for an answer…"
	localFailedPrefix   = "no answer: "
)

// localRefusal is what every action that would write to GitHub says about a
// local thread. It is the only gate: no identifier of a local thread reaches
// the client, because every write takes its target from the resolver.
func localRefusal(words string) string {
	return "this answer is local to strig, not on GitHub: nothing to " + strings.TrimSuffix(words, " it")
}

// setPaneThreads gives the diff pane the pull request's threads from GitHub
// and the local ones. view.threads stays GitHub's alone, so the Overview, the
// counts and every write look at real threads only.
func (view *View) setPaneThreads() {
	view.filePane.SetThreads(view.paneThreads())
}

func (view *View) paneThreads() []github.ReviewThread {
	all := slices.Clone(view.threads)
	for _, local := range view.localThreads {
		if local.Repository == view.repository && local.Number == view.number {
			all = append(all, localReviewThread(local, view.diffHead))
		}
	}
	return all
}

// localReviewThread draws a local thread as a review thread. It is outdated
// when it was asked on another head than the diff on screen, and then, like a
// GitHub one, it has no line in the diff and is listed after the last row.
func localReviewThread(local userinterface.LocalThread, diffHead string) github.ReviewThread {
	thread := github.ReviewThread{
		Identifier:    local.Identifier,
		SubjectType:   github.SubjectLine,
		Local:         local.State,
		IsOutdated:    local.Head != diffHead,
		Path:          local.Path,
		DiffSide:      local.Side,
		StartDiffSide: local.Side,
	}
	startLine := local.StartLine
	if startLine == local.Line {
		startLine = 0 // one line
	}
	if thread.IsOutdated {
		thread.OriginalLine, thread.OriginalStartLine = local.Line, startLine
	} else {
		thread.Line, thread.StartLine = local.Line, startLine
	}
	answer := github.ReviewComment{Author: localAnswerAuthor}
	switch local.State {
	case github.LocalAnswered:
		answer.Body = local.Text
	case github.LocalFailed:
		answer.Body = localFailedPrefix + local.Text
	default:
		answer.Body = localWaitingText
	}
	thread.Comments = []github.ReviewComment{{Author: localQuestionAuthor, Body: local.Question}, answer}
	return thread
}
