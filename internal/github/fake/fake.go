// Package fake provides an in-memory github.Client for tests and demos. Seed
// it with the Set* methods, call it like the real client, then inspect what
// was recorded with Calls, Replies and Comments.
//
// The fake mirrors the real client where UI code can observe it: it returns
// only open pull requests newest-updated first, rejects empty comment bodies,
// answers ErrNotFound for unknown pull requests and threads, and updates the
// comment counts that later reads report. Like the real client it never shares
// memory with callers: everything stored and returned is deep-copied, so a
// view can append to or edit what it received without affecting the fake.
//
// Seeded data is returned as given, including pending (draft) reviews and
// comments; nothing is filtered by viewer.
package fake

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MikePors/strigidae/internal/github"
)

// Method names recorded in Call.Method and accepted by SetError.
const (
	MethodListPullRequests   = "ListPullRequests"
	MethodSearchPullRequests = "SearchPullRequests"
	MethodGetPullRequest     = "GetPullRequest"
	MethodGetDiff            = "GetDiff"
	MethodListFilePatches    = "ListFilePatches"
	MethodListReviewThreads  = "ListReviewThreads"
	MethodReplyToThread      = "ReplyToThread"
	MethodAddComment         = "AddComment"
	MethodGetReviewStatus    = "GetReviewStatus"
	MethodSubmitReview       = "SubmitReview"
	MethodSetFileViewed      = "SetFileViewed"
	MethodGetMergeStatus     = "GetMergeStatus"
	MethodMergePullRequest   = "MergePullRequest"
	MethodUpdatePullRequest  = "UpdatePullRequest"
	MethodListBranches       = "ListBranches"
	MethodCreatePullRequest  = "CreatePullRequest"
)

// Call is one recorded call with the arguments that apply to its method.
type Call struct {
	Method           string
	Repository       github.Repository
	Number           int
	Query            string
	Page             github.PageRequest
	ThreadIdentifier string
	Body             string
	// Title is set by UpdatePullRequest and CreatePullRequest.
	Title string
	// Head, Base and Draft are set by CreatePullRequest.
	Head, Base string
	Draft      bool
	// ReviewIdentifier is set by DeleteReview.
	ReviewIdentifier string
	// PullRequestIdentifier, Path and Viewed are set by the methods that take them:
	// PullRequestIdentifier by SetFileViewed, StartReview and MergePullRequest, Path by
	// SetFileViewed and AddReviewThread, Viewed by SetFileViewed.
	PullRequestIdentifier string
	Path                  string
	Viewed                bool
	// SubjectIdentifier and Reaction are set by AddReaction and RemoveReaction.
	SubjectIdentifier string
	Reaction          github.ReactionContent
}

// Reply is a recorded successful ReplyToThread call.
type Reply struct {
	ThreadIdentifier string
	Body             string
}

// Comment is a recorded successful AddComment call.
type Comment struct {
	Repository github.Repository
	Number     int
	Body       string
}

type pullRequestKey struct {
	repository github.Repository
	number     int
}

// Client implements github.Client in memory. The zero value is not usable;
// call New. Cursors are decimal offsets. All methods are safe for concurrent use.
type Client struct {
	// Viewer is the login used as the author of replies and comments.
	Viewer string
	// Now supplies timestamps for created comments.
	Now func() time.Time

	mutex          sync.Mutex
	lists          map[github.Repository][]github.PullRequest
	search         map[string][]github.PullRequest
	details        map[pullRequestKey]github.PullRequestDetail
	diffs          map[pullRequestKey]string
	filePatches    map[pullRequestKey][]github.FilePatch
	threads        map[pullRequestKey][]github.ReviewThread
	injectedErrors map[string]error
	calls          []Call
	replies        []Reply
	comments       []Comment
	pending        map[pullRequestKey]github.PendingReview
	reviews        []Submission
	nextIdentifier int
	merge          map[github.Repository]MergeSettings
	merges         []Merge
	updates        []github.UpdatePullRequestInput
	branches       map[github.Repository][]github.Branch
	created        []github.CreatePullRequestInput
	added          []github.AddThreadInput
	started        []StartedReview
	deleted        []string
	deletedReviews []string
}

var _ github.Client = (*Client)(nil)

// New returns an empty fake client whose viewer is "me".
func New() *Client {
	return &Client{
		Viewer:         "me",
		Now:            time.Now,
		lists:          map[github.Repository][]github.PullRequest{},
		search:         map[string][]github.PullRequest{},
		details:        map[pullRequestKey]github.PullRequestDetail{},
		diffs:          map[pullRequestKey]string{},
		filePatches:    map[pullRequestKey][]github.FilePatch{},
		threads:        map[pullRequestKey][]github.ReviewThread{},
		injectedErrors: map[string]error{},
		pending:        map[pullRequestKey]github.PendingReview{},
		merge:          map[github.Repository]MergeSettings{},
		branches:       map[github.Repository][]github.Branch{},
	}
}

func cloneReactions(reactions []github.ReactionSummary) []github.ReactionSummary {
	return slices.Clone(reactions)
}

func cloneIssueComment(comment github.IssueComment) github.IssueComment {
	comment.Reactions = cloneReactions(comment.Reactions)
	return comment
}

func cloneReviewComment(comment github.ReviewComment) github.ReviewComment {
	comment.Reactions = cloneReactions(comment.Reactions)
	return comment
}

func cloneThread(thread github.ReviewThread) github.ReviewThread {
	comments := make([]github.ReviewComment, len(thread.Comments))
	for i, comment := range thread.Comments {
		comments[i] = cloneReviewComment(comment)
	}
	if thread.Comments == nil {
		comments = nil
	}
	thread.Comments = comments
	return thread
}

func cloneThreads(threads []github.ReviewThread) []github.ReviewThread {
	if threads == nil {
		return nil
	}
	out := make([]github.ReviewThread, len(threads))
	for i, thread := range threads {
		out[i] = cloneThread(thread)
	}
	return out
}

func cloneDetail(detail github.PullRequestDetail) github.PullRequestDetail {
	detail.Labels = slices.Clone(detail.Labels)
	detail.ReviewRequests = slices.Clone(detail.ReviewRequests)
	detail.Files = slices.Clone(detail.Files)
	detail.Reactions = cloneReactions(detail.Reactions)
	detail.Reviews = slices.Clone(detail.Reviews)
	detail.CheckList = slices.Clone(detail.CheckList)
	if detail.Comments != nil {
		comments := make([]github.IssueComment, len(detail.Comments))
		for i, comment := range detail.Comments {
			comments[i] = cloneIssueComment(comment)
		}
		detail.Comments = comments
	}
	return detail
}

// SetPullRequests sets the pull requests known for the repository. ListPullRequests
// returns the open ones, most recently updated first.
func (client *Client) SetPullRequests(repository github.Repository, pullRequests []github.PullRequest) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.lists[repository] = slices.Clone(pullRequests)
}

// SetSearchResults sets what SearchPullRequests returns for exactly this
// query, in the given order. Unseeded queries return an empty page.
func (client *Client) SetSearchResults(query string, pullRequests []github.PullRequest) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.search[query] = slices.Clone(pullRequests)
}

// SetPullRequest sets the detail returned for detail.Repository and detail.Number.
func (client *Client) SetPullRequest(detail github.PullRequestDetail) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.details[pullRequestKey{detail.Repository, detail.Number}] = cloneDetail(detail)
}

// SetDiff sets the raw diff returned for a pull request.
func (client *Client) SetDiff(repository github.Repository, number int, diff string) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.diffs[pullRequestKey{repository, number}] = diff
}

// SetFilePatches sets the file list ListFilePatches returns for a pull
// request. To exercise the fallback for a large diff, pair it with
// SetError(MethodGetDiff, github.ErrDiffTooLarge).
func (client *Client) SetFilePatches(repository github.Repository, number int, patches []github.FilePatch) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.filePatches[pullRequestKey{repository, number}] = slices.Clone(patches)
}

// SetReviewThreads sets the threads returned for a pull request.
func (client *Client) SetReviewThreads(repository github.Repository, number int, threads []github.ReviewThread) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.threads[pullRequestKey{repository, number}] = client.permissions(cloneThreads(threads))
}

// SetError makes the named method fail with err until cleared with a nil err.
// An empty method applies to every method that has no error of its own.
func (client *Client) SetError(method string, err error) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if err == nil {
		delete(client.injectedErrors, method)
		return
	}
	client.injectedErrors[method] = err
}

// Calls returns every call made so far with its arguments, in order,
// including calls that failed.
func (client *Client) Calls() []Call {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return slices.Clone(client.calls)
}

// Replies returns the recorded successful ReplyToThread calls.
func (client *Client) Replies() []Reply {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return slices.Clone(client.replies)
}

// Comments returns the recorded successful AddComment calls.
func (client *Client) Comments() []Comment {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return slices.Clone(client.comments)
}

// begin records the call and returns the injected error, if any. Callers hold client.mu.
func (client *Client) begin(ctx context.Context, call Call) error {
	client.calls = append(client.calls, call)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err, ok := client.injectedErrors[call.Method]; ok {
		return err
	}
	return client.injectedErrors[""]
}

func paginate[Value any](items []Value, pageRequest github.PageRequest) (github.Page[Value], error) {
	start := 0
	if pageRequest.After != "" {
		count, err := strconv.Atoi(pageRequest.After)
		if err != nil || count < 0 || count > len(items) {
			return github.Page[Value]{}, fmt.Errorf("invalid cursor %q", pageRequest.After)
		}
		start = count
	}
	end := min(start+pageRequest.Size(), len(items))
	page := github.Page[Value]{Items: slices.Clone(items[start:end]), Total: len(items)}
	if end < len(items) {
		page.Next = strconv.Itoa(end)
	}
	return page, nil
}

// ListPullRequests implements github.Client.
func (client *Client) ListPullRequests(ctx context.Context, repository github.Repository, pageRequest github.PageRequest) (github.Page[github.PullRequest], error) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if err := client.begin(ctx, Call{Method: MethodListPullRequests, Repository: repository, Page: pageRequest}); err != nil {
		return github.Page[github.PullRequest]{}, err
	}
	all, ok := client.lists[repository]
	if !ok {
		return github.Page[github.PullRequest]{}, fmt.Errorf("list pull requests in %s: %w", repository, github.ErrNotFound)
	}
	var open []github.PullRequest
	for _, pullRequest := range all {
		if pullRequest.State == "" || pullRequest.State == github.PullRequestStateOpen {
			open = append(open, pullRequest)
		}
	}
	slices.SortStableFunc(open, func(first, pullRequest github.PullRequest) int { return pullRequest.UpdatedAt.Compare(first.UpdatedAt) })
	return paginate(open, pageRequest)
}

// SearchPullRequests implements github.Client.
func (client *Client) SearchPullRequests(ctx context.Context, query string, pageRequest github.PageRequest) (github.Page[github.PullRequest], error) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if err := client.begin(ctx, Call{Method: MethodSearchPullRequests, Query: query, Page: pageRequest}); err != nil {
		return github.Page[github.PullRequest]{}, err
	}
	return paginate(client.search[query], pageRequest)
}

// GetPullRequest implements github.Client.
func (client *Client) GetPullRequest(ctx context.Context, repository github.Repository, number int) (github.PullRequestDetail, error) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if err := client.begin(ctx, Call{Method: MethodGetPullRequest, Repository: repository, Number: number}); err != nil {
		return github.PullRequestDetail{}, err
	}
	detail, ok := client.details[pullRequestKey{repository, number}]
	if !ok {
		return github.PullRequestDetail{}, fmt.Errorf("load pull request %s#%d: %w", repository, number, github.ErrNotFound)
	}
	return cloneDetail(detail), nil
}

// GetDiff implements github.Client.
func (client *Client) GetDiff(ctx context.Context, repository github.Repository, number int) (string, error) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if err := client.begin(ctx, Call{Method: MethodGetDiff, Repository: repository, Number: number}); err != nil {
		return "", err
	}
	diffText, ok := client.diffs[pullRequestKey{repository, number}]
	if !ok {
		return "", fmt.Errorf("load diff of %s#%d: %w", repository, number, github.ErrNotFound)
	}
	return diffText, nil
}

// ListFilePatches implements github.Client.
func (client *Client) ListFilePatches(ctx context.Context, repository github.Repository, number int) ([]github.FilePatch, error) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if err := client.begin(ctx, Call{Method: MethodListFilePatches, Repository: repository, Number: number}); err != nil {
		return nil, err
	}
	patches, ok := client.filePatches[pullRequestKey{repository, number}]
	if !ok {
		return nil, fmt.Errorf("list files of %s#%d: %w", repository, number, github.ErrNotFound)
	}
	return slices.Clone(patches), nil
}

// ListReviewThreads implements github.Client. A pull request with no threads
// set yields an empty page.
func (client *Client) ListReviewThreads(ctx context.Context, repository github.Repository, number int, pageRequest github.PageRequest) (github.Page[github.ReviewThread], error) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if err := client.begin(ctx, Call{Method: MethodListReviewThreads, Repository: repository, Number: number, Page: pageRequest}); err != nil {
		return github.Page[github.ReviewThread]{}, err
	}
	currentPage, err := paginate(client.threads[pullRequestKey{repository, number}], pageRequest)
	currentPage.Items = cloneThreads(currentPage.Items)
	return currentPage, err
}

// ReplyToThread implements github.Client. The reply is appended to the thread
// so later ListReviewThreads calls include it.
func (client *Client) ReplyToThread(ctx context.Context, threadIdentifier, body string) (github.ReviewComment, error) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if err := client.begin(ctx, Call{Method: MethodReplyToThread, ThreadIdentifier: threadIdentifier, Body: body}); err != nil {
		return github.ReviewComment{}, err
	}
	if strings.TrimSpace(body) == "" {
		return github.ReviewComment{}, fmt.Errorf("reply to thread %s: empty body", threadIdentifier)
	}
	for _, threads := range client.threads {
		for i := range threads {
			if threads[i].Identifier != threadIdentifier {
				continue
			}
			client.nextIdentifier++
			reviewComment := github.ReviewComment{
				Identifier: "fake-review-comment-" + strconv.Itoa(client.nextIdentifier),
				Author:     client.Viewer,
				Body:       body,
				CreatedAt:  client.Now(),

				ViewerCanUpdate: true, ViewerCanDelete: true, ViewerDidAuthor: true,
			}
			// Build a fresh slice so slices handed out earlier are never written to.
			threads[i].Comments = append(slices.Clip(threads[i].Comments), reviewComment)
			client.replies = append(client.replies, Reply{ThreadIdentifier: threadIdentifier, Body: body})
			return reviewComment, nil
		}
	}
	return github.ReviewComment{}, fmt.Errorf("reply to thread %s: %w", threadIdentifier, github.ErrNotFound)
}

// AddComment implements github.Client. It fails with ErrNotFound for a pull
// request the fake has no data for, and otherwise bumps the comment count in
// the detail, the repo list and search results.
func (client *Client) AddComment(ctx context.Context, repository github.Repository, number int, body string) (github.IssueComment, error) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if err := client.begin(ctx, Call{Method: MethodAddComment, Repository: repository, Number: number, Body: body}); err != nil {
		return github.IssueComment{}, err
	}
	if strings.TrimSpace(body) == "" {
		return github.IssueComment{}, fmt.Errorf("add comment to %s#%d: empty body", repository, number)
	}
	key := pullRequestKey{repository, number}
	detail, hasDetail := client.details[key]
	known := hasDetail
	bump := func(pullRequests []github.PullRequest) {
		for i := range pullRequests {
			if pullRequests[i].Repository == repository && pullRequests[i].Number == number {
				pullRequests[i].CommentCount++
				known = true
			}
		}
	}
	for _, pullRequests := range client.lists {
		bump(pullRequests)
	}
	for _, pullRequests := range client.search {
		bump(pullRequests)
	}
	if !known {
		return github.IssueComment{}, fmt.Errorf("add comment to %s#%d: %w", repository, number, github.ErrNotFound)
	}
	client.nextIdentifier++
	issueComment := github.IssueComment{
		Identifier: "fake-comment-" + strconv.Itoa(client.nextIdentifier),
		Author:     client.Viewer,
		Body:       body,
		CreatedAt:  client.Now(),
	}
	if hasDetail {
		detail.Comments = append(slices.Clip(detail.Comments), issueComment)
		detail.CommentCount++
		client.details[key] = detail
	}
	client.comments = append(client.comments, Comment{Repository: repository, Number: number, Body: body})
	return issueComment, nil
}

// SetFileViewed implements github.Client. It updates the file's state in the
// pull request detail with the matching ID, so later reads report it; an
// unknown pull request or path fails with ErrNotFound.
func (client *Client) SetFileViewed(ctx context.Context, pullRequestIdentifier, path string, viewed bool) error {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if err := client.begin(ctx, Call{Method: MethodSetFileViewed, PullRequestIdentifier: pullRequestIdentifier, Path: path, Viewed: viewed}); err != nil {
		return err
	}
	state := github.FileUnviewed
	if viewed {
		state = github.FileViewed
	}
	for key, detail := range client.details {
		if detail.Identifier != pullRequestIdentifier {
			continue
		}
		for i := range detail.Files {
			if detail.Files[i].Path == path {
				detail.Files = slices.Clone(detail.Files)
				detail.Files[i].Viewed = state
				client.details[key] = detail
				return nil
			}
		}
	}
	return fmt.Errorf("mark %s as viewed in %s: %w", path, pullRequestIdentifier, github.ErrNotFound)
}
