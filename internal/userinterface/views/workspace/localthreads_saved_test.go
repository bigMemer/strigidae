package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/localthreads"
	"github.com/MikePors/strigidae/internal/userinterface"
)

func withStateDirectory(directory string) func(*userinterface.Options) {
	return func(startup *userinterface.Options) { startup.StateDirectory = directory }
}

// opened tells the root the pull request on screen has loaded, as the view does
// when its load ends, and runs what follows.
func (session *pluginSession) opened() {
	session.test.Helper()
	identity, ok := session.inliner.view.PullRequestIdentity()
	if !ok {
		session.test.Fatal("the pull request has not loaded")
	}
	session.pump(session.send(userinterface.PullRequestOpenedMessage{Identity: identity}))
}

func TestPullRequestLoadTellsTheRootWhichHostItIsOn(test *testing.T) {
	inliner := newInliner(test)
	command := inliner.view.pullRequestLoaded(pullRequestLoadedMessage{detail: *inliner.view.detail})
	if command == nil {
		test.Fatal("loading a pull request returned no command")
	}
	opened, ok := command().(userinterface.PullRequestOpenedMessage)
	if !ok {
		test.Fatalf("message = %T, want PullRequestOpenedMessage", command())
	}
	if opened.Identity.Host != "example.test" || opened.Identity.Number != 7 || opened.Identity.Repository != testRepository {
		test.Errorf("identity = %+v", opened.Identity)
	}
}

// An answer is on the diff after strig is started again, and a question that
// was waiting says why it has no answer.
func TestLocalThreadsComeBackAfterARestart(test *testing.T) {
	directory := test.TempDir()
	first := newThreadSession(test, &recordingRunner{effects: answerEffects("Because it is.")}, withStateDirectory(directory))
	first.opened()
	first.ask()
	if !strings.Contains(first.screen(), "Because it is.") {
		test.Fatal("the answer is not on screen before the restart")
	}
	first.model.Close()

	second := newThreadSession(test, &recordingRunner{}, withStateDirectory(directory))
	second.opened()
	screen := second.screen()
	for _, want := range []string{"why?", "Because it is."} {
		if !strings.Contains(screen, want) {
			test.Errorf("after the restart the screen lacks %q:\n%s", want, screen)
		}
	}
}

func TestWaitingLocalThreadSaysStrigWasClosed(test *testing.T) {
	directory := test.TempDir()
	first := newStackedSession(test, &recordingRunner{effects: answerEffects("never seen")}, withThreadCommands, withStateDirectory(directory))
	first.opened()
	first.held("ask why?") // the answer never arrives: strig exits first
	first.model.Close()

	second := newThreadSession(test, &recordingRunner{}, withStateDirectory(directory))
	second.opened()
	if screen := second.screen(); !strings.Contains(screen, "no answer: strig was closed") {
		test.Errorf("screen lacks the reason:\n%s", screen)
	}
}

// Text read from a file is as untrusted as the plugin's own.
func TestLoadedLocalThreadTextIsCleaned(test *testing.T) {
	hostile := "hi\x1b]0;owned\x07\x1b[2J\u009b31m\u202etxt\xff"
	directory := test.TempDir()
	session := newThreadSession(test, &recordingRunner{}, withStateDirectory(directory))
	identity, _ := session.inliner.view.PullRequestIdentity()
	path, err := localthreads.Path(directory, identity.Host, identity.Repository.Owner, identity.Repository.Name, identity.Number)
	if err != nil {
		test.Fatal(err)
	}
	data, err := localthreads.Encode([]localthreads.Thread{
		{Identifier: "local-1", Head: session.inliner.view.diffHead, Path: "internal/sync/client.go", Side: github.SideRight, Line: 15, StartLine: 15, Question: hostile, State: localthreads.StateAnswered, Text: hostile},
		{Identifier: "local-2", Head: session.inliner.view.diffHead, Path: "internal/sync/client.go", Side: github.SideRight, Line: 15, StartLine: 15, Question: "q", State: localthreads.StateFailed, Text: hostile},
	})
	if err != nil {
		test.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		test.Fatal(err)
	}
	session.opened()
	raw := session.model.View().Content
	screen := ansi.Strip(raw)
	if !strings.Contains(screen, "txt") {
		test.Errorf("the loaded text is not shown:\n%s", screen)
	}
	if strings.IndexFunc(screen, func(character rune) bool { return unicode.IsControl(character) && character != '\n' }) >= 0 {
		test.Error("screen holds a control character")
	}
	for _, bad := range []string{"owned\x07", "\x1b]0", "\x1b[2J", "\u202e", "\u009b"} {
		if strings.Contains(raw, bad) {
			test.Errorf("raw output holds %q", bad)
		}
	}
}
