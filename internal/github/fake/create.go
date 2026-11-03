package fake

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/MikePors/strigidae/internal/github"
)

// SetBranches sets the branches ListBranches returns for the repository, in
// the given order. A repository with none set answers ErrNotFound.
func (client *Client) SetBranches(repository github.Repository, branches []github.Branch) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.branches[repository] = slices.Clone(branches)
}

// CreatedPullRequests returns the inputs of the successful CreatePullRequest calls.
func (client *Client) CreatedPullRequests() []github.CreatePullRequestInput {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return slices.Clone(client.created)
}

// ListBranches implements github.Client.
func (client *Client) ListBranches(ctx context.Context, repository github.Repository, pageRequest github.PageRequest) (github.Page[github.Branch], error) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if err := client.begin(ctx, Call{Method: MethodListBranches, Repository: repository, Page: pageRequest}); err != nil {
		return github.Page[github.Branch]{}, err
	}
	branches, ok := client.branches[repository]
	if !ok {
		return github.Page[github.Branch]{}, fmt.Errorf("list branches of %s: %w", repository, github.ErrNotFound)
	}
	return paginate(branches, pageRequest)
}

// CreatePullRequest implements github.Client. Like GitHub it refuses an empty
// title and a head equal to the base. The new pull request joins the
// repository's list, numbered after the highest one known.
func (client *Client) CreatePullRequest(ctx context.Context, in github.CreatePullRequestInput) (github.PullRequest, error) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	call := Call{Method: MethodCreatePullRequest, Repository: in.Repository, Title: in.Title, Body: in.Body, Head: in.Head, Base: in.Base, Draft: in.Draft}
	if err := client.begin(ctx, call); err != nil {
		return github.PullRequest{}, err
	}
	switch {
	case strings.TrimSpace(in.Title) == "":
		return github.PullRequest{}, fmt.Errorf("create pull request in %s: empty title", in.Repository)
	case in.Head == in.Base:
		return github.PullRequest{}, fmt.Errorf("create pull request in %s: no commits between %s and %s", in.Repository, in.Base, in.Head)
	}
	number := 0
	for _, existing := range client.lists[in.Repository] {
		number = max(number, existing.Number)
	}
	for key := range client.details {
		if key.repository == in.Repository {
			number = max(number, key.number)
		}
	}
	number++
	now := client.Now()
	pullRequest := github.PullRequest{
		Identifier: fmt.Sprintf("fake-pull-request-%d", number), Repository: in.Repository, Number: number,
		Title: in.Title, Author: client.Viewer, State: github.PullRequestStateOpen, Draft: in.Draft,
		CreatedAt: now, UpdatedAt: now, HeadBranch: in.Head, BaseBranch: in.Base,
	}
	client.lists[in.Repository] = append(slices.Clip(client.lists[in.Repository]), pullRequest)
	if _, ok := client.details[pullRequestKey{in.Repository, number}]; !ok {
		client.details[pullRequestKey{in.Repository, number}] = github.PullRequestDetail{PullRequest: pullRequest, Body: in.Body}
	}
	client.created = append(client.created, in)
	return pullRequest, nil
}
