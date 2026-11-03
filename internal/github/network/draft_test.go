package network

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/github"
)

func TestDraftStateMutations(test *testing.T) {
	tests := []struct {
		name      string
		call      func(*Client, context.Context, github.DraftStateInput) error
		operation string
		field     string
	}{
		{"ready", (*Client).MarkReadyForReview, operationMarkReadyForReview, "markPullRequestReadyForReview"},
		{"draft", (*Client).ConvertToDraft, operationConvertToDraft, "convertPullRequestToDraft"},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			testServer := &server{}
			client := newTestClient(test, testServer)
			in := github.DraftStateInput{Repository: repository, Number: 27, PullRequestIdentifier: "PR_kwDOExample01"}
			if err := testCase.call(client, context.Background(), in); err != nil {
				test.Fatal(err)
			}
			call := testServer.Calls()[0]
			if call.Operation != testCase.operation {
				test.Fatalf("operation = %q, want %q", call.Operation, testCase.operation)
			}
			if !strings.Contains(call.Query, testCase.field+"(input: {pullRequestId: $pullRequestId})") {
				test.Errorf("query does not call %s:\n%s", testCase.field, call.Query)
			}
			if diff := cmp.Diff(map[string]any{"pullRequestId": "PR_kwDOExample01"}, call.Variables); diff != "" {
				test.Errorf("variables (-want +got):\n%s", diff)
			}
		})
	}
}
