// Package analysis reads a review analysis: a pull request's changes regrouped
// for a reader (format prreview/analysis@1). Lenses are ways of looking at the
// pull request; each holds segments, and each segment names spans of lines
// defined once at the top level, so a span can appear in several segments and
// lenses, annotated differently in each. Edges relate spans.
//
// The format calls the spans "hunks"; this package calls them spans, because
// they are not git hunks: a span may cover several of them, or none.
//
// Parse checks the file's structure and that every reference resolves. It
// knows nothing of the pull request on screen or of git, and keeps paths and
// text as given: sanitising them is the user interface's job.
package analysis

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/MikePors/strigidae/internal/github"
)

// SchemaVersion is the only format version Parse reads.
const SchemaVersion = "prreview/analysis@1"

// MaximumFileSize is the largest analysis Parse reads.
const MaximumFileSize = 8 << 20

// Analysis is one pull request regrouped for review.
type Analysis struct {
	SchemaVersion string `json:"schema_version"`
	GeneratedAt   string `json:"generated_at,omitempty"`
	Generator     string `json:"generator,omitempty"`
	// PullRequest is the pull request and the commits the line numbers are
	// against.
	PullRequest PullRequest `json:"pr"`
	// Spans are keyed by their identifier.
	Spans  map[string]Span `json:"hunks"`
	Edges  []Edge          `json:"edges,omitempty"`
	Lenses []Lens          `json:"lenses"`
}

// PullRequest says which pull request an analysis is about and which commits
// its line numbers are against.
type PullRequest struct {
	// Repository is "owner/name".
	Repository string `json:"repo"`
	Number     int    `json:"number"`
	Title      string `json:"title,omitempty"`
	// HeadSHA is the commit RIGHT-side line numbers are against.
	HeadSHA string `json:"head_sha"`
	BaseSHA string `json:"base_sha,omitempty"`
	// MergeBase is the commit LEFT-side line numbers are against: the diff is
	// computed from it, not from BaseSHA.
	MergeBase string `json:"merge_base"`
}

// Span is a run of adjacent lines of one file that read together. It may
// cover several git hunks and unchanged lines.
type Span struct {
	Path string `json:"path"`
	// Side is RIGHT for lines of the head commit, LEFT for lines of the merge
	// base.
	Side  github.DiffSide `json:"side"`
	Start int             `json:"start"`
	End   int             `json:"end"`
	// Label is a few words naming the span, whatever the lens.
	Label string `json:"label,omitempty"`
	// ContextOnly is set when the span has no changed lines: it is there to
	// orient the reader.
	ContextOnly bool `json:"context_only,omitempty"`
}

// Edge is a directed relationship between two spans, such as "calls" or
// "tests". Kind is an open vocabulary.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
	Note string `json:"note,omitempty"`
}

// Lens is one way of looking at the pull request.
type Lens struct {
	Identifier string `json:"id"`
	Title      string `json:"title"`
	// Purpose is the question the lens helps a reviewer answer.
	Purpose string `json:"purpose,omitempty"`
	// Order lists segment identifiers in reading order.
	Order    []string  `json:"order,omitempty"`
	Segments []Segment `json:"segments"`
}

// Segment is a set of spans read together under a lens.
type Segment struct {
	Identifier string `json:"id"`
	Title      string `json:"title"`
	Risk       Risk   `json:"risk,omitempty"`
	// Annotation is about the whole set of spans.
	Annotation string `json:"annotation,omitempty"`
	// Spans lists span identifiers.
	Spans []string `json:"hunks"`
	// Notes are about one span each, keyed by span identifier, and specific
	// to this segment.
	Notes map[string]string `json:"notes,omitempty"`
	// Order lists span identifiers in reading order.
	Order []string `json:"order,omitempty"`
}

// Risk is how carefully a segment needs reading; empty when not given.
type Risk string

// The risks a segment may have.
const (
	RiskHigh   Risk = "high"
	RiskMedium Risk = "medium"
	RiskLow    Risk = "low"
)

// Error lists what is wrong with an analysis, each problem prefixed with
// where it is, such as `lenses[1].segments[0].hunks[2]: no hunk "x"`.
type Error struct {
	Problems []string
}

func (err *Error) Error() string {
	text := "invalid analysis: " + err.Problems[0]
	if len(err.Problems) > 1 {
		text += fmt.Sprintf(" (and %d more)", len(err.Problems)-1)
	}
	return text
}

// Parse decodes and validates an analysis. Unknown fields, another schema
// version, a missing required field and a reference that does not resolve are
// all errors; a structural problem is an *Error listing every one found.
func Parse(data []byte) (Analysis, error) {
	if len(data) > MaximumFileSize {
		return Analysis{}, fmt.Errorf("analysis is too large (limit %d bytes)", MaximumFileSize)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var analysis Analysis
	if err := decoder.Decode(&analysis); err != nil {
		return Analysis{}, fmt.Errorf("read analysis: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Analysis{}, errors.New("read analysis: more than one JSON value")
	}
	if problems := validate(analysis); len(problems) > 0 {
		return Analysis{}, &Error{Problems: problems}
	}
	return analysis, nil
}
