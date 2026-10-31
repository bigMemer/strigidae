package diffview

import (
	"math"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MikePors/strigidae/internal/diff"
	"github.com/MikePors/strigidae/internal/userinterface/components/search"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

// tokenNoNewline styles the text of "\ No newline" marker rows. It is no
// Chroma token type, so no highlighter reports it.
const tokenNoNewline = chroma.TokenType(math.MinInt32)

// Line-kind indexes into the style set.
const (
	kindContext = iota
	kindAdd
	kindDelete
	numberKinds
)

// variant is the set of styles for one (kind, cursor) combination.
type variant struct {
	code   map[chroma.TokenType]lipgloss.Style // every standard token type, and tokenNoNewline
	fill   lipgloss.Style                      // pads the code area to full width
	gutter lipgloss.Style
	thread [2]lipgloss.Style // gutter marker: [0] quiet thread, [1] open thread
	search lipgloss.Style    // the match marker in the cursor column
}

// styleSet precomputes every style combination so rendering never builds
// styles.
type styleSet struct {
	variants    [2][numberKinds]variant // [cursor][kind]
	hunk        [2]lipgloss.Style
	placeholder [2]lipgloss.Style // a hidden hunk's row
}

func newStyleSet(activeTheme theme.Theme) styleSet {
	var style styleSet
	cursorBackground := activeTheme.DiffCursor.GetBackground()
	bases := [numberKinds]lipgloss.Style{activeTheme.DiffContext, activeTheme.DiffAdd, activeTheme.DiffDelete}
	gutters := [numberKinds]lipgloss.Style{activeTheme.DiffGutter, activeTheme.DiffGutterAdd, activeTheme.DiffGutterDelete}
	foreground := map[chroma.TokenType]lipgloss.Style{tokenNoNewline: activeTheme.DiffNoNewline}
	for _, tokenType := range activeTheme.Syntax.TokenTypes() {
		foreground[tokenType] = activeTheme.Syntax.For(tokenType)
	}
	for current := range 2 {
		for k := range numberKinds {
			base, gutter := bases[k], gutters[k]
			if current == 1 {
				base, gutter = base.Background(cursorBackground), gutter.Background(cursorBackground)
			}
			variantStyles := variant{fill: base, gutter: gutter}
			variantStyles.thread[0] = activeTheme.Muted.Background(gutter.GetBackground())
			variantStyles.thread[1] = activeTheme.ThreadMarker.Background(gutter.GetBackground())
			variantStyles.search = activeTheme.SearchMarker.Background(gutter.GetBackground())
			variantStyles.code = make(map[chroma.TokenType]lipgloss.Style, len(foreground))
			for tokenType, style := range foreground {
				variantStyles.code[tokenType] = style.Background(base.GetBackground())
			}
			style.variants[current][k] = variantStyles
		}
	}
	style.hunk[0] = activeTheme.DiffHunk
	style.hunk[1] = activeTheme.DiffHunkCursor
	style.placeholder[0] = activeTheme.Muted
	style.placeholder[1] = activeTheme.Muted.Background(cursorBackground)
	return style
}

// codeStyle returns the style for a token type, or the plain one for a type
// the table lacks (a custom Highlighter may report any type).
func (variantStyles *variant) codeStyle(tokenType chroma.TokenType) lipgloss.Style {
	if style, ok := variantStyles.code[tokenType]; ok {
		return style
	}
	return variantStyles.code[chroma.Text]
}

func (model *Model) renderRow(i int) string {
	row := model.rows[i]
	current := 0
	if i == model.window.Cursor() {
		current = 1
	}
	if row.threadRow != nil {
		return model.renderThreadRow(i, row)
	}
	if row.message {
		return model.renderNoticeRow(i, row)
	}
	if row.placeholder {
		return model.renderPlaceholder(i, row, current)
	}
	if row.line == nil {
		header := model.file.Hunks[row.hunk].Header()
		if model.hunkReviewed != nil && model.hunkReviewed(model.file, row.hunk) {
			header = markerReviewed + " " + header
		}
		return model.renderHunk(i, header, current)
	}
	variantStyles, mark := model.rowStyle(i)
	gutter := model.gutterText(i, mark, variantStyles)
	codeWidth := model.codeWidth()
	if codeWidth == 0 {
		return ansi.Truncate(gutter, model.width, "")
	}

	return gutter + model.codeText(model.codeSpans(i), model.window.HorizontalOffset(), codeWidth, codeWidth, variantStyles)
}

// rowStyle returns the style variant and cursor mark of code row i.
func (model *Model) rowStyle(i int) (*variant, string) {
	row := model.rows[i]
	current := 0
	if i == model.window.Cursor() {
		current = 1
	}
	var kind int
	switch row.line.Kind {
	case diff.Add:
		kind = kindAdd
	case diff.Delete:
		kind = kindDelete
	}
	mark := " "
	switch {
	case current == 1:
		mark = ">"
	case model.selected(row.baseIndex):
		mark, current = markSelected, 1 // the selection takes the cursor tint
	case model.searchMarked(i):
		mark = search.Glyph
	case model.inSpan(row.baseIndex):
		mark = markSpan
	}
	return &model.styles.variants[current][kind], mark
}

// codeSpans returns the highlighted text of code row i.
func (model *Model) codeSpans(i int) []Span {
	if row := model.rows[i]; row.line.Kind == diff.NoNewline {
		return []Span{{Text: row.text, Token: tokenNoNewline}}
	}
	return model.spansFor(i)
}

// codeText draws the columns [startColumn, startColumn+width) of spans, padded
// with the row's background to padTo cells.
func (model *Model) codeText(spans []Span, startColumn, width, padTo int, variantStyles *variant) string {
	visible, used := window(spans, startColumn, width)
	var builder strings.Builder
	for _, span := range visible {
		builder.WriteString(variantStyles.codeStyle(span.Token).Render(span.Text))
	}
	if pad := padTo - used; pad > 0 {
		builder.WriteString(variantStyles.fill.Render(spaces(pad)))
	}
	return builder.String()
}

// gutterText is the styled fixed part of diff row i: cursor mark, relative
// number, line numbers, thread marker, change marker.
func (model *Model) gutterText(i int, mark string, variantStyles *variant) string {
	line, baseIndex := model.rows[i].line, model.rows[i].baseIndex
	marker := " "
	switch line.Kind {
	case diff.Add:
		marker = "+"
	case diff.Delete:
		marker = "-"
	case diff.NoNewline:
		marker = `\`
	}
	glyph, open := model.threadGlyph(baseIndex)
	titleStyle := variantStyles.gutter
	if glyph != " " {
		titleStyle = variantStyles.thread[0]
		if open {
			titleStyle = variantStyles.thread[1]
		}
	}
	head := ""
	switch mark {
	case search.Glyph:
		head, mark = variantStyles.search.Render(mark), "" // a match and a span are drawn apart from the gutter
	case markSpan:
		head, mark = variantStyles.thread[1].Render(mark), ""
	}
	return head + variantStyles.gutter.Render(mark+model.relativeCell(i)+numberCell(line.Old, model.numberWidth)+" "+numberCell(line.New, model.numberWidth)+" ") +
		titleStyle.Render(glyph) + variantStyles.gutter.Render(marker)
}

func numberCell(count, width int) string {
	text := ""
	if count > 0 {
		text = strconv.Itoa(count)
	}
	return spaces(width-len(text)) + text
}

func (model *Model) renderHunk(i int, header string, current int) string {
	mark := " "
	if current == 1 {
		mark = ">"
	}
	header, _ = displayText(header) // the section text is copied from file content
	text := ansi.Truncate(mark+model.relativeCell(i)+header, model.width, "")
	text += spaces(model.width - ansi.StringWidth(text))
	return model.styles.hunk[current].Render(text)
}

// spansFor returns the highlighted spans of row i, highlighting on first use.
func (model *Model) spansFor(i int) []Span {
	baseIndex := model.rows[i].baseIndex
	if span, ok := model.cache[baseIndex]; ok {
		return span
	}
	span := model.highlight(model.file.Path(), model.rows[i].text)
	model.cache[baseIndex] = span
	return span
}
