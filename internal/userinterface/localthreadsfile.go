package userinterface

import (
	"errors"
	"log/slog"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/localthreads"
)

// PullRequestOpenedMessage tells the root that a view has loaded a pull request.
// The first time it hears of one in a run, the root reads its saved local
// threads. Identity.Host is the pull request's host; with none, nothing is kept.
type PullRequestOpenedMessage struct{ Identity PullRequestIdentity }

type (
	localThreadsLoadedMessage struct {
		key     pullRequestKey
		threads []LocalThread
		err     error
	}
	localThreadsSavedMessage struct {
		key pullRequestKey
		err error
	}
)

// localSave is where one pull request's file stands. At most one operation on
// it (the first read, or a write) is in flight; a change that comes meanwhile
// only sets pending, and one write of the latest threads follows.
type localSave struct {
	inFlight bool
	pending  bool
}

// localThreadFiles is the root's bookkeeping for the files. Update uses the
// fields above the mutex; the fields below it are shared with the commands
// and Close, and are used only with the mutex held.
//
// Every change of a pull request's threads bumps its generation, and a write
// carries the generation it was made from. A write whose generation is not newer
// than the last one written is skipped, so an older copy can never land after a
// newer one, whoever writes (a command that ran late, or Close).
type localThreadFiles struct {
	directory  string // the state directory; empty keeps nothing
	opened     map[pullRequestKey]bool
	loaded     map[pullRequestKey]bool // the saved threads have been merged
	saves      map[pullRequestKey]*localSave
	generation map[pullRequestKey]int
	explained  map[pullRequestKey]bool // why nothing is kept was logged

	flushed <-chan struct{} // closes when Close's writes end; for tests

	mutex      sync.Mutex
	written    map[pullRequestKey]int  // the generation last written
	unreadable map[pullRequestKey]bool // the file can't be used: move it aside before writing
}

func (files *localThreadFiles) save(key pullRequestKey) *localSave {
	if files.saves == nil {
		files.saves = map[pullRequestKey]*localSave{}
	}
	if files.saves[key] == nil {
		files.saves[key] = &localSave{}
	}
	return files.saves[key]
}

// changed records that the threads of key differ from the last write.
func (files *localThreadFiles) changed(key pullRequestKey) {
	if files.generation == nil {
		files.generation = map[pullRequestKey]int{}
	}
	files.generation[key]++
}

func (files *localThreadFiles) markUnreadable(key pullRequestKey) {
	files.mutex.Lock()
	defer files.mutex.Unlock()
	if files.unreadable == nil {
		files.unreadable = map[pullRequestKey]bool{}
	}
	files.unreadable[key] = true
}

func (files *localThreadFiles) writtenAtLeast(key pullRequestKey, generation int) bool {
	files.mutex.Lock()
	defer files.mutex.Unlock()
	return generation <= files.written[key]
}

// write replaces the file with threads, made from the given generation, unless
// a newer copy has been written. An unreadable file is moved aside first, once,
// and if that fails nothing is overwritten.
func (files *localThreadFiles) write(key pullRequestKey, path string, generation int, threads []localthreads.Thread) error {
	files.mutex.Lock()
	defer files.mutex.Unlock()
	if generation <= files.written[key] {
		return nil
	}
	data, err := localthreads.Encode(threads)
	if err != nil {
		return err
	}
	if files.unreadable[key] {
		if err := localthreads.SetAside(path); err != nil {
			return err
		}
		delete(files.unreadable, key)
	}
	if err := localthreads.Write(path, data); err != nil {
		return err
	}
	if files.written == nil {
		files.written = map[pullRequestKey]int{}
	}
	files.written[key] = generation
	return nil
}

// localThreadPath is the file of a pull request. When there can be none (no
// state directory, no host, a name that isn't plain) it says so once in the log
// and the threads live for the session only.
func (model *Model) localThreadPath(key pullRequestKey) (string, bool) {
	files := &model.localFiles
	if files.directory == "" {
		return "", false
	}
	path, err := localthreads.Path(files.directory, key.host, key.repository.Owner, key.repository.Name, key.number)
	if err != nil {
		if files.explained == nil {
			files.explained = map[pullRequestKey]bool{}
		}
		if !files.explained[key] {
			files.explained[key] = true
			model.log.Warn("local threads are not kept for this pull request", "reason", err.Error(), "number", key.number, "host empty", key.host == "")
		}
		return "", false
	}
	return path, true
}

// pullRequestOpened reads the saved threads the first time a pull request is
// opened.
func (model *Model) pullRequestOpened(message PullRequestOpenedMessage) tea.Cmd {
	if model.quitting {
		return nil
	}
	return model.readLocalThreads(pullRequestKey{message.Identity.Host, message.Identity.Repository, message.Identity.Number})
}

// readLocalThreads starts the read of a pull request's file, once per run.
// Until the read returns it counts as a write in flight, so nothing
// overwrites the file before its threads are loaded.
func (model *Model) readLocalThreads(key pullRequestKey) tea.Cmd {
	files := &model.localFiles
	if files.opened[key] {
		return nil
	}
	path, ok := model.localThreadPath(key)
	if !ok {
		return nil
	}
	if files.opened == nil {
		files.opened = map[pullRequestKey]bool{}
	}
	files.opened[key] = true
	files.save(key).inFlight = true
	return func() tea.Msg {
		file, err := localthreads.Read(path)
		return localThreadsLoadedMessage{key: key, threads: fromFile(key, file.Threads), err: err}
	}
}

// localThreadsLoaded merges what the file held. A file that can't be used is
// logged, without its content, and set aside before the next write.
func (model *Model) localThreadsLoaded(message localThreadsLoadedMessage) tea.Cmd {
	files := &model.localFiles
	save := files.save(message.key)
	save.inFlight = false
	if files.loaded == nil {
		files.loaded = map[pullRequestKey]bool{}
	}
	files.loaded[message.key] = true
	changed := false
	if message.err != nil {
		model.log.Warn("local threads file ignored", "reason", loadFailure(message.err), "number", message.key.number)
		files.markUnreadable(message.key)
	} else {
		changed = model.localThreads.merge(message.key, message.threads)
	}
	var fitted tea.Cmd
	if changed {
		files.changed(message.key)
		save.pending = true
		fitted = model.fitLocalThreads(message.key)
	}
	var written tea.Cmd
	if save.pending {
		save.pending = false
		written = model.writeLocalThreads(message.key)
	}
	if message.err != nil || len(message.threads) == 0 {
		return tea.Batch(written, fitted)
	}
	return tea.Batch(written, model.broadcast(LocalThreadsMessage{Threads: model.localThreads.snapshot()}))
}

// loadFailure names why a file wasn't used, in words that hold none of its content.
func loadFailure(err error) string {
	switch {
	case errors.Is(err, localthreads.ErrTooLarge):
		return "too large"
	case errors.Is(err, localthreads.ErrUnknownVersion):
		return "unknown version"
	case errors.Is(err, localthreads.ErrCorrupt):
		return "can't be decoded"
	}
	return "can't be read"
}

// saveLocalThreads writes the pull request's threads after a change: at once
// if nothing is in flight for it, else when what is in flight ends. A change
// to a pull request whose saved threads have not been read yet starts the read
// and waits for it, so the file is never written without them.
func (model *Model) saveLocalThreads(key pullRequestKey) tea.Cmd {
	if _, ok := model.localThreadPath(key); !ok {
		return nil
	}
	files := &model.localFiles
	files.changed(key)
	fitted := model.fitLocalThreads(key)
	save := files.save(key)
	if !files.opened[key] {
		save.pending = true
		return tea.Batch(fitted, model.readLocalThreads(key))
	}
	if save.inFlight {
		save.pending = true
		return fitted
	}
	return tea.Batch(fitted, model.writeLocalThreads(key))
}

// fitLocalThreads keeps the pull request's file small enough to be read back,
// dropping the oldest threads from the store as well so the screen and the
// file agree. It logs how many, never what they said.
func (model *Model) fitLocalThreads(key pullRequestKey) tea.Cmd {
	dropped := model.localThreads.fit(key)
	if dropped == 0 {
		return nil
	}
	model.log.Warn("local threads dropped to keep the file small", "dropped", dropped, "number", key.number)
	return model.broadcast(LocalThreadsMessage{Threads: model.localThreads.snapshot()})
}

// writeLocalThreads starts a write of the threads as they are now.
func (model *Model) writeLocalThreads(key pullRequestKey) tea.Cmd {
	path, ok := model.localThreadPath(key)
	if !ok {
		return nil
	}
	files := &model.localFiles
	files.save(key).inFlight = true
	threads := toFile(model.localThreads.forRequest(key))
	generation := files.generation[key]
	return func() tea.Msg {
		return localThreadsSavedMessage{key: key, err: files.write(key, path, generation, threads)}
	}
}

// localThreadsSaved ends a write. A failure is logged, without any thread
// text, and stops nothing. A change that came meanwhile is written now.
func (model *Model) localThreadsSaved(message localThreadsSavedMessage) tea.Cmd {
	save := model.localFiles.save(message.key)
	save.inFlight = false
	if message.err != nil {
		model.log.Warn("local threads not saved", "err", message.err, "number", message.key.number)
	}
	if !save.pending {
		return nil
	}
	save.pending = false
	return model.writeLocalThreads(message.key)
}

// flushLocalThreads starts, from Close, the write of every pull request whose
// threads changed since the last write that completed. It does not depend on
// any command having run: a write command that Bubble Tea never started is
// made good here. A pull request whose saved threads were never read is read
// first and merged, so they are kept. The program has returned, so Close may
// use the model's own state here; everything that touches the files, the
// mutex included, runs in the goroutine, because a stalled write holds the
// mutex and must not hold the caller. The returned channel closes when the
// writes end, and the caller bounds the wait so a stuck disk can't hold quit.
func (model *Model) flushLocalThreads() <-chan struct{} {
	type job struct {
		key        pullRequestKey
		path       string
		generation int
		threads    []LocalThread
		loaded     bool
	}
	files := &model.localFiles
	var jobs []job
	for key, generation := range files.generation {
		if path, ok := model.localThreadPath(key); ok {
			jobs = append(jobs, job{key, path, generation, model.localThreads.forRequest(key), files.loaded[key]})
		}
	}
	next, limit, bytes := model.localThreads.next, model.localThreads.limit, model.localThreads.bytes
	logger := model.log
	finished := make(chan struct{})
	files.flushed = finished
	go func() {
		defer close(finished)
		for _, pending := range jobs {
			if files.writtenAtLeast(pending.key, pending.generation) {
				continue
			}
			threads := pending.threads
			if !pending.loaded {
				threads = mergeUnread(pending.key, pending.path, pending.threads, next, limit, bytes, files, logger)
			}
			if err := files.write(pending.key, pending.path, pending.generation, toFile(threads)); err != nil {
				logger.Warn("local threads not saved", "err", err, "number", pending.key.number)
			}
		}
	}()
	return finished
}

// mergeUnread reads the file of a pull request whose read never finished and
// merges it with the threads asked this run, as a load would.
func mergeUnread(key pullRequestKey, path string, asked []LocalThread, next, limit, bytes int, files *localThreadFiles, logger *slog.Logger) []LocalThread {
	file, err := localthreads.Read(path)
	if err != nil {
		logger.Warn("local threads file ignored", "reason", loadFailure(err), "number", key.number)
		files.markUnreadable(key)
		return asked
	}
	scratch := localThreadStore{next: next, limit: limit, bytes: bytes, byRequest: map[pullRequestKey][]LocalThread{key: asked}}
	scratch.merge(key, fromFile(key, file.Threads))
	scratch.fit(key)
	return scratch.forRequest(key)
}

func toFile(threads []LocalThread) []localthreads.Thread {
	saved := make([]localthreads.Thread, 0, len(threads))
	for _, thread := range threads {
		saved = append(saved, localthreads.Thread{
			Identifier: thread.Identifier, Head: thread.Head, Path: thread.Path, Side: thread.Side,
			Line: thread.Line, StartLine: thread.StartLine, Question: thread.Question,
			State: stateName(thread.State), Text: thread.Text, Asked: thread.Asked,
		})
	}
	return saved
}

// fromFile turns what a file held into threads of one pull request. A thread
// in a state this version doesn't know is left out.
func fromFile(key pullRequestKey, saved []localthreads.Thread) []LocalThread {
	threads := make([]LocalThread, 0, len(saved))
	for _, thread := range saved {
		state, ok := stateFromName(thread.State)
		if !ok {
			continue
		}
		threads = append(threads, LocalThread{
			Identifier: thread.Identifier, Host: key.host, Repository: key.repository, Number: key.number,
			Head: thread.Head, Path: thread.Path, Side: thread.Side, Line: thread.Line, StartLine: thread.StartLine,
			Question: thread.Question, State: state, Text: thread.Text, Asked: thread.Asked,
		})
	}
	return threads
}

func stateName(state github.LocalState) string {
	switch state {
	case github.LocalAnswered:
		return localthreads.StateAnswered
	case github.LocalFailed:
		return localthreads.StateFailed
	}
	return localthreads.StateWaiting
}

func stateFromName(name string) (github.LocalState, bool) {
	switch name {
	case localthreads.StateWaiting:
		return github.LocalWaiting, true
	case localthreads.StateAnswered:
		return github.LocalAnswered, true
	case localthreads.StateFailed:
		return github.LocalFailed, true
	}
	return github.NotLocal, false
}
