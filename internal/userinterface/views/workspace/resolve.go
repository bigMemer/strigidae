package workspace

import (
	"errors"
	"fmt"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/terminaltext"
)

// Resolving and unresolving a thread changes what other people see, so both
// always ask y/N, naming the thread's file and line. The thread is re-read
// before the question and again after the yes; a thread that is no longer in
// the state the question assumed is not written to.

const (
	errThreadGone         = staleError("that thread is gone; refreshed")
	errAlreadyResolved    = staleError("that thread is already resolved; refreshed")
	errAlreadyUnresolved  = staleError("that thread is already unresolved; refreshed")
	noticeAlreadyResolved = "thread is already resolved"
	noticeNotResolved     = "thread is not resolved"
)

// resolveMode says which direction a key or command may take.
type resolveMode int

const (
	resolveToggle resolveMode = iota // x: the opposite of the thread's state
	resolveOnly                      // :resolve
	unresolveOnly                    // :unresolve
)

// threadResolvedMessage is the result of resolving or unresolving a thread (or
// of a refusal before the write). It carries the identity of the request: the
// workspace and pull request, the load sequence the thread was picked under,
// and the thread.
type threadResolvedMessage struct {
	origin           origin
	sequence         int
	threadIdentifier string
	resolve          bool
	location         string // "path:line", already cleaned
	resolution       github.ThreadResolution
	err              error
}

// threadAction is one guarded resolve or unresolve of a thread.
type threadAction struct {
	origin           origin
	sequence         int
	client           github.Client
	timeouts         userinterface.Timeouts
	threadIdentifier string
	location         string
	resolve          bool
}

// resolveThread starts resolving or unresolving the thread at the diff cursor.
func (view *View) resolveThread(mode resolveMode, thread github.ReviewThread) tea.Cmd {
	switch {
	case mode == resolveOnly && thread.IsResolved:
		return userinterface.Info(noticeAlreadyResolved)
	case mode == unresolveOnly && !thread.IsResolved:
		return userinterface.Info(noticeNotResolved)
	}
	action := threadAction{
		origin: view.origin(), sequence: view.sequence, client: view.client, timeouts: view.timeouts,
		threadIdentifier: thread.Identifier, location: terminaltext.Clean(threadLocation(thread)),
		resolve: !thread.IsResolved,
	}
	view.writeStarted()
	return func() tea.Msg { return action.check(false) }
}

// verb is "Resolve" or "Unresolve".
func (action threadAction) verb() string {
	if action.resolve {
		return "Resolve"
	}
	return "Unresolve"
}

// check re-reads the thread. Unless the user has said yes already it then asks;
// after the yes it runs again and writes.
func (action threadAction) check(asked bool) tea.Msg {
	loadContext, cancelLoad := action.timeouts.LoadContext()
	defer cancelLoad()
	threads, err := loadThreads(loadContext, action.client, action.origin.repository, action.origin.number)
	if err != nil {
		return action.finish(github.ThreadResolution{}, fmt.Errorf("check thread: %w", err))
	}
	index := slices.IndexFunc(threads, func(thread github.ReviewThread) bool { return thread.Identifier == action.threadIdentifier })
	switch {
	case index < 0:
		return action.finish(github.ThreadResolution{}, errThreadGone)
	case threads[index].IsResolved == action.resolve:
		// Somebody else already did it, or undid what the question assumed.
		if action.resolve {
			return action.finish(github.ThreadResolution{}, errAlreadyResolved)
		}
		return action.finish(github.ThreadResolution{}, errAlreadyUnresolved)
	}
	if !asked {
		again := func() tea.Msg { return action.check(true) }
		return commentCheckedMessage{origin: action.origin, question: action.verb() + " thread on " + action.location + "?", onYes: agreed(action.origin, writeThread, again)}
	}
	var resolution github.ThreadResolution
	ctx, cancel := action.timeouts.RequestContext()
	defer cancel()
	if action.resolve {
		resolution, err = action.client.ResolveReviewThread(ctx, action.threadIdentifier)
	} else {
		resolution, err = action.client.UnresolveReviewThread(ctx, action.threadIdentifier)
	}
	err = userinterface.WriteError(err)
	if errors.Is(err, github.ErrNotFound) {
		err = errThreadGone
	}
	return action.finish(resolution, err)
}

// finish builds the result. A failure is also sent as an error to show, so it
// shows even if the view was left meanwhile.
func (action threadAction) finish(resolution github.ThreadResolution, err error) tea.Msg {
	result := threadResolvedMessage{
		origin: action.origin, sequence: action.sequence, threadIdentifier: action.threadIdentifier,
		resolve: action.resolve, location: action.location, resolution: resolution, err: err,
	}
	if err == nil {
		return result
	}
	return withShownError(result, commentFailure(err, action.verb()+" thread"))
}

// threadResolved applies a successful change to the thread on screen and
// refreshes when the screen is out of date.
func (view *View) threadResolved(message threadResolvedMessage) tea.Cmd {
	view.writeDone()
	if message.err != nil {
		if isStale(message.err) {
			return view.reloadThreads() // the error is already shown
		}
		return nil
	}
	text := "Unresolved thread on " + message.location
	if message.resolve {
		text = "Resolved thread on " + message.location
	}
	// The fold and the fade are recorded first so that a reload honours them
	// too. An unresolved thread's fade ends once the screen shows it
	// unresolved: here, or when the reload arrives, holding it until then.
	view.filePane.SetThreadExpanded(message.threadIdentifier, !message.resolution.IsResolved)
	var fade tea.Cmd
	if message.resolution.IsResolved {
		fade = view.beginThreadFade(message.threadIdentifier)
	}
	// A reload started since the thread was picked may have read it before the
	// write, so it is read again rather than patched.
	if message.sequence != view.sequence || !view.applyResolution(message.threadIdentifier, message.resolution) {
		if !message.resolution.IsResolved {
			view.holdThreadFade(message.threadIdentifier)
		}
		return tea.Batch(userinterface.Info(text), view.reloadThreads(), fade)
	}
	view.endUnresolvedFades()
	return tea.Batch(userinterface.Info(text), fade)
}

// applyResolution updates the thread in place (the fold was recorded by the
// caller). It reports false, changing nothing, when the thread is not shown or
// a thread load is in flight: that load may carry newer data than the screen,
// so the caller reloads after the write instead, which supersedes it.
func (view *View) applyResolution(identifier string, resolution github.ThreadResolution) bool {
	index := slices.IndexFunc(view.threads, func(thread github.ReviewThread) bool { return thread.Identifier == identifier })
	if index < 0 || view.threadPending {
		return false
	}
	// A fresh slice, so the one handed to the file pane earlier is never written to.
	view.threads = slices.Clone(view.threads)
	view.threads[index].IsResolved, view.threads[index].ResolvedBy = resolution.IsResolved, resolution.ResolvedBy
	view.filePane.SetThreads(view.threads)
	view.overview.invalidate()
	view.layout()
	return true
}
