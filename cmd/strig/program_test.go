//go:build unix

package main

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/config"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/plugins"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

// guard bounds a wait that only a broken implementation would not finish.
const guard = 20 * time.Second

// blankView is a screen with nothing on it.
type blankView struct{}

func (blankView) Init() tea.Cmd                                     { return nil }
func (view blankView) Update(tea.Msg) (userinterface.View, tea.Cmd) { return view, nil }
func (blankView) Render(int, int) string                            { return "" }
func (blankView) Title() string                                     { return "blank" }
func (blankView) ShortHelp() []key.Binding                          { return nil }
func (blankView) FullHelp() [][]key.Binding                         { return nil }

// A program that ends without the model seeing a quit (SIGTERM ends Bubble Tea
// with a QuitMsg that goes no further than the program) must still leave no
// plugin running: runProgram cancels the runs and waits for them.
func TestProgramEndedBySignalKillsRunningPlugins(test *testing.T) {
	directory := test.TempDir()
	ready := filepath.Join(directory, "ready")
	if err := syscall.Mkfifo(ready, 0o600); err != nil {
		test.Fatal(err)
	}
	script := filepath.Join(directory, "slow.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho $$ > \"$READY\"\nexec sleep 300\n"), 0o700); err != nil {
		test.Fatal(err)
	}
	runner := plugins.New(
		[]config.Plugin{{Command: "slow", OnRun: []string{script}, Target: config.TargetPullRequest, Timeout: guard}},
		plugins.Options{Environment: []string{"PATH=" + os.Getenv("PATH"), "READY=" + ready}},
	)
	model := userinterface.New(blankView{}, theme.Default(), userinterface.Options{
		PluginRunner:   runner,
		PluginCommands: []userinterface.PluginCommand{{Name: "slow", Target: config.TargetPullRequest}},
		PluginQuitWait: guard,
	})
	input, _ := io.Pipe() // never written: the program reads no keys
	program := tea.NewProgram(model, tea.WithInput(input), tea.WithOutput(io.Discard), tea.WithoutSignals())
	finished := make(chan error, 1)
	go func() { finished <- runProgram(program, model) }()

	program.Send(userinterface.RunPluginMessage{
		Name:     "slow",
		Identity: userinterface.PullRequestIdentity{Repository: github.Repository{Owner: "o", Name: "r"}, Number: 1},
	})
	processIdentifier := readProcess(test, ready)
	reaped := false
	test.Cleanup(func() {
		if !reaped { // still ours only if the test never saw the program finish
			_ = syscall.Kill(processIdentifier, syscall.SIGKILL)
		}
	})

	program.Send(tea.QuitMsg{}) // what a SIGTERM does: the model never hears of it
	select {
	case err := <-finished:
		if err != nil && !errors.Is(err, tea.ErrProgramKilled) {
			test.Logf("program ended with %v", err)
		}
	case <-time.After(guard):
		test.Fatal("the program did not end")
	}
	reaped = true
	if !errors.Is(syscall.Kill(processIdentifier, 0), syscall.ESRCH) {
		_ = syscall.Kill(processIdentifier, syscall.SIGKILL) // it was just seen alive: clean up what the failure left
		test.Error("the plugin is still running after the program ended")
	}
}

// readProcess waits for the plugin to report its process number.
func readProcess(test *testing.T, pipePath string) int {
	test.Helper()
	opened := make(chan *os.File, 1)
	go func() {
		pipe, err := os.Open(pipePath) // blocks until the plugin opens it
		if err != nil {
			pipe = nil
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
		test.Fatal(err)
	}
	processIdentifier, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		test.Fatal(err)
	}
	return processIdentifier
}
