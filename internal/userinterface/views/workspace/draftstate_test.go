package workspace

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/github/fake"
	"github.com/MikePors/strigidae/internal/userinterface"
)

// setServerState changes the pull request on the fake only, so the screen is
// stale until a reload.
func (inliner *inliner) setServerState(state github.PullRequestState, draft bool) {
	inliner.test.Helper()
	detail := testDetail()
	detail.Identifier, detail.ViewerCanUpdate = pullRequestNodeIdentifier, true
	detail.State, detail.Draft = state, draft
	inliner.client.SetPullRequest(detail)
}

// setDraft changes the pull request on the fake and reloads the view.
func (inliner *inliner) setDraft(draft bool) {
	inliner.test.Helper()
	inliner.setServerState(github.PullRequestStateOpen, draft)
	inliner.press("R")
}

func (inliner *inliner) draftChanges() []fake.DraftChange { return inliner.client.DraftChanges() }

func (inliner *inliner) loads() int {
	return len(inliner.methods(fake.MethodGetPullRequest))
}

func TestDraftCommandsAskThenChange(test *testing.T) {
	tests := []struct {
		name       string
		command    string
		startDraft bool
		question   string
		method     string
		notice     string
	}{
		{"ready", "ready", true, "Mark #7 ready for review?", fake.MethodMarkReadyForReview, "#7 is ready for review"},
		{"draft", "draft", false, "Convert #7 to a draft?", fake.MethodConvertToDraft, "#7 is now a draft"},
	}
	for _, testCase := range tests {
		test.Run(testCase.name+" confirmed", func(test *testing.T) {
			inliner := newInliner(test)
			inliner.setDraft(testCase.startDraft)
			inliner.messages = nil
			inliner.command(testCase.command)
			confirmation, asked := inliner.lastConfirm()
			if !asked || confirmation.Text != testCase.question {
				test.Fatalf("question = %q (asked %v), want %q", confirmation.Text, asked, testCase.question)
			}
			if len(inliner.draftChanges()) != 0 {
				test.Fatal("written before the answer")
			}
			before := inliner.loads()
			inliner.messages = nil
			inliner.run(confirmation.OnYes)
			want := []fake.DraftChange{{Method: testCase.method, Repository: testRepository, Number: 7, PullRequestIdentifier: pullRequestNodeIdentifier}}
			if diff := cmp.Diff(want, inliner.draftChanges()); diff != "" {
				test.Errorf("changes (-want +got):\n%s", diff)
			}
			if !contains(inliner.infoMessages(), testCase.notice) {
				test.Errorf("notices = %v, want %q", inliner.infoMessages(), testCase.notice)
			}
			if inliner.loads() <= before {
				test.Error("the pull request was not reloaded")
			}
			if inliner.view.detail.Draft == testCase.startDraft {
				test.Error("the reloaded pull request still has the old draft flag")
			}
			if inliner.view.writes != 0 {
				test.Errorf("writes in flight: %d", inliner.view.writes)
			}
			inliner.press("h", "g g")
			if shown := inliner.shown(); strings.Contains(shown, "draft") != !testCase.startDraft {
				test.Errorf("the header does not show the new state:\n%s", shown)
			}
		})
		test.Run(testCase.name+" declined", func(test *testing.T) {
			inliner := newInliner(test)
			inliner.setDraft(testCase.startDraft)
			inliner.messages = nil
			inliner.command(testCase.command)
			if _, asked := inliner.lastConfirm(); !asked {
				test.Fatal("no question")
			}
			// Declining runs nothing: the OnYes command is never called.
			if len(inliner.draftChanges()) != 0 || inliner.view.writes != 0 {
				test.Error("something was written or is in flight")
			}
		})
	}
}

func TestDraftCommandRefusals(test *testing.T) {
	tests := []struct {
		name    string
		command string
		state   github.PullRequestState
		draft   bool
		want    string
	}{
		{"ready on a pull request that is not a draft", "ready", github.PullRequestStateOpen, false, "this pull request is not a draft"},
		{"draft on a draft", "draft", github.PullRequestStateOpen, true, "this pull request is already a draft"},
		{"ready on a closed draft", "ready", github.PullRequestStateClosed, true, "this pull request is closed"},
		{"draft on a closed pull request", "draft", github.PullRequestStateClosed, false, "this pull request is closed"},
		{"ready on a merged pull request", "ready", github.PullRequestStateMerged, true, "this pull request is merged"},
		{"draft on a merged pull request", "draft", github.PullRequestStateMerged, false, "this pull request is merged"},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			inliner := newInliner(test)
			inliner.setServerState(testCase.state, testCase.draft)
			inliner.press("R")
			inliner.messages = nil
			inliner.command(testCase.command)
			if diff := cmp.Diff([]string{testCase.want}, inliner.infoMessages()); diff != "" {
				test.Errorf("notices (-want +got):\n%s", diff)
			}
			if _, asked := inliner.lastConfirm(); asked {
				test.Error("a question was asked")
			}
			if len(inliner.draftChanges()) != 0 {
				test.Error("something was sent")
			}
		})
	}
}

// The screen says one thing and GitHub another: the re-read decides.
func TestDraftCommandFreshCheckCatchesAStaleFlag(test *testing.T) {
	tests := []struct {
		name         string
		command      string
		screenDraft  bool
		serverState  github.PullRequestState
		serverDraft  bool
		want         string
		wantReloaded bool
	}{
		{"ready after it was made ready elsewhere", "ready", true, github.PullRequestStateOpen, false, "this pull request is not a draft", true},
		{"draft after it was made a draft elsewhere", "draft", false, github.PullRequestStateOpen, true, "this pull request is already a draft", true},
		{"ready after it was closed elsewhere", "ready", true, github.PullRequestStateClosed, true, "this pull request is closed", true},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			inliner := newInliner(test)
			inliner.setDraft(testCase.screenDraft)
			inliner.setServerState(testCase.serverState, testCase.serverDraft)
			before := inliner.loads()
			inliner.messages = nil
			inliner.command(testCase.command)
			if diff := cmp.Diff([]string{testCase.want}, inliner.infoMessages()); diff != "" {
				test.Errorf("notices (-want +got):\n%s", diff)
			}
			if _, asked := inliner.lastConfirm(); asked {
				test.Error("a question was asked on a stale flag")
			}
			if len(inliner.draftChanges()) != 0 {
				test.Error("a mutation was sent on a stale flag")
			}
			if testCase.wantReloaded && inliner.loads() < before+2 {
				test.Errorf("loads = %d, want the re-read and a reload after %d", inliner.loads(), before)
			}
			if inliner.view.detail.Draft != testCase.serverDraft {
				test.Error("the screen was not refreshed")
			}
		})
	}
}

func TestDraftCommandShowsFailures(test *testing.T) {
	tests := []struct {
		name       string
		command    string
		startDraft bool
		method     string
	}{
		{"ready", "ready", true, fake.MethodMarkReadyForReview},
		{"draft", "draft", false, fake.MethodConvertToDraft},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			inliner := newInliner(test)
			inliner.setDraft(testCase.startDraft)
			inliner.messages = nil
			inliner.command(testCase.command)
			confirmation, _ := inliner.lastConfirm()
			inliner.client.SetError(testCase.method, errors.New("boom \x1b]0;pwned\x07"))
			inliner.messages = nil
			inliner.run(confirmation.OnYes)
			err := inliner.lastErr()
			if err == nil || !strings.Contains(err.Error(), "boom") || strings.Contains(err.Error(), "\x1b") {
				test.Errorf("error = %v, want boom without escapes", err)
			}
			if inliner.view.writes != 0 {
				test.Errorf("writes in flight: %d", inliner.view.writes)
			}
			if inliner.view.detail.Draft != testCase.startDraft {
				test.Error("the screen shows a change that was not made")
			}
		})
	}
}

func TestDraftCommandTimeoutReloadsAndSaysItMayHaveGoneThrough(test *testing.T) {
	tests := []struct {
		name       string
		command    string
		startDraft bool
	}{
		{"ready", "ready", true},
		{"draft", "draft", false},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			inliner := newInliner(test)
			inliner.setDraft(testCase.startDraft)
			inliner.view.client = blockingClient{Client: inliner.client}
			inliner.view.timeouts = shortTimeouts
			inliner.messages = nil
			inliner.command(testCase.command)
			confirmation, _ := inliner.lastConfirm()
			before := inliner.loads()
			inliner.messages = nil
			inliner.run(confirmation.OnYes)
			err := inliner.lastErr()
			if !userinterface.IsWriteTimeout(err) || !strings.Contains(err.Error(), "it may have gone through") {
				test.Errorf("error = %v, want a write timeout that may have gone through", err)
			}
			if inliner.loads() <= before {
				test.Error("no reload after the timeout")
			}
			if inliner.view.writes != 0 {
				test.Errorf("writes in flight: %d", inliner.view.writes)
			}
		})
	}
}

// The state changes while the question is open: the yes re-reads and sends
// nothing.
func TestDraftCommandYesRereadsBeforeWriting(test *testing.T) {
	tests := []struct {
		name        string
		command     string
		screenDraft bool
		serverState github.PullRequestState
		serverDraft bool
		want        string
	}{
		{"merged meanwhile", "ready", true, github.PullRequestStateMerged, true, "this pull request is merged"},
		{"closed meanwhile", "draft", false, github.PullRequestStateClosed, false, "this pull request is closed"},
		{"made ready meanwhile", "ready", true, github.PullRequestStateOpen, false, "this pull request is not a draft"},
		{"made a draft meanwhile", "draft", false, github.PullRequestStateOpen, true, "this pull request is already a draft"},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			inliner := newInliner(test)
			inliner.setDraft(testCase.screenDraft)
			inliner.messages = nil
			inliner.command(testCase.command)
			confirmation, asked := inliner.lastConfirm()
			if !asked {
				test.Fatal("no question")
			}
			inliner.setServerState(testCase.serverState, testCase.serverDraft)
			before := inliner.loads()
			inliner.messages = nil
			inliner.run(confirmation.OnYes)
			if len(inliner.draftChanges()) != 0 || len(inliner.methods(fake.MethodMarkReadyForReview, fake.MethodConvertToDraft)) != 0 {
				test.Fatal("a mutation was sent on a stale yes")
			}
			if diff := cmp.Diff([]string{testCase.want}, inliner.infoMessages()); diff != "" {
				test.Errorf("notices (-want +got):\n%s", diff)
			}
			if inliner.loads() < before+2 { // the re-read and the reload
				test.Errorf("loads = %d after %d, want a re-read and a reload", inliner.loads(), before)
			}
			if inliner.view.detail.Draft != testCase.serverDraft || inliner.view.detail.State != testCase.serverState {
				test.Error("the screen was not refreshed")
			}
			if inliner.view.writes != 0 {
				test.Errorf("writes in flight: %d", inliner.view.writes)
			}
		})
	}
}

// A screen made stale elsewhere is refused after a read, and the header
// follows the reload.
func TestDraftCommandRefusesAStaleScreenAndUpdatesTheHeader(test *testing.T) {
	inliner := newInliner(test)
	inliner.setDraft(true)
	inliner.setServerState(github.PullRequestStateOpen, false) // made ready elsewhere
	if !strings.Contains(inliner.shown(), "draft") {
		test.Fatal("the header does not show the draft state before")
	}
	inliner.messages = nil
	inliner.command("ready")
	if diff := cmp.Diff([]string{noticeNotADraft}, inliner.infoMessages()); diff != "" {
		test.Errorf("notices (-want +got):\n%s", diff)
	}
	if strings.Contains(inliner.shown(), "draft") {
		test.Error("the header still shows draft after the refresh")
	}
}

func TestDraftCommandReadFailureAsksNothing(test *testing.T) {
	inliner := newInliner(test)
	inliner.setDraft(true)
	inliner.client.SetError(fake.MethodGetPullRequest, errors.New("offline"))
	inliner.messages = nil
	inliner.command("ready")
	if err := inliner.lastErr(); err == nil || !strings.Contains(err.Error(), "offline") {
		test.Errorf("error = %v", err)
	}
	if _, asked := inliner.lastConfirm(); asked || len(inliner.draftChanges()) != 0 {
		test.Error("asked or sent after a failed re-read")
	}
}
