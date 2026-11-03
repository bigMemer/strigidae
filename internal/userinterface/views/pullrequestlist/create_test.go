package pullrequestlist

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/config"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/github/fake"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

var testBranches = []github.Branch{{Name: "feat/a"}, {Name: "feat/b"}, {Name: "main", Default: true}}

// editorReturning is an EditFunction that "closes" at once with text, and
// counts how often it was opened.
func editorReturning(text string, opened *int) func(string, func(string, error) tea.Msg) tea.Cmd {
	return func(initial string, done func(string, error) tea.Msg) tea.Cmd {
		*opened++
		if initial != "" {
			panic("the editor must open on an empty file")
		}
		return func() tea.Msg { return done(text, nil) }
	}
}

type createHarness struct {
	view   *View
	client *fake.Client
	opened int
}

func newCreateHarness(test *testing.T, currentBranch, editorText string) *createHarness {
	test.Helper()
	return newCreateHarnessWith(test, 2, currentBranch, editorText)
}

func newCreateHarnessWith(test *testing.T, pullRequests int, currentBranch, editorText string) *createHarness {
	test.Helper()
	client := fakeWith(makePullRequests(pullRequests))
	client.SetBranches(testRepository, testBranches)
	harness := &createHarness{client: client}
	harness.view = New(client, testRepository, config.Default().Tabs, theme.Default(), Options{
		Now: func() time.Time { return testNow }, CurrentBranch: currentBranch,
		Edit: editorReturning(editorText, &harness.opened),
	})
	harness.view.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	deliver(test, harness.view, harness.view.Init())
	return harness
}

// command runs ":name" and delivers the branches, leaving the head picker open.
func (harness *createHarness) command(test *testing.T, name string) {
	test.Helper()
	command, handled := harness.view.Command(name, "")
	if !handled || command == nil {
		test.Fatalf(":%s handled=%v command=%v", name, handled, command)
	}
	if !harness.view.Loading() {
		test.Error("the view should show loading while the branches load")
	}
	deliver(test, harness.view, command)
}

func enter(view *View) tea.Cmd {
	_, command := view.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return command
}

func escape(view *View) tea.Cmd {
	_, command := view.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	return command
}

// chooseBoth accepts both pickers and returns the editor's message.
func (harness *createHarness) chooseBoth(test *testing.T) createTextMessage {
	test.Helper()
	if command := enter(harness.view); command != nil {
		test.Fatalf("choosing the head should only open the base picker, got a command")
	}
	command := enter(harness.view)
	if command == nil {
		test.Fatal("choosing the base should open the editor")
	}
	message, ok := command().(createTextMessage)
	if !ok {
		test.Fatalf("editor result is %T", command())
	}
	return message
}

func TestHeadPickerPreselection(test *testing.T) {
	for _, testCase := range []struct {
		name    string
		current string
		want    string
	}{
		{"checkout branch is preselected", "feat/b", "feat/b"},
		{"no checkout selects nothing, the first branch is under the cursor", "", "feat/a"},
		{"a checkout branch GitHub lacks selects nothing", "local-only", "feat/a"},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			harness := newCreateHarness(test, testCase.current, "Title")
			harness.command(test, "create-draft")
			if !harness.view.CapturesKeys() {
				test.Fatal("the head picker should capture keys")
			}
			if got := harness.chooseBoth(test).head; got != testCase.want {
				test.Errorf("head = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestBasePickerLeavesOutTheHeadAndPreselectsTheDefault(test *testing.T) {
	for _, testCase := range []struct {
		name, current, wantBase string
		wantItems               int
	}{
		{"default branch under the cursor", "feat/b", "main", 2},
		{"head is the default branch: first of the rest", "main", "feat/a", 2},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			harness := newCreateHarness(test, testCase.current, "Title")
			harness.command(test, "create-draft")
			enter(harness.view)
			if got := harness.view.creating.picker.Matches(); got != testCase.wantItems {
				test.Errorf("base picker lists %d branches, want %d (the head is left out)", got, testCase.wantItems)
			}
			for _, name := range harness.view.creating.choices {
				if name == testCase.current {
					test.Errorf("the head %q is offered as the base", name)
				}
			}
			message := func() createTextMessage {
				command := enter(harness.view)
				return command().(createTextMessage)
			}()
			if message.base != testCase.wantBase || message.head != testCase.current {
				test.Errorf("head, base = %q, %q; want %q, %q", message.head, message.base, testCase.current, testCase.wantBase)
			}
		})
	}
}

func TestEscapeCancelsEitherPicker(test *testing.T) {
	for _, presses := range []int{0, 1} { // escape in the head picker, escape in the base picker
		harness := newCreateHarness(test, "feat/a", "Title")
		harness.command(test, "create")
		for range presses {
			enter(harness.view)
		}
		if command := escape(harness.view); command != nil {
			test.Errorf("after %d choices, esc produced a command", presses)
		}
		if harness.view.CapturesKeys() || harness.view.creating != nil {
			test.Errorf("after %d choices, esc did not end the flow", presses)
		}
		if harness.opened != 0 || len(harness.client.CreatedPullRequests()) != 0 {
			test.Errorf("after %d choices, a cancelled flow went on (editor opened %d times)", presses, harness.opened)
		}
	}
}

func TestOnlyBranchLeavesNothingToMergeInto(test *testing.T) {
	harness := newCreateHarness(test, "main", "Title")
	harness.client.SetBranches(testRepository, []github.Branch{{Name: "main", Default: true}})
	harness.command(test, "create")
	command := enter(harness.view)
	if message, ok := command().(userinterface.InfoMessage); !ok || !strings.Contains(message.Text, "no other branch") {
		test.Errorf("got %#v, want a notice that there is no other branch", command())
	}
	if harness.view.CapturesKeys() {
		test.Error("the flow should have ended")
	}
}

func TestBranchListFailureIsShown(test *testing.T) {
	harness := newCreateHarness(test, "", "Title")
	harness.client.SetError(fake.MethodListBranches, errors.New("boom\x1b[31m"))
	command, _ := harness.view.Command("create", "")
	next := deliver(test, harness.view, command)
	message, ok := next().(userinterface.ErrorMessage)
	if !ok || !strings.Contains(message.Err.Error(), "boom") || strings.Contains(message.Err.Error(), "\x1b") {
		test.Errorf("got %#v, want a cleaned error naming boom", next())
	}
	if harness.view.CapturesKeys() {
		test.Error("no picker should open after a failure")
	}
}

func TestStaleBranchesAreIgnored(test *testing.T) {
	harness := newCreateHarness(test, "", "Title")
	first, _ := harness.view.Command("create", "")
	harness.view.Command("create-draft", "") // supersedes the first
	deliver(test, harness.view, first)
	if harness.view.CapturesKeys() {
		test.Error("a superseded flow opened a picker")
	}
}

func TestHostileBranchNamesAreCleaned(test *testing.T) {
	harness := newCreateHarness(test, "", "Title")
	harness.client.SetBranches(testRepository, []github.Branch{{Name: "evil\x1b]52;c;aGk=\x07branch"}})
	harness.command(test, "create")
	if screen := harness.view.Render(80, 20); strings.ContainsAny(screen, "\x07") || strings.Contains(screen, "\x1b]") {
		test.Errorf("branch name reached the screen uncleaned: %q", screen)
	}
}

func TestEditorTextBecomesTitleAndBody(test *testing.T) {
	for _, testCase := range []struct {
		name, text, wantTitle, wantBody string
	}{
		{"title only", "Add the thing\n", "Add the thing", ""},
		{"title and body", "Add the thing\n\nFirst line.\n\nSecond.\n", "Add the thing", "First line.\n\nSecond."},
		{"no blank line after the title", "Add the thing\nBody right away\n", "Add the thing", "Body right away"},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			harness := newCreateHarness(test, "feat/b", testCase.text)
			harness.command(test, "create-draft")
			created := runCreateDraft(test, harness)
			want := github.CreatePullRequestInput{Repository: testRepository, Head: "feat/b", Base: "main", Title: testCase.wantTitle, Body: testCase.wantBody, Draft: true}
			if diff := cmp.Diff([]github.CreatePullRequestInput{want}, created); diff != "" {
				test.Errorf("create input (-want +got):\n%s", diff)
			}
		})
	}
}

// runCreateDraft goes through both pickers and the editor and returns what the
// client was asked to create; it fails if the flow asked a question first.
func runCreateDraft(test *testing.T, harness *createHarness) []github.CreatePullRequestInput {
	test.Helper()
	message := harness.chooseBoth(test)
	_, command := harness.view.Update(message)
	if command == nil {
		test.Fatal("the editor text produced no command")
	}
	result := command()
	if _, asked := result.(userinterface.ConfirmMessage); asked {
		test.Fatal("a draft must be created without y/N")
	}
	if _, ok := result.(pullRequestCreatedMessage); !ok {
		test.Fatalf("got %T, want the create result", result)
	}
	return harness.client.CreatedPullRequests()
}

func TestEmptyTitleCreatesNothing(test *testing.T) {
	for _, command := range []string{"create", "create-draft"} {
		harness := newCreateHarness(test, "feat/b", "\n\nbody without a title\n")
		harness.command(test, command)
		_, next := harness.view.Update(harness.chooseBoth(test))
		message, ok := next().(userinterface.ErrorMessage)
		if !ok || !strings.Contains(message.Err.Error(), "empty title") {
			test.Errorf(":%s: got %#v, want an empty-title error", command, next())
		}
		if got := harness.client.CreatedPullRequests(); len(got) != 0 {
			test.Errorf(":%s created %v", command, got)
		}
	}
}

func TestCreateAsksBeforePublishingAndDraftDoesNot(test *testing.T) {
	harness := newCreateHarness(test, "feat/b", "Add the thing\n\nBody\n")
	harness.command(test, "create")
	_, command := harness.view.Update(harness.chooseBoth(test))
	confirm, ok := command().(userinterface.ConfirmMessage)
	if !ok {
		test.Fatalf(":create produced %T, want a y/N question", command())
	}
	for _, want := range []string{"Add the thing", "feat/b", "main"} {
		if !strings.Contains(confirm.Text, want) {
			test.Errorf("question %q does not name %q", confirm.Text, want)
		}
	}
	if got := harness.client.CreatedPullRequests(); len(got) != 0 {
		test.Fatalf("created before the yes: %v", got)
	}
	if _, ok := confirm.OnYes().(pullRequestCreatedMessage); !ok {
		test.Fatal("the yes did not create")
	}
	created := harness.client.CreatedPullRequests()
	if len(created) != 1 || created[0].Draft || created[0].Title != "Add the thing" || created[0].Body != "Body" {
		test.Errorf("created = %+v, want one published pull request", created)
	}

	draft := newCreateHarness(test, "feat/b", "Add the thing\n")
	draft.command(test, "create-draft")
	if got := runCreateDraft(test, draft); len(got) != 1 || !got[0].Draft {
		test.Errorf("created = %+v, want one draft", got)
	}
}

func TestSuccessRefreshesAndOpensTheWorkspace(test *testing.T) {
	harness := newCreateHarness(test, "feat/b", "Add the thing\n")
	harness.command(test, "create-draft")
	message := harness.chooseBoth(test)
	_, command := harness.view.Update(message)
	_, next := harness.view.Update(command())
	if next == nil {
		test.Fatal("success produced no commands")
	}
	var pushed userinterface.View
	var informed string
	listsBefore := len(harness.client.Calls())
	for _, produced := range flatten(next()) {
		switch result := produced.(type) {
		case userinterface.PushMessage:
			pushed = result.View
		case userinterface.InfoMessage:
			informed = result.Text
		case pullRequestsLoadedMessage:
			harness.view.Update(result)
		}
	}
	if pushed == nil || pushed.Title() != "#3" {
		test.Errorf("pushed %v, want the workspace of #3", pushed)
	}
	if !strings.Contains(informed, "#3") {
		test.Errorf("notice = %q, want it to name #3", informed)
	}
	if len(harness.client.Calls()) <= listsBefore {
		test.Error("the list was not reloaded")
	}
	found := false
	for _, pullRequest := range harness.view.current().pullRequests {
		found = found || pullRequest.Number == 3
	}
	if !found {
		test.Error("the refreshed list lacks the new pull request")
	}
}

// flatten runs a command and, if it was a batch, every command in it.
func flatten(message tea.Msg) []tea.Msg {
	batch, ok := message.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{message}
	}
	var out []tea.Msg
	for _, command := range batch {
		if command != nil {
			out = append(out, flatten(command())...)
		}
	}
	return out
}

func TestGitHubErrorIsShownCleaned(test *testing.T) {
	harness := newCreateHarness(test, "feat/b", "Add the thing\n")
	harness.client.SetError(fake.MethodCreatePullRequest, errors.New("No commits between main and feat/b\x1b]52;c;aGk=\x07"))
	harness.command(test, "create-draft")
	_, command := harness.view.Update(harness.chooseBoth(test))
	_, next := harness.view.Update(command())
	message, ok := next().(userinterface.ErrorMessage)
	if !ok || !strings.Contains(message.Err.Error(), "No commits between main and feat/b") || strings.ContainsAny(message.Err.Error(), "\x1b\x07") {
		test.Errorf("got %#v, want GitHub's message, cleaned", next())
	}
	if harness.view.Loading() {
		test.Error("still loading after a failure")
	}
}

func TestEditorFailureCreatesNothing(test *testing.T) {
	harness := newCreateHarness(test, "feat/b", "Title")
	harness.command(test, "create-draft")
	message := harness.chooseBoth(test)
	message.err = errors.New("editor exited 1")
	_, next := harness.view.Update(message)
	if result, ok := next().(userinterface.ErrorMessage); !ok || !strings.Contains(result.Err.Error(), "editor exited 1") {
		test.Errorf("got %#v, want the editor's error", next())
	}
	if len(harness.client.CreatedPullRequests()) != 0 {
		test.Error("created after the editor failed")
	}
}

// A branch result from an earlier :create must never be taken for a later
// flow's, also after a cancel: the reviewer got one pick used as head and base.
func TestLateBranchResultsFromEarlierFlowsAreIgnored(test *testing.T) {
	harness := newCreateHarness(test, "", "Title")
	first, _ := harness.view.Command("create", "")
	second, _ := harness.view.Command("create", "") // supersedes the first
	deliver(test, harness.view, second)
	if !harness.view.CapturesKeys() {
		test.Fatal("the second flow's branches should open the head picker")
	}
	escape(harness.view) // cancel
	third, _ := harness.view.Command("create", "")

	deliver(test, harness.view, first)  // late result of the first flow
	deliver(test, harness.view, second) // late duplicate of the cancelled one
	if harness.view.CapturesKeys() {
		test.Fatal("a late result of an earlier flow opened a picker for the third")
	}
	deliver(test, harness.view, third)
	if !harness.view.CapturesKeys() {
		test.Fatal("the third flow's own result should open the head picker")
	}
	if command := enter(harness.view); command != nil { // the head is chosen
		test.Fatal("choosing the head should open the base picker")
	}
	deliver(test, harness.view, third) // a repeat while the flow is past loading
	command := enter(harness.view)
	if command == nil {
		test.Fatal("choosing the base should open the editor")
	}
	message, ok := command().(createTextMessage)
	if !ok {
		test.Fatalf("got %T, want the editor result", command())
	}
	if message.head != "feat/a" || message.base == message.head || message.base == "" {
		test.Errorf("head, base = %q, %q; want distinct branches", message.head, message.base)
	}
}

func TestQuestionCleansBranchesAndTitle(test *testing.T) {
	harness := newCreateHarness(test, "", "T\x1b[31mitle\x07\n")
	harness.client.SetBranches(testRepository, []github.Branch{
		{Name: "head\x1b]52;c;aGk=\x07x"}, {Name: "base\x1b[2Jy", Default: true},
	})
	harness.command(test, "create")
	_, command := harness.view.Update(harness.chooseBoth(test))
	confirm, ok := command().(userinterface.ConfirmMessage)
	if !ok {
		test.Fatalf("got %T, want a question", command())
	}
	if strings.ContainsAny(confirm.Text, "\x1b\x07") {
		test.Errorf("question carries control characters: %q", confirm.Text)
	}
	for _, want := range []string{"head", "base", "itle"} {
		if !strings.Contains(confirm.Text, want) {
			test.Errorf("question %q lacks %q", confirm.Text, want)
		}
	}
}

func TestSuccessMarksOtherTabsForReload(test *testing.T) {
	harness := newCreateHarness(test, "", "Title")
	press(harness.view, "g t") // the second tab loads once
	press(harness.view, "g T")
	if !harness.view.tabs[1].requested {
		test.Fatal("setup: the second tab should have been requested")
	}
	harness.view.Update(pullRequestCreatedMessage{owner: harness.view, pullRequest: github.PullRequest{Number: 3}})
	if harness.view.tabs[1].requested {
		test.Error("the other tab would show its old rows; it must reload when shown")
	}
}

func TestPickerIsWholeOverShortLists(test *testing.T) {
	for _, pullRequests := range []int{0, 1} {
		harness := newCreateHarnessWith(test, pullRequests, "feat/a", "Title")
		harness.command(test, "create")
		screen := ansi.Strip(harness.view.Render(80, 20))
		for _, want := range []string{"Head branch", "feat/a", "feat/b", "main", "╰"} {
			if !strings.Contains(screen, want) {
				test.Errorf("%d pull requests: the picker is cut off, %q is missing:\n%s", pullRequests, want, screen)
			}
		}
	}
}

func TestUnknownCommandIsNotHandled(test *testing.T) {
	harness := newCreateHarness(test, "", "Title")
	if command, handled := harness.view.Command("merge", ""); handled || command != nil {
		test.Error("the list claimed :merge")
	}
}

func TestCreateTimeoutReloadsTheListButOpensNothing(test *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantReload bool
	}{
		{"timeout", context.DeadlineExceeded, true},
		{"other failure", errors.New("boom"), false},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			harness := newCreateHarness(test, "feat/b", "Add the thing\n")
			harness.client.SetError(fake.MethodCreatePullRequest, testCase.err)
			press(harness.view, "g t") // load the second tab, so it can be marked for reload
			press(harness.view, "g T")
			harness.command(test, "create-draft")
			_, command := harness.view.Update(harness.chooseBoth(test))
			_, next := harness.view.Update(command())
			var shown error
			var results []tea.Msg
			for _, produced := range flatten(next()) {
				switch result := produced.(type) {
				case userinterface.PushMessage:
					test.Errorf("opened %v, but no pull request was created", result.View)
				case userinterface.ErrorMessage:
					shown = result.Err
				default:
					results = append(results, result)
				}
			}
			if shown == nil {
				test.Error("the error was not shown")
			}
			reloaded := false
			for _, result := range results {
				_, reloaded = result.(pullRequestsLoadedMessage)
				if reloaded {
					break
				}
			}
			if reloaded != testCase.wantReload {
				test.Errorf("list reloaded = %v, want %v", reloaded, testCase.wantReload)
			}
			if marked := !harness.view.tabs[1].requested; marked != testCase.wantReload {
				test.Errorf("other tab marked for reload = %v, want %v", marked, testCase.wantReload)
			}
		})
	}
}
