package userinterface

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/localthreads"
)

// DefaultLocalThreadLimit is the most local threads kept for one pull request.
// When another is asked, the oldest goes, from memory and from the file.
const DefaultLocalThreadLimit = 200

// localClosedText is why a thread that was still waiting when strig exited has
// no answer.
const localClosedText = "strig was closed"

const localIdentifierPrefix = "local-"

// LocalThread is a plugin's question about diff lines and what came of it. It
// lives in the root model, and in a file under the state directory between
// runs of strig.
type LocalThread struct {
	// Identifier is unique among local threads; the views use it as a key
	// for folds. Nothing may treat it as telling a local thread from a
	// GitHub one: that is github.ReviewThread.Local.
	Identifier string
	// Host is the GitHub host of the pull request; the views do not look at it.
	Host       string
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
	// Asked is when the question was asked; it picks the thread to drop when
	// there are too many, and is not shown.
	Asked time.Time
}

// LocalThreadsMessage gives views every local thread, after any of them
// changed and to each view as it is pushed. A view keeps the ones of its own
// pull request. Threads are copies; the root owns the store.
type LocalThreadsMessage struct{ Threads []LocalThread }

// pullRequestKey is what local threads are stored by: the host, the repository
// and the pull request number. The head is on each thread.
type pullRequestKey struct {
	host       string
	repository github.Repository
	number     int
}

func (thread LocalThread) key() pullRequestKey {
	return pullRequestKey{thread.Host, thread.Repository, thread.Number}
}

// localThreadStore holds the local threads for the life of strig. Only the
// root model's Update touches it.
type localThreadStore struct {
	next      int
	limit     int // 0 means DefaultLocalThreadLimit
	bytes     int // 0 means localthreads.MaxFileSize
	byRequest map[pullRequestKey][]LocalThread
}

func (store *localThreadStore) maximum() int {
	if store.limit > 0 {
		return store.limit
	}
	return DefaultLocalThreadLimit
}

func (store *localThreadStore) maximumBytes() int {
	if store.bytes > 0 {
		return store.bytes
	}
	return localthreads.MaxFileSize
}

// fit drops the oldest threads of key, by asked time as the count limit does,
// until the file they make is no bigger than the file size limit (a bigger one
// would be refused when read back), and says how many went. The newest thread
// is never dropped: one answer is at most plugins.MaximumOutputSize, which
// is far below the limit.
func (store *localThreadStore) fit(key pullRequestKey) (dropped int) {
	threads := store.byRequest[key]
	sizes := make([]int, len(threads))
	total := localthreads.FileOverhead + max(len(threads)-1, 0)
	for i, thread := range threads {
		sizes[i] = localthreads.ThreadSize(toFile([]LocalThread{thread})[0])
		total += sizes[i]
	}
	for total > store.maximumBytes() && len(threads) > 1 {
		oldest := 0
		for i := range threads {
			if threads[i].Asked.Before(threads[oldest].Asked) {
				oldest = i
			}
		}
		total -= sizes[oldest] + 1
		threads = slices.Delete(threads, oldest, oldest+1)
		sizes = slices.Delete(sizes, oldest, oldest+1)
		dropped++
	}
	store.byRequest[key] = threads
	return dropped
}

// newIdentifier returns an identifier no thread has had in this run or in a
// file loaded in it.
func (store *localThreadStore) newIdentifier() string {
	store.next++
	return localIdentifierPrefix + strconv.Itoa(store.next)
}

// add stores a new thread and gives it its identifier. Over the limit, the
// oldest thread of its pull request is dropped.
func (store *localThreadStore) add(thread LocalThread) string {
	if store.byRequest == nil {
		store.byRequest = map[pullRequestKey][]LocalThread{}
	}
	thread.Identifier = store.newIdentifier()
	key := thread.key()
	store.byRequest[key] = append(store.byRequest[key], thread)
	store.trim(key)
	return thread.Identifier
}

// trim drops the oldest threads of key until it is within the limit, and
// reports whether any went. Of equal times the one stored first goes.
func (store *localThreadStore) trim(key pullRequestKey) (dropped bool) {
	for len(store.byRequest[key]) > store.maximum() {
		threads := store.byRequest[key]
		oldest := 0
		for i := range threads {
			if threads[i].Asked.Before(threads[oldest].Asked) {
				oldest = i
			}
		}
		store.byRequest[key] = slices.Delete(threads, oldest, oldest+1)
		dropped = true
	}
	return dropped
}

// finish records how a thread's question ended, and says which pull request
// it belongs to.
func (store *localThreadStore) finish(identifier string, state github.LocalState, text string) (key pullRequestKey, found bool) {
	for key, threads := range store.byRequest {
		for i := range threads {
			if threads[i].Identifier == identifier {
				store.byRequest[key][i].State, store.byRequest[key][i].Text = state, text
				return key, true
			}
		}
	}
	return pullRequestKey{}, false
}

// snapshot copies every thread.
func (store *localThreadStore) snapshot() []LocalThread {
	var all []LocalThread
	for _, threads := range store.byRequest {
		all = append(all, threads...)
	}
	return all
}

// forRequest copies the threads of one pull request.
func (store *localThreadStore) forRequest(key pullRequestKey) []LocalThread {
	return slices.Clone(store.byRequest[key])
}

// find returns the thread with identifier, if any pull request has it.
func (store *localThreadStore) find(identifier string) (LocalThread, bool) {
	for _, threads := range store.byRequest {
		for _, thread := range threads {
			if thread.Identifier == identifier {
				return thread, true
			}
		}
	}
	return LocalThread{}, false
}

// localNumber is the number in an identifier of the form local-N.
func localNumber(identifier string) (int, bool) {
	text, ok := strings.CutPrefix(identifier, localIdentifierPrefix)
	if !ok {
		return 0, false
	}
	number, err := strconv.Atoi(text)
	if err != nil || number <= 0 || strconv.Itoa(number) != text {
		return 0, false
	}
	return number, true
}

// merge adds the threads read from a file to the pull request's own, which
// are normally none. A thread already in the store (same identifier and same
// question) is not added twice. A loaded identifier that is not of the form
// local-N, or that another thread already has, is replaced by a new one, and
// numbering continues after the largest loaded number, so identifiers stay
// unique across restarts. A thread that was waiting when strig exited has no
// answer now. It reports whether the result differs from the file, so the
// caller writes it back.
func (store *localThreadStore) merge(key pullRequestKey, loaded []LocalThread) (changed bool) {
	if store.byRequest == nil {
		store.byRequest = map[pullRequestKey][]LocalThread{}
	}
	for _, thread := range loaded {
		if number, valid := localNumber(thread.Identifier); valid {
			store.next = max(store.next, number)
		}
	}
	merged := make([]LocalThread, 0, len(loaded)+len(store.byRequest[key]))
	for _, thread := range loaded {
		_, taken := store.find(thread.Identifier)
		// Only this pull request's threads can be the same thread; another's
		// with the same identifier is a clash, renumbered below.
		duplicate := func(other LocalThread) bool {
			return other.Identifier == thread.Identifier && sameQuestion(other, thread)
		}
		if slices.ContainsFunc(store.byRequest[key], duplicate) || slices.ContainsFunc(merged, duplicate) {
			changed = true // a duplicate is not kept
			continue
		}
		_, valid := localNumber(thread.Identifier)
		taken = taken || slices.ContainsFunc(merged, func(other LocalThread) bool { return other.Identifier == thread.Identifier })
		if !valid || taken {
			thread.Identifier, changed = store.newIdentifier(), true
		}
		if thread.State == github.LocalWaiting {
			thread.State, thread.Text, changed = github.LocalFailed, localClosedText, true
		}
		merged = append(merged, thread)
	}
	store.byRequest[key] = append(merged, store.byRequest[key]...)
	if store.trim(key) {
		changed = true
	}
	return changed
}

func sameQuestion(first, second LocalThread) bool {
	return first.Question == second.Question && first.Path == second.Path && first.Asked.Equal(second.Asked)
}
