package network

import (
	"context"
	"fmt"

	"github.com/MikePors/strigidae/internal/github"
)

// Operation names for the branch and creation calls; they double as fixture
// keys in tests.
const (
	operationListBranches         = "ListBranches"
	operationRepositoryIdentifier = "RepositoryIdentifier"
	operationCreatePullRequest    = "CreatePullRequest"
)

const listBranchesQuery = `
query ` + operationListBranches + `($owner: String!, $name: String!, $first: Int!, $after: String) {
  repository(owner: $owner, name: $name) {
    defaultBranchRef { name }
    refs(refPrefix: "refs/heads/", first: $first, after: $after, orderBy: {field: ALPHABETICAL, direction: ASC}) {
      totalCount
      pageInfo { hasNextPage endCursor }
      nodes { name }
    }
  }
}`

const repositoryIdentifierQuery = `
query ` + operationRepositoryIdentifier + `($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) { id }
}`

const createPullRequestMutation = `
mutation ` + operationCreatePullRequest + `($repositoryId: ID!, $head: String!, $base: String!, $title: String!, $body: String!, $draft: Boolean!) {
  createPullRequest(input: {repositoryId: $repositoryId, headRefName: $head, baseRefName: $base, title: $title, body: $body, draft: $draft}) {
    pullRequest { ...PullRequestFields }
  }
}` + pullRequestFields

// ListBranches implements github.Client.
func (client *Client) ListBranches(ctx context.Context, repository github.Repository, pageRequest github.PageRequest) (github.Page[github.Branch], error) {
	var response struct {
		Repository *struct {
			DefaultBranchRef *struct{ Name string }
			Refs             graphqlConnection[struct{ Name string }]
		}
	}
	variables := map[string]any{"owner": repository.Owner, "name": repository.Name, "first": pageRequest.Size(), "after": afterCursor(pageRequest)}
	if err := client.query(ctx, listBranchesQuery, variables, &response); err != nil {
		return github.Page[github.Branch]{}, fmt.Errorf("list branches of %s: %w", repository, err)
	}
	if response.Repository == nil {
		return github.Page[github.Branch]{}, fmt.Errorf("list branches of %s: %w", repository, github.ErrNotFound)
	}
	defaultName := ""
	if response.Repository.DefaultBranchRef != nil {
		defaultName = response.Repository.DefaultBranchRef.Name
	}
	connection := response.Repository.Refs
	items := make([]github.Branch, 0, len(connection.Nodes))
	for _, node := range connection.Nodes {
		items = append(items, github.Branch{Name: node.Name, Default: node.Name == defaultName})
	}
	return github.Page[github.Branch]{Items: items, Next: connection.PageInfo.next(), Total: connection.TotalCount}, nil
}

// CreatePullRequest implements github.Client. The mutation needs the
// repository's node ID, which is looked up first.
func (client *Client) CreatePullRequest(ctx context.Context, in github.CreatePullRequestInput) (github.PullRequest, error) {
	var lookup struct {
		Repository *struct{ ID string }
	}
	identifierVariables := map[string]any{"owner": in.Repository.Owner, "name": in.Repository.Name}
	if err := client.query(ctx, repositoryIdentifierQuery, identifierVariables, &lookup); err != nil {
		return github.PullRequest{}, fmt.Errorf("create pull request in %s: %w", in.Repository, err)
	}
	if lookup.Repository == nil {
		return github.PullRequest{}, fmt.Errorf("create pull request in %s: %w", in.Repository, github.ErrNotFound)
	}
	var response struct {
		CreatePullRequest *struct {
			PullRequest graphqlPullRequest
		}
	}
	variables := map[string]any{
		"repositoryId": lookup.Repository.ID, "head": in.Head, "base": in.Base,
		"title": in.Title, "body": in.Body, "draft": in.Draft,
	}
	if err := client.query(ctx, createPullRequestMutation, variables, &response); err != nil {
		return github.PullRequest{}, fmt.Errorf("create pull request in %s: %w", in.Repository, err)
	}
	if response.CreatePullRequest == nil {
		return github.PullRequest{}, fmt.Errorf("create pull request in %s: empty response", in.Repository)
	}
	pullRequest, err := mapPullRequest(response.CreatePullRequest.PullRequest)
	if err != nil {
		return github.PullRequest{}, fmt.Errorf("create pull request in %s: %w", in.Repository, err)
	}
	return pullRequest, nil
}
