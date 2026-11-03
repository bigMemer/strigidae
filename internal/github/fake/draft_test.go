package fake

import (
	"context"
	"errors"
	"testing"

	"github.com/MikePors/strigidae/internal/github"
)

func TestDraftStateCallsRefuseClosedAndMergedPullRequests(test *testing.T) {
	repository := github.Repository{Owner: "acme", Name: "widgets"}
	for _, state := range []github.PullRequestState{github.PullRequestStateClosed, github.PullRequestStateMerged} {
		test.Run(string(state), func(test *testing.T) {
			client := New()
			client.SetPullRequest(github.PullRequestDetail{
				PullRequest:     github.PullRequest{Identifier: "PR_1", Repository: repository, Number: 1, State: state, Draft: true},
				ViewerCanUpdate: true,
			})
			input := github.DraftStateInput{Repository: repository, Number: 1, PullRequestIdentifier: "PR_1"}
			if err := client.MarkReadyForReview(context.Background(), input); err == nil {
				test.Error("ready was accepted")
			}
			if err := client.ConvertToDraft(context.Background(), input); err == nil {
				test.Error("draft was accepted")
			}
			if len(client.DraftChanges()) != 0 {
				test.Error("a change was recorded")
			}
		})
	}
}

func TestDraftStateCalls(test *testing.T) {
	repository := github.Repository{Owner: "acme", Name: "widgets"}
	tests := []struct {
		name       string
		startDraft bool
		canUpdate  bool
		identifier string
		call       func(*Client, context.Context, github.DraftStateInput) error
		wantErr    error
		wantAnyErr bool
		wantDraft  bool
	}{
		{"ready", true, true, "PR_1", (*Client).MarkReadyForReview, nil, false, false},
		{"draft", false, true, "PR_1", (*Client).ConvertToDraft, nil, false, true},
		{"ready on a ready pull request", false, true, "PR_1", (*Client).MarkReadyForReview, nil, true, false},
		{"draft on a draft", true, true, "PR_1", (*Client).ConvertToDraft, nil, true, true},
		{"forbidden", true, false, "PR_1", (*Client).MarkReadyForReview, github.ErrForbidden, false, true},
		{"wrong identifier", true, true, "PR_other", (*Client).MarkReadyForReview, nil, true, true},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			client := New()
			client.SetPullRequest(github.PullRequestDetail{
				PullRequest:     github.PullRequest{Identifier: "PR_1", Repository: repository, Number: 1, Draft: testCase.startDraft},
				ViewerCanUpdate: testCase.canUpdate,
			})
			err := testCase.call(client, context.Background(), github.DraftStateInput{Repository: repository, Number: 1, PullRequestIdentifier: testCase.identifier})
			switch {
			case testCase.wantErr != nil && !errors.Is(err, testCase.wantErr):
				test.Errorf("error = %v, want %v", err, testCase.wantErr)
			case testCase.wantAnyErr && err == nil:
				test.Error("no error")
			case testCase.wantErr == nil && !testCase.wantAnyErr && err != nil:
				test.Errorf("error = %v", err)
			}
			detail, getErr := client.GetPullRequest(context.Background(), repository, 1)
			if getErr != nil {
				test.Fatal(getErr)
			}
			if detail.Draft != testCase.wantDraft {
				test.Errorf("draft = %v, want %v", detail.Draft, testCase.wantDraft)
			}
			if failed := err != nil; failed == (len(client.DraftChanges()) == 1) {
				test.Errorf("recorded %d changes for error %v", len(client.DraftChanges()), err)
			}
		})
	}
}
