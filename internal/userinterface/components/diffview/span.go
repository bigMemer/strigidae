package diffview

import (
	"github.com/MikePors/strigidae/internal/diff"
	"github.com/MikePors/strigidae/internal/github"
)

// markSpan is the gutter mark of a row in the marked span.
const markSpan = "┃"

// spanBounds caches the base rows the marked span covers in one file.
type spanBounds struct {
	file        *diff.File
	span        Range
	first, last int // base indexes; first > last when no row is in the span
}

// SetSpan marks the lines of span in the gutter, so a reader sees where a
// span starts and ends. It applies only while span.Path is the file shown;
// the zero Range marks nothing. Every row from the first line in the span to
// the last is marked, including the other side's lines between them.
func (model *Model) SetSpan(span Range) { model.span = span }

// inSpan reports whether base row baseIndex is in the marked span.
func (model *Model) inSpan(baseIndex int) bool {
	if model.file == nil || model.span.Path == "" || model.span.Path != model.file.Path() {
		return false
	}
	if model.spanCache.file != model.file || model.spanCache.span != model.span {
		model.spanCache = model.spanBoundsOf(model.span)
	}
	return baseIndex >= model.spanCache.first && baseIndex <= model.spanCache.last
}

func (model *Model) spanBoundsOf(span Range) spanBounds {
	bounds := spanBounds{file: model.file, span: span, first: 1, last: 0}
	found := false
	for i, row := range model.base {
		if !row.isLine() {
			continue
		}
		number := row.line.New
		if span.Side == github.SideLeft {
			number = row.line.Old
		}
		if number < span.Start || number > span.End || number <= 0 {
			continue
		}
		if !found {
			bounds.first, found = i, true
		}
		bounds.last = i
	}
	return bounds
}
