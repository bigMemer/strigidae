package fake

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/MikePors/strigidae/internal/github"
)

// DraftChange is a recorded successful MarkReadyForReview or ConvertToDraft.
type DraftChange struct {
	Method                string
	Repository            github.Repository
	Number                int
	PullRequestIdentifier string
}

// DraftChanges returns the successful draft-state calls, in order.
func (client *Client) DraftChanges() []DraftChange {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return slices.Clone(client.draftChanges)
}

// MarkReadyForReview implements github.Client. Like GitHub it refuses a wrong
// pull request ID, a viewer who may not update the pull request, and a pull
// request that is not a draft.
func (client *Client) MarkReadyForReview(ctx context.Context, in github.DraftStateInput) error {
	return client.changeDraft(ctx, MethodMarkReadyForReview, false, in)
}

// ConvertToDraft implements github.Client. Like GitHub it refuses a wrong pull
// request ID, a viewer who may not update the pull request, and a pull request
// that already is a draft.
func (client *Client) ConvertToDraft(ctx context.Context, in github.DraftStateInput) error {
	return client.changeDraft(ctx, MethodConvertToDraft, true, in)
}

// changeDraft sets the draft flag to becomesDraft.
func (client *Client) changeDraft(ctx context.Context, method string, becomesDraft bool, in github.DraftStateInput) error {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if err := client.begin(ctx, Call{Method: method, Repository: in.Repository, Number: in.Number, PullRequestIdentifier: in.PullRequestIdentifier}); err != nil {
		return err
	}
	key := pullRequestKey{in.Repository, in.Number}
	detail, ok := client.details[key]
	switch {
	case !ok:
		return fmt.Errorf("%s %s#%d: %w", method, in.Repository, in.Number, github.ErrNotFound)
	case in.PullRequestIdentifier == "" || in.PullRequestIdentifier != detail.Identifier:
		return fmt.Errorf("%s %s#%d: unknown pull request ID %q", method, in.Repository, in.Number, in.PullRequestIdentifier)
	case !detail.ViewerCanUpdate:
		return fmt.Errorf("%s %s#%d: %w", method, in.Repository, in.Number, github.ErrForbidden)
	case detail.State == github.PullRequestStateClosed || detail.State == github.PullRequestStateMerged:
		return fmt.Errorf("%s %s#%d: the pull request is %s", method, in.Repository, in.Number, strings.ToLower(string(detail.State)))
	case detail.Draft == becomesDraft:
		return fmt.Errorf("%s %s#%d: the pull request is already in that state", method, in.Repository, in.Number)
	}
	detail.Draft = becomesDraft
	client.details[key] = detail
	client.draftChanges = append(client.draftChanges, DraftChange{Method: method, Repository: in.Repository, Number: in.Number, PullRequestIdentifier: in.PullRequestIdentifier})
	return nil
}
