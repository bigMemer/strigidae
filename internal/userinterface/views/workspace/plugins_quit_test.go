//go:build unix

package workspace

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/config"
	"github.com/MikePors/strigidae/internal/plugins"
	"github.com/MikePors/strigidae/internal/userinterface"
)

// guard bounds a wait that only a broken implementation would not finish.
const guard = 20 * time.Second

// slowPlugin is a real program: it counts its starts, tells its process
// number through a named pipe the test reads (so the test knows it runs) and
// then waits for a signal.
type slowPlugin struct {
	ready, count string
	runner       *plugins.Runner
	process      int  // the last process number the plugin reported
	ended        bool // the test has seen the run end, so the number is no longer its own
}

func newSlowPlugin(test *testing.T) *slowPlugin {
	test.Helper()
	directory := test.TempDir()
	script := filepath.Join(directory, "slow.sh")
	body := "#!/bin/sh\necho run >> \"$COUNT_FILE\"\necho $$ > \"$READY\"\nexec sleep 300\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		test.Fatal(err)
	}
	slow := &slowPlugin{ready: filepath.Join(directory, "ready"), count: filepath.Join(directory, "count")}
	if err := syscall.Mkfifo(slow.ready, 0o600); err != nil {
		test.Fatal(err)
	}
	slow.runner = plugins.New(
		[]config.Plugin{{Command: "summarize", OnRun: []string{script}, Target: config.TargetPullRequest, Timeout: guard}},
		plugins.Options{Environment: []string{"PATH=" + os.Getenv("PATH"), "COUNT_FILE=" + slow.count, "READY=" + slow.ready}},
	)
	test.Cleanup(func() {
		// Only a process the test still knows is alive: once Run has returned
		// the runner has killed and reaped it, and the number may be someone
		// else's.
		if slow.process > 0 && !slow.ended {
			_ = syscall.Kill(slow.process, syscall.SIGKILL)
		}
	})
	return slow
}

// started waits until the plugin runs and returns its process number.
func (slow *slowPlugin) started(test *testing.T) int {
	test.Helper()
	opened := make(chan *os.File, 1)
	go func() {
		pipe, err := os.Open(slow.ready) // blocks until the plugin opens it
		if err != nil {
			opened <- nil
			return
		}
		opened <- pipe
	}()
	var pipe *os.File
	select {
	case pipe = <-opened:
	case <-time.After(guard):
		test.Fatal("the plugin did not start")
	}
	if pipe == nil {
		test.Fatal("cannot open the pipe")
	}
	defer func() { _ = pipe.Close() }()
	text, err := bufio.NewReader(pipe).ReadString('\n')
	if err != nil {
		test.Fatalf("read the process number: %v", err)
	}
	processIdentifier, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		test.Fatal(err)
	}
	slow.process = processIdentifier
	return processIdentifier
}

func (slow *slowPlugin) starts() int {
	data, err := os.ReadFile(slow.count)
	if err != nil {
		return 0
	}
	return strings.Count(string(data), "run\n")
}

func alive(processIdentifier int) bool {
	return !errors.Is(syscall.Kill(processIdentifier, 0), syscall.ESRCH)
}

// startAsync types a line that starts a run and performs the run in the
// background; what it produces arrives on the channel.
func (session *pluginSession) startAsync(text string, results chan<- tea.Msg) {
	session.test.Helper()
	session.key(":")
	for _, character := range text {
		session.key(string(character))
	}
	asked := session.key("enter")()
	runInBackground(session.send(asked), results)
}

// runInBackground performs a command, and the members of a batch it turns out
// to be, in goroutines of their own, because the run blocks.
func runInBackground(command tea.Cmd, results chan<- tea.Msg) {
	if command == nil {
		return
	}
	go func() {
		message := command()
		if members, ok := message.(tea.BatchMsg); ok {
			for _, member := range members {
				runInBackground(member, results)
			}
			return
		}
		results <- message
	}()
}

func receiveResult(test *testing.T, results <-chan tea.Msg) tea.Msg {
	test.Helper()
	for {
		select {
		case message := <-results:
			if _, isFrame := message.(spinner.TickMsg); !isFrame && message != nil {
				return message
			}
		case <-time.After(guard):
			test.Fatal("no result arrived")
		}
	}
}

// Quitting cancels the runs: the plugin process is killed, the run queued
// behind it never starts, and what comes back after quit began is not shown.
func TestQuitDuringARunKillsItAndSkipsTheQueue(test *testing.T) {
	slow := newSlowPlugin(test)
	var timers []func(time.Time) tea.Msg
	session := newStackedSession(test, slow.runner, func(startup *userinterface.Options) {
		startup.Tick = func(_ time.Duration, function func(time.Time) tea.Msg) tea.Cmd {
			timers = append(timers, function)
			return nil
		}
	})
	results := make(chan tea.Msg, 16)
	session.startAsync("summarize", results)
	processIdentifier := slow.started(test)
	session.startAsync("summarize", results) // waits its turn behind the first

	session.line("qa")
	if session.quit {
		test.Fatal("quit at once with a run under way")
	}
	for range 2 {
		session.pump(func() tea.Msg { return receiveResult(test, results) })
	}
	slow.ended = true
	if !session.quit {
		test.Error("strig did not quit once the cancelled runs ended")
	}
	if alive(processIdentifier) {
		test.Error("the plugin process is still running")
	}
	if got := slow.starts(); got != 1 {
		test.Errorf("the plugin started %d times, want the queued run never to start", got)
	}
	if got := session.status(); strings.Contains(got, "context canceled") {
		test.Errorf("the cancelled run was shown: %q", got)
	}
}

// Closing the pull request leaves the run alone.
func TestClosingThePullRequestKeepsTheRunGoing(test *testing.T) {
	slow := newSlowPlugin(test)
	session := newStackedSession(test, slow.runner)
	results := make(chan tea.Msg, 16)
	session.startAsync("summarize", results)
	processIdentifier := slow.started(test)

	session.line("q")
	select {
	case message := <-results:
		if _, isFrame := message.(spinner.TickMsg); !isFrame {
			test.Fatalf("closing the view ended the run: %v", message)
		}
	default:
	}
	if !alive(processIdentifier) {
		test.Fatal("closing the view killed the plugin")
	}
	session.line("qa")
	session.pump(func() tea.Msg { return receiveResult(test, results) })
	slow.ended = true
	if !session.quit || alive(processIdentifier) {
		test.Errorf("quit = %v, plugin alive = %v", session.quit, alive(processIdentifier))
	}
}
