package analysis

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/github"
)

func readValid(test *testing.T) []byte {
	test.Helper()
	data, err := os.ReadFile("testdata/valid.json")
	if err != nil {
		test.Fatal(err)
	}
	return data
}

func TestParseValid(test *testing.T) {
	got, err := Parse(readValid(test))
	if err != nil {
		test.Fatal(err)
	}
	want := Analysis{
		SchemaVersion: SchemaVersion, GeneratedAt: "2026-10-30T18:00:00Z", Generator: "test",
		PullRequest: PullRequest{
			Repository: "acme/widgets", Number: 7, Title: "Retry sync on timeout",
			HeadSHA:   "1111111111111111111111111111111111111111",
			BaseSHA:   "2222222222222222222222222222222222222222",
			MergeBase: "3333333333333333333333333333333333333333",
		},
		Spans: map[string]Span{
			"retry-loop": {Path: "internal/sync/client.go", Side: github.SideRight, Start: 10, End: 24, Label: "retry loop"},
			"old-call":   {Path: "internal/sync/client.go", Side: github.SideLeft, Start: 12, End: 13},
			"caller":     {Path: "cmd/sync/main.go", Side: github.SideRight, Start: 40, End: 52, Label: "unchanged caller", ContextOnly: true},
			"retry-test": {Path: "internal/sync/client_test.go", Side: github.SideRight, Start: 1, End: 30},
			"readme":     {Path: "README.md", Side: github.SideRight, Start: 5, End: 9},
		},
		Edges: []Edge{
			{From: "caller", To: "retry-loop", Kind: "calls"},
			{From: "retry-test", To: "retry-loop", Kind: "tests", Note: "covers the timeout path only"},
			{From: "retry-loop", To: "old-call", Kind: "supersedes"},
		},
		Lenses: []Lens{
			{
				Identifier: "flow", Title: "General Flow", Purpose: "Follow the call path", Order: []string{"entry", "retry"},
				Segments: []Segment{
					{
						Identifier: "retry", Title: "The retry loop", Risk: RiskHigh, Annotation: "Check the backoff is bounded.",
						Spans: []string{"retry-loop", "old-call"}, Notes: map[string]string{"retry-loop": "Sleeps between attempts."},
						Order: []string{"old-call", "retry-loop"},
					},
					{Identifier: "entry", Title: "Where it is called", Risk: RiskLow, Spans: []string{"caller"}},
				},
			},
			{
				Identifier: "claims", Title: "Claim Justifications",
				Segments: []Segment{{
					Identifier: "retries", Title: "Claim: sync retries on timeout", Risk: RiskMedium,
					Spans: []string{"retry-loop", "retry-test", "readme"},
					Notes: map[string]string{"retry-loop": "Here under a different lens: the claim itself.", "readme": "Documents it."},
				}},
			},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		test.Errorf("analysis (-want +got):\n%s", diff)
	}
}

// document is the valid fixture as plain JSON values, for a test case to
// break one thing in.
type document = map[string]any

func object(value any) document { return value.(document) }

func list(value any) []any { return value.([]any) }

func firstLens(analysis document) document { return object(list(analysis["lenses"])[0]) }

func firstSegment(analysis document) document {
	return object(list(firstLens(analysis)["segments"])[0])
}

func span(analysis document, identifier string) document {
	return object(object(analysis["hunks"])[identifier])
}

func TestParseRefusesStructuralProblems(test *testing.T) {
	for _, testCase := range []struct {
		name   string
		damage func(analysis document)
		want   string
	}{
		{"other schema version", func(analysis document) { analysis["schema_version"] = "prreview/analysis@2" }, `schema_version: "prreview/analysis@2", want "prreview/analysis@1"`},
		{"no repository", func(analysis document) { delete(object(analysis["pr"]), "repo") }, "pr.repo: required"},
		{"no number", func(analysis document) { delete(object(analysis["pr"]), "number") }, "pr.number: required, at least 1"},
		{"no head", func(analysis document) { delete(object(analysis["pr"]), "head_sha") }, "pr.head_sha: required"},
		{"no merge base", func(analysis document) { delete(object(analysis["pr"]), "merge_base") }, "pr.merge_base: required"},
		{"no spans", func(analysis document) { delete(analysis, "hunks") }, "hunks: required"},
		{"bad span identifier", func(analysis document) { object(analysis["hunks"])["-x"] = span(analysis, "readme") }, `hunks["-x"]: "-x" is not a valid id`},
		{"span without path", func(analysis document) { delete(span(analysis, "readme"), "path") }, `hunks["readme"].path: required`},
		{"bad side", func(analysis document) { span(analysis, "readme")["side"] = "BOTH" }, `hunks["readme"].side: "BOTH", want RIGHT or LEFT`},
		{"start zero", func(analysis document) { span(analysis, "retry-test")["start"] = 0 }, `hunks["retry-test"].start: required, at least 1`},
		{"end before start", func(analysis document) { span(analysis, "readme")["end"] = 4 }, `hunks["readme"].end: 4 is before start 5`},
		{"edge to nowhere", func(analysis document) { object(list(analysis["edges"])[1])["to"] = "gone" }, `edges[1].to: no hunk "gone"`},
		{"edge from nowhere", func(analysis document) { object(list(analysis["edges"])[0])["from"] = "gone" }, `edges[0].from: no hunk "gone"`},
		{"edge without kind", func(analysis document) { delete(object(list(analysis["edges"])[2]), "kind") }, "edges[2].kind: required"},
		{"no lenses", func(analysis document) { analysis["lenses"] = []any{} }, "lenses: at least one lens is required"},
		{"bad lens identifier", func(analysis document) { firstLens(analysis)["id"] = "a b" }, `lenses[0].id: "a b" is not a valid id`},
		{"lens identifier twice", func(analysis document) { object(list(analysis["lenses"])[1])["id"] = "flow" }, `lenses[1].id: "flow" is used by another lens`},
		{"lens without title", func(analysis document) { delete(firstLens(analysis), "title") }, "lenses[0].title: required"},
		{"lens without segments", func(analysis document) {
			lens := object(list(analysis["lenses"])[1])
			lens["segments"] = []any{}
		}, "lenses[1].segments: at least one segment is required"},
		{"lens order names another lens's segment", func(analysis document) { firstLens(analysis)["order"] = []any{"retries"} }, `lenses[0].order[0]: no segment in this lens "retries"`},
		{"lens order twice", func(analysis document) { firstLens(analysis)["order"] = []any{"retry", "retry"} }, `lenses[0].order[1]: "retry" is listed twice`},
		{"segment identifier twice", func(analysis document) {
			object(list(firstLens(analysis)["segments"])[1])["id"] = "retry"
		}, `lenses[0].segments[1].id: "retry" is used by another segment of this lens`},
		{"segment without title", func(analysis document) { delete(firstSegment(analysis), "title") }, "lenses[0].segments[0].title: required"},
		{"bad risk", func(analysis document) { firstSegment(analysis)["risk"] = "huge" }, `lenses[0].segments[0].risk: "huge", want high, medium or low`},
		{"segment without spans", func(analysis document) {
			segment := firstSegment(analysis)
			segment["hunks"], segment["order"], segment["notes"] = []any{}, []any{}, document{}
		}, "lenses[0].segments[0].hunks: at least one hunk is required"},
		{"segment names a missing span", func(analysis document) {
			firstSegment(analysis)["hunks"] = []any{"retry-loop", "old-call", "gone"}
		}, `lenses[0].segments[0].hunks[2]: no hunk "gone"`},
		{"segment names a span twice", func(analysis document) {
			firstSegment(analysis)["hunks"] = []any{"retry-loop", "old-call", "old-call"}
		}, `lenses[0].segments[0].hunks[2]: "old-call" is listed twice`},
		{"segment order names a span of another segment", func(analysis document) {
			firstSegment(analysis)["order"] = []any{"caller"}
		}, `lenses[0].segments[0].order[0]: no hunk in this segment "caller"`},
		{"note on a span of another segment", func(analysis document) {
			object(firstSegment(analysis)["notes"])["readme"] = "x"
		}, `lenses[0].segments[0].notes["readme"]: no hunk "readme" in this segment`},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			var analysis document
			if err := json.Unmarshal(readValid(test), &analysis); err != nil {
				test.Fatal(err)
			}
			testCase.damage(analysis)
			data, err := json.Marshal(analysis)
			if err != nil {
				test.Fatal(err)
			}
			_, err = Parse(data)
			var analysisError *Error
			if !errors.As(err, &analysisError) {
				test.Fatalf("error = %v, want an *Error", err)
			}
			// The first problem is the broken thing; any after it follow from
			// it (a missing span fails every reference to it too).
			if got := analysisError.Problems[0]; !strings.HasPrefix(got, testCase.want) {
				test.Errorf("first problem = %q, want %q (all: %q)", got, testCase.want, analysisError.Problems)
			}
		})
	}
}

func TestParseRefusesWhatIsNotAnAnalysis(test *testing.T) {
	valid := string(readValid(test))
	for _, testCase := range []struct {
		name string
		data string
		want string
	}{
		{"not JSON", "nope", "read analysis"},
		{"unknown field", strings.Replace(valid, `"generator"`, `"generated_by": "x", "generator"`, 1), `unknown field "generated_by"`},
		{"unknown field in a span", strings.Replace(valid, `"label": "retry loop"`, `"lable": "retry loop"`, 1), `unknown field "lable"`},
		{"wrong type", strings.Replace(valid, `"number": 7`, `"number": "7"`, 1), "read analysis"},
		{"two values", valid + "{}", "more than one JSON value"},
		{"too large", strings.Repeat(" ", MaximumFileSize+1), "too large"},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			_, err := Parse([]byte(testCase.data))
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				test.Errorf("error = %v, want one containing %q", err, testCase.want)
			}
		})
	}
}

func TestParseReportsEveryProblemUpToACap(test *testing.T) {
	spans := map[string]any{}
	for i := range 30 {
		spans[fmt.Sprintf("span%02d", i)] = map[string]any{"path": "a.go", "side": "UP", "start": 1, "end": 1}
	}
	data, err := json.Marshal(map[string]any{
		"schema_version": SchemaVersion,
		"pr":             map[string]any{"repo": "o/r", "number": 1, "head_sha": "a", "merge_base": "b"},
		"hunks":          spans,
		"lenses":         []any{map[string]any{"id": "l", "title": "L", "segments": []any{map[string]any{"id": "s", "title": "S", "hunks": []any{"span00"}}}}},
	})
	if err != nil {
		test.Fatal(err)
	}
	_, err = Parse(data)
	var analysisError *Error
	if !errors.As(err, &analysisError) {
		test.Fatalf("error = %v, want an *Error", err)
	}
	if len(analysisError.Problems) != maximumProblems {
		test.Errorf("%d problems, want %d", len(analysisError.Problems), maximumProblems)
	}
	if !strings.HasPrefix(analysisError.Problems[0], `hunks["span00"].side`) {
		test.Errorf("first problem = %q, want span00's (problems in identifier order)", analysisError.Problems[0])
	}
	want := `invalid analysis: hunks["span00"].side: "UP", want RIGHT or LEFT (and 19 more)`
	if got := err.Error(); got != want {
		test.Errorf("Error() = %q, want %q", got, want)
	}
}
