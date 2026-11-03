package userinterface

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/config"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/localthreads"
	"github.com/MikePors/strigidae/internal/plugins"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

var widgetsRepository = github.Repository{Owner: "acme", Name: "widgets"}

// savedSession is a root model over a pull request view, keeping local threads
// in a directory, with a clock that moves a minute each time it is read.
type savedSession struct {
	test   *testing.T
	model  *Model
	view   *pullRequestView
	logs   *bytes.Buffer
	runner PluginRunner
	clock  time.Time
}

type savedOptions struct {
	directory  string
	host       string
	limit      int
	repository github.Repository
	times      []time.Time  // asked times to use, in order, before the clock moves on
	runner     PluginRunner // replaces the one that answers at once
	quitWait   time.Duration
	bytes      int // the file size limit; 0 for the real one
}

func newSavedSession(test *testing.T, options savedOptions) *savedSession {
	test.Helper()
	session := &savedSession{
		test:   test,
		logs:   &bytes.Buffer{},
		runner: &fakeRunner{effects: []plugins.Effect{{Name: plugins.EffectAnswer, Text: "because"}}},
		clock:  time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC),
	}
	if options.repository.IsZero() {
		options.repository = widgetsRepository
	}
	if options.runner != nil {
		session.runner = options.runner
	}
	identity := PullRequestIdentity{Host: options.host, Repository: options.repository, Number: 7, Head: "abc"}
	session.view = newPullRequestView(identity)
	times := options.times
	quitWait := options.quitWait
	if quitWait == 0 {
		quitWait = 300 * time.Millisecond
	}
	session.model = New(session.view, theme.Default(), Options{
		Logger: slog.New(slog.NewTextHandler(session.logs, nil)),
		Tick: func(_ time.Duration, function func(time.Time) tea.Msg) tea.Cmd {
			return func() tea.Msg { return function(time.Time{}) }
		},
		ViewCommands:     testViewCommands,
		PluginRunner:     session.runner,
		PluginCommands:   []PluginCommand{{Name: "ask", Target: config.TargetLines, Show: config.ShowThread}},
		PluginQuitWait:   quitWait,
		StateDirectory:   options.directory,
		LocalThreadLimit: options.limit,
		LocalThreadBytes: options.bytes,
		Clock: func() time.Time {
			if len(times) > 0 {
				asked := times[0]
				times = times[1:]
				return asked
			}
			session.clock = session.clock.Add(time.Minute)
			return session.clock
		},
	})
	return session
}

// run performs a command and everything it leads to, in order, handing each
// message to the model.
func (session *savedSession) run(command tea.Cmd) {
	session.test.Helper()
	queue := []tea.Cmd{command}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 200 {
			session.test.Fatal("commands do not stop")
		}
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		switch message := next().(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, message...)
		default:
			queue = append(queue, session.model.update(message))
		}
	}
}

func (session *savedSession) open() {
	session.test.Helper()
	identity, _ := session.view.PullRequestIdentity()
	session.run(session.model.update(PullRequestOpenedMessage{Identity: identity}))
}

// request is what the view sends when :ask is run on a line.
func (session *savedSession) request(question string) RunPluginMessage {
	identity, _ := session.view.PullRequestIdentity()
	return RunPluginMessage{Name: "ask", Identity: identity, Request: plugins.Request{
		Command: "ask", Arguments: question, Head: identity.Head,
		Repository: plugins.Repository{Host: identity.Host, Owner: identity.Repository.Owner, Name: identity.Repository.Name},
		Number:     identity.Number,
		Target:     &plugins.Target{Path: "a.go", Side: "RIGHT", Line: 5, StartLine: 5},
	}}
}

// ask runs :ask on a line and delivers the answer.
func (session *savedSession) ask(question string) {
	session.test.Helper()
	session.run(session.model.update(session.request(question)))
}

// askHeld runs :ask on a line with a blockingRunner. It returns when the
// waiting thread has been written; the function it returns lets the plugin
// answer and delivers the answer.
func (session *savedSession) askHeld(question string) (answer func()) {
	session.test.Helper()
	messages := make(chan tea.Msg, 16)
	var spawn func(command tea.Cmd)
	spawn = func(command tea.Cmd) {
		if command == nil {
			return
		}
		go func() {
			message := command()
			if batch, ok := message.(tea.BatchMsg); ok {
				for _, member := range batch {
					spawn(member)
				}
				return
			}
			messages <- message
		}()
	}
	spawn(session.model.update(session.request(question)))
	for waiting := true; waiting; {
		select {
		case message := <-messages:
			if saved, ok := message.(localThreadsSavedMessage); ok {
				session.run(session.model.update(saved))
				waiting = false
			}
		case <-time.After(5 * time.Second):
			session.test.Fatal("asking did not write the waiting thread")
		}
	}
	return func() {
		session.test.Helper()
		close(session.runner.(*blockingRunner).release)
		for {
			if result, ok := (<-messages).(pluginResultMessage); ok {
				session.run(session.model.update(result))
				return
			}
		}
	}
}

// blockingRunner answers when released, or fails when its context ends.
type blockingRunner struct{ release chan struct{} }

func newBlockingRunner() *blockingRunner { return &blockingRunner{release: make(chan struct{})} }

func (runner *blockingRunner) Run(ctx context.Context, _ string, _ plugins.Request) ([]plugins.Effect, error) {
	select {
	case <-runner.release:
		return []plugins.Effect{{Name: plugins.EffectAnswer, Text: "late answer"}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// shown is the local threads the view was last told of.
func (session *savedSession) shown() []LocalThread {
	var last []LocalThread
	for _, message := range session.view.got {
		if threads, ok := message.(LocalThreadsMessage); ok {
			last = threads.Threads
		}
	}
	return last
}

func (session *savedSession) path() string {
	session.test.Helper()
	identity, _ := session.view.PullRequestIdentity()
	return savedPath(session.test, session.model.localFiles.directory, identity)
}

func savedPath(test *testing.T, directory string, identity PullRequestIdentity) string {
	test.Helper()
	path, err := localthreads.Path(directory, identity.Host, identity.Repository.Owner, identity.Repository.Name, identity.Number)
	if err != nil {
		test.Fatal(err)
	}
	return path
}

func readSaved(test *testing.T, path string) []localthreads.Thread {
	test.Helper()
	file, err := localthreads.Read(path)
	if err != nil {
		test.Fatal(err)
	}
	return file.Threads
}

func questions(threads []LocalThread) []string {
	var asked []string
	for _, thread := range threads {
		asked = append(asked, thread.Question)
	}
	return asked
}

func TestLocalThreadsSurviveARestart(test *testing.T) {
	directory := test.TempDir()
	first := newSavedSession(test, savedOptions{directory: directory, host: "github.com"})
	first.open()
	first.ask("why?")
	first.ask("and this?")
	first.model.Close()

	second := newSavedSession(test, savedOptions{directory: directory, host: "github.com"})
	second.open()
	got := second.shown()
	if diff := cmp.Diff([]string{"why?", "and this?"}, questions(got)); diff != "" {
		test.Fatalf("questions after restart (-want +got):\n%s", diff)
	}
	for _, thread := range got {
		if thread.State != github.LocalAnswered || thread.Text != "because" || thread.Head != "abc" || thread.Path != "a.go" || thread.Line != 5 {
			test.Errorf("thread = %+v", thread)
		}
		if thread.Repository != widgetsRepository || thread.Number != 7 {
			test.Errorf("thread belongs to %v #%d", thread.Repository, thread.Number)
		}
	}
}

func TestAskingAndAnsweringAreEachWritten(test *testing.T) {
	directory := test.TempDir()
	session := newSavedSession(test, savedOptions{directory: directory, host: "github.com", runner: newBlockingRunner()})
	session.open()
	answer := session.askHeld("why?")
	saved := readSaved(test, session.path())
	if len(saved) != 1 || saved[0].State != localthreads.StateWaiting || saved[0].Question != "why?" {
		test.Fatalf("file after asking = %+v", saved)
	}
	answer()
	saved = readSaved(test, session.path())
	if len(saved) != 1 || saved[0].State != localthreads.StateAnswered || saved[0].Text != "late answer" {
		test.Errorf("file after the answer = %+v", saved)
	}
}

func TestRestartWhileWaiting(test *testing.T) {
	directory := test.TempDir()
	first := newSavedSession(test, savedOptions{directory: directory, host: "github.com", runner: newBlockingRunner()})
	first.open()
	first.askHeld("why?")
	first.model.Close() // strig exits with the question unanswered

	second := newSavedSession(test, savedOptions{directory: directory, host: "github.com"})
	second.open()
	got := second.shown()
	if len(got) != 1 || got[0].State != github.LocalFailed || got[0].Text != "strig was closed" {
		test.Fatalf("after restart = %+v", got)
	}
	// The change of state is saved back.
	saved := readSaved(test, second.path())
	if len(saved) != 1 || saved[0].State != localthreads.StateFailed || saved[0].Text != "strig was closed" {
		test.Errorf("file after restart = %+v", saved)
	}
}

func TestFilesThatCannotBeUsed(test *testing.T) {
	for _, testCase := range []struct {
		name    string
		content []byte
		logged  string
	}{
		{"corrupt", []byte("{secret-not-json"), "can't be decoded"},
		{"unknown version", []byte(`{"version":2,"threads":[{"question":"secret"}]}`), "unknown version"},
		{"too large", append([]byte(`{"version":1,"threads":[],"p":"`), bytes.Repeat([]byte("s"), localthreads.MaxFileSize)...), "too large"},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			directory := test.TempDir()
			session := newSavedSession(test, savedOptions{directory: directory, host: "github.com"})
			path := session.path()
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				test.Fatal(err)
			}
			if err := os.WriteFile(path, testCase.content, 0o600); err != nil {
				test.Fatal(err)
			}
			session.open()
			if len(session.shown()) != 0 {
				test.Errorf("threads shown from an unusable file: %v", session.shown())
			}
			if !strings.Contains(session.logs.String(), testCase.logged) {
				test.Errorf("log lacks %q: %s", testCase.logged, session.logs)
			}
			if strings.Contains(session.logs.String(), "secret") {
				test.Errorf("log holds file content: %s", session.logs)
			}
			// Opening alone leaves the file where it is.
			if kept, err := os.ReadFile(path); err != nil || !bytes.Equal(kept, testCase.content) {
				test.Fatalf("file changed by opening: %v", err)
			}
			// The first write moves it aside.
			session.ask("why?")
			aside, err := os.ReadFile(path + localthreads.UnreadableSuffix)
			if err != nil || !bytes.Equal(aside, testCase.content) {
				test.Errorf("unreadable copy missing or changed: %v", err)
			}
			if saved := readSaved(test, path); len(saved) != 1 || saved[0].Question != "why?" {
				test.Errorf("new file = %+v", saved)
			}
			// A second write does not move the new file aside again.
			session.ask("again?")
			if saved := readSaved(test, path); len(saved) != 2 {
				test.Errorf("file after the second write = %+v", saved)
			}
			if aside, _ := os.ReadFile(path + localthreads.UnreadableSuffix); !bytes.Equal(aside, testCase.content) {
				test.Error("the unreadable copy was replaced")
			}
		})
	}
}

func TestLimitDropsTheOldestByAskedTime(test *testing.T) {
	directory := test.TempDir()
	base := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	// The second thread stored is the oldest asked.
	times := []time.Time{base.Add(2 * time.Hour), base, base.Add(time.Hour), base.Add(3 * time.Hour)}
	session := newSavedSession(test, savedOptions{directory: directory, host: "github.com", limit: 3, times: times})
	session.open()
	for _, question := range []string{"q1", "q2", "q3", "q4"} {
		session.ask(question)
	}
	if diff := cmp.Diff([]string{"q1", "q3", "q4"}, questions(session.shown())); diff != "" {
		test.Errorf("threads shown (-want +got):\n%s", diff)
	}
	var kept []string
	for _, thread := range readSaved(test, session.path()) {
		kept = append(kept, thread.Question)
	}
	if diff := cmp.Diff([]string{"q1", "q3", "q4"}, kept); diff != "" {
		test.Errorf("threads in the file (-want +got):\n%s", diff)
	}
}

func TestLimitAppliesToWhatIsLoaded(test *testing.T) {
	directory := test.TempDir()
	first := newSavedSession(test, savedOptions{directory: directory, host: "github.com", limit: 5})
	first.open()
	for _, question := range []string{"q1", "q2", "q3", "q4"} {
		first.ask(question)
	}
	first.model.Close()
	second := newSavedSession(test, savedOptions{directory: directory, host: "github.com", limit: 2})
	second.open()
	if diff := cmp.Diff([]string{"q3", "q4"}, questions(second.shown())); diff != "" {
		test.Errorf("threads (-want +got):\n%s", diff)
	}
}

func TestDefaultLimit(test *testing.T) {
	var store localThreadStore
	for i := 0; i < DefaultLocalThreadLimit+5; i++ {
		store.add(LocalThread{Number: 1, Asked: time.Unix(int64(i), 0)})
	}
	if got := len(store.byRequest[pullRequestKey{number: 1}]); got != DefaultLocalThreadLimit {
		test.Errorf("kept %d, want %d", got, DefaultLocalThreadLimit)
	}
}

// A write command is held back and run by hand, to see what a change made
// while it was in flight does.
func TestWritesAreOneAtATimeAndInOrder(test *testing.T) {
	directory := test.TempDir()
	session := newSavedSession(test, savedOptions{directory: directory, host: "github.com"})
	session.open()
	identity, _ := session.view.PullRequestIdentity()
	key := pullRequestKey{"github.com", identity.Repository, identity.Number}
	ask := func(question string) {
		session.model.localThreads.add(LocalThread{Host: "github.com", Repository: identity.Repository, Number: identity.Number, Question: question, Asked: session.clock})
	}

	ask("first")
	firstWrite := session.model.saveLocalThreads(key)
	if firstWrite == nil {
		test.Fatal("a change with nothing in flight did not start a write")
	}
	ask("second")
	if command := session.model.saveLocalThreads(key); command != nil {
		test.Error("a second write started while the first was in flight")
	}
	ask("third")
	if command := session.model.saveLocalThreads(key); command != nil {
		test.Error("a third write started while the first was in flight")
	}

	// The first write holds only what was there when it started.
	saved := firstWrite().(localThreadsSavedMessage)
	if saved.err != nil {
		test.Fatal(saved.err)
	}
	if got := len(readSaved(test, session.path())); got != 1 {
		test.Fatalf("file holds %d threads after the first write, want 1", got)
	}
	// Its end starts one write of the latest copy.
	latest := session.model.localThreadsSaved(saved)
	if latest == nil {
		test.Fatal("no write followed for the changes made meanwhile")
	}
	message := latest().(localThreadsSavedMessage)
	if message.err != nil {
		test.Fatal(message.err)
	}
	var got []string
	for _, thread := range readSaved(test, session.path()) {
		got = append(got, thread.Question)
	}
	if diff := cmp.Diff([]string{"first", "second", "third"}, got); diff != "" {
		test.Errorf("file after the last write (-want +got):\n%s", diff)
	}
	if command := session.model.localThreadsSaved(message); command != nil {
		test.Error("a write followed with nothing changed")
	}
	// Idle again: the next change writes at once.
	ask("fourth")
	if session.model.saveLocalThreads(key) == nil {
		test.Error("a change after the writes ended did not write")
	}
}

func TestCloseWritesTheChangesWaitingForAWrite(test *testing.T) {
	directory := test.TempDir()
	session := newSavedSession(test, savedOptions{directory: directory, host: "github.com"})
	session.open()
	identity, _ := session.view.PullRequestIdentity()
	key := pullRequestKey{"github.com", identity.Repository, identity.Number}
	ask := func(question string) {
		session.model.localThreads.add(LocalThread{Host: "github.com", Repository: identity.Repository, Number: identity.Number, Question: question, Asked: session.clock})
	}
	ask("first")
	inFlight := session.model.saveLocalThreads(key)
	ask("second") // waits for the write in flight
	if session.model.saveLocalThreads(key) != nil {
		test.Fatal("expected the second change to wait")
	}

	closed := make(chan struct{})
	go func() {
		session.model.Close()
		close(closed)
	}()
	inFlight() // the write ends; its result never reaches Update, as at exit
	<-closed

	var got []string
	for _, thread := range readSaved(test, session.path()) {
		got = append(got, thread.Question)
	}
	if diff := cmp.Diff([]string{"first", "second"}, got); diff != "" {
		test.Errorf("file after Close (-want +got):\n%s", diff)
	}
}

func TestCloseDoesNotWriteWhenNothingWaits(test *testing.T) {
	directory := test.TempDir()
	session := newSavedSession(test, savedOptions{directory: directory, host: "github.com"})
	session.open()
	session.ask("why?")
	before, err := os.Stat(session.path())
	if err != nil {
		test.Fatal(err)
	}
	session.model.Close()
	after, err := os.Stat(session.path())
	if err != nil || !after.ModTime().Equal(before.ModTime()) {
		test.Errorf("Close rewrote an up-to-date file: %v", err)
	}
}

func TestIdentifiersContinueAfterALoad(test *testing.T) {
	directory := test.TempDir()
	session := newSavedSession(test, savedOptions{directory: directory, host: "github.com"})
	path := session.path()
	data, err := localthreads.Encode([]localthreads.Thread{
		{Identifier: "local-5", Question: "old", State: localthreads.StateAnswered},
		{Identifier: "local-9", Question: "older", State: localthreads.StateAnswered},
	})
	if err != nil {
		test.Fatal(err)
	}
	if err := localthreads.Write(path, data); err != nil {
		test.Fatal(err)
	}
	session.open()
	session.ask("new")
	seen := map[string]bool{}
	for _, thread := range session.shown() {
		if seen[thread.Identifier] {
			test.Errorf("identifier %q is used twice", thread.Identifier)
		}
		seen[thread.Identifier] = true
	}
	if !seen["local-5"] || !seen["local-9"] || !seen["local-10"] || len(seen) != 3 {
		test.Errorf("identifiers = %v, want local-5, local-9 and local-10", seen)
	}
}

func TestLoadedIdentifiersThatClashAreRenumbered(test *testing.T) {
	var store localThreadStore
	key := pullRequestKey{"github.com", widgetsRepository, 7}
	live := store.add(LocalThread{Host: "github.com", Repository: widgetsRepository, Number: 7, Question: "live"})
	if live != "local-1" {
		test.Fatalf("live identifier = %q", live)
	}
	loaded := []LocalThread{
		{Identifier: "local-1", Question: "from the file"},
		{Identifier: "../escape", Question: "odd identifier"},
		{Identifier: "local-1", Question: "from the file", State: github.LocalAnswered}, // same again
	}
	store.merge(key, loaded)
	seen := map[string]bool{}
	for _, thread := range store.byRequest[key] {
		if seen[thread.Identifier] {
			test.Errorf("identifier %q is used twice", thread.Identifier)
		}
		seen[thread.Identifier] = true
		if _, valid := localNumber(thread.Identifier); !valid {
			test.Errorf("identifier %q is not of the form local-N", thread.Identifier)
		}
	}
	if next := store.add(LocalThread{Host: "github.com", Repository: widgetsRepository, Number: 7}); seen[next] {
		test.Errorf("new identifier %q was already used", next)
	}
}

func TestMergeDoesNotDuplicateAThreadAlreadyThere(test *testing.T) {
	var store localThreadStore
	key := pullRequestKey{"github.com", widgetsRepository, 7}
	asked := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	thread := LocalThread{Identifier: "local-1", Host: "github.com", Repository: widgetsRepository, Number: 7, Question: "why?", Path: "a.go", Asked: asked, State: github.LocalAnswered}
	store.byRequest = map[pullRequestKey][]LocalThread{key: {thread}}
	store.merge(key, []LocalThread{thread})
	if got := len(store.byRequest[key]); got != 1 {
		test.Errorf("%d threads after merging the same one, want 1", got)
	}
}

func TestNothingIsKeptWithoutAUsableHost(test *testing.T) {
	for _, testCase := range []struct {
		name       string
		host       string
		repository github.Repository
	}{
		{"empty host", "", widgetsRepository},
		{"slash in host", "a/b", widgetsRepository},
		{"dot dot owner", "github.com", github.Repository{Owner: "..", Name: "widgets"}},
		{"slash in name", "github.com", github.Repository{Owner: "acme", Name: "x/y"}},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			directory := test.TempDir()
			session := newSavedSession(test, savedOptions{directory: directory, host: testCase.host, repository: testCase.repository})
			session.open()
			session.ask("why?")
			session.ask("and?")
			session.model.Close()
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 0 {
				test.Errorf("state directory holds %v (%v), want nothing", entries, err)
			}
			if got := len(session.shown()); got != 2 {
				test.Errorf("the session shows %d threads, want 2: they still work", got)
			}
			if got := strings.Count(session.logs.String(), "local threads are not kept"); got != 1 {
				test.Errorf("explained %d times, want once:\n%s", got, session.logs)
			}
		})
	}
}

func TestNoStateDirectoryKeepsNothingAndSaysNothing(test *testing.T) {
	session := newSavedSession(test, savedOptions{host: "github.com"})
	session.open()
	session.ask("why?")
	session.model.Close()
	if got := len(session.shown()); got != 1 {
		test.Errorf("shows %d threads, want 1", got)
	}
	if session.logs.Len() != 0 {
		test.Errorf("logged: %s", session.logs)
	}
}

func TestFailedWriteIsLoggedWithoutText(test *testing.T) {
	directory := test.TempDir()
	session := newSavedSession(test, savedOptions{directory: directory, host: "github.com"})
	session.open()
	// A file where the directory should be makes every write fail.
	blocker := filepath.Join(directory, "strig")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		test.Fatal(err)
	}
	session.ask("top secret question")
	if !strings.Contains(session.logs.String(), "local threads not saved") {
		test.Errorf("failure not logged: %s", session.logs)
	}
	if strings.Contains(session.logs.String(), "secret") {
		test.Errorf("log holds thread text: %s", session.logs)
	}
	if got := len(session.shown()); got != 1 {
		test.Errorf("the thread is not shown after a failed write: %d", got)
	}
}

func TestOpeningAgainDoesNotReadAgain(test *testing.T) {
	directory := test.TempDir()
	session := newSavedSession(test, savedOptions{directory: directory, host: "github.com"})
	session.open()
	identity, _ := session.view.PullRequestIdentity()
	if command := session.model.update(PullRequestOpenedMessage{Identity: identity}); command != nil {
		test.Error("a second open of the same pull request read the file again")
	}
}

// A question asked before the saved threads have been read must not overwrite
// them: its write waits for the read, and the file ends up with both.
func TestAskingBeforeTheLoadEndsKeepsTheSavedThreads(test *testing.T) {
	directory := test.TempDir()
	session := newSavedSession(test, savedOptions{directory: directory, host: "github.com"})
	data, err := localthreads.Encode([]localthreads.Thread{{Identifier: "local-1", Question: "old", State: localthreads.StateAnswered, Asked: time.Unix(1, 0)}})
	if err != nil {
		test.Fatal(err)
	}
	if err := localthreads.Write(session.path(), data); err != nil {
		test.Fatal(err)
	}
	identity, _ := session.view.PullRequestIdentity()
	read := session.model.update(PullRequestOpenedMessage{Identity: identity}) // held back
	session.ask("new")
	session.run(read)
	var got []string
	for _, thread := range readSaved(test, session.path()) {
		got = append(got, thread.Question)
	}
	if diff := cmp.Diff([]string{"old", "new"}, got); diff != "" {
		test.Errorf("file (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"old", "new"}, questions(session.shown())); diff != "" {
		test.Errorf("shown (-want +got):\n%s", diff)
	}
}

// The order of events in a real run: the question is asked before the root has
// heard that the pull request opened. The saved thread must survive.
func TestAskingBeforeOpeningKeepsTheSavedThreads(test *testing.T) {
	directory := test.TempDir()
	session := newSavedSession(test, savedOptions{directory: directory, host: "github.com"})
	data, err := localthreads.Encode([]localthreads.Thread{{Identifier: "local-1", Question: "old", State: localthreads.StateAnswered, Asked: time.Unix(1, 0)}})
	if err != nil {
		test.Fatal(err)
	}
	if err := localthreads.Write(session.path(), data); err != nil {
		test.Fatal(err)
	}
	session.ask("new")
	session.open()
	var got []string
	for _, thread := range readSaved(test, session.path()) {
		got = append(got, thread.Question)
	}
	if diff := cmp.Diff([]string{"old", "new"}, got); diff != "" {
		test.Errorf("file (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"old", "new"}, questions(session.shown())); diff != "" {
		test.Errorf("shown (-want +got):\n%s", diff)
	}
}

// Close makes good a write command that was never run, and does not wait for
// it: the program ended before Bubble Tea ran the batch.
func TestCloseWritesWhenTheWriteCommandNeverRan(test *testing.T) {
	directory := test.TempDir()
	session := newSavedSession(test, savedOptions{directory: directory, host: "github.com"})
	session.open()
	session.model.update(session.request("lost")) // its commands are dropped
	session.model.Close()
	if saved := readSaved(test, session.path()); len(saved) != 1 || saved[0].Question != "lost" {
		test.Errorf("file after Close = %+v", saved)
	}
}

// The same when a plugin run outlasts the quit wait.
func TestCloseWritesWhenARunOutlastsTheWait(test *testing.T) {
	directory := test.TempDir()
	runner := &stubbornRunner{started: make(chan struct{}), let: make(chan struct{})}
	session := newSavedSession(test, savedOptions{directory: directory, host: "github.com", runner: runner, quitWait: 50 * time.Millisecond})
	session.open()
	results := make(chan tea.Msg, 4)
	var spawn func(command tea.Cmd)
	spawn = func(command tea.Cmd) {
		if command == nil {
			return
		}
		go func() {
			message := command()
			if batch, ok := message.(tea.BatchMsg); ok {
				for _, member := range batch {
					spawn(member)
				}
				return
			}
			results <- message
		}()
	}
	spawn(session.model.update(session.request("stuck")))
	<-runner.started
	session.model.update(session.request("lost")) // its commands are dropped
	session.model.Close()
	close(runner.let)
	saved := readSaved(test, session.path())
	if len(saved) != 2 {
		test.Errorf("file after Close = %+v, want both questions", saved)
	}
}

// A pull request whose saved threads were never read is read by Close and
// merged, so the saved ones are not lost.
func TestCloseKeepsSavedThreadsThatWereNeverRead(test *testing.T) {
	directory := test.TempDir()
	session := newSavedSession(test, savedOptions{directory: directory, host: "github.com"})
	data, err := localthreads.Encode([]localthreads.Thread{{Identifier: "local-1", Question: "old", State: localthreads.StateAnswered, Asked: time.Unix(1, 0)}})
	if err != nil {
		test.Fatal(err)
	}
	if err := localthreads.Write(session.path(), data); err != nil {
		test.Fatal(err)
	}
	session.model.update(session.request("new")) // the read never runs either
	session.model.Close()
	var got []string
	for _, thread := range readSaved(test, session.path()) {
		got = append(got, thread.Question)
	}
	if diff := cmp.Diff([]string{"old", "new"}, got); diff != "" {
		test.Errorf("file (-want +got):\n%s", diff)
	}
}

// A copy made before a newer one was written never replaces it, whoever
// writes it.
func TestAnOlderCopyNeverLandsAfterANewerOne(test *testing.T) {
	directory := test.TempDir()
	session := newSavedSession(test, savedOptions{directory: directory, host: "github.com"})
	session.open()
	identity, _ := session.view.PullRequestIdentity()
	key := pullRequestKey{"github.com", identity.Repository, identity.Number}
	session.model.localThreads.add(LocalThread{Host: "github.com", Repository: identity.Repository, Number: identity.Number, Question: "first"})
	older := session.model.saveLocalThreads(key)
	session.model.localThreads.add(LocalThread{Host: "github.com", Repository: identity.Repository, Number: identity.Number, Question: "second"})
	session.model.Close() // writes the second copy
	older()               // the first copy's command runs late
	if saved := readSaved(test, session.path()); len(saved) != 2 {
		test.Errorf("file = %+v, want both questions", saved)
	}
}

// A thread of another pull request with the same identifier and text is not a
// duplicate.
func TestMergeDuplicateCheckStaysWithinThePullRequest(test *testing.T) {
	var store localThreadStore
	asked := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	thread := LocalThread{Identifier: "local-1", Question: "why?", Path: "a.go", Asked: asked, State: github.LocalAnswered}
	first := pullRequestKey{"github.com", widgetsRepository, 1}
	second := pullRequestKey{"github.com", widgetsRepository, 2}
	store.merge(first, []LocalThread{thread})
	store.merge(second, []LocalThread{thread})
	if got := len(store.byRequest[second]); got != 1 {
		test.Errorf("second pull request has %d threads, want 1", got)
	}
	if store.byRequest[first][0].Identifier == store.byRequest[second][0].Identifier {
		test.Error("both pull requests share an identifier")
	}
}

// bigAnswer is an answer of about size bytes of ordinary text.
func bigAnswer(size int) string { return strings.Repeat("0123456789abcdef", size/16) }

// Five answers of about 900 KB each are more than the file may hold. The file
// is trimmed, oldest first, so it can be read back, and the screen agrees.
func TestSavedFileAlwaysFitsWhatCanBeReadBack(test *testing.T) {
	directory := test.TempDir()
	session := newSavedSession(test, savedOptions{directory: directory, host: "github.com"})
	session.runner = &fakeRunner{effects: []plugins.Effect{{Name: plugins.EffectAnswer, Text: bigAnswer(900_000)}}}
	session.model.pluginRunner = session.runner
	session.open()
	var asked []string
	for _, question := range []string{"q1", "q2", "q3", "q4", "q5"} {
		session.ask(question)
		asked = append(asked, question)
	}
	file, err := localthreads.Read(session.path())
	if err != nil {
		test.Fatalf("the saved file can't be read back: %v", err)
	}
	var kept []string
	for _, thread := range file.Threads {
		kept = append(kept, thread.Question)
	}
	if len(kept) == 0 || len(kept) >= len(asked) || kept[len(kept)-1] != "q5" {
		test.Fatalf("kept %v of %v, want the newest only, fewer than all", kept, asked)
	}
	if diff := cmp.Diff(asked[len(asked)-len(kept):], kept); diff != "" {
		test.Errorf("kept threads are not the newest (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(kept, questions(session.shown())); diff != "" {
		test.Errorf("the screen and the file disagree (-file +screen):\n%s", diff)
	}
	if !strings.Contains(session.logs.String(), "dropped") || strings.Contains(session.logs.String(), "0123456789") {
		test.Errorf("log should count the dropped threads and hold no text:\n%.300s", session.logs)
	}
}

func TestFileSizeLimitDropsTheOldestByAskedTime(test *testing.T) {
	directory := test.TempDir()
	base := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	// Each thread is about 500 bytes in the file; the limit holds three. The
	// second one stored is the oldest asked.
	times := []time.Time{base.Add(2 * time.Hour), base, base.Add(time.Hour), base.Add(3 * time.Hour)}
	session := newSavedSession(test, savedOptions{directory: directory, host: "github.com", bytes: 1700, times: times})
	session.runner = &fakeRunner{effects: []plugins.Effect{{Name: plugins.EffectAnswer, Text: bigAnswer(320)}}}
	session.model.pluginRunner = session.runner
	session.open()
	for _, question := range []string{"q1", "q2", "q3", "q4"} {
		session.ask(question)
	}
	var kept []string
	for _, thread := range readSaved(test, session.path()) {
		kept = append(kept, thread.Question)
	}
	if diff := cmp.Diff([]string{"q1", "q3", "q4"}, kept); diff != "" {
		test.Errorf("file (-want +got):\n%s", diff)
	}
	if info, err := os.Stat(session.path()); err != nil || info.Size() > 1700 {
		test.Errorf("file is over the limit: %v", err)
	}
}

// The newest thread is kept even when it alone is over the limit; dropping it
// would lose the answer the user is waiting for.
func TestNewestThreadIsNeverDroppedForSize(test *testing.T) {
	var store localThreadStore
	store.bytes = 100
	key := pullRequestKey{"github.com", widgetsRepository, 7}
	store.byRequest = map[pullRequestKey][]LocalThread{key: {{Question: "only", Text: bigAnswer(1000)}}}
	if dropped := store.fit(key); dropped != 0 || len(store.byRequest[key]) != 1 {
		test.Errorf("dropped %d, %d left", dropped, len(store.byRequest[key]))
	}
}

// One answer is at most what a plugin may print, which must stay far below
// what a file may hold, so the newest thread always fits. Answers hold code,
// so characters JSON could escape are the worst ordinary case.
func TestAFullSizeAnswerFitsInAFile(test *testing.T) {
	text := strings.Repeat("<a & b>\n", plugins.MaximumOutputSize/8)
	size := localthreads.FileOverhead + localthreads.ThreadSize(localthreads.Thread{Question: "q", Text: text, State: localthreads.StateAnswered})
	if size*2 > localthreads.MaxFileSize {
		test.Errorf("a full-size answer takes %d bytes of a %d byte file", size, localthreads.MaxFileSize)
	}
}

// A save that is stuck holds the file mutex. Quitting must still cancel the
// plugin runs and return within the quit wait.
func TestCloseIsBoundedWhileAWriteIsStuck(test *testing.T) {
	directory := test.TempDir()
	session := newSavedSession(test, savedOptions{directory: directory, host: "github.com", quitWait: 100 * time.Millisecond})
	session.open()
	session.ask("first") // starts the plugin context
	session.model.update(session.request("lost"))
	files := &session.model.localFiles
	files.mutex.Lock()
	released := false
	release := func() {
		if !released {
			released = true
			files.mutex.Unlock()
		}
	}
	defer release()
	closed := make(chan struct{})
	go func() {
		session.model.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		test.Fatal("Close did not return while a write held the mutex")
	}
	if session.model.pluginContext.Err() == nil {
		test.Error("the plugin runs were not cancelled")
	}
	release()
	<-files.flushed // the stuck writes end before the directory is removed
}
