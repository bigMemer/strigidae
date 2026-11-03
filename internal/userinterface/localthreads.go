package userinterface

import (
	"strconv"

	"github.com/MikePors/strigidae/internal/github"
)

// LocalThread is a plugin's question about diff lines and what came of it. It
// exists only inside strig, for as long as strig runs.
type LocalThread struct {
	// Identifier is unique among local threads; the views use it as a key
	// for folds. Nothing may treat it as telling a local thread from a
	// GitHub one: that is github.ReviewThread.Local.
	Identifier string
	Repository github.Repository
	Number     int
	// Head is the head commit the question was asked on. Whether the thread
	// is outdated is worked out against the diff on screen, not stored.
	Head      string
	Path      string
	Side      github.DiffSide
	Line      int
	StartLine int
	Question  string
	State     github.LocalState
	// Text is the answer once State is LocalAnswered, and why there is none
	// once it is LocalFailed. It is the plugin's text and has not been cleaned.
	Text string
}

// LocalThreadsMessage gives views every local thread, after any of them
// changed and to each view as it is pushed. A view keeps the ones of its own
// pull request. Threads are copies; the root owns the store.
type LocalThreadsMessage struct{ Threads []LocalThread }

// pullRequestKey is what local threads are stored by: the repository and the
// pull request number. The head is on each thread.
type pullRequestKey struct {
	repository github.Repository
	number     int
}

// localThreadStore holds the local threads for the life of strig. Only the
// root model's Update touches it.
type localThreadStore struct {
	next      int
	byRequest map[pullRequestKey][]LocalThread
}

func (store *localThreadStore) add(thread LocalThread) string {
	if store.byRequest == nil {
		store.byRequest = map[pullRequestKey][]LocalThread{}
	}
	store.next++
	thread.Identifier = "local-" + strconv.Itoa(store.next)
	key := pullRequestKey{thread.Repository, thread.Number}
	store.byRequest[key] = append(store.byRequest[key], thread)
	return thread.Identifier
}

// finish records how a thread's question ended.
func (store *localThreadStore) finish(identifier string, state github.LocalState, text string) {
	for key, threads := range store.byRequest {
		for i := range threads {
			if threads[i].Identifier == identifier {
				store.byRequest[key][i].State, store.byRequest[key][i].Text = state, text
				return
			}
		}
	}
}

// snapshot copies every thread.
func (store *localThreadStore) snapshot() []LocalThread {
	var all []LocalThread
	for _, threads := range store.byRequest {
		all = append(all, threads...)
	}
	return all
}
