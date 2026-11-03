package fake

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/github"
)

func TestListBranches(test *testing.T) {
	repository := github.Repository{Owner: "acme", Name: "widgets"}
	client := New()
	branches := []github.Branch{{Name: "main", Default: true}, {Name: "topic"}}
	client.SetBranches(repository, branches)
	page, err := client.ListBranches(context.Background(), repository, github.PageRequest{})
	if err != nil {
		test.Fatal(err)
	}
	if diff := cmp.Diff(branches, page.Items); diff != "" {
		test.Errorf("branches (-want +got):\n%s", diff)
	}
	_, err = client.ListBranches(context.Background(), github.Repository{Owner: "x", Name: "y"}, github.PageRequest{})
	if !errors.Is(err, github.ErrNotFound) {
		test.Errorf("unknown repository: err = %v, want ErrNotFound", err)
	}
}

func TestCreatePullRequest(test *testing.T) {
	repository := github.Repository{Owner: "acme", Name: "widgets"}
	for _, testCase := range []struct {
		name    string
		input   github.CreatePullRequestInput
		wantErr bool
	}{
		{"creates a draft", github.CreatePullRequestInput{Repository: repository, Head: "topic", Base: "main", Title: "Add", Body: "Text", Draft: true}, false},
		{"empty title", github.CreatePullRequestInput{Repository: repository, Head: "topic", Base: "main"}, true},
		{"same branch", github.CreatePullRequestInput{Repository: repository, Head: "main", Base: "main", Title: "Add"}, true},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			client := New()
			created, err := client.CreatePullRequest(context.Background(), testCase.input)
			if (err != nil) != testCase.wantErr {
				test.Fatalf("err = %v, want error %v", err, testCase.wantErr)
			}
			if testCase.wantErr {
				if len(client.CreatedPullRequests()) != 0 {
					test.Error("a refused call was recorded as created")
				}
				return
			}
			if created.Number != 1 || !created.Draft || created.HeadBranch != "topic" {
				test.Errorf("created = %+v", created)
			}
			if diff := cmp.Diff([]github.CreatePullRequestInput{testCase.input}, client.CreatedPullRequests()); diff != "" {
				test.Errorf("created inputs (-want +got):\n%s", diff)
			}
			listed, err := client.ListPullRequests(context.Background(), repository, github.PageRequest{})
			if err != nil || len(listed.Items) != 1 {
				test.Errorf("list = %v, %v; want the new pull request", listed.Items, err)
			}
		})
	}
}
