package analysis

import (
	"fmt"
	"maps"
	"regexp"
	"slices"

	"github.com/MikePors/strigidae/internal/github"
)

// maximumProblems caps the problems reported: past that, more are noise.
const maximumProblems = 20

// identifierPattern is what span, lens and segment identifiers must match.
var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)

// checker collects problems, each prefixed with where it is.
type checker struct {
	problems []string
}

func (checker *checker) add(where, format string, arguments ...any) {
	if len(checker.problems) < maximumProblems {
		checker.problems = append(checker.problems, where+": "+fmt.Sprintf(format, arguments...))
	}
}

// required reports an empty required string.
func (checker *checker) required(where, value string) {
	if value == "" {
		checker.add(where, "required")
	}
}

func (checker *checker) identifier(where, identifier string) {
	if !identifierPattern.MatchString(identifier) {
		checker.add(where, "%q is not a valid id (letters, digits, and . _ : - after the first, at most 64)", identifier)
	}
}

// references checks that every name in names is in known and appears once.
func (checker *checker) references(where string, names []string, known func(string) bool, what string) {
	seen := map[string]bool{}
	for i, name := range names {
		at := fmt.Sprintf("%s[%d]", where, i)
		switch {
		case !known(name):
			checker.add(at, "no %s %q", what, name)
		case seen[name]:
			checker.add(at, "%q is listed twice", name)
		}
		seen[name] = true
	}
}

func validate(analysis Analysis) []string {
	checker := &checker{}
	if analysis.SchemaVersion != SchemaVersion {
		checker.add("schema_version", "%q, want %q", analysis.SchemaVersion, SchemaVersion)
	}
	checker.pullRequest(analysis.PullRequest)
	checker.spans(analysis.Spans)
	spanExists := func(identifier string) bool {
		_, ok := analysis.Spans[identifier]
		return ok
	}
	for i, edge := range analysis.Edges {
		at := fmt.Sprintf("edges[%d]", i)
		checker.required(at+".kind", edge.Kind)
		if !spanExists(edge.From) {
			checker.add(at+".from", "no hunk %q", edge.From)
		}
		if !spanExists(edge.To) {
			checker.add(at+".to", "no hunk %q", edge.To)
		}
	}
	if len(analysis.Lenses) == 0 {
		checker.add("lenses", "at least one lens is required")
	}
	lensSeen := map[string]bool{}
	for i, lens := range analysis.Lenses {
		at := fmt.Sprintf("lenses[%d]", i)
		checker.identifier(at+".id", lens.Identifier)
		if lensSeen[lens.Identifier] {
			checker.add(at+".id", "%q is used by another lens", lens.Identifier)
		}
		lensSeen[lens.Identifier] = true
		checker.lens(at, lens, spanExists)
	}
	return checker.problems
}

func (checker *checker) pullRequest(pullRequest PullRequest) {
	checker.required("pr.repo", pullRequest.Repository)
	if pullRequest.Number < 1 {
		checker.add("pr.number", "required, at least 1")
	}
	checker.required("pr.head_sha", pullRequest.HeadSHA)
	checker.required("pr.merge_base", pullRequest.MergeBase)
}

// spans checks each span in identifier order, so problems come out the same
// every time.
func (checker *checker) spans(spans map[string]Span) {
	if spans == nil {
		checker.add("hunks", "required")
	}
	for _, identifier := range slices.Sorted(maps.Keys(spans)) {
		span, at := spans[identifier], fmt.Sprintf("hunks[%q]", identifier)
		checker.identifier(at, identifier)
		checker.required(at+".path", span.Path)
		if span.Side != github.SideRight && span.Side != github.SideLeft {
			checker.add(at+".side", "%q, want RIGHT or LEFT", span.Side)
		}
		if span.Start < 1 {
			checker.add(at+".start", "required, at least 1")
		}
		if span.End < span.Start {
			checker.add(at+".end", "%d is before start %d", span.End, span.Start)
		}
	}
}

func (checker *checker) lens(at string, lens Lens, spanExists func(string) bool) {
	checker.required(at+".title", lens.Title)
	if len(lens.Segments) == 0 {
		checker.add(at+".segments", "at least one segment is required")
	}
	segmentSeen := map[string]bool{}
	for i, segment := range lens.Segments {
		segmentAt := fmt.Sprintf("%s.segments[%d]", at, i)
		checker.identifier(segmentAt+".id", segment.Identifier)
		if segmentSeen[segment.Identifier] {
			checker.add(segmentAt+".id", "%q is used by another segment of this lens", segment.Identifier)
		}
		segmentSeen[segment.Identifier] = true
		checker.segment(segmentAt, segment, spanExists)
	}
	checker.references(at+".order", lens.Order, func(identifier string) bool { return segmentSeen[identifier] }, "segment in this lens")
}

func (checker *checker) segment(at string, segment Segment, spanExists func(string) bool) {
	checker.required(at+".title", segment.Title)
	switch segment.Risk {
	case "", RiskHigh, RiskMedium, RiskLow:
	default:
		checker.add(at+".risk", "%q, want high, medium or low", segment.Risk)
	}
	if len(segment.Spans) == 0 {
		checker.add(at+".hunks", "at least one hunk is required")
	}
	checker.references(at+".hunks", segment.Spans, spanExists, "hunk")
	inSegment := func(identifier string) bool { return slices.Contains(segment.Spans, identifier) }
	checker.references(at+".order", segment.Order, inSegment, "hunk in this segment")
	for _, identifier := range slices.Sorted(maps.Keys(segment.Notes)) {
		if !inSegment(identifier) {
			checker.add(fmt.Sprintf("%s.notes[%q]", at, identifier), "no hunk %q in this segment", identifier)
		}
	}
}
