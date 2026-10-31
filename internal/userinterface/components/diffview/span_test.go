package diffview

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/diff"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

func TestSpanMarksItsRows(test *testing.T) {
	hunk := diff.Hunk{OldStart: 1, NewStart: 1, Lines: []diff.Line{
		{Kind: diff.Context, Old: 1, New: 1, Text: "a"},
		{Kind: diff.Delete, Old: 2, Text: "gone"},
		{Kind: diff.Add, New: 2, Text: "new"},
		{Kind: diff.Add, New: 3, Text: "newer"},
		{Kind: diff.Context, Old: 3, New: 4, Text: "z"},
	}}
	file := syntheticFile(diff.Modified, hunk)
	model := New(theme.New(true))
	model.SetFile(file)
	model.SetSize(80, 10)
	marked := func() []int {
		var indexes []int
		for i := range model.base {
			if model.inSpan(i) {
				indexes = append(indexes, i)
			}
		}
		return indexes
	}
	// base 0 is the hunk header; the lines are 1..5.
	for _, testCase := range []struct {
		name string
		span Range
		want []int
	}{
		{"new side, with the deleted line between", Range{Path: "a.go", Side: github.SideRight, Start: 1, End: 2}, []int{1, 2, 3}},
		{"old side", Range{Path: "a.go", Side: github.SideLeft, Start: 2, End: 3}, []int{2, 3, 4, 5}},
		{"outside the hunk", Range{Path: "a.go", Side: github.SideRight, Start: 40, End: 50}, nil},
		{"another file", Range{Path: "b.go", Side: github.SideRight, Start: 1, End: 4}, nil},
		{"none", Range{}, nil},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			model.SetSpan(testCase.span)
			if diff := cmp.Diff(testCase.want, marked()); diff != "" {
				test.Errorf("marked rows (-want +got):\n%s", diff)
			}
		})
	}
}
