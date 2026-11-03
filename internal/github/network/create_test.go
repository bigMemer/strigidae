package network

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/github"
)

func TestListBranches(test *testing.T) {
	testServer := &server{}
	client := newTestClient(test, testServer)
	page, err := client.ListBranches(context.Background(), repository, github.PageRequest{Limit: 3})
	if err != nil {
		test.Fatal(err)
	}
	want := github.Page[github.Branch]{
		Items: []github.Branch{{Name: "chore/3-templates"}, {Name: "feat/4-github-client"}, {Name: "main", Default: true}},
		Next:  "Y3Vyc29yOnYyOpHOAAAAAw==", Total: 3,
	}
	if diff := cmp.Diff(want, page); diff != "" {
		test.Errorf("page (-want +got):\n%s", diff)
	}
	variables := testServer.Calls()[0].Variables
	if variables["first"] != float64(3) || variables["after"] != nil {
		test.Errorf("variables = %v, want first 3 and no cursor", variables)
	}
}

func TestListBranchesNotFound(test *testing.T) {
	testServer := &server{override: func(_ call, writer http.ResponseWriter) bool {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":{"repository":null}}`))
		return true
	}}
	_, err := newTestClient(test, testServer).ListBranches(context.Background(), repository, github.PageRequest{})
	if !errors.Is(err, github.ErrNotFound) {
		test.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestCreatePullRequest(test *testing.T) {
	testServer := &server{}
	client := newTestClient(test, testServer)
	in := github.CreatePullRequestInput{Repository: repository, Head: "feat/new-thing", Base: "main", Title: "feat: new thing", Body: "Text", Draft: true}
	got, err := client.CreatePullRequest(context.Background(), in)
	if err != nil {
		test.Fatal(err)
	}
	if got.Number != 99 || got.Identifier != "PR_kwDOExample99" || !got.Draft || got.HeadBranch != "feat/new-thing" {
		test.Errorf("pull request = %+v", got)
	}
	calls := testServer.Calls()
	if len(calls) != 2 || calls[0].Operation != operationRepositoryIdentifier || calls[1].Operation != operationCreatePullRequest {
		test.Fatalf("calls = %+v, want the repository lookup then the mutation", calls)
	}
	want := map[string]any{"repositoryId": "R_kgDOExample", "head": "feat/new-thing", "base": "main", "title": "feat: new thing", "body": "Text", "draft": true}
	if diff := cmp.Diff(want, calls[1].Variables); diff != "" {
		test.Errorf("variables (-want +got):\n%s", diff)
	}
}

func TestCreatePullRequestRefused(test *testing.T) {
	testServer := &server{override: func(recordedCall call, writer http.ResponseWriter) bool {
		if recordedCall.Operation != operationCreatePullRequest {
			return false
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":{"createPullRequest":null},"errors":[{"message":"No commits between main and feat/x"}]}`))
		return true
	}}
	_, err := newTestClient(test, testServer).CreatePullRequest(context.Background(), github.CreatePullRequestInput{Repository: repository, Head: "feat/x", Base: "main", Title: "t"})
	if err == nil || !strings.Contains(err.Error(), "No commits between") {
		test.Errorf("err = %v, want GitHub's message", err)
	}
}
