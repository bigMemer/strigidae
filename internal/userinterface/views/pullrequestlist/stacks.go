package pullrequestlist

import "github.com/MikePors/strigidae/internal/github"

// stackOrder arranges pullRequests, in their loaded order, so that each pull
// request is followed by the ones stacked on it, and reports how deep each row
// is in its stack (0 for a top-level row).
//
// A pull request is stacked on another when its base branch is the other's
// head branch, both are open, and the other's head branch lives in this
// repository: a fork's branch may share a name with one here. Pull requests
// whose parent is not in the list stay top-level. When two candidates share a
// head branch name the first in order is the parent. Every pull request
// appears exactly once, even when bases and heads point at each other.
func stackOrder(pullRequests []github.PullRequest) (ordered []github.PullRequest, depths []int) {
	parents := stackParents(pullRequests)
	children := make([][]int, len(pullRequests))
	for i, parent := range parents {
		if parent >= 0 {
			children[parent] = append(children[parent], i)
		}
	}
	onCycle := cycleRows(parents)

	ordered = make([]github.PullRequest, 0, len(pullRequests))
	depths = make([]int, 0, len(pullRequests))
	visited := make([]bool, len(pullRequests))
	var place func(index, depth int)
	place = func(index, depth int) {
		visited[index] = true
		ordered = append(ordered, pullRequests[index])
		depths = append(depths, depth)
		for _, child := range children[index] {
			if !visited[child] {
				place(child, depth+1)
			}
		}
	}
	// A row is top-level when it has no parent, or when it is on a cycle and
	// no other row of the cycle has been placed yet: the cycle opens at its
	// first row in order, and the rest of it, with whatever hangs off it, is
	// drawn below.
	for i := range pullRequests {
		if !visited[i] && (parents[i] < 0 || onCycle[i]) {
			place(i, 0)
		}
	}
	return ordered, depths
}

// stackParents is, for every pull request, the index of the one it is stacked
// on, or -1. It is built from one map of head branches, so it takes linear
// time.
func stackParents(pullRequests []github.PullRequest) []int {
	heads := make(map[string][]int) // open, non-fork pull requests by head branch, in order
	for i, pullRequest := range pullRequests {
		if pullRequest.State == github.PullRequestStateOpen && !pullRequest.CrossRepository {
			heads[pullRequest.HeadBranch] = append(heads[pullRequest.HeadBranch], i)
		}
	}
	parents := make([]int, len(pullRequests))
	for i, child := range pullRequests {
		parents[i] = -1
		if child.State != github.PullRequestStateOpen || child.BaseBranch == "" {
			continue
		}
		for _, candidate := range heads[child.BaseBranch] {
			if candidate != i { // the first, and at most one is the row itself
				parents[i] = candidate
				break
			}
		}
	}
	return parents
}

// cycleRows marks the rows that are on a cycle of parents, in linear time.
func cycleRows(parents []int) []bool {
	const (
		unseen = iota
		walking
		done
	)
	state := make([]int, len(parents))
	onCycle := make([]bool, len(parents))
	for start := range parents {
		var path []int
		row := start
		for row >= 0 && state[row] == unseen {
			state[row] = walking
			path = append(path, row)
			row = parents[row]
		}
		if row >= 0 && state[row] == walking { // came back to this walk's own path
			for member := row; ; member = parents[member] {
				onCycle[member] = true
				if parents[member] == row {
					break
				}
			}
		}
		for _, visitedRow := range path {
			state[visitedRow] = done
		}
	}
	return onCycle
}
