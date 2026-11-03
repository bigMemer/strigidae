package github

import "strings"

// CheckResult is the outcome of one check on the head commit, normalised from
// GitHub's check run statuses and conclusions and commit status states.
type CheckResult string

// Check results.
const (
	CheckFailed         CheckResult = "FAILED"
	CheckCancelled      CheckResult = "CANCELLED"
	CheckTimedOut       CheckResult = "TIMED_OUT"
	CheckActionRequired CheckResult = "ACTION_REQUIRED"
	CheckRunning        CheckResult = "RUNNING"
	CheckQueued         CheckResult = "QUEUED"
	CheckPassed         CheckResult = "PASSED"
	CheckSkipped        CheckResult = "SKIPPED"
	CheckNeutral        CheckResult = "NEUTRAL"
)

// Check is one check run or commit status of the head commit.
type Check struct {
	// Name is "workflow / job" for a GitHub Actions job, the check run's name
	// for other apps, and the context for a commit status.
	Name   string
	Result CheckResult
	// URL is the check's details page; empty if GitHub gave none.
	URL string
}

// CheckGroup orders checks by how much they need attention.
type CheckGroup int

// Check groups, most urgent first.
const (
	CheckGroupFailing CheckGroup = iota
	CheckGroupPending
	CheckGroupPassing
	CheckGroupInactive
)

// Group returns the group the result sorts into: failing, then running or
// queued, then passing, then skipped, neutral or cancelled.
func (result CheckResult) Group() CheckGroup {
	switch result {
	case CheckFailed, CheckTimedOut, CheckActionRequired:
		return CheckGroupFailing
	case CheckRunning, CheckQueued:
		return CheckGroupPending
	case CheckPassed:
		return CheckGroupPassing
	case CheckSkipped, CheckNeutral, CheckCancelled:
		return CheckGroupInactive
	}
	return CheckGroupInactive
}

// Word is the result in words for display.
func (result CheckResult) Word() string {
	switch result {
	case CheckFailed:
		return "failed"
	case CheckCancelled:
		return "cancelled"
	case CheckTimedOut:
		return "timed out"
	case CheckActionRequired:
		return "action required"
	case CheckRunning:
		return "running"
	case CheckQueued:
		return "queued"
	case CheckPassed:
		return "passed"
	case CheckSkipped:
		return "skipped"
	case CheckNeutral:
		return "neutral"
	}
	return strings.ToLower(string(result))
}

// CheckResultFromRun maps a check run's GraphQL status (QUEUED, IN_PROGRESS,
// COMPLETED, WAITING, PENDING, REQUESTED) and conclusion (SUCCESS, FAILURE,
// …) to a CheckResult. An unknown completed conclusion counts as neutral.
func CheckResultFromRun(status, conclusion string) CheckResult {
	switch status {
	case "COMPLETED":
	case "IN_PROGRESS":
		return CheckRunning
	default:
		return CheckQueued
	}
	switch conclusion {
	case "SUCCESS":
		return CheckPassed
	case "FAILURE", "STARTUP_FAILURE":
		return CheckFailed
	case "CANCELLED":
		return CheckCancelled
	case "TIMED_OUT":
		return CheckTimedOut
	case "ACTION_REQUIRED":
		return CheckActionRequired
	case "SKIPPED":
		return CheckSkipped
	}
	return CheckNeutral
}

// CheckResultFromStatus maps a commit status state (SUCCESS, FAILURE, ERROR,
// PENDING, EXPECTED) to a CheckResult.
func CheckResultFromStatus(state string) CheckResult {
	switch state {
	case "SUCCESS":
		return CheckPassed
	case "FAILURE", "ERROR":
		return CheckFailed
	case "PENDING":
		return CheckRunning
	case "EXPECTED":
		return CheckQueued
	}
	return CheckNeutral
}

// MergeState is GitHub's merge state status of a pull request. Currently only
// the three states that stand in the way of a merge are told apart.
type MergeState string

// Merge states. Everything else (clean, unstable, has hooks, draft, not yet
// computed) is MergeStateOther.
const (
	MergeStateOther     MergeState = ""
	MergeStateConflicts MergeState = "DIRTY"
	MergeStateBehind    MergeState = "BEHIND"
	MergeStateBlocked   MergeState = "BLOCKED"
)

// MergeStateFromStatus maps a GraphQL MergeStateStatus to a MergeState.
func MergeStateFromStatus(status string) MergeState {
	switch state := MergeState(status); state {
	case MergeStateConflicts, MergeStateBehind, MergeStateBlocked:
		return state
	case MergeStateOther:
	}
	return MergeStateOther
}
