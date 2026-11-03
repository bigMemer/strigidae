package network

import (
	"context"
	"fmt"

	"github.com/MikePors/strigidae/internal/github"
)

// The operation names double as the fixture keys in tests.
const (
	operationMarkReadyForReview = "MarkReadyForReview"
	operationConvertToDraft     = "ConvertToDraft"
)

const markReadyForReviewMutation = `
mutation ` + operationMarkReadyForReview + `($pullRequestId: ID!) {
  markPullRequestReadyForReview(input: {pullRequestId: $pullRequestId}) {
    pullRequest { id isDraft }
  }
}`

const convertToDraftMutation = `
mutation ` + operationConvertToDraft + `($pullRequestId: ID!) {
  convertPullRequestToDraft(input: {pullRequestId: $pullRequestId}) {
    pullRequest { id isDraft }
  }
}`

// MarkReadyForReview implements github.Client.
func (client *Client) MarkReadyForReview(ctx context.Context, in github.DraftStateInput) error {
	return client.changeDraftState(ctx, markReadyForReviewMutation, "mark ready for review", in)
}

// ConvertToDraft implements github.Client.
func (client *Client) ConvertToDraft(ctx context.Context, in github.DraftStateInput) error {
	return client.changeDraftState(ctx, convertToDraftMutation, "convert to draft", in)
}

func (client *Client) changeDraftState(ctx context.Context, mutation, what string, in github.DraftStateInput) error {
	var response map[string]any
	variables := map[string]any{"pullRequestId": in.PullRequestIdentifier}
	if err := client.query(ctx, mutation, variables, &response); err != nil {
		return fmt.Errorf("%s %s#%d: %w", what, in.Repository, in.Number, err)
	}
	return nil
}
