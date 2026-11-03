package pullrequestrows

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface/components/search"
	"github.com/MikePors/strigidae/internal/userinterface/terminaltext"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

const (
	gap                 = "  "
	marker              = "> " // prefix of the selected row, so selection survives without colour
	noMarker            = "  "
	draftPrefix         = "[draft] "
	minimumTitleColumns = 12
	maximumAuthor       = 16
	maximumRepository   = 24
	maximumHead         = 24
	maximumBase         = 20
	minimumRepository   = 10 // narrowest the repository column shrinks to before it is dropped
	ageColumns          = 4
	reviewColumns       = 2
	checksColumns       = 2
	commentColumns      = 3
)

// MarkerColumns is the width of the selection marker in front of every row.
// Callers subtract it from the width they give Layout.
const MarkerColumns = len(marker)

// Table draws rows with one theme and clock.
type Table struct {
	theme   theme.Theme
	now     func() time.Time
	options Options
}

// Options chooses the optional columns of a Table.
type Options struct {
	// Repository adds a column with owner/name, for lists that span
	// repositories.
	Repository bool
	// Branches adds the HEAD and BASE columns after the title.
	Branches bool
}

// New returns a Table drawing the columns options asks for.
func New(activeTheme theme.Theme, now func() time.Time, options Options) Table {
	return Table{theme: activeTheme, now: now, options: options}
}

// Layout holds the column widths for one render. A zero width hides a column.
type Layout struct {
	repository, number, title, head, base, author, age, checks, review, comments int
}

// Layout sizes the columns for pullRequests in width columns (not counting the
// selection marker).
func (table Table) Layout(pullRequests []github.PullRequest, width int) Layout {
	layout := Layout{age: ageColumns, checks: checksColumns, review: reviewColumns, comments: commentColumns, number: len("#")}
	for _, pullRequest := range pullRequests {
		layout.number = max(layout.number, len(fmt.Sprintf("#%d", pullRequest.Number)))
		layout.author = min(maximumAuthor, max(layout.author, ansi.StringWidth(terminaltext.Clean(pullRequest.Author))))
		if table.options.Branches {
			layout.head = min(maximumHead, max(layout.head, ansi.StringWidth(terminaltext.Clean(pullRequest.HeadBranch))))
			layout.base = min(maximumBase, max(layout.base, ansi.StringWidth(terminaltext.Clean(pullRequest.BaseBranch))))
		}
		if table.options.Repository {
			layout.repository = min(maximumRepository, max(layout.repository, ansi.StringWidth(terminaltext.Clean(pullRequest.Repository.String()))))
		}
	}
	layout.author = max(layout.author, len("AUTHOR"))
	if table.options.Branches {
		layout.head = max(layout.head, len("HEAD"))
		layout.base = max(layout.base, len("BASE"))
	}
	if table.options.Repository {
		layout.repository = max(layout.repository, len("REPO"))
	}
	fits := func() bool { return layout.fixed() <= width-minimumTitleColumns }
	// Drop the least important columns first on narrow terminals.
	for _, drop := range []*int{&layout.head, &layout.base, &layout.author, &layout.comments, &layout.checks, &layout.review} {
		if fits() {
			break
		}
		*drop = 0
	}
	if table.options.Repository {
		table.narrowRepository(&layout, width, fits)
	}
	layout.title = max(1, width-layout.fixed())
	return layout
}

// narrowRepository makes room for the title in a list that spans repositories:
// shrink the repository column to a floor, then drop it, then drop the age.
// The title takes whatever is left.
func (table Table) narrowRepository(layout *Layout, width int, fits func() bool) {
	if fits() {
		return
	}
	excess := layout.fixed() - (width - minimumTitleColumns)
	layout.repository = max(layout.repository-excess, min(layout.repository, minimumRepository))
	if fits() {
		return
	}
	layout.repository = 0
	if !fits() {
		layout.age = 0
	}
}

// shortRepository fits owner/name into width columns, giving up the owner
// before the name: "…/widgets" rather than "acme/wi…". text must be clean.
func shortRepository(text string, width int) string {
	if ansi.StringWidth(text) <= width {
		return text
	}
	const ownerGap = "…/" // stands in for the owner
	if slash := strings.LastIndex(text, "/"); slash >= 0 && width > ansi.StringWidth(ownerGap) {
		// Keep the name and give up the owner, cutting the name only when it
		// alone does not fit.
		return ownerGap + ansi.Truncate(text[slash+1:], width-ansi.StringWidth(ownerGap), "…")
	}
	return ansi.Truncate(text, width, "…")
}

// fixed is the width of every column but the title, including the gaps.
func (layout Layout) fixed() int {
	width := layout.number + len(gap) // number, then the gap before the title
	for _, count := range []int{layout.repository, layout.head, layout.base, layout.author, layout.age, layout.checks, layout.review, layout.comments} {
		if count > 0 {
			width += count + len(gap)
		}
	}
	return width
}

// Header returns the column headings, padded to width.
func (table Table) Header(layout Layout, width int) string {
	return table.theme.PullRequestList.Header.Render(pad(noMarker+layout.row("REPO", "#", "TITLE", "HEAD", "BASE", "AUTHOR", "UPD", "CI", "RV", "CMT"), width))
}

// row pads each cell to its column and joins them. Right-aligned columns are
// numbers; the rest are left-aligned.
func (layout Layout) row(repository, number, title, head, base, author, age, checks, review, comments string) string {
	var cells []string
	if layout.repository > 0 {
		cells = append(cells, pad(repository, layout.repository))
	}
	cells = append(cells, padLeft(number, layout.number), pad(title, layout.title))
	add := func(text string, width int, right bool) {
		switch {
		case width == 0:
		case right:
			cells = append(cells, padLeft(text, width))
		default:
			cells = append(cells, pad(text, width))
		}
	}
	add(head, layout.head, false)
	add(base, layout.base, false)
	add(author, layout.author, false)
	add(age, layout.age, true)
	add(checks, layout.checks, false)
	add(review, layout.review, false)
	add(comments, layout.comments, true)
	return strings.Join(cells, gap)
}

// SearchText is the clean text a search looks in for a pull request: title,
// author, owner/name, head and base branch, and #number.
func SearchText(pullRequest github.PullRequest) string {
	return strings.Join([]string{
		terminaltext.Clean(pullRequest.Title),
		terminaltext.Clean(pullRequest.Author),
		terminaltext.Clean(pullRequest.Repository.String()),
		terminaltext.Clean(pullRequest.HeadBranch),
		terminaltext.Clean(pullRequest.BaseBranch),
		fmt.Sprintf("#%d", pullRequest.Number),
	}, " ")
}

// SearchItems turns pull requests into search rows: their SearchText, and the
// identity the caller gives each one.
func SearchItems(pullRequests []github.PullRequest, identity func(index int, pullRequest github.PullRequest) string) []search.Item {
	items := make([]search.Item, len(pullRequests))
	for i, pullRequest := range pullRequests {
		items[i] = search.Item{Text: SearchText(pullRequest), Identity: identity(i, pullRequest)}
	}
	return items
}

// indentColumns is how far the title moves in for each level of depth.
const indentColumns = 2

// Row draws one pull request, padded to width. The selected row is
// highlighted and carries a marker; a row that matches the search carries the
// search marker unless it is selected.
func (table Table) Row(layout Layout, pullRequest github.PullRequest, selected, matched bool, width int) string {
	return table.IndentedRow(layout, pullRequest, 0, selected, matched, width)
}

// IndentedRow is Row with the title moved in by depth levels of indentColumns
// spaces before it is truncated; the other columns stay where they are.
func (table Table) IndentedRow(layout Layout, pullRequest github.PullRequest, depth int, selected, matched bool, width int) string {
	indent := strings.Repeat(" ", indentColumns*max(depth, 0))
	title := terminaltext.Clean(pullRequest.Title)
	if pullRequest.Draft {
		title = draftPrefix + title
	}
	title = ansi.Truncate(indent+title, layout.title, "…")
	head := ansi.Truncate(terminaltext.Clean(pullRequest.HeadBranch), layout.head, "…")
	base := ansi.Truncate(terminaltext.Clean(pullRequest.BaseBranch), layout.base, "…")
	author := ansi.Truncate(terminaltext.Clean(pullRequest.Author), layout.author, "…")
	repository := shortRepository(terminaltext.Clean(pullRequest.Repository.String()), layout.repository)
	number := fmt.Sprintf("#%d", pullRequest.Number)
	age := relativeAge(table.now(), pullRequest.UpdatedAt)
	checksMark, checksStyle := table.checksMark(pullRequest.Checks)
	reviewMark, reviewStyle := table.reviewMark(pullRequest.ReviewDecision)
	comments := commentText(pullRequest.CommentCount)

	if selected {
		// A selected row that matches the search shows the match glyph next to
		// the cursor, in the row's own style, which is readable on its background.
		lead := marker
		if matched {
			lead = search.SelectedGlyph
		}
		return table.theme.PullRequestList.Selected.Render(pad(lead+layout.row(repository, number, title, head, base, author, age, checksMark, reviewMark, comments), width))
	}

	styles := table.theme.PullRequestList
	styledTitle := table.theme.Normal.Render(title)
	if pullRequest.Draft && strings.HasPrefix(title, indent+draftPrefix) {
		styledTitle = indent + styles.Draft.Render(draftPrefix) + table.theme.Normal.Render(strings.TrimPrefix(title, indent+draftPrefix))
	}
	// Style after padding so the escape codes don't skew the widths.
	var out []string
	if layout.repository > 0 {
		out = append(out, styles.Repository.Render(pad(repository, layout.repository)))
	}
	out = append(out, styles.Number.Render(padLeft(number, layout.number)), styledTitle+strings.Repeat(" ", max(0, layout.title-ansi.StringWidth(title))))
	if layout.head > 0 {
		out = append(out, styles.Branch.Render(pad(head, layout.head)))
	}
	if layout.base > 0 {
		out = append(out, styles.Branch.Render(pad(base, layout.base)))
	}
	if layout.author > 0 {
		out = append(out, styles.Author.Render(pad(author, layout.author)))
	}
	if layout.age > 0 {
		out = append(out, styles.Age.Render(padLeft(age, layout.age)))
	}
	if layout.checks > 0 {
		out = append(out, checksStyle.Render(pad(checksMark, layout.checks)))
	}
	if layout.review > 0 {
		out = append(out, reviewStyle.Render(pad(reviewMark, layout.review)))
	}
	if layout.comments > 0 {
		out = append(out, styles.Comments.Render(padLeft(comments, layout.comments)))
	}
	lead := noMarker
	if matched {
		lead = table.theme.SearchMarker.Render(search.Glyph) + " "
	}
	return lead + strings.Join(out, gap)
}
