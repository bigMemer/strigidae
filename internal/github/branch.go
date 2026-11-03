package github

// Branch is one branch of a repository.
type Branch struct {
	Name string
	// Default is true for the repository's default branch.
	Default bool
}

// CreatePullRequestInput describes a new pull request. Head and Base are
// branch names in Repository (heads from forks are not supported).
type CreatePullRequestInput struct {
	Repository Repository
	Head       string
	Base       string
	Title      string
	Body       string
	// Draft creates the pull request as a draft.
	Draft bool
}
