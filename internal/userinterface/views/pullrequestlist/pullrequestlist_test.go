package pullrequestlist

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/config"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/github/fake"
	"github.com/MikePors/strigidae/internal/golden"
	"github.com/MikePors/strigidae/internal/options"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/keys"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
	"github.com/MikePors/strigidae/internal/userinterface/views/workspace"
)

var (
	testRepository = github.Repository{Owner: "acme", Name: "widgets"}
	testNow        = time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
)

func makePullRequests(count int) []github.PullRequest {
	pullRequests := make([]github.PullRequest, count)
	for i := range pullRequests {
		pullRequests[i] = github.PullRequest{
			Repository: testRepository,
			Number:     count - i,
			Title:      fmt.Sprintf("Change number %d", count-i),
			Author:     "alice",
			State:      github.PullRequestStateOpen,
			URL:        fmt.Sprintf("https://github.com/acme/widgets/pull/%d", count-i),
			UpdatedAt:  testNow.Add(-time.Duration(i) * time.Hour),
		}
	}
	return pullRequests
}

func newView(client github.Client, open func(string) error) *View {
	view := New(client, testRepository, config.Default().Tabs, theme.Default(), Options{Now: func() time.Time { return testNow }, Open: open})
	view.Update(tea.WindowSizeMsg{Width: 80, Height: 13})
	return view
}

// deliver runs command and feeds the resulting message back into the view.
func deliver(test *testing.T, view *View, command tea.Cmd) tea.Cmd {
	test.Helper()
	if command == nil {
		test.Fatal("expected a command")
	}
	_, next := view.Update(command())
	return next
}

func press(view *View, sequence string) tea.Cmd {
	_, command := view.Update(keys.Message{Sequence: sequence})
	return command
}

func loadedView(test *testing.T, client github.Client, open func(string) error) *View {
	test.Helper()
	view := newView(client, open)
	deliver(test, view, view.Init())
	return view
}

func fakeWith(pullRequests []github.PullRequest) *fake.Client {
	client := fake.New()
	client.SetPullRequests(testRepository, pullRequests)
	return client
}

func TestFirstLoad(test *testing.T) {
	client := fakeWith(makePullRequests(3))
	view := newView(client, nil)
	command := view.Init()
	if !view.Loading() {
		test.Fatal("view should be loading once started")
	}
	if view.Title() != "acme/widgets" {
		test.Errorf("title = %q", view.Title())
	}
	if next := deliver(test, view, command); next != nil {
		test.Errorf("single page should not trigger more loading")
	}
	if view.Loading() || len(view.current().pullRequests) != 3 {
		test.Errorf("loading=%v prs=%d, want loaded with 3", view.Loading(), len(view.current().pullRequests))
	}
	want := []fake.Call{{Method: fake.MethodListPullRequests, Repository: testRepository}}
	if diff := cmp.Diff(want, client.Calls()); diff != "" {
		test.Errorf("calls (-want +got):\n%s", diff)
	}
}

func TestPaginationTrigger(test *testing.T) {
	client := fakeWith(makePullRequests(45))
	view := loadedView(test, client, nil)
	if len(view.current().pullRequests) != 30 || view.current().next == "" {
		test.Fatalf("first page: prs=%d next=%q", len(view.current().pullRequests), view.current().next)
	}

	// Far from the end: no request.
	if command := press(view, "j"); command != nil {
		test.Fatal("moving near the top should not load more")
	}
	// Jump to the end: the next page is requested once.
	command := press(view, "G")
	if command == nil || !view.Loading() {
		test.Fatal("reaching the end should load the next page")
	}
	if again := press(view, "k"); again != nil {
		test.Error("a load is in flight; no second request")
	}
	deliver(test, view, command)
	if len(view.current().pullRequests) != 45 || view.current().next != "" {
		test.Errorf("after page 2: prs=%d next=%q", len(view.current().pullRequests), view.current().next)
	}
	calls := client.Calls()
	if len(calls) != 2 || calls[1].Page.After != "30" {
		test.Errorf("calls = %+v, want second call After=30", calls)
	}
	if command := press(view, "G"); command != nil {
		test.Error("no more pages; no request")
	}
}

func TestStaleResultsIgnored(test *testing.T) {
	client := fakeWith(makePullRequests(3))
	view := newView(client, nil)
	first := view.Init()

	refresh := press(view, "R")
	if refresh == nil {
		test.Fatal("R should reload")
	}
	// The first load finishes after the refresh was requested.
	deliver(test, view, first)
	if len(view.current().pullRequests) != 0 || !view.Loading() {
		test.Errorf("stale result applied: prs=%d loading=%v", len(view.current().pullRequests), view.Loading())
	}
	// A result for another repository is not ours either.
	view.Update(pullRequestsLoadedMessage{repository: github.Repository{Owner: "x", Name: "y"}, sequence: view.current().sequence, page: github.Page[github.PullRequest]{Items: makePullRequests(1)}})
	if len(view.current().pullRequests) != 0 {
		test.Error("other repo's result applied")
	}
	deliver(test, view, refresh)
	if len(view.current().pullRequests) != 3 || view.Loading() {
		test.Errorf("refresh result not applied: prs=%d", len(view.current().pullRequests))
	}
}

func TestRefreshKeepsSelection(test *testing.T) {
	client := fakeWith(makePullRequests(10))
	view := loadedView(test, client, nil)
	press(view, "j")
	press(view, "j")
	want := view.current().pullRequests[view.current().window.Cursor()].Number
	deliver(test, view, press(view, "R"))
	if got := view.current().pullRequests[view.current().window.Cursor()].Number; got != want {
		test.Errorf("selected #%d after refresh, want #%d", got, want)
	}
}

func TestRefreshKeepsSelectionPastFirstPage(test *testing.T) {
	client := fakeWith(makePullRequests(45))
	view := loadedView(test, client, nil)
	deliver(test, view, press(view, "G"))
	press(view, "G")
	if len(view.current().pullRequests) != 45 || view.current().window.Cursor() != 44 {
		test.Fatalf("setup: prs=%d cursor=%d", len(view.current().pullRequests), view.current().window.Cursor())
	}
	want := view.current().pullRequests[view.current().window.Cursor()].Number

	deliver(test, view, press(view, "R"))
	calls := client.Calls()
	if got := calls[len(calls)-1].Page.Limit; got != 45 {
		test.Errorf("refresh limit = %d, want 45", got)
	}
	if len(view.current().pullRequests) != 45 || view.current().pullRequests[view.current().window.Cursor()].Number != want {
		test.Errorf("after refresh: prs=%d selected #%d, want 45 rows and #%d", len(view.current().pullRequests), view.current().pullRequests[view.current().window.Cursor()].Number, want)
	}
}

func TestRefreshCapsAtMaximumPageSize(test *testing.T) {
	client := fakeWith(makePullRequests(250))
	view := loadedView(test, client, nil)
	for len(view.current().pullRequests) < 150 {
		deliver(test, view, press(view, "G"))
	}
	view.current().window.Restore(120, 0, 0)
	deliver(test, view, press(view, "R"))
	calls := client.Calls()
	if got := calls[len(calls)-1].Page.Limit; got != github.MaximumPageSize {
		test.Errorf("refresh limit = %d, want %d", got, github.MaximumPageSize)
	}
	// Pinned: only the first 100 rows are reloaded and the cursor keeps its
	// index (clamped), even though that may now be a different PR.
	if len(view.current().pullRequests) != 100 || view.current().window.Cursor() != 99 {
		test.Errorf("after refresh: prs=%d cursor=%d, want 100 and 99", len(view.current().pullRequests), view.current().window.Cursor())
	}
}

func TestUntrustedTextIsCleaned(test *testing.T) {
	repository := github.Repository{Owner: "ev\x1b[2Jil", Name: "re\x07po"}
	client := fake.New()
	client.SetError(fake.MethodListPullRequests, errors.New("boom\x1b]52;c;aGk=\x07\u202e\u009b"))
	view := New(client, repository, config.Default().Tabs, theme.Default(), Options{})
	view.Update(tea.WindowSizeMsg{Width: 80, Height: 8})

	if strings.ContainsAny(view.Title(), "\x1b\x07") {
		test.Errorf("title contains control characters: %q", view.Title())
	}
	if out := ansi.Strip(view.Render(80, 8)); strings.ContainsAny(out, "\x1b\x07") {
		test.Errorf("loading text contains control characters: %q", out)
	}

	command := deliver(test, view, view.Init())
	status := command().(userinterface.ErrorMessage).Err.Error()
	out := ansi.Strip(view.Render(80, 8))
	for _, text := range []string{status, out} {
		for _, character := range strings.ReplaceAll(text, "\n", "") {
			if character < ' ' || (character >= 0x7f && character <= 0x9f) || character == '\u202e' {
				test.Fatalf("control rune %q reached the screen: %q", character, text)
			}
		}
	}
	if !strings.Contains(status, "boom") {
		test.Errorf("status lost the message: %q", status)
	}
}

func TestRefreshSelectionGone(test *testing.T) {
	client := fakeWith(makePullRequests(10))
	view := loadedView(test, client, nil)
	for range 4 {
		press(view, "j")
	}
	// The selected PR disappears from the list; the row index is kept.
	client.SetPullRequests(testRepository, makePullRequests(10)[:3])
	press(view, "R")
	view.Update(pullRequestsLoadedMessage{repository: testRepository, sequence: view.current().sequence, page: github.Page[github.PullRequest]{Items: makePullRequests(3)}})
	if view.current().window.Cursor() != 2 {
		test.Errorf("cursor = %d, want 2 (clamped)", view.current().window.Cursor())
	}
}

func TestLoadError(test *testing.T) {
	client := fakeWith(makePullRequests(3))
	boom := errors.New("boom")
	client.SetError(fake.MethodListPullRequests, boom)
	view := newView(client, nil)
	command := deliver(test, view, view.Init())
	if view.Loading() {
		test.Error("still loading after error")
	}
	message, ok := command().(userinterface.ErrorMessage)
	if !ok || !errors.Is(message.Err, boom) {
		test.Fatalf("cmd produced %#v, want userinterface.ErrorMessage wrapping boom", message)
	}
	if !strings.Contains(view.Render(80, 10), "boom") {
		test.Error("error state should show the error")
	}
}

func TestFailedNextPageIsNotRetried(test *testing.T) {
	client := fakeWith(makePullRequests(45))
	view := loadedView(test, client, nil)
	command := press(view, "G")
	client.SetError(fake.MethodListPullRequests, errors.New("rate limited"))
	errCommand := deliver(test, view, command)
	if _, ok := errCommand().(userinterface.ErrorMessage); !ok {
		test.Fatal("failed page should report an error")
	}
	if view.Loading() {
		test.Error("still loading after failure")
	}
	calls := len(client.Calls())
	for _, key := range []string{"k", "j", "G", "j"} {
		if command := press(view, key); command != nil {
			test.Fatalf("key %q retried the failed page", key)
		}
	}
	if len(client.Calls()) != calls {
		test.Error("extra requests were made")
	}

	// R clears the flag; once it succeeds, paging resumes.
	client.SetError(fake.MethodListPullRequests, nil)
	// The selection is still at the end, so the refresh result itself pages on.
	if more := deliver(test, view, press(view, "R")); more == nil {
		test.Error("paging should resume after a successful refresh")
	}
}

func TestOpenInBrowser(test *testing.T) {
	var opened []string
	open := func(address string) error { opened = append(opened, address); return nil }
	view := loadedView(test, fakeWith(makePullRequests(3)), open)

	command := press(view, "o")
	if command == nil {
		test.Fatal("o should produce a command")
	}
	if len(opened) != 0 {
		test.Fatal("browser launched during Update; it must run in the command")
	}
	message, ok := command().(userinterface.InfoMessage)
	if !ok || len(opened) != 1 || opened[0] != "https://github.com/acme/widgets/pull/3" {
		test.Errorf("opened=%v msg=%#v", opened, message)
	}

	failing := loadedView(test, fakeWith(makePullRequests(1)), func(string) error { return errors.New("no browser") })
	if _, ok := press(failing, "o")().(userinterface.ErrorMessage); !ok {
		test.Error("launch failure should become userinterface.ErrorMessage")
	}

	empty := loadedView(test, fakeWith(nil), open)
	if command := press(empty, "o"); command != nil {
		test.Error("o on an empty list should do nothing")
	}
}

func TestEnterPushesWorkspace(test *testing.T) {
	view := loadedView(test, fakeWith(makePullRequests(3)), nil)
	press(view, "j") // second row is PR #2
	command := press(view, "enter")
	if command == nil {
		test.Fatal("enter should produce a command")
	}
	push, ok := command().(userinterface.PushMessage)
	if !ok {
		test.Fatalf("enter produced %T, want userinterface.PushMessage", command())
	}
	if got := push.View.Title(); got != "#2" {
		test.Errorf("pushed view title = %q, want #2", got)
	}

	empty := loadedView(test, fakeWith(nil), nil)
	if command := press(empty, "enter"); command != nil {
		test.Error("enter on an empty list should do nothing")
	}
}

func TestNavigation(test *testing.T) {
	view := loadedView(test, fakeWith(makePullRequests(25)), nil) // window shows 11 rows
	tests := []struct {
		keys []string
		want int
	}{
		{[]string{"j", "j", "j"}, 3},
		{[]string{"j", "k", "k"}, 0},
		{[]string{"G"}, 24},
		{[]string{"G", "g g"}, 0},
		{[]string{"ctrl+d"}, 5},
		{[]string{"ctrl+f"}, 11},
		{[]string{"ctrl+f", "ctrl+f", "ctrl+f"}, 24},
		{[]string{"ctrl+f", "ctrl+b"}, 0},
		{[]string{"G", "ctrl+u"}, 19},
	}
	for _, testCase := range tests {
		press(view, "g g")
		for _, key := range testCase.keys {
			press(view, key)
		}
		if view.current().window.Cursor() != testCase.want {
			test.Errorf("keys %v: cursor = %d, want %d", testCase.keys, view.current().window.Cursor(), testCase.want)
		}
	}
}

func TestHostileTextIsNeutralised(test *testing.T) {
	pullRequests := makePullRequests(1)
	pullRequests[0].Title = "evil\x1b]52;c;aGk=\x07\x1b[2J title\u202e\u009b"
	pullRequests[0].Author = "a\x1b[31muthor"
	view := loadedView(test, fakeWith(pullRequests), nil)
	out := ansi.Strip(view.Render(80, 6))
	for _, character := range out {
		if character != '\n' && (character < ' ' || (character >= 0x7f && character <= 0x9f) || character == '\u202e') {
			test.Fatalf("control rune %q reached the screen: %q", character, out)
		}
	}
}

func goldenPullRequests() []github.PullRequest {
	pullRequests := makePullRequests(8)
	pullRequests[0].Title = "Add a really quite extraordinarily long title that cannot possibly fit in the available columns"
	pullRequests[0].Author = "someone-with-a-long-name"
	pullRequests[0].ReviewDecision = github.ReviewDecisionApproved
	pullRequests[0].CommentCount = 4
	pullRequests[0].Checks = github.ChecksPassing
	pullRequests[1].Checks = github.ChecksRunning
	pullRequests[2].Checks = github.ChecksFailing
	pullRequests[1].Draft = true
	pullRequests[1].Title = "WIP: rework the parser"
	pullRequests[1].CommentCount = 0
	pullRequests[2].ReviewDecision = github.ReviewDecisionChangesRequested
	pullRequests[2].CommentCount = 120
	pullRequests[3].ReviewDecision = github.ReviewDecisionReviewRequired
	pullRequests[3].Draft = true
	pullRequests[3].Title = "Draft with a very long title that gets truncated after the marker is shown"
	pullRequests[4].UpdatedAt = testNow.Add(-50 * time.Hour)
	pullRequests[5].UpdatedAt = testNow.Add(-100 * 24 * time.Hour)
	return pullRequests
}

func TestGolden(test *testing.T) {
	tests := []struct {
		name          string
		setup         func(test *testing.T) *View
		width, height int
	}{
		{"populated", func(test *testing.T) *View {
			view := loadedView(test, fakeWith(goldenPullRequests()), nil)
			press(view, "j")
			press(view, "j")
			return view
		}, 80, 12},
		{"scrolled", func(test *testing.T) *View {
			view := loadedView(test, fakeWith(makePullRequests(20)), nil)
			press(view, "G")
			return view
		}, 80, 8},
		{"medium", func(test *testing.T) *View {
			return loadedView(test, fakeWith(goldenPullRequests()), nil)
		}, 56, 10},
		{"narrow", func(test *testing.T) *View {
			return loadedView(test, fakeWith(goldenPullRequests()), nil)
		}, 36, 10},
		{"loading", func(*testing.T) *View {
			view := newView(fakeWith(nil), nil)
			view.Init()
			return view
		}, 80, 6},
		{"empty", func(test *testing.T) *View { return loadedView(test, fakeWith(nil), nil) }, 80, 6},
		{"error", func(test *testing.T) *View {
			client := fakeWith(nil)
			client.SetError(fake.MethodListPullRequests, fmt.Errorf("list pull requests: %w", github.ErrNotFound))
			view := newView(client, nil)
			deliver(test, view, view.Init())
			return view
		}, 40, 8},
		{"tiny", func(test *testing.T) *View {
			return loadedView(test, fakeWith(goldenPullRequests()), nil)
		}, 12, 3},
		{"error_tiny", func(test *testing.T) *View {
			client := fakeWith(nil)
			client.SetError(fake.MethodListPullRequests, fmt.Errorf("list pull requests: %w", github.ErrNotFound))
			view := newView(client, nil)
			deliver(test, view, view.Init())
			return view
		}, 20, 2},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			view := testCase.setup(test)
			out := view.Render(testCase.width, testCase.height)
			lines := strings.Split(out, "\n")
			if len(lines) > testCase.height {
				test.Errorf("%d lines, max %d", len(lines), testCase.height)
			}
			for i, line := range lines {
				if width := ansi.StringWidth(line); width > testCase.width {
					test.Errorf("line %d is %d columns wide, max %d: %q", i, width, testCase.width, line)
				}
			}
			golden.Assert(test, testCase.name, out, false)
		})
	}
}

func TestPagingKeysAtTheEnds(test *testing.T) {
	downKeys := []string{"j", "j", "j", "j", "j", "j", "j", "j"}
	upKeys := []string{"G", "k", "k", "k", "k"}
	tests := []struct {
		name    string
		start   []string
		key     string
		want    int
		wantTop int
	}{
		{"ctrl+u at top", []string{"g g"}, "ctrl+u", 0, 0},
		{"ctrl+b at top", []string{"g g"}, "ctrl+b", 0, 0},
		{"ctrl+d at bottom", []string{"G"}, "ctrl+d", 24, 14},
		{"ctrl+f at bottom", []string{"G"}, "ctrl+f", 24, 14},
		{"ctrl+d from top", []string{"g g"}, "ctrl+d", 5, 5},
		{"ctrl+b from bottom", []string{"G"}, "ctrl+b", 13, 3},
		{"ctrl+u from bottom", []string{"G"}, "ctrl+u", 19, 9},
		{"ctrl+d mid-list", downKeys, "ctrl+d", 13, 5},
		{"ctrl+f mid-list", downKeys, "ctrl+f", 19, 11},
		{"ctrl+u mid-list", upKeys, "ctrl+u", 15, 9},
		{"ctrl+b mid-list", upKeys, "ctrl+b", 9, 3},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			view := loadedView(test, fakeWith(makePullRequests(25)), nil) // window shows 11 rows
			for _, key := range testCase.start {
				press(view, key)
			}
			press(view, testCase.key)
			if got := view.current().window.Cursor(); got != testCase.want {
				test.Errorf("cursor = %d, want %d", got, testCase.want)
			}
			if got := view.current().window.Top(); got != testCase.wantTop {
				test.Errorf("top = %d, want %d", got, testCase.wantTop)
			}
		})
	}
}

func TestPagingNearTheEndLoadsMore(test *testing.T) {
	for _, key := range []string{"ctrl+d", "ctrl+f"} {
		test.Run(key, func(test *testing.T) {
			client := fakeWith(makePullRequests(45))
			view := loadedView(test, client, nil) // 30 loaded, 11 visible
			var command tea.Cmd
			for i := 0; i < 10 && command == nil; i++ {
				command = press(view, key)
				if command == nil && view.current().window.Cursor() >= len(view.current().pullRequests)-1-loadMoreMargin {
					test.Fatalf("%s reached cursor %d inside the margin without loading", key, view.current().window.Cursor())
				}
			}
			if command == nil || !view.Loading() {
				test.Fatalf("%s into the margin should load the next page", key)
			}
			cursor, top := view.current().window.Cursor(), view.current().window.Top()
			deliver(test, view, command)
			if len(view.current().pullRequests) != 45 {
				test.Fatalf("rows after next page = %d, want 45", len(view.current().pullRequests))
			}
			calls := client.Calls()
			if calls[len(calls)-1].Page.After == "" {
				test.Errorf("last call = %+v, want a next-page fetch", calls[len(calls)-1])
			}
			if view.current().window.Cursor() != cursor || view.current().window.Top() != top {
				test.Errorf("append moved the window: cursor %d->%d top %d->%d", cursor, view.current().window.Cursor(), top, view.current().window.Top())
			}
			press(view, "G")
			if got := view.current().window.Cursor(); got != 44 {
				test.Errorf("G after append: cursor = %d, want 44 (window must know the new row count)", got)
			}
		})
	}
}

// TestWorkspacesShareHunkMarks proves the list hands every workspace it opens
// the same hunk marks, so marks survive closing and reopening a pull request.
func TestWorkspacesShareHunkMarks(test *testing.T) {
	view := loadedView(test, fakeWith(makePullRequests(3)), nil)
	opened := func() *workspace.HunkMarks {
		push, ok := press(view, "enter")().(userinterface.PushMessage)
		if !ok {
			test.Fatal("enter should push a view")
		}
		return push.View.(*workspace.View).HunkMarks()
	}
	first, second := opened(), opened()
	if first == nil || first != second || first != view.hunkMarks {
		test.Error("two workspaces opened from the list do not share the list's hunk marks")
	}
}

type stubCopier struct{}

func (stubCopier) Copy(context.Context, string) error { return nil }

func TestClipboardPassedToWorkspaces(test *testing.T) {
	copier := stubCopier{}
	view := New(fakeWith(makePullRequests(1)), testRepository, config.Default().Tabs, theme.Default(), Options{Clipboard: copier})
	view.Update(tea.WindowSizeMsg{Width: 80, Height: 13})
	deliver(test, view, view.Init())
	push, ok := press(view, "enter")().(userinterface.PushMessage)
	if !ok {
		test.Fatal("enter should push a view")
	}
	if got := push.View.(*workspace.View).Clipboard(); got != copier {
		test.Errorf("workspace clipboard = %#v, want the list's", got)
	}
}

func TestWrapPassedToWorkspaces(test *testing.T) {
	for _, wrap := range []bool{false, true} {
		view := New(fakeWith(makePullRequests(1)), testRepository, config.Default().Tabs, theme.Default(), Options{})
		view.Update(userinterface.OptionsMessage{Values: options.Values{options.Wrap: wrap}})
		view.Update(tea.WindowSizeMsg{Width: 80, Height: 13})
		deliver(test, view, view.Init())
		push, ok := press(view, "enter")().(userinterface.PushMessage)
		if !ok {
			test.Fatal("enter should push a view")
		}
		if got := push.View.(*workspace.View).Wrap(); got != wrap {
			test.Errorf("wrap option %v: workspace wraps = %v", wrap, got)
		}
	}
}

func TestStartsWithAPullRequestOpen(test *testing.T) {
	view := New(fakeWith(makePullRequests(3)), github.Repository{Owner: "o", Name: "r"}, nil, theme.New(true), Options{PullRequest: 189})
	batch, ok := view.Init()().(tea.BatchMsg)
	if !ok {
		test.Fatal("Init did not batch the list load with opening the pull request")
	}
	var pushed []string
	for _, command := range batch {
		if push, ok := command().(userinterface.PushMessage); ok {
			pushed = append(pushed, push.View.Title())
		}
	}
	if len(pushed) != 1 || pushed[0] != "#189" {
		test.Errorf("pushed %q, want one #189", pushed)
	}

	plain := New(fakeWith(makePullRequests(3)), github.Repository{Owner: "o", Name: "r"}, nil, theme.New(true), Options{})
	if _, ok := plain.Init()().(userinterface.PushMessage); ok {
		test.Error("without a number Init opened a pull request")
	}
}
