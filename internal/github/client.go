package github

import "context"

// Client is everything the UI needs from GitHub. All methods are safe for
// concurrent use. Errors wrap ErrNotAuthenticated, ErrNotFound, ErrForbidden
// or *RateLimitError where applicable; test with errors.Is / errors.As.
type Client interface {
	// ListPullRequests lists open pull requests in repo, most recently updated first.
	ListPullRequests(ctx context.Context, repository Repository, pageRequest PageRequest) (Page[PullRequest], error)
	// SearchPullRequests runs a GitHub search query restricted to pull
	// requests ("is:pr" is added automatically), e.g. "review-requested:@me is:open".
	SearchPullRequests(ctx context.Context, query string, pageRequest PageRequest) (Page[PullRequest], error)
	// GetPullRequest returns one pull request with all its files, conversation
	// comments and review summaries.
	GetPullRequest(ctx context.Context, repository Repository, number int) (PullRequestDetail, error)
	// GetDiff returns the raw unified diff of the pull request.
	GetDiff(ctx context.Context, repository Repository, number int) (string, error)
	// ListFilePatches returns every file of the pull request with its own
	// patch, up to GitHub's limit of 3000 files. It is the fallback for a diff
	// GetDiff refuses with ErrDiffTooLarge.
	ListFilePatches(ctx context.Context, repository Repository, number int) ([]FilePatch, error)
	// ListReviewThreads lists review threads, including resolved and outdated ones.
	ListReviewThreads(ctx context.Context, repository Repository, number int, pageRequest PageRequest) (Page[ReviewThread], error)
	// ReplyToThread posts a reply to the thread with the given ID and returns the new comment.
	ReplyToThread(ctx context.Context, threadIdentifier, body string) (ReviewComment, error)
	// AddComment posts a top-level comment on the pull request.
	AddComment(ctx context.Context, repository Repository, number int, body string) (IssueComment, error)
	// GetReviewStatus returns what a review submission needs to know right
	// now: the pull request's node ID, its current head commit, and the
	// viewer's pending review if there is one. It is a small query, cheap
	// enough to run just before asking for confirmation.
	GetReviewStatus(ctx context.Context, repository Repository, number int) (ReviewStatus, error)
	// SubmitReview submits a review verdict. With in.PendingReviewIdentifier set it
	// submits that pending review (so its inline comments go too); otherwise
	// it creates and submits a new review on in.CommitObjectIdentifier. A pending review
	// that no longer exists yields ErrNotFound.
	SubmitReview(ctx context.Context, in SubmitReviewInput) error
	// SetFileViewed marks the file at path as viewed (or, with viewed false,
	// clears the mark) for the viewer. pullRequestIdentifier is PullRequest.Identifier.
	SetFileViewed(ctx context.Context, pullRequestIdentifier, path string, viewed bool) error
	// GetMergeStatus returns what a merge needs to know right now: the pull
	// request's node ID, head commit and base branch, and the repository's
	// allowed merge methods and branch deletion setting.
	GetMergeStatus(ctx context.Context, repository Repository, number int) (MergeStatus, error)
	// MergePullRequest merges the pull request with in.Method and GitHub's
	// default commit title and message. GitHub's refusal (failing checks,
	// conflicts, a moved head) comes back as an error.
	MergePullRequest(ctx context.Context, in MergeInput) error
	// UpdatePullRequest sets the title and body of the pull request. GitHub
	// decides by permissions (see PullRequestDetail.ViewerCanUpdate); a refusal
	// comes back as an error.
	UpdatePullRequest(ctx context.Context, in UpdatePullRequestInput) error
	// StartReview creates the viewer's pending review on in.CommitObjectIdentifier and
	// returns it. GitHub allows one pending review per user per pull request,
	// so this fails when one exists; call GetReviewStatus first.
	StartReview(ctx context.Context, in StartReviewInput) (PendingReview, error)
	// AddReviewThread adds an inline comment, as a new thread, to the pending
	// review in.PendingReviewIdentifier and returns the thread. The comment stays
	// private until the review is submitted. A pending review that no longer
	// exists yields ErrNotFound; a position GitHub does not accept (for example
	// a line outside the diff) yields ErrCommentRejected.
	AddReviewThread(ctx context.Context, in AddThreadInput) (ReviewThread, error)
	// GetReviewComment returns one review comment by node ID, with its current
	// state (Pending) and what the viewer may do with it. A comment that does
	// not exist any more yields ErrNotFound.
	GetReviewComment(ctx context.Context, commentIdentifier string) (ReviewComment, error)
	// UpdateReviewComment changes the body of a review comment, pending or
	// published, and returns it. GitHub decides by permissions (see
	// ReviewComment.ViewerCanUpdate): the viewer's own comments, and with write
	// access other people's too.
	UpdateReviewComment(ctx context.Context, commentIdentifier, body string) (ReviewComment, error)
	// DeleteReviewComment deletes a review comment, pending or published, under
	// the same permissions (see ReviewComment.ViewerCanDelete).
	// Deleting a thread's only comment removes the thread.
	DeleteReviewComment(ctx context.Context, commentIdentifier string) error
	// DeleteReview deletes the viewer's pending review with the given node ID,
	// together with all its draft comments. A review that no longer exists
	// yields ErrNotFound. A review that was already submitted cannot be deleted;
	// how GitHub reports that is not pinned down, so it may be another error.
	DeleteReview(ctx context.Context, reviewIdentifier string) error
	// ResolveReviewThread marks the thread resolved and returns its new state.
	// A thread that does not exist yields ErrNotFound.
	ResolveReviewThread(ctx context.Context, threadIdentifier string) (ThreadResolution, error)
	// UnresolveReviewThread marks the thread unresolved and returns its new state.
	UnresolveReviewThread(ctx context.Context, threadIdentifier string) (ThreadResolution, error)
	// AddReaction adds the viewer's reaction to the comment with the given
	// node ID. Adding one the viewer already has changes nothing. A comment
	// that does not exist yields ErrNotFound.
	AddReaction(ctx context.Context, subjectIdentifier string, content ReactionContent) error
	// RemoveReaction removes the viewer's reaction from the comment with the
	// given node ID. Removing one the viewer does not have changes nothing.
	RemoveReaction(ctx context.Context, subjectIdentifier string, content ReactionContent) error
	// ListBranches lists the repository's branches in name order. The default
	// branch is marked on the item that is it.
	ListBranches(ctx context.Context, repository Repository, pageRequest PageRequest) (Page[Branch], error)
	// CreatePullRequest opens a pull request from in.Head into in.Base and
	// returns it. GitHub's refusal (no commits between the branches, a pull
	// request already open for the head, an unknown branch) comes back as an
	// error.
	CreatePullRequest(ctx context.Context, in CreatePullRequestInput) (PullRequest, error)
}
