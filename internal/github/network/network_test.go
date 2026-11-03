package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/github"
)

var operationPattern = regexp.MustCompile(`(?:query|mutation)\s+(\w+)`)

// call is one request seen by the fake server.
type call struct {
	Method        string
	Path          string
	RawQuery      string
	Query         string
	Operation     string
	Variables     map[string]any
	Accept        string
	Authorization string
	RESTRaw       string
}

// server serves testdata fixtures in place of GitHub. It never opens a socket:
// requests are handed to the handler through a RoundTripper.
type server struct {
	test *testing.T
	// override, if it returns true, has produced the response itself.
	override func(recordedCall call, writer http.ResponseWriter) bool

	mutex sync.Mutex
	calls []call
}

func (server *server) fixture(writer http.ResponseWriter, status int, name string) {
	bytes, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		server.test.Errorf("read fixture: %v", err)
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	if filepath.Ext(name) == ".json" {
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	writer.WriteHeader(status)
	_, _ = writer.Write(bytes)
}

func (server *server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	var raw []byte
	if request.Body != nil {
		raw, _ = io.ReadAll(request.Body)
	}
	recordedCall := call{Method: request.Method, Path: request.URL.Path, RawQuery: request.URL.RawQuery, Accept: request.Header.Get("Accept"), Authorization: request.Header.Get("Authorization"), RESTRaw: string(raw)}
	if request.URL.Path == "/graphql" {
		var request struct {
			Query     string
			Variables map[string]any
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			server.test.Errorf("bad graphql request: %v", err)
		}
		recordedCall.Variables = request.Variables
		recordedCall.Query = request.Query
		if messages := operationPattern.FindStringSubmatch(request.Query); messages != nil {
			recordedCall.Operation = messages[1]
		}
	}
	server.mutex.Lock()
	server.calls = append(server.calls, recordedCall)
	server.mutex.Unlock()

	if server.override != nil && server.override(recordedCall, writer) {
		return
	}
	switch {
	case recordedCall.Operation != "":
		name := recordedCall.Operation + ".json"
		if after, _ := recordedCall.Variables["after"].(string); after != "" {
			name = recordedCall.Operation + ".after_" + after + ".json"
		}
		server.fixture(writer, http.StatusOK, name)
	case recordedCall.Method == "GET" && recordedCall.Path == "/repos/MikePors/strigidae/pulls/27":
		server.fixture(writer, http.StatusOK, "pull_request.diff")
	case recordedCall.Method == "POST" && recordedCall.Path == "/repos/MikePors/strigidae/issues/27/comments":
		server.fixture(writer, http.StatusCreated, "issue_comment.json")
	default:
		server.test.Errorf("unexpected request %s %s", recordedCall.Method, recordedCall.Path)
		writer.WriteHeader(http.StatusNotFound)
	}
}

func (server *server) Calls() []call {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	return append([]call(nil), server.calls...)
}

type handlerTransport struct{ handler http.Handler }

func (transport handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	transport.handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	response.Request = request
	return response, nil
}

func newTestClient(test *testing.T, server *server) *Client {
	test.Helper()
	server.test = test
	client, err := New(Options{
		Host:      "github.com",
		APIHost:   "api.github.test",
		Token:     "tok",
		Transport: handlerTransport{server},
		Now:       func() time.Time { return time.Unix(1_000, 0) },
	})
	if err != nil {
		test.Fatal(err)
	}
	return client
}

var repository = github.Repository{Owner: "MikePors", Name: "strigidae"}

func sha(count int) string { return fmt.Sprintf("%040x", count*0x1111) }

func mustTime(test *testing.T, text string) time.Time {
	test.Helper()
	value, err := time.Parse(time.RFC3339, text)
	if err != nil {
		test.Fatal(err)
	}
	return value
}

func TestListPullRequestsPagination(test *testing.T) {
	testServer := &server{}
	client := newTestClient(test, testServer)
	ctx := context.Background()

	firstPage, err := client.ListPullRequests(ctx, repository, github.PageRequest{Limit: 2})
	if err != nil {
		test.Fatal(err)
	}
	want1 := github.Page[github.PullRequest]{
		Total: 3,
		Next:  "Y3Vyc29yOnYyOpHOAAAAAg==",
		Items: []github.PullRequest{
			{
				Identifier: "PR_kwDOExample01", HeadSHA: sha(1), Repository: repository, Number: 27, Title: "feat(api): add GitHub client layer", Author: "MikePors",
				State: github.PullRequestStateOpen, Draft: true, CommentCount: 2, Checks: github.ChecksPassing,
				CreatedAt: mustTime(test, "2026-10-18T09:00:00Z"), UpdatedAt: mustTime(test, "2026-10-19T08:30:00Z"),
				URL: "https://github.com/MikePors/strigidae/pull/27", HeadBranch: "feat/4-github-client", BaseBranch: "main",
			},
			{
				Identifier: "PR_kwDOExample02", HeadSHA: sha(2), Repository: repository, Number: 26, Title: "chore: add issue forms and PR template", Author: "ghost",
				State: github.PullRequestStateOpen, ReviewDecision: github.ReviewDecisionApproved, Checks: github.ChecksFailing,
				CreatedAt: mustTime(test, "2026-10-17T12:00:00Z"), UpdatedAt: mustTime(test, "2026-10-18T10:15:00Z"),
				URL: "https://github.com/MikePors/strigidae/pull/26", HeadBranch: "chore/3-templates", BaseBranch: "main", CrossRepository: true,
			},
		},
	}
	if diff := cmp.Diff(want1, firstPage); diff != "" {
		test.Errorf("page 1 mismatch (-want +got):\n%s", diff)
	}
	if !firstPage.HasMore() {
		test.Error("page 1 should have more")
	}

	secondPage, err := client.ListPullRequests(ctx, repository, github.PageRequest{After: firstPage.Next})
	if err != nil {
		test.Fatal(err)
	}
	if secondPage.HasMore() || len(secondPage.Items) != 1 || secondPage.Items[0].ReviewDecision != github.ReviewDecisionReviewRequired || secondPage.Items[0].Checks != github.ChecksNone {
		test.Errorf("unexpected page 2: %+v", secondPage)
	}

	calls := testServer.Calls()
	if len(calls) != 2 {
		test.Fatalf("got %d calls, want 2", len(calls))
	}
	if got := calls[0].Variables["first"]; got != float64(2) {
		test.Errorf("first = %v, want 2", got)
	}
	if got := calls[0].Variables["after"]; got != nil {
		test.Errorf("first page sent after = %v", got)
	}
	if got := calls[1].Variables["first"]; got != float64(github.DefaultPageSize) {
		test.Errorf("default page size = %v", got)
	}
	if got := calls[0].Authorization; got != "token tok" {
		test.Errorf("authorization = %q", got)
	}
}

func TestSearchPullRequests(test *testing.T) {
	testServer := &server{}
	client := newTestClient(test, testServer)
	page, err := client.SearchPullRequests(context.Background(), "review-requested:@me", github.PageRequest{})
	if err != nil {
		test.Fatal(err)
	}
	if got := testServer.Calls()[0].Variables["query"]; got != "is:pr review-requested:@me" {
		test.Errorf("query = %v", got)
	}
	// The empty node (an inaccessible result) is skipped.
	if len(page.Items) != 1 || page.Total != 2 {
		test.Fatalf("unexpected page: %+v", page)
	}
	want := github.PullRequest{
		Identifier: "PR_kwDOOther01", HeadSHA: sha(1), Repository: github.Repository{Owner: "octo", Name: "widgets"}, Number: 5, Title: "Fix typo in README", Author: "octocat",
		State: github.PullRequestStateOpen, ReviewDecision: github.ReviewDecisionChangesRequested, CommentCount: 4, Checks: github.ChecksRunning,
		CreatedAt: mustTime(test, "2026-09-01T10:00:00Z"), UpdatedAt: mustTime(test, "2026-09-02T10:00:00Z"),
		URL: "https://github.com/octo/widgets/pull/5", HeadBranch: "fix-typo", BaseBranch: "main",
	}
	if diff := cmp.Diff(want, page.Items[0]); diff != "" {
		test.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestGetPullRequest(test *testing.T) {
	testServer := &server{}
	client := newTestClient(test, testServer)
	got, err := client.GetPullRequest(context.Background(), repository, 27)
	if err != nil {
		test.Fatal(err)
	}
	want := github.PullRequestDetail{
		PullRequest: github.PullRequest{
			Identifier: "PR_kwDOExample01", HeadSHA: sha(1), Repository: repository, Number: 27, Title: "feat(api): add GitHub client layer", Author: "MikePors",
			State: github.PullRequestStateOpen, ReviewDecision: github.ReviewDecisionChangesRequested, CommentCount: 2,
			CreatedAt: mustTime(test, "2026-10-18T09:00:00Z"), UpdatedAt: mustTime(test, "2026-10-19T08:30:00Z"),
			URL: "https://github.com/MikePors/strigidae/pull/27", HeadBranch: "feat/4-github-client", BaseBranch: "main",
		},
		ViewerDidAuthor: true,
		ViewerCanUpdate: true,
		Body:            "Closes #4\n\n## Summary\nAdds the client layer.\n",
		Reactions:       []github.ReactionSummary{{Content: github.ReactionHeart, Count: 3, ViewerReacted: true}},
		Additions:       120, Deletions: 7,
		Labels:         []github.Label{{Name: "enhancement", Color: "a2eeef"}},
		ReviewRequests: []github.Reviewer{{Name: "octocat"}, {Name: "maintainers", IsTeam: true}},
		Files: []github.ChangedFile{
			{Path: "internal/github/client.go", Additions: 40, Change: github.FileAdded, Viewed: github.FileViewed},
			{Path: "go.mod", Additions: 3, Deletions: 1, Change: github.FileModified, Viewed: github.FileUnviewed},
			{Path: "docs/old.md", Deletions: 12, Change: github.FileDeleted},
			{Path: "docs/new.md", Additions: 5, Deletions: 5, Change: github.FileRenamed, Viewed: github.FileDismissed},
		},
		Comments: []github.IssueComment{
			{
				Identifier: "IC_kwDOExample01", Author: "octocat", Body: "Looks good overall.",
				CreatedAt: mustTime(test, "2026-10-18T11:00:00Z"),
				URL:       "https://github.com/MikePors/strigidae/pull/27#issuecomment-1",
				Reactions: []github.ReactionSummary{{Content: github.ReactionThumbsUp, Count: 2, ViewerReacted: true}},
			},
			{
				Identifier: "IC_kwDOExample02", Author: "ghost", Body: "Thanks!",
				CreatedAt: mustTime(test, "2026-10-18T12:00:00Z"),
				URL:       "https://github.com/MikePors/strigidae/pull/27#issuecomment-2",
			},
			{
				Identifier: "IC_kwDOExample04", Author: "octocat", Body: "One more thing.",
				CreatedAt: mustTime(test, "2026-10-18T15:00:00Z"),
				URL:       "https://github.com/MikePors/strigidae/pull/27#issuecomment-3",
				Reactions: []github.ReactionSummary{{Content: github.ReactionRocket, Count: 1}},
			},
		},
		Reviews: []github.Review{
			{
				Identifier: "PRR_kwDOExample01", Author: "octocat", State: github.ReviewChangesRequested,
				Body: "Please add tests.", SubmittedAt: mustTime(test, "2026-10-18T13:00:00Z"),
			},
			// The viewer's draft review is kept; its SubmittedAt is zero.
			{Identifier: "PRR_kwDOExample02", Author: "MikePors", State: github.ReviewPending},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		test.Errorf("mismatch (-want +got):\n%s", diff)
	}
	if query := testServer.Calls()[0].Query; !strings.Contains(query, "viewerCanUpdate") {
		test.Error("the detail query does not ask for viewerCanUpdate")
	}
	if count := len(testServer.Calls()); count != 4 {
		test.Errorf("got %d requests, want 4 (detail + remaining files, comments, reviews)", count)
	}
}

func TestGetDiff(test *testing.T) {
	testServer := &server{}
	client := newTestClient(test, testServer)
	got, err := client.GetDiff(context.Background(), repository, 27)
	if err != nil {
		test.Fatal(err)
	}
	want, err := os.ReadFile("testdata/pull_request.diff")
	if err != nil {
		test.Fatal(err)
	}
	if got != string(want) {
		test.Errorf("diff mismatch:\n%s", got)
	}
	if first := testServer.Calls()[0].Accept; first != diffAccept {
		test.Errorf("accept = %q, want %q", first, diffAccept)
	}
}

func TestListReviewThreads(test *testing.T) {
	testServer := &server{}
	client := newTestClient(test, testServer)
	ctx := context.Background()

	firstPage, err := client.ListReviewThreads(ctx, repository, 27, github.PageRequest{})
	if err != nil {
		test.Fatal(err)
	}
	if firstPage.Next != "THREADS1" || firstPage.Total != 5 || len(firstPage.Items) != 2 {
		test.Fatalf("unexpected page 1: next=%q total=%d items=%d", firstPage.Next, firstPage.Total, len(firstPage.Items))
	}
	open := github.ReviewThread{
		Identifier: "PRRT_kwDOExample01", SubjectType: github.SubjectLine, Path: "internal/github/client.go",
		Line: 12, OriginalLine: 12, StartLine: 10, OriginalStartLine: 10,
		DiffSide: github.SideRight, StartDiffSide: github.SideRight,
		Comments: []github.ReviewComment{
			{
				Identifier: "PRRC_kwDOExample01", Author: "octocat", Body: "Should this take a context?",
				CreatedAt: mustTime(test, "2026-10-18T13:05:00Z"),
				URL:       "https://github.com/MikePors/strigidae/pull/27#discussion_r1",
				DiffHunk:  "@@ -0,0 +1,20 @@\n+package github\n",
				Reactions: []github.ReactionSummary{{Content: github.ReactionEyes, Count: 1, ViewerReacted: true}},
			},
			{
				Identifier: "PRRC_kwDOExample02", Author: "MikePors", Body: "Yes, fixed.",
				CreatedAt: mustTime(test, "2026-10-18T14:00:00Z"),
				URL:       "https://github.com/MikePors/strigidae/pull/27#discussion_r2",
				DiffHunk:  "@@ -0,0 +1,20 @@\n+package github\n",
			},
		},
	}
	if diff := cmp.Diff(open, firstPage.Items[0]); diff != "" {
		test.Errorf("open thread mismatch (-want +got):\n%s", diff)
	}
	resolved := firstPage.Items[1]
	if !resolved.IsResolved || !resolved.IsOutdated || resolved.ResolvedBy != "MikePors" ||
		resolved.Line != 0 || resolved.OriginalLine != 5 || resolved.DiffSide != github.SideLeft ||
		resolved.Comments[0].Author != "ghost" {
		test.Errorf("unexpected resolved+outdated thread: %+v", resolved)
	}

	secondPage, err := client.ListReviewThreads(ctx, repository, 27, github.PageRequest{After: firstPage.Next})
	if err != nil {
		test.Fatal(err)
	}
	if secondPage.HasMore() || len(secondPage.Items) != 3 || !secondPage.Items[0].IsOutdated || secondPage.Items[0].IsResolved {
		test.Fatalf("unexpected page 2: %+v", secondPage)
	}
	// A file-level thread has no lines but is not outdated.
	file := secondPage.Items[1]
	if file.SubjectType != github.SubjectFile || file.IsOutdated || file.Line != 0 || file.OriginalLine != 0 {
		test.Errorf("unexpected file-level thread: %+v", file)
	}
	// A range can start on the removed side and end on the added side, and
	// a pending draft comment is flagged.
	reviewThread := secondPage.Items[2]
	if reviewThread.StartDiffSide != github.SideLeft || reviewThread.DiffSide != github.SideRight || reviewThread.StartLine != 12 || reviewThread.Line != 30 ||
		len(reviewThread.Comments) != 1 || !reviewThread.Comments[0].Pending {
		test.Errorf("unexpected cross-side range thread: %+v", reviewThread)
	}
}

func TestReplyToThread(test *testing.T) {
	testServer := &server{}
	client := newTestClient(test, testServer)
	got, err := client.ReplyToThread(context.Background(), "PRRT_kwDOExample01", "Done.")
	if err != nil {
		test.Fatal(err)
	}
	if got.Identifier != "PRRC_kwDOExample05" || got.Body != "Done." || got.Author != "MikePors" {
		test.Errorf("unexpected comment: %+v", got)
	}
	variables := testServer.Calls()[0].Variables
	if variables["threadId"] != "PRRT_kwDOExample01" || variables["body"] != "Done." {
		test.Errorf("unexpected variables: %v", variables)
	}
}

func TestAddComment(test *testing.T) {
	testServer := &server{}
	client := newTestClient(test, testServer)
	got, err := client.AddComment(context.Background(), repository, 27, "Ship it.")
	if err != nil {
		test.Fatal(err)
	}
	want := github.IssueComment{
		Identifier: "IC_kwDOExample03", Author: "MikePors", Body: "Ship it.",
		CreatedAt: mustTime(test, "2026-10-19T09:30:00Z"),
		URL:       "https://github.com/MikePors/strigidae/pull/27#issuecomment-2001",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		test.Errorf("mismatch (-want +got):\n%s", diff)
	}
	if raw := testServer.Calls()[0].RESTRaw; raw != `{"body":"Ship it."}` {
		test.Errorf("request body = %s", raw)
	}
}

func TestErrors(test *testing.T) {
	resetAt := time.Unix(2_000, 0)
	jsonError := func(writer http.ResponseWriter, status int, headers map[string]string, body string) {
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		for key, value := range headers {
			writer.Header().Set(key, value)
		}
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(body))
	}
	fixtureError := func(server *server, name string, headers map[string]string) func(call, http.ResponseWriter) bool {
		return func(_ call, writer http.ResponseWriter) bool {
			for key, value := range headers {
				writer.Header().Set(key, value)
			}
			server.fixture(writer, http.StatusOK, name)
			return true
		}
	}

	tests := []struct {
		name     string
		override func(testServer *server) func(call, http.ResponseWriter) bool
		run      func(client *Client) error
		check    func(test *testing.T, err error)
	}{
		{
			name: "graphql not found",
			override: func(server *server) func(call, http.ResponseWriter) bool {
				return fixtureError(server, "error_not_found.json", nil)
			},
			run: func(client *Client) error {
				_, err := client.GetPullRequest(context.Background(), repository, 9999)
				return err
			},
			check: func(test *testing.T, err error) {
				if !errors.Is(err, github.ErrNotFound) {
					test.Errorf("want ErrNotFound, got %v", err)
				}
			},
		},
		{
			name: "graphql rate limited with points remaining uses graphql bucket reset",
			override: func(server *server) func(call, http.ResponseWriter) bool {
				return fixtureError(server, "error_rate_limited.json", map[string]string{
					"X-RateLimit-Resource": "graphql", "X-RateLimit-Remaining": "5", "X-RateLimit-Reset": strconv.FormatInt(resetAt.Unix(), 10),
				})
			},
			run: func(client *Client) error {
				_, err := client.ListPullRequests(context.Background(), repository, github.PageRequest{})
				return err
			},
			check: func(test *testing.T, err error) {
				var rateLimitError *github.RateLimitError
				if !errors.As(err, &rateLimitError) {
					test.Fatalf("want RateLimitError, got %v", err)
				}
				if !rateLimitError.ResetAt.Equal(resetAt) {
					test.Errorf("ResetAt = %v, want %v", rateLimitError.ResetAt, resetAt)
				}
				if duration := rateLimitError.RetryAfter(time.Unix(1_000, 0)); duration != 1000*time.Second {
					test.Errorf("RetryAfter = %v", duration)
				}
			},
		},
	}

	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			testServer := &server{}
			testServer.override = testCase.override(testServer)
			client := newTestClient(test, testServer)
			testCase.check(test, testCase.run(client))
		})
	}

	httpTests := []struct {
		name    string
		status  int
		headers map[string]string
		body    string
		check   func(test *testing.T, err error)
	}{
		{
			name: "404", status: 404, body: `{"message":"Not Found"}`,
			check: func(test *testing.T, err error) {
				if !errors.Is(err, github.ErrNotFound) {
					test.Errorf("want ErrNotFound, got %v", err)
				}
			},
		},
		{
			name: "401", status: 401, body: `{"message":"Bad credentials"}`,
			check: func(test *testing.T, err error) {
				if !errors.Is(err, github.ErrNotAuthenticated) {
					test.Errorf("want ErrNotAuthenticated, got %v", err)
				}
			},
		},
		{
			name: "403 permission", status: 403, body: `{"message":"Resource not accessible by integration"}`,
			check: func(test *testing.T, err error) {
				var rateLimitError *github.RateLimitError
				if !errors.Is(err, github.ErrForbidden) || errors.As(err, &rateLimitError) {
					test.Errorf("want ErrForbidden only, got %v", err)
				}
			},
		},
		{
			name: "403 primary rate limit", status: 403,
			headers: map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "2000"},
			body:    `{"message":"API rate limit exceeded"}`,
			check: func(test *testing.T, err error) {
				var rateLimitError *github.RateLimitError
				if !errors.As(err, &rateLimitError) || !rateLimitError.ResetAt.Equal(resetAt) {
					test.Errorf("want RateLimitError reset at %v, got %v", resetAt, err)
				}
			},
		},
		{
			name: "403 secondary rate limit", status: 403,
			headers: map[string]string{"Retry-After": "60"},
			body:    `{"message":"You have exceeded a secondary rate limit"}`,
			check: func(test *testing.T, err error) {
				var rateLimitError *github.RateLimitError
				if !errors.As(err, &rateLimitError) || !rateLimitError.ResetAt.Equal(time.Unix(1_060, 0)) {
					test.Errorf("want RateLimitError reset at 1060, got %v", err)
				}
			},
		},
		{
			// The primary bucket still has requests; its reset must not be used.
			name: "403 secondary rate limit without Retry-After", status: 403,
			headers: map[string]string{"X-RateLimit-Remaining": "4200", "X-RateLimit-Reset": "4600"},
			body:    `{"message":"You have exceeded a secondary rate limit"}`,
			check: func(test *testing.T, err error) {
				var rateLimitError *github.RateLimitError
				if !errors.As(err, &rateLimitError) || !rateLimitError.ResetAt.Equal(time.Unix(1_060, 0)) {
					test.Errorf("want RateLimitError reset at now+1m, got %v", err)
				}
			},
		},
		{
			name: "403 secondary rate limit with Retry-After date", status: 403,
			headers: map[string]string{"Retry-After": time.Unix(1_300, 0).UTC().Format(http.TimeFormat)},
			body:    `{"message":"You have exceeded a secondary rate limit"}`,
			check: func(test *testing.T, err error) {
				var rateLimitError *github.RateLimitError
				if !errors.As(err, &rateLimitError) || !rateLimitError.ResetAt.Equal(time.Unix(1_300, 0)) {
					test.Errorf("want RateLimitError reset at 1300, got %v", err)
				}
			},
		},
		{
			name: "429 without headers", status: 429, body: `{"message":"slow down"}`,
			check: func(test *testing.T, err error) {
				var rateLimitError *github.RateLimitError
				if !errors.As(err, &rateLimitError) || !rateLimitError.ResetAt.Equal(time.Unix(1_060, 0)) {
					test.Errorf("want RateLimitError reset at now+1m, got %v", err)
				}
			},
		},
	}
	for _, testCase := range httpTests {
		test.Run("http "+testCase.name, func(test *testing.T) {
			testServer := &server{override: func(_ call, writer http.ResponseWriter) bool {
				jsonError(writer, testCase.status, testCase.headers, testCase.body)
				return true
			}}
			client := newTestClient(test, testServer)
			_, err := client.AddComment(context.Background(), repository, 27, "x")
			testCase.check(test, err)
			// GraphQL goes through the same mapping.
			_, err = client.ListPullRequests(context.Background(), repository, github.PageRequest{})
			testCase.check(test, err)
		})
	}
}

func TestNewWithoutToken(test *testing.T) {
	_, err := New(Options{
		Host:        "github.com",
		TokenSource: func(string) string { return "" },
		Transport:   handlerTransport{http.NotFoundHandler()},
	})
	if !errors.Is(err, github.ErrNotAuthenticated) {
		test.Fatalf("want ErrNotAuthenticated, got %v", err)
	}
	if got := err.Error(); !regexp.MustCompile(`gh auth login`).MatchString(got) {
		test.Errorf("error should mention gh auth login: %q", got)
	}
}

func TestNewUsesTokenSource(test *testing.T) {
	var asked string
	testServer := &server{test: test}
	client, err := New(Options{
		Host:    "ghe.example.test",
		APIHost: "api.ghe.example.test",
		TokenSource: func(host string) string {
			asked = host
			return "from-source"
		},
		Transport: handlerTransport{testServer},
	})
	if err != nil {
		test.Fatal(err)
	}
	if asked != "ghe.example.test" || client.Host() != "ghe.example.test" {
		test.Errorf("token looked up for %q, client host %q", asked, client.Host())
	}
}

func TestSetFileViewed(test *testing.T) {
	tests := []struct {
		name      string
		viewed    bool
		operation string
		field     string
	}{
		{"mark", true, "MarkFileViewed", "markFileAsViewed"},
		{"unmark", false, "UnmarkFileViewed", "unmarkFileAsViewed"},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			testServer := &server{}
			client := newTestClient(test, testServer)
			if err := client.SetFileViewed(context.Background(), "PR_kwDOExample01", "go.mod", testCase.viewed); err != nil {
				test.Fatal(err)
			}
			got := testServer.Calls()[0]
			if got.Operation != testCase.operation || !strings.Contains(got.Query, testCase.field+"(input:") {
				test.Errorf("op = %q, query = %q", got.Operation, got.Query)
			}
			if got.Variables["pullRequestId"] != "PR_kwDOExample01" || got.Variables["path"] != "go.mod" {
				test.Errorf("unexpected variables: %v", got.Variables)
			}
		})
	}
}

func TestSetFileViewedError(test *testing.T) {
	testServer := &server{}
	testServer.override = func(_ call, writer http.ResponseWriter) bool {
		testServer.fixture(writer, http.StatusOK, "error_not_found.json")
		return true
	}
	client := newTestClient(test, testServer)
	err := client.SetFileViewed(context.Background(), "PR_x", "go.mod", true)
	if !errors.Is(err, github.ErrNotFound) {
		test.Errorf("err = %v, want ErrNotFound", err)
	}
}
