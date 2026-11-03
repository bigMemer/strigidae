package network

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/github"
)

func mapChecksFromNodes(test *testing.T, nodes string, total int) github.PullRequestDetail {
	test.Helper()
	var node graphqlPullRequestDetail
	body := `{"statusCheckRollup": {"state": "SUCCESS", "contexts": {"totalCount": ` + strconv.Itoa(total) + `, "nodes": [` + nodes + `]}}}`
	if err := json.Unmarshal([]byte(body), &node); err != nil {
		test.Fatal(err)
	}
	node.Repository.NameWithOwner = "MikePors/strigidae"
	detail, err := mapDetail(node, nil, nil, nil)
	if err != nil {
		test.Fatal(err)
	}
	return detail
}

func checkRunNode(workflow, status, conclusion, startedAt, url string) string {
	identifiers := map[string]string{"CI": "1", "Docs": "2"}
	return checkRunIn(workflowSuite(identifiers[workflow], workflow, "push"), status, conclusion, startedAt, url)
}

// workflowSuite is the checkSuite of a run in a workflow.
func workflowSuite(identifier, workflow, event string) string {
	return `{"workflowRun": {"event": "` + event + `", "workflow": {"databaseId": ` + identifier + `, "name": "` + workflow + `"}}}`
}

// appSuite is the checkSuite of a run outside a workflow.
func appSuite(identifier string) string {
	return `{"app": {"databaseId": ` + identifier + `}, "workflowRun": null}`
}

func checkRunIn(suite, status, conclusion, startedAt, url string) string {
	started := "null"
	if startedAt != "" {
		started = `"` + startedAt + `"`
	}
	conclusionValue := "null"
	if conclusion != "" {
		conclusionValue = `"` + conclusion + `"`
	}
	return `{"__typename": "CheckRun", "name": "test", "status": "` + status + `", "conclusion": ` + conclusionValue +
		`, "detailsUrl": "` + url + `", "startedAt": ` + started + `, "checkSuite": ` + suite + `}`
}

func TestMapDetailChecksKeepsLatestRun(test *testing.T) {
	const (
		early = "2026-10-01T10:00:00Z"
		late  = "2026-10-01T10:05:00Z"
	)
	tests := []struct {
		name  string
		nodes []string
		want  []github.Check
	}{
		{
			"cancelled then passing",
			[]string{checkRunNode("CI", "COMPLETED", "CANCELLED", early, "u1"), checkRunNode("CI", "COMPLETED", "SUCCESS", late, "u2")},
			[]github.Check{{Name: "CI / test", Result: github.CheckPassed, URL: "u2"}},
		},
		{
			"newest first in GitHub's order",
			[]string{checkRunNode("CI", "COMPLETED", "SUCCESS", late, "u2"), checkRunNode("CI", "COMPLETED", "CANCELLED", early, "u1")},
			[]github.Check{{Name: "CI / test", Result: github.CheckPassed, URL: "u2"}},
		},
		{
			"queued re-run after cancelled",
			[]string{checkRunNode("CI", "COMPLETED", "CANCELLED", early, "u1"), checkRunNode("CI", "QUEUED", "", "", "u2")},
			[]github.Check{{Name: "CI / test", Result: github.CheckQueued, URL: "u2"}},
		},
		{
			"queued run listed first still wins",
			[]string{checkRunNode("CI", "QUEUED", "", "", "u2"), checkRunNode("CI", "COMPLETED", "CANCELLED", early, "u1")},
			[]github.Check{{Name: "CI / test", Result: github.CheckQueued, URL: "u2"}},
		},
		{
			"tie keeps the later one",
			[]string{checkRunNode("CI", "COMPLETED", "FAILURE", early, "u1"), checkRunNode("CI", "COMPLETED", "SUCCESS", early, "u2")},
			[]github.Check{{Name: "CI / test", Result: github.CheckPassed, URL: "u2"}},
		},
		{
			"lone cancelled run stays",
			[]string{checkRunNode("CI", "COMPLETED", "CANCELLED", early, "u1")},
			[]github.Check{{Name: "CI / test", Result: github.CheckCancelled, URL: "u1"}},
		},
		{
			"same job name in different workflows",
			[]string{checkRunNode("CI", "COMPLETED", "SUCCESS", early, "u1"), checkRunNode("Docs", "COMPLETED", "SUCCESS", late, "u2")},
			[]github.Check{{Name: "CI / test", Result: github.CheckPassed, URL: "u1"}, {Name: "Docs / test", Result: github.CheckPassed, URL: "u2"}},
		},
		{
			"push and pull_request runs of one job both stay and show the event",
			[]string{
				checkRunIn(workflowSuite("1", "CI", "push"), "COMPLETED", "FAILURE", early, "u1"),
				checkRunIn(workflowSuite("1", "CI", "pull_request"), "COMPLETED", "SUCCESS", late, "u2"),
			},
			[]github.Check{
				{Name: "CI / test (push)", Result: github.CheckFailed, URL: "u1"},
				{Name: "CI / test (pull_request)", Result: github.CheckPassed, URL: "u2"},
			},
		},
		{
			"same workflow name from two workflow files both stay",
			[]string{
				checkRunIn(workflowSuite("1", "CI", "push"), "COMPLETED", "SUCCESS", early, "u1"),
				checkRunIn(workflowSuite("9", "CI", "push"), "COMPLETED", "SUCCESS", late, "u2"),
			},
			[]github.Check{
				{Name: "CI / test (push)", Result: github.CheckPassed, URL: "u1"},
				{Name: "CI / test (push)", Result: github.CheckPassed, URL: "u2"},
			},
		},
		{
			"same check name from two apps both stay",
			[]string{
				checkRunIn(appSuite("5"), "COMPLETED", "SUCCESS", early, "u1"),
				checkRunIn(appSuite("6"), "COMPLETED", "FAILURE", late, "u2"),
			},
			[]github.Check{
				{Name: "test", Result: github.CheckPassed, URL: "u1"},
				{Name: "test", Result: github.CheckFailed, URL: "u2"},
			},
		},
		{
			"re-run of an app check collapses",
			[]string{
				checkRunIn(appSuite("5"), "COMPLETED", "CANCELLED", early, "u1"),
				checkRunIn(appSuite("5"), "COMPLETED", "SUCCESS", late, "u2"),
			},
			[]github.Check{{Name: "test", Result: github.CheckPassed, URL: "u2"}},
		},
		{
			"status contexts are untouched and survivors keep their order",
			[]string{
				`{"__typename": "StatusContext", "context": "ci/legacy", "state": "ERROR", "targetUrl": "s1"}`,
				checkRunNode("CI", "COMPLETED", "CANCELLED", early, "u1"),
				`{"__typename": "StatusContext", "context": "ci/legacy", "state": "SUCCESS", "targetUrl": "s2"}`,
				checkRunNode("CI", "COMPLETED", "SUCCESS", late, "u2"),
			},
			[]github.Check{
				{Name: "ci/legacy", Result: github.CheckFailed, URL: "s1"},
				{Name: "ci/legacy", Result: github.CheckPassed, URL: "s2"},
				{Name: "CI / test", Result: github.CheckPassed, URL: "u2"},
			},
		},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			detail := mapChecksFromNodes(test, strings.Join(testCase.nodes, ","), len(testCase.nodes))
			if diff := cmp.Diff(testCase.want, detail.CheckList); diff != "" {
				test.Errorf("checks (-want +got):\n%s", diff)
			}
			if detail.CheckTotal != len(testCase.want) {
				test.Errorf("CheckTotal = %d, want %d", detail.CheckTotal, len(testCase.want))
			}
		})
	}
}
