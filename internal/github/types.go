package github

import (
	"slices"
	"time"
)

// PullRequestState is the lifecycle state of a pull request.
type PullRequestState string

// Pull request states.
const (
	PullRequestStateOpen   PullRequestState = "OPEN"
	PullRequestStateClosed PullRequestState = "CLOSED"
	PullRequestStateMerged PullRequestState = "MERGED"
)

// ReviewDecision is the aggregate review outcome. Empty means no decision
// (reviews not required, or none submitted).
type ReviewDecision string

// Review decisions.
const (
	ReviewDecisionNone             ReviewDecision = ""
	ReviewDecisionApproved         ReviewDecision = "APPROVED"
	ReviewDecisionChangesRequested ReviewDecision = "CHANGES_REQUESTED"
	ReviewDecisionReviewRequired   ReviewDecision = "REVIEW_REQUIRED"
)

// PullRequest is the summary shown in lists.
type PullRequest struct {
	// Identifier is the GraphQL node ID, needed by mutations that target the pull request.
	Identifier string
	// HeadSHA is the head commit; review comments are anchored to it, and a
	// change since a diff was loaded means that diff is stale.
	HeadSHA        string
	Repository     Repository
	Number         int
	Title          string
	Author         string
	State          PullRequestState
	Draft          bool
	ReviewDecision ReviewDecision
	// Checks is the combined check state of the head commit.
	Checks CheckState
	// CommentCount counts top-level (conversation) comments only.
	CommentCount int
	CreatedAt    time.Time
	UpdatedAt    time.Time
	URL          string
	HeadBranch   string
	BaseBranch   string
	// CrossRepository is true when the head branch lives in a fork, so a
	// branch of the same name here is a different branch.
	CrossRepository bool
}

// Label is a repository label.
type Label struct {
	Name string
	// Color is a hex RGB value without the leading "#".
	Color string
}

// Reviewer is a user or team whose review was requested.
type Reviewer struct {
	// Name is the login, or the team slug when IsTeam is set. A team's Name is
	// empty when the token lacks the read:org scope needed to read it.
	Name   string
	IsTeam bool
}

// FileChangeType describes how a file changed.
type FileChangeType string

// File change types.
const (
	FileAdded    FileChangeType = "ADDED"
	FileModified FileChangeType = "MODIFIED"
	FileDeleted  FileChangeType = "DELETED"
	FileRenamed  FileChangeType = "RENAMED"
	FileCopied   FileChangeType = "COPIED"
	FileChanged  FileChangeType = "CHANGED"
)

// ViewedState is the viewer's "Viewed" mark on a file of a pull request. It is
// private to the viewer and shared with github.com.
type ViewedState string

// Viewed states. The zero value means the state is unknown and is treated as
// unviewed.
const (
	FileUnviewed ViewedState = "UNVIEWED"
	FileViewed   ViewedState = "VIEWED"
	// FileDismissed means the file was viewed but changed in a later push.
	FileDismissed ViewedState = "DISMISSED"
)

// ChangedFile is one file in a pull request.
type ChangedFile struct {
	Path      string
	Additions int
	Deletions int
	Change    FileChangeType
	// Viewed is the viewer's viewed state of the file.
	Viewed ViewedState
}

// FilePatch is one file of a pull request with its own patch, from the pull
// request's file list.
type FilePatch struct {
	Path string
	// PreviousPath is the path before a rename or copy; empty otherwise.
	PreviousPath string
	Change       FileChangeType
	Additions    int
	Deletions    int
	// Patch is the file's hunks in unified diff format, from its first "@@"
	// line, with no file header. GitHub leaves it empty for a binary file, a
	// file too large on its own, and a file with no changed lines.
	Patch string
}

// ReviewState is the state of a submitted review.
type ReviewState string

// Review states.
const (
	ReviewApproved         ReviewState = "APPROVED"
	ReviewChangesRequested ReviewState = "CHANGES_REQUESTED"
	ReviewCommented        ReviewState = "COMMENTED"
	ReviewDismissed        ReviewState = "DISMISSED"
	ReviewPending          ReviewState = "PENDING"
)

// Review is a review summary (the verdict and its optional body). Reviews in
// state ReviewPending are the viewer's own unsubmitted drafts; they stay in
// the list and have a zero SubmittedAt, so sort or filter them deliberately.
type Review struct {
	Identifier  string
	Author      string
	State       ReviewState
	Body        string
	SubmittedAt time.Time
}

// PullRequestDetail is a pull request with everything the detail view needs.
type PullRequestDetail struct {
	PullRequest
	// ViewerDidAuthor is set when the viewer opened the pull request. GitHub
	// refuses to approve or request changes on your own pull request.
	ViewerDidAuthor bool
	// ViewerCanUpdate says whether GitHub lets the viewer change the title and
	// body (the author, and people with write access).
	ViewerCanUpdate bool
	Body            string
	// Reactions are the reactions on the description (Body).
	Reactions []ReactionSummary
	Additions int
	Deletions int
	Labels    []Label
	// ReviewRequests are the reviewers currently asked to review.
	ReviewRequests []Reviewer
	Files          []ChangedFile
	// Comments are the top-level conversation comments, oldest first.
	Comments []IssueComment
	// Reviews are submitted review summaries, oldest first.
	Reviews []Review
	// CheckList holds the first 100 checks of the head commit, in GitHub's
	// order, without runs that a later run of the same check replaced;
	// CheckTotal is how many there are in all, likewise without those.
	CheckList  []Check
	CheckTotal int
	// MergeState says whether conflicts, a stale branch or branch protection
	// stand in the way of merging.
	MergeState MergeState
}

// ReactionContent is a kind of emoji reaction.
type ReactionContent string

// Reaction kinds.
const (
	ReactionThumbsUp   ReactionContent = "THUMBS_UP"
	ReactionThumbsDown ReactionContent = "THUMBS_DOWN"
	ReactionLaugh      ReactionContent = "LAUGH"
	ReactionHooray     ReactionContent = "HOORAY"
	ReactionConfused   ReactionContent = "CONFUSED"
	ReactionHeart      ReactionContent = "HEART"
	ReactionRocket     ReactionContent = "ROCKET"
	ReactionEyes       ReactionContent = "EYES"
)

// ReactionContents lists every kind of reaction in GitHub's order.
var ReactionContents = []ReactionContent{
	ReactionThumbsUp, ReactionThumbsDown, ReactionLaugh, ReactionHooray,
	ReactionConfused, ReactionHeart, ReactionRocket, ReactionEyes,
}

// Shortcode returns the name GitHub gives the reaction (+1, heart, ...), or
// the raw content if unknown.
func (content ReactionContent) Shortcode() string {
	switch content {
	case ReactionThumbsUp:
		return "+1"
	case ReactionThumbsDown:
		return "-1"
	case ReactionLaugh:
		return "laugh"
	case ReactionHooray:
		return "hooray"
	case ReactionConfused:
		return "confused"
	case ReactionHeart:
		return "heart"
	case ReactionRocket:
		return "rocket"
	case ReactionEyes:
		return "eyes"
	default:
		return string(content)
	}
}

// Emoji returns the emoji for the reaction, or the raw content if unknown.
func (content ReactionContent) Emoji() string {
	switch content {
	case ReactionThumbsUp:
		return "👍"
	case ReactionThumbsDown:
		return "👎"
	case ReactionLaugh:
		return "😄"
	case ReactionHooray:
		return "🎉"
	case ReactionConfused:
		return "😕"
	case ReactionHeart:
		return "❤️"
	case ReactionRocket:
		return "🚀"
	case ReactionEyes:
		return "👀"
	default:
		return string(content)
	}
}

// ReactionSummary is the count of one kind of reaction on a comment.
type ReactionSummary struct {
	Content       ReactionContent
	Count         int
	ViewerReacted bool
}

// WithViewerReaction returns reactions with the viewer's reaction of kind
// content added (add true) or removed, as GitHub counts it: the group's count
// goes up or down, a group that reaches zero is dropped, and the groups keep
// GitHub's order. Adding one the viewer already has, or removing one the viewer
// lacks, changes nothing. The argument is not modified.
func WithViewerReaction(reactions []ReactionSummary, content ReactionContent, add bool) []ReactionSummary {
	out := slices.Clone(reactions)
	index := slices.IndexFunc(out, func(summary ReactionSummary) bool { return summary.Content == content })
	switch {
	case add && index < 0:
		out = append(out, ReactionSummary{Content: content, Count: 1, ViewerReacted: true})
	case add && !out[index].ViewerReacted:
		out[index].Count++
		out[index].ViewerReacted = true
	case !add && index >= 0 && out[index].ViewerReacted:
		out[index].Count--
		out[index].ViewerReacted = false
		if out[index].Count <= 0 {
			out = slices.Delete(out, index, index+1)
		}
	}
	order := func(summary ReactionSummary) int { return slices.Index(ReactionContents, summary.Content) }
	slices.SortStableFunc(out, func(first, second ReactionSummary) int { return order(first) - order(second) })
	return out
}

// IssueComment is a top-level comment on a pull request.
type IssueComment struct {
	Identifier string
	Author     string
	Body       string
	CreatedAt  time.Time
	URL        string
	Reactions  []ReactionSummary
}

// DiffSide says which side of a split diff a thread line refers to.
type DiffSide string

// Diff sides.
const (
	// SideLeft is the base (removed) side.
	SideLeft DiffSide = "LEFT"
	// SideRight is the head (added) side.
	SideRight DiffSide = "RIGHT"
)

// ReviewComment is a comment inside a review thread.
type ReviewComment struct {
	Identifier string
	Author     string
	Body       string
	CreatedAt  time.Time
	URL        string
	// Pending marks a draft comment of the viewer's own pending review, which
	// nobody else can see yet.
	Pending bool
	// ViewerCanUpdate and ViewerCanDelete say whether the viewer may change or
	// delete the comment. They are also true for other people's comments when
	// the viewer has write access, so authorship needs ViewerDidAuthor.
	ViewerCanUpdate bool
	ViewerCanDelete bool
	// ViewerDidAuthor is set when the viewer wrote the comment.
	ViewerDidAuthor bool
	// DiffHunk is the hunk the comment was made on, useful for outdated threads.
	DiffHunk  string
	Reactions []ReactionSummary
}

// ThreadSubject says what a review thread is attached to.
type ThreadSubject string

// Thread subjects.
const (
	// SubjectLine is a thread on a line or range of lines.
	SubjectLine ThreadSubject = "LINE"
	// SubjectFile is a thread on a whole file; it has no line numbers and is
	// not outdated.
	SubjectFile ThreadSubject = "FILE"
)

// LocalState says whether a review thread exists only inside strig (a plugin's
// answer shown on the lines it was asked about) and, if so, how its answer
// stands. GitHub never returns a local thread and none can be written back:
// every action that writes to GitHub refuses one before it starts.
type LocalState int

// Local states. NotLocal, the zero value, is every thread GitHub knows.
const (
	NotLocal LocalState = iota
	// LocalWaiting is a question whose answer has not come yet.
	LocalWaiting
	// LocalAnswered has its answer.
	LocalAnswered
	// LocalFailed has no answer: the plugin failed, timed out or said why not.
	LocalFailed
)

// IsLocal reports whether the thread exists only inside strig.
func (state LocalState) IsLocal() bool { return state != NotLocal }

// ReviewThread is a conversation anchored to a line of a diff. Threads may
// include the viewer's pending (draft) comments; see ReviewComment.Pending.
type ReviewThread struct {
	// Local marks a thread that exists only inside strig; see LocalState. A
	// local thread is never resolved and has no GitHub identifier.
	Local LocalState
	// Identifier is the node ID used to reply to the thread.
	Identifier  string
	SubjectType ThreadSubject
	IsResolved  bool
	// IsOutdated means the anchored lines changed after the comment was made.
	IsOutdated bool
	ResolvedBy string
	Path       string
	// Line is the line number in the file on DiffSide (the old file for
	// SideLeft, the new file for SideRight), not a position in the diff. It is
	// zero when the thread is outdated and no longer maps onto the current
	// diff (use OriginalLine then), and for file-level threads (SubjectFile).
	Line         int
	OriginalLine int
	// StartLine and OriginalStartLine are set for multi-line threads, else
	// zero. They are on StartDiffSide, which can differ from DiffSide.
	StartLine         int
	OriginalStartLine int
	DiffSide          DiffSide
	StartDiffSide     DiffSide
	Comments          []ReviewComment
}

// ThreadResolution is the state of a review thread after it was resolved or
// unresolved. ResolvedBy is empty when the thread is not resolved.
type ThreadResolution struct {
	Identifier string
	IsResolved bool
	ResolvedBy string
}

// ReviewEvent is the verdict of a review being submitted.
type ReviewEvent string

// Review events.
const (
	ReviewEventApprove        ReviewEvent = "APPROVE"
	ReviewEventRequestChanges ReviewEvent = "REQUEST_CHANGES"
	ReviewEventComment        ReviewEvent = "COMMENT"
)

// PendingReview is the viewer's unsubmitted review on a pull request. GitHub
// allows one per user per pull request.
type PendingReview struct {
	Identifier string
	// Body is the review summary written so far.
	Body string
	// CommentCount is the number of inline comments waiting in it.
	CommentCount int
	// CommitObjectIdentifier is the commit the review is anchored to; empty if unknown.
	CommitObjectIdentifier string
}

// ReviewStatus is the state a review submission depends on.
type ReviewStatus struct {
	// PullRequestIdentifier is the pull request's node ID.
	PullRequestIdentifier string
	// HeadSHA is the pull request's current head commit.
	HeadSHA string
	// Pending is the viewer's pending review, or nil.
	Pending *PendingReview
}

// SubmitReviewInput describes a review to submit.
type SubmitReviewInput struct {
	Repository            Repository
	Number                int
	PullRequestIdentifier string
	Event                 ReviewEvent
	Body                  string
	// CommitObjectIdentifier is the commit a new review applies to. A pending review keeps
	// the commit it was started on.
	CommitObjectIdentifier string
	// PendingReviewIdentifier, if set, submits that pending review instead of
	// creating a new one.
	PendingReviewIdentifier string
}

// StartReviewInput describes a new pending review.
type StartReviewInput struct {
	Repository            Repository
	Number                int
	PullRequestIdentifier string
	// CommitObjectIdentifier is the commit the review's comments are anchored to.
	CommitObjectIdentifier string
}

// AddThreadInput describes an inline comment thread to add to a pending
// review. Line is the last line of the range and StartLine the first one for a
// multi-line comment (zero for a single line); both are line numbers on the
// given side of the diff, not positions in it.
type AddThreadInput struct {
	Repository              Repository
	Number                  int
	PendingReviewIdentifier string
	Path                    string
	Line                    int
	Side                    DiffSide
	StartLine               int
	StartSide               DiffSide
	Body                    string
}

// UpdatePullRequestInput is what UpdatePullRequest changes: the title and the
// body, both always sent.
type UpdatePullRequestInput struct {
	Repository Repository
	Number     int
	// PullRequestIdentifier is PullRequest.Identifier.
	PullRequestIdentifier string
	Title                 string
	Body                  string
}
