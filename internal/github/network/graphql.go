package network

import (
	"fmt"
	"time"

	"github.com/MikePors/strigidae/internal/github"
)

// ghost is shown for authors whose account was deleted (GraphQL returns null).
const ghost = "ghost"

type graphqlActor struct{ Login string }

func (actor *graphqlActor) login() string {
	if actor == nil || actor.Login == "" {
		return ghost
	}
	return actor.Login
}

type graphqlPageInfo struct {
	HasNextPage bool
	EndCursor   string
}

func (pageInformation graphqlPageInfo) next() string {
	if !pageInformation.HasNextPage {
		return ""
	}
	return pageInformation.EndCursor
}

type graphqlConnection[Node any] struct {
	TotalCount int
	PageInfo   graphqlPageInfo
	Nodes      []Node
}

type graphqlPullRequest struct {
	ID                string
	Number            int
	Title             string
	URL               string
	IsDraft           bool
	State             string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	ReviewDecision    string
	BaseRefName       string
	HeadRefName       string
	HeadRefOid        string
	IsCrossRepository bool
	StatusCheckRollup *graphqlRollup
	MergeStateStatus  string
	Author            *graphqlActor
	CommentCount      struct{ TotalCount int }
	Repository        struct{ NameWithOwner string }
}

// graphqlRollup is the head commit's check rollup. Contexts is only selected
// by the detail query.
type graphqlRollup struct {
	State    string
	Contexts graphqlConnection[graphqlCheckContext]
}

// graphqlCheckContext is a CheckRun or a StatusContext, told apart by Typename.
type graphqlCheckContext struct {
	Typename string `json:"__typename"`

	// CheckRun
	Name       string
	Status     string
	Conclusion string
	DetailsURL string `json:"detailsUrl"`
	// StartedAt is null until the run starts.
	StartedAt  *time.Time `json:"startedAt"`
	CheckSuite *struct {
		WorkflowRun *struct {
			Event    string
			Workflow struct {
				DatabaseID int `json:"databaseId"`
				Name       string
			}
		}
		App *struct {
			DatabaseID int `json:"databaseId"`
		}
	}

	// StatusContext
	Context   string
	State     string
	TargetURL string `json:"targetUrl"`
}

func mapCheck(node graphqlCheckContext) github.Check {
	if node.Typename == "StatusContext" {
		return github.Check{Name: node.Context, Result: github.CheckResultFromStatus(node.State), URL: node.TargetURL}
	}
	name := node.Name
	if node.CheckSuite != nil && node.CheckSuite.WorkflowRun != nil && node.CheckSuite.WorkflowRun.Workflow.Name != "" {
		name = node.CheckSuite.WorkflowRun.Workflow.Name + " / " + name
	}
	return github.Check{Name: name, Result: github.CheckResultFromRun(node.Status, node.Conclusion), URL: node.DetailsURL}
}

// runKey identifies a check across its re-runs: the workflow, the event that
// triggered it and the job name, or for a run outside a workflow the app and
// the check name. Runs with different keys are different checks, even when
// they display the same name.
func runKey(context graphqlCheckContext) string {
	suite := context.CheckSuite
	if suite != nil && suite.WorkflowRun != nil {
		run := suite.WorkflowRun
		return fmt.Sprintf("workflow\x00%d\x00%s\x00%s", run.Workflow.DatabaseID, run.Event, context.Name)
	}
	appIdentifier := 0
	if suite != nil && suite.App != nil {
		appIdentifier = suite.App.DatabaseID
	}
	return fmt.Sprintf("app\x00%d\x00%s", appIdentifier, context.Name)
}

// latestRuns drops each check run that a later run of the same check (same
// runKey) replaced, keeping the order of the survivors. A run that has not
// started counts as newer than any started one; on a tie the later one in
// GitHub's order wins. Status contexts are kept as they are.
func latestRuns(contexts []graphqlCheckContext) []graphqlCheckContext {
	newest := map[string]int{}
	for i, context := range contexts {
		if context.Typename == "StatusContext" {
			continue
		}
		key := runKey(context)
		if earlier, seen := newest[key]; !seen || !startedBefore(contexts[i], contexts[earlier]) {
			newest[key] = i
		}
	}
	var kept []graphqlCheckContext
	for i, context := range contexts {
		if context.Typename == "StatusContext" || newest[runKey(context)] == i {
			kept = append(kept, context)
		}
	}
	return kept
}

// mapCheckRuns maps contexts to checks. Check runs that would show the same name
// (the same job run for different events) get " (<event>)" appended.
func mapCheckRuns(contexts []graphqlCheckContext) []github.Check {
	checks := make([]github.Check, len(contexts))
	shown := map[string]int{}
	for i, context := range contexts {
		checks[i] = mapCheck(context)
		shown[checks[i].Name]++
	}
	for i, context := range contexts {
		suite := context.CheckSuite
		if context.Typename == "StatusContext" || shown[checks[i].Name] < 2 || suite == nil || suite.WorkflowRun == nil || suite.WorkflowRun.Event == "" {
			continue
		}
		checks[i].Name += " (" + suite.WorkflowRun.Event + ")"
	}
	return checks
}

// startedBefore reports whether first started strictly before second.
func startedBefore(first, second graphqlCheckContext) bool {
	switch {
	case first.StartedAt == nil:
		return false
	case second.StartedAt == nil:
		return true
	}
	return first.StartedAt.Before(*second.StartedAt)
}

type graphqlReactionGroup struct {
	Content          string
	ViewerHasReacted bool
	Reactors         struct{ TotalCount int }
}

type graphqlIssueComment struct {
	ID             string
	URL            string
	Body           string
	CreatedAt      time.Time
	Author         *graphqlActor
	ReactionGroups []graphqlReactionGroup
}

type graphqlReviewComment struct {
	ID             string
	State          string
	URL            string
	Body           string
	DiffHunk       string
	CreatedAt      time.Time
	Author         *graphqlActor
	ReactionGroups []graphqlReactionGroup

	ViewerCanUpdate bool
	ViewerCanDelete bool
	ViewerDidAuthor bool
}

type graphqlFile struct {
	Path       string
	Additions  int
	Deletions  int
	ChangeType string
	// ViewerViewedState is VIEWED, UNVIEWED or DISMISSED.
	ViewerViewedState string
}

type graphqlReview struct {
	ID          string
	State       string
	Body        string
	SubmittedAt time.Time
	Author      *graphqlActor
}

type graphqlPullRequestDetail struct {
	graphqlPullRequest
	ViewerDidAuthor bool
	ViewerCanUpdate bool
	Body            string
	ReactionGroups  []graphqlReactionGroup
	Additions       int
	Deletions       int
	Labels          struct {
		Nodes []struct{ Name, Color string }
	}

	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer *struct {
				Typename string `json:"__typename"`
				Login    string
				Slug     string
			}
		}
	}
	Files    graphqlConnection[graphqlFile]
	Comments graphqlConnection[graphqlIssueComment]
	Reviews  graphqlConnection[graphqlReview]
}

type graphqlThread struct {
	ID                string
	SubjectType       string
	IsResolved        bool
	IsOutdated        bool
	Path              string
	Line              *int
	OriginalLine      *int
	StartLine         *int
	OriginalStartLine *int
	DiffSide          string
	StartDiffSide     string
	ResolvedBy        *graphqlActor
	Comments          struct{ Nodes []graphqlReviewComment }
}

// graphqlThreadState is what the resolve and unresolve mutations return.
type graphqlThreadState struct {
	ID         string
	IsResolved bool
	ResolvedBy *graphqlActor
}

func (state graphqlThreadState) resolution() github.ThreadResolution {
	resolution := github.ThreadResolution{Identifier: state.ID, IsResolved: state.IsResolved}
	if state.IsResolved {
		resolution.ResolvedBy = state.ResolvedBy.login()
	}
	return resolution
}

func mapPullRequest(node graphqlPullRequest) (github.PullRequest, error) {
	repository, err := github.ParseRepository(node.Repository.NameWithOwner)
	if err != nil {
		return github.PullRequest{}, err
	}
	return github.PullRequest{
		Identifier:     node.ID,
		HeadSHA:        node.HeadRefOid,
		Repository:     repository,
		Number:         node.Number,
		Title:          node.Title,
		Author:         node.Author.login(),
		State:          github.PullRequestState(node.State),
		Draft:          node.IsDraft,
		ReviewDecision: github.ReviewDecision(node.ReviewDecision),
		Checks:         mapChecks(node),
		CommentCount:   node.CommentCount.TotalCount,
		CreatedAt:      node.CreatedAt,
		UpdatedAt:      node.UpdatedAt,
		URL:            node.URL,
		HeadBranch:     node.HeadRefName,
		BaseBranch:     node.BaseRefName,

		CrossRepository: node.IsCrossRepository,
	}, nil
}

// mapChecks reads the head commit's rollup; null means it has no checks.
func mapChecks(node graphqlPullRequest) github.CheckState {
	if node.StatusCheckRollup == nil {
		return github.ChecksNone
	}
	return github.CheckStateFromRollup(node.StatusCheckRollup.State)
}

func mapPullRequests(nodes []graphqlPullRequest) ([]github.PullRequest, error) {
	out := make([]github.PullRequest, 0, len(nodes))
	for _, node := range nodes {
		// Search results can contain nulls for inaccessible items, which decode
		// to zero values.
		if node.Number == 0 {
			continue
		}
		pullRequest, err := mapPullRequest(node)
		if err != nil {
			return nil, err
		}
		out = append(out, pullRequest)
	}
	return out, nil
}

func mapReactions(groups []graphqlReactionGroup) []github.ReactionSummary {
	var out []github.ReactionSummary
	for _, group := range groups {
		if group.Reactors.TotalCount == 0 {
			continue
		}
		out = append(out, github.ReactionSummary{
			Content:       github.ReactionContent(group.Content),
			Count:         group.Reactors.TotalCount,
			ViewerReacted: group.ViewerHasReacted,
		})
	}
	return out
}

func mapIssueComment(node graphqlIssueComment) github.IssueComment {
	return github.IssueComment{
		Identifier: node.ID,
		Author:     node.Author.login(),
		Body:       node.Body,
		CreatedAt:  node.CreatedAt,
		URL:        node.URL,
		Reactions:  mapReactions(node.ReactionGroups),
	}
}

func mapReviewComment(node graphqlReviewComment) github.ReviewComment {
	return github.ReviewComment{
		Identifier: node.ID,
		Author:     node.Author.login(),
		Body:       node.Body,
		CreatedAt:  node.CreatedAt,
		URL:        node.URL,
		DiffHunk:   node.DiffHunk,
		Pending:    node.State == "PENDING",

		ViewerCanUpdate: node.ViewerCanUpdate,
		ViewerCanDelete: node.ViewerCanDelete,
		ViewerDidAuthor: node.ViewerDidAuthor,
		Reactions:       mapReactions(node.ReactionGroups),
	}
}

func mapDetail(node graphqlPullRequestDetail, files []graphqlFile, comments []graphqlIssueComment, reviews []graphqlReview) (github.PullRequestDetail, error) {
	pullRequest, err := mapPullRequest(node.graphqlPullRequest)
	if err != nil {
		return github.PullRequestDetail{}, err
	}
	detail := github.PullRequestDetail{
		PullRequest:     pullRequest,
		ViewerDidAuthor: node.ViewerDidAuthor,
		ViewerCanUpdate: node.ViewerCanUpdate,
		Body:            node.Body,
		Reactions:       mapReactions(node.ReactionGroups),
		Additions:       node.Additions,
		Deletions:       node.Deletions,
	}
	detail.MergeState = github.MergeStateFromStatus(node.MergeStateStatus)
	if node.StatusCheckRollup != nil {
		detail.CheckTotal = node.StatusCheckRollup.Contexts.TotalCount
		var contexts []graphqlCheckContext
		for _, context := range node.StatusCheckRollup.Contexts.Nodes {
			if context.Typename == "" { // a null node, or a type the query did not ask for
				continue
			}
			contexts = append(contexts, context)
		}
		kept := latestRuns(contexts)
		detail.CheckList = mapCheckRuns(kept)
		// GitHub's total counts the superseded runs; the rows shown do not.
		detail.CheckTotal -= len(contexts) - len(kept)
	}
	for _, label := range node.Labels.Nodes {
		detail.Labels = append(detail.Labels, github.Label{Name: label.Name, Color: label.Color})
	}
	for _, record := range node.ReviewRequests.Nodes {
		switch requestedReviewer := record.RequestedReviewer; {
		case requestedReviewer == nil:
		case requestedReviewer.Typename == "Team":
			detail.ReviewRequests = append(detail.ReviewRequests, github.Reviewer{Name: requestedReviewer.Slug, IsTeam: true})
		default:
			detail.ReviewRequests = append(detail.ReviewRequests, github.Reviewer{Name: requestedReviewer.Login})
		}
	}
	for _, file := range files {
		detail.Files = append(detail.Files, github.ChangedFile{
			Path: file.Path, Additions: file.Additions, Deletions: file.Deletions, Change: github.FileChangeType(file.ChangeType), Viewed: github.ViewedState(file.ViewerViewedState),
		})
	}
	for _, graphqlComment := range comments {
		detail.Comments = append(detail.Comments, mapIssueComment(graphqlComment))
	}
	for _, review := range reviews {
		detail.Reviews = append(detail.Reviews, github.Review{
			Identifier: review.ID, Author: review.Author.login(), State: github.ReviewState(review.State), Body: review.Body, SubmittedAt: review.SubmittedAt,
		})
	}
	return detail, nil
}

func integerOrZero(pointer *int) int {
	if pointer == nil {
		return 0
	}
	return *pointer
}

func mapThread(node graphqlThread) github.ReviewThread {
	thread := github.ReviewThread{
		Identifier:        node.ID,
		SubjectType:       github.ThreadSubject(node.SubjectType),
		IsResolved:        node.IsResolved,
		IsOutdated:        node.IsOutdated,
		Path:              node.Path,
		Line:              integerOrZero(node.Line),
		OriginalLine:      integerOrZero(node.OriginalLine),
		StartLine:         integerOrZero(node.StartLine),
		OriginalStartLine: integerOrZero(node.OriginalStartLine),
		DiffSide:          github.DiffSide(node.DiffSide),
		StartDiffSide:     github.DiffSide(node.StartDiffSide),
	}
	if node.ResolvedBy != nil {
		thread.ResolvedBy = node.ResolvedBy.login()
	}
	for _, graphqlComment := range node.Comments.Nodes {
		thread.Comments = append(thread.Comments, mapReviewComment(graphqlComment))
	}
	return thread
}
