package workspace

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/github/fake"
	"github.com/MikePors/strigidae/internal/userinterface"
)

// blockingClient makes the calls under test wait until their context ends, as
// a connection that hangs does.
type blockingClient struct {
	*fake.Client
	// load names the one load that blocks (fake.Method...); writes always do.
	load string
}

// backstop ends a wait whose context has no deadline, so a call that ignores
// the deadline fails the test at once instead of hanging until go test's own
// timeout. It is far longer than shortTimeouts.
const backstop = 2 * time.Second

func (client blockingClient) wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(backstop):
		return errors.New("call outlived the test: it has no deadline")
	}
}

func (client blockingClient) GetPullRequest(ctx context.Context, repository github.Repository, number int) (github.PullRequestDetail, error) {
	if client.load != fake.MethodGetPullRequest {
		return client.Client.GetPullRequest(ctx, repository, number)
	}
	return github.PullRequestDetail{}, client.wait(ctx)
}

func (client blockingClient) GetDiff(ctx context.Context, repository github.Repository, number int) (string, error) {
	if client.load != fake.MethodGetDiff {
		return client.Client.GetDiff(ctx, repository, number)
	}
	return "", client.wait(ctx)
}

func (client blockingClient) ListReviewThreads(ctx context.Context, repository github.Repository, number int, pageRequest github.PageRequest) (github.Page[github.ReviewThread], error) {
	if client.load != fake.MethodListReviewThreads {
		return client.Client.ListReviewThreads(ctx, repository, number, pageRequest)
	}
	return github.Page[github.ReviewThread]{}, client.wait(ctx)
}

func (client blockingClient) AddComment(ctx context.Context, _ github.Repository, _ int, _ string) (github.IssueComment, error) {
	return github.IssueComment{}, client.wait(ctx)
}

func (client blockingClient) ReplyToThread(ctx context.Context, _, _ string) (github.ReviewComment, error) {
	return github.ReviewComment{}, client.wait(ctx)
}

func (client blockingClient) SetFileViewed(ctx context.Context, _, _ string, _ bool) error {
	return client.wait(ctx)
}

func (client blockingClient) DeleteReview(ctx context.Context, _ string) error {
	return client.wait(ctx)
}

func (client blockingClient) UpdatePullRequest(ctx context.Context, _ github.UpdatePullRequestInput) error {
	return client.wait(ctx)
}

func (client blockingClient) UpdateReviewComment(ctx context.Context, _, _ string) (github.ReviewComment, error) {
	return github.ReviewComment{}, client.wait(ctx)
}

func (client blockingClient) DeleteReviewComment(ctx context.Context, _ string) error {
	return client.wait(ctx)
}

func (client blockingClient) ResolveReviewThread(ctx context.Context, _ string) (github.ThreadResolution, error) {
	return github.ThreadResolution{}, client.wait(ctx)
}

func (client blockingClient) UnresolveReviewThread(ctx context.Context, _ string) (github.ThreadResolution, error) {
	return github.ThreadResolution{}, client.wait(ctx)
}

func (client blockingClient) MergePullRequest(ctx context.Context, _ github.MergeInput) error {
	return client.wait(ctx)
}

func (client blockingClient) SubmitReview(ctx context.Context, _ github.SubmitReviewInput) error {
	return client.wait(ctx)
}

func (client blockingClient) StartReview(ctx context.Context, _ github.StartReviewInput) (github.PendingReview, error) {
	return github.PendingReview{}, client.wait(ctx)
}

// countingClient counts the SetFileViewed calls it hangs.
type countingClient struct {
	blockingClient
	viewedCalls int
}

func (client *countingClient) SetFileViewed(ctx context.Context, pullRequestIdentifier, path string, viewed bool) error {
	client.viewedCalls++
	return client.blockingClient.SetFileViewed(ctx, pullRequestIdentifier, path, viewed)
}

var shortTimeouts = userinterface.Timeouts{Request: 5 * time.Millisecond, Load: 5 * time.Millisecond}

func TestLoadTimesOut(test *testing.T) {
	for _, method := range []string{fake.MethodGetPullRequest, fake.MethodGetDiff, fake.MethodListReviewThreads} {
		test.Run(method, func(test *testing.T) {
			harness := newHarness(test, 100, 24)
			harness.view.client = blockingClient{Client: harness.client, load: method}
			harness.view.timeouts = shortTimeouts
			harness.run(harness.view.Init())
			if harness.view.Loading() {
				test.Error("still loading after the deadline passed")
			}
			if err := harness.lastErr(); !errors.Is(err, context.DeadlineExceeded) {
				test.Errorf("error = %v, want a deadline error", err)
			}
		})
	}
}

func TestWritesTimeOutAndSayTheyMayHaveGoneThrough(test *testing.T) {
	tests := []struct {
		name  string
		start func(harness *harness)
	}{
		{"comment", func(harness *harness) {
			harness.view.writeStarted()
			harness.run(harness.view.post(target{}, "body"))
		}},
		{"reply", func(harness *harness) {
			harness.view.writeStarted()
			harness.run(harness.view.post(target{kind: kindReply, threadIdentifier: "T1"}, "body"))
		}},
		{"viewed mark", func(harness *harness) {
			harness.press("j", "v")
		}},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			harness := loaded(test, 100, 24)
			harness.view.client = blockingClient{Client: harness.client}
			harness.view.timeouts = shortTimeouts
			testCase.start(harness)
			err := harness.lastErr()
			if err == nil || !strings.Contains(err.Error(), "it may have gone through") {
				test.Errorf("error = %v, want it to say the write may have gone through", err)
			}
			if testCase.name == "viewed mark" {
				counting := &countingClient{blockingClient: blockingClient{Client: harness.client}}
				harness.view.client = counting
				harness.press("v")
				if counting.viewedCalls != 1 {
					test.Errorf("a later v sent %d SetFileViewed calls, want 1", counting.viewedCalls)
				}
			} else if harness.view.writes != 0 {
				test.Errorf("writes in flight = %d, want 0", harness.view.writes)
			}
		})
	}
}
