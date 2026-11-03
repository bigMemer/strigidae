package workspace

import (
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/terminaltext"
)

// viewedToggle is the client-side state of one file's viewed mark while a
// toggle is in flight or not yet reflected by a reload. Mutations for a path
// are sent one at a time, so the server cannot see them out of order.
type viewedToggle struct {
	inflight  bool
	want      github.ViewedState // latest state the user asked for; shown at once
	confirmed github.ViewedState // last state known to be on the server
	settledAt int                // load seq current when the toggle settled
}

// viewedMessage is the result of one mutation. It is matched on owner, repo,
// number and path, not on the load sequence: a reload does not make the
// result irrelevant.
type viewedMessage struct {
	owner      *View
	repository github.Repository
	number     int
	path       string
	sent       github.ViewedState // state the mutation asked for
	err        error
}

// viewedCount returns how many files are marked viewed. Dismissed files
// (changed since they were viewed) do not count.
func (view *View) viewedCount() int {
	count := 0
	for _, file := range view.files() {
		if file.Viewed == github.FileViewed {
			count++
		}
	}
	return count
}

// filesTitle is the Files pane title, with review progress once files exist.
func (view *View) filesTitle() string {
	if count := len(view.files()); count > 0 {
		return fmt.Sprintf("Files %d/%d viewed", view.viewedCount(), count)
	}
	return "Files"
}

// toggleViewed flips the viewed mark of the selected file at once. A viewed
// file becomes unviewed; an unviewed or dismissed one becomes viewed. The
// mutation is sent now if none is in flight for the file, else when the
// running one finishes.
func (view *View) toggleViewed() tea.Cmd {
	selected := view.selection()
	if selected == 0 || selected > len(view.files()) {
		return nil
	}
	file := &view.detail.Files[selected-1]
	// The user decided this file's mark themselves: autoviewed leaves it
	// alone from now on.
	delete(view.hunkMarks.autoViewed, fileKey{view.repository, view.number, file.Path})
	want := github.FileViewed
	if file.Viewed == github.FileViewed {
		want = github.FileUnviewed
	}
	return view.setFileViewed(file, want)
}

// setFileViewed shows file's viewed mark as want at once and syncs it with
// GitHub: the mutation is sent now if none is in flight for the file, else
// when the running one finishes.
func (view *View) setFileViewed(file *github.ChangedFile, want github.ViewedState) tea.Cmd {
	if view.detail.Identifier == "" {
		return userinterface.Error(errors.New("mark file as viewed: pull request ID unknown"))
	}
	toggle := view.viewedState[file.Path]
	if toggle == nil {
		toggle = &viewedToggle{confirmed: file.Viewed}
		view.viewedState[file.Path] = toggle
	}
	toggle.want = want
	file.Viewed = want
	var fading tea.Cmd
	switch {
	case want != github.FileViewed:
		// Taking the mark back: the file stays, and stops fading.
		view.fades.cancel(fadeKey(file.Path))
		view.place(view.current)
	case view.options.HideViewed():
		fading = view.startFade(file.Path)
		view.moveOffMarked()
	}
	if toggle.inflight {
		return fading
	}
	toggle.inflight = true
	return tea.Batch(fading, view.sendViewed(file.Path, want))
}

// sendViewed is the command that runs one mutation. On failure it also
// returns the error itself, so it shows even if the view was left meanwhile.
func (view *View) sendViewed(path string, state github.ViewedState) tea.Cmd {
	client, repository, number, owner, pullRequestIdentifier, timeouts := view.client, view.repository, view.number, view, view.detail.Identifier, view.timeouts
	return func() tea.Msg {
		ctx, cancel := timeouts.RequestContext()
		defer cancel()
		err := userinterface.WriteError(client.SetFileViewed(ctx, pullRequestIdentifier, path, state == github.FileViewed))
		result := viewedMessage{owner: owner, repository: repository, number: number, path: path, sent: state, err: err}
		if err == nil {
			return result
		}
		verb := "mark"
		if state != github.FileViewed {
			verb = "unmark"
		}
		return withShownError(result, terminaltext.SafeError(fmt.Errorf("%s %s as viewed: %w", verb, terminaltext.Clean(path), err)))
	}
}

func (view *View) viewedMine(message viewedMessage) bool {
	return view.owns(origin{message.owner, message.repository, message.number})
}

// viewedDone records a mutation's outcome and sends the next wanted state, if
// the user toggled again meanwhile.
func (view *View) viewedDone(message viewedMessage) tea.Cmd {
	toggle := view.viewedState[message.path]
	if toggle == nil {
		return nil
	}
	if message.err == nil {
		toggle.confirmed = message.sent
	} else {
		toggle.want = toggle.confirmed
		view.setViewed(message.path, toggle.want)
		if message.sent == github.FileViewed {
			// The mark failed, or timed out and may or may not have landed. Either
			// way autoviewed lets go of the file, so it never unmarks a mark strig
			// is not sure it made (say, one the user made on GitHub instead).
			delete(view.hunkMarks.autoViewed, fileKey{view.repository, view.number, message.path})
		}
		if toggle.want != github.FileViewed {
			view.fades.cancel(fadeKey(message.path))
		}
		view.reflow() // a mark taken back may be hidden, one restored may not
	}
	if toggle.want != message.sent && toggle.want != toggle.confirmed {
		return view.sendViewed(message.path, toggle.want)
	}
	toggle.inflight, toggle.settledAt = false, view.sequence
	if userinterface.IsWriteTimeout(message.err) {
		// GitHub may have applied the mark: forget what strig believed, so the
		// reload decides what shows.
		delete(view.viewedState, message.path)
		return view.reloadPullRequest()
	}
	return nil
}

func (view *View) setViewed(path string, state github.ViewedState) {
	if view.detail == nil {
		return
	}
	for i := range view.detail.Files {
		if view.detail.Files[i].Path == path {
			view.detail.Files[i].Viewed = state
		}
	}
}

// reapplyViewed runs after the detail is replaced by a reload. Files with a
// toggle in flight or unconfirmed keep their wanted mark, because the reload
// may have read before the mutation landed; settled entries are dropped so
// the server's state shows (once the reload started after they settled), and so
// are entries for files that vanished.
func (view *View) reapplyViewed(sequence int) {
	present := map[string]bool{}
	for _, file := range view.files() {
		present[file.Path] = true
	}
	for path, toggle := range view.viewedState {
		settled := !toggle.inflight && toggle.want == toggle.confirmed
		switch {
		case !present[path], settled && sequence > toggle.settledAt:
			delete(view.viewedState, path)
		default:
			// A settled entry whose reload began before it settled may hold
			// older server data; toggle.want equals toggle.confirmed then.
			view.setViewed(path, toggle.want)
		}
	}
}
