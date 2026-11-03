package workspace

import (
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/github/fake"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/keys"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

var (
	testNow        = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	testRepository = github.Repository{Owner: "acme", Name: "widgets"}
)

const longBody = `## Summary

This change adds a **retry policy** to the sync client so transient network
failures no longer abort a whole run. It replaces the ad-hoc loop in
` + "`sync.go`" + ` with a small, well-tested helper.

### What changed

- Added ` + "`Retry`" + ` with exponential backoff and jitter
- Moved the timeout handling into the helper
- Deleted the legacy ` + "`retryForever`" + ` function

### Example

` + "```go" + `
err := Retry(ctx, 5, func() error {
	return client.Sync(ctx)
})
` + "```" + `

### Notes

The backoff starts at 100ms and doubles up to a cap of 10s. See the design
doc for why jitter matters when many clients restart at once, and the
follow-up issue for making the policy configurable.

1. Review the helper first
2. Then the call sites
3. Finally the removed code`

func testDetail() github.PullRequestDetail {
	return github.PullRequestDetail{
		PullRequest: github.PullRequest{
			Identifier: "PR_7",
			Repository: testRepository, Number: 7, Title: "Add retry policy to sync client",
			Author: "alice", State: github.PullRequestStateOpen,
			ReviewDecision: github.ReviewDecisionChangesRequested,
			HeadBranch:     "feat/retry", BaseBranch: "main", BaseSHA: "bbbb2222",
			URL: "https://example.test/acme/widgets/pull/7",
		},
		Body:           longBody,
		Labels:         []github.Label{{Name: "enhancement"}, {Name: "sync"}},
		ReviewRequests: []github.Reviewer{{Name: "bob"}, {Name: "core", IsTeam: true}},
		Files: []github.ChangedFile{
			{Path: "internal/sync/client.go", Additions: 25, Change: github.FileModified, Deletions: 0},
			{Path: "internal/sync/retry.go", Additions: 214, Change: github.FileAdded},
			{Path: "internal/sync/legacy.go", Deletions: 90, Change: github.FileDeleted},
			{Path: "docs/design/a-very-long-directory-name/retry-policy-and-backoff.md", Additions: 12, Deletions: 3, Change: github.FileRenamed},
			{Path: "README.md", Additions: 2, Deletions: 1, Change: github.FileModified},
		},
		Comments: []github.IssueComment{
			{Author: "bob", CreatedAt: testNow.Add(-48 * time.Hour), Body: "Thanks! Could you add a test for the **jitter** bounds?"},
			{Author: "alice", CreatedAt: testNow.Add(-26 * time.Hour), Body: "Done in the latest push."},
		},
		Reviews: []github.Review{
			{Author: "carol", State: github.ReviewChangesRequested, SubmittedAt: testNow.Add(-30 * time.Hour), Body: "The cap should be configurable."},
			{Author: "dave", State: github.ReviewCommented, SubmittedAt: testNow.Add(-29 * time.Hour)},
			{Author: "me", State: github.ReviewPending, Body: "Draft: looks good overall, nit on naming."},
			{Author: "bob", State: github.ReviewApproved, SubmittedAt: testNow.Add(-90 * time.Minute)},
		},
	}
}

type harness struct {
	test     *testing.T
	view     *View
	client   *fake.Client
	opened   []string
	messages []tea.Msg // ui messages emitted by commands
}

func readDiff(test *testing.T) string {
	test.Helper()
	bytes, err := os.ReadFile("testdata/pull_request.diff")
	if err != nil {
		test.Fatal(err)
	}
	return string(bytes)
}

func newHarness(test *testing.T, width, height int) *harness {
	test.Helper()
	client := fake.New()
	client.SetPullRequest(testDetail())
	client.SetDiff(testRepository, 7, readDiff(test))
	otherHarness := &harness{test: test, client: client}
	otherHarness.view = New(client, testRepository, 7,
		WithClock(func() time.Time { return testNow }),
		WithTheme(theme.New(true)),
		WithOpener(func(address string) error { otherHarness.opened = append(otherHarness.opened, address); return nil }),
	)
	otherHarness.send(tea.WindowSizeMsg{Width: width, Height: height})
	return otherHarness
}

// loaded returns a harness whose Init results have been delivered.
func loaded(test *testing.T, width, height int) *harness {
	otherHarness := newHarness(test, width, height)
	otherHarness.run(otherHarness.view.Init())
	return otherHarness
}

func (harness *harness) send(message tea.Msg) {
	harness.test.Helper()
	view, command := harness.view.Update(message)
	harness.view = view.(*View)
	harness.run(command)
}

// run executes command, delivering result messages back to the view.
func (harness *harness) run(command tea.Cmd) {
	harness.test.Helper()
	if command == nil {
		return
	}
	switch message := command().(type) {
	case nil:
	case tea.BatchMsg:
		for _, command := range message {
			harness.run(command)
		}
	case userinterface.InfoMessage, userinterface.ErrorMessage, userinterface.ConfirmMessage:
		harness.messages = append(harness.messages, message)
	default:
		harness.send(message)
	}
}

func (harness *harness) press(sequences ...string) {
	harness.test.Helper()
	for _, text := range sequences {
		harness.send(keys.Message{Sequence: text})
	}
}

// typeText sends raw key presses, as the root does while the view captures keys.
func (harness *harness) typeText(text string) {
	harness.test.Helper()
	for _, character := range text {
		harness.send(tea.KeyPressMsg{Code: character, Text: string(character)})
	}
}

func (harness *harness) render() string {
	return harness.view.Render(harness.view.width, harness.view.height)
}

func (harness *harness) lastErr() error {
	for i := len(harness.messages) - 1; i >= 0; i-- {
		if errorMessage, ok := harness.messages[i].(userinterface.ErrorMessage); ok {
			return errorMessage.Err
		}
	}
	return nil
}

// messages runs command (and the commands of any batch) and returns what they produce.
func messages(command tea.Cmd) []tea.Msg {
	if command == nil {
		return nil
	}
	switch message := command().(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, command := range message {
			out = append(out, messages(command)...)
		}
		return out
	default:
		return []tea.Msg{message}
	}
}
