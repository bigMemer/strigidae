// Package thread renders a review thread as lines of styled text: a header
// line that is always shown, and, when expanded, every comment with its author,
// age, read-only reactions and Markdown body. Hosts (the diff pane, the
// conversation) place the lines; the package knows nothing about scrolling.
package thread

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface/components/comment"
	"github.com/MikePors/strigidae/internal/userinterface/components/markdown"
	"github.com/MikePors/strigidae/internal/userinterface/terminaltext"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

// Environment is what rendering needs besides the thread.
type Environment struct {
	Theme    theme.Theme
	Markdown *markdown.Renderer
	Now      time.Time
}

// Options say how one thread is drawn.
type Options struct {
	// Expanded shows every comment below the header line.
	Expanded bool
	// Location puts "path:line" in the header, for lists where the thread is
	// not already next to its code.
	Location bool
}

// DefaultExpanded is whether a thread starts expanded: resolved ones do not.
func DefaultExpanded(thread github.ReviewThread) bool { return !thread.IsResolved }

// Quiet reports whether a thread is de-emphasised: resolved or outdated.
func Quiet(thread github.ReviewThread) bool { return thread.IsResolved || thread.IsOutdated }

// Lines renders t in at most width columns per line. The first line is the
// header; its first cell is the fold glyph. Lines are not padded.
func Lines(environment Environment, thread github.ReviewThread, width int, options Options) []string {
	lines, _ := build(environment, thread, width, options)
	return lines
}

// Layout is Lines together with the owner of each line, built in one pass: the lines and,
// parallel to them, the index in thread.Comments each belongs to (-1 for the
// header line and the blank rows between comments).
func Layout(environment Environment, thread github.ReviewThread, width int, options Options) (lines []string, owners []int) {
	return build(environment, thread, width, options)
}

// build returns the lines and, parallel to them, the comment each belongs to.
func build(environment Environment, thread github.ReviewThread, width int, options Options) (lines []string, owners []int) {
	width = max(width, 8)
	out := []string{header(environment, thread, width, options)}
	owner := []int{-1}
	if !options.Expanded {
		return out, owner
	}
	theme := environment.Theme
	bar := theme.ThreadBar.Render("│ ")
	bodyWidth := width - lipgloss.Width(bar)
	for i, reviewComment := range thread.Comments {
		reviewComment := comment.Comment{
			Author: reviewComment.Author, At: reviewComment.CreatedAt, Body: reviewComment.Body, Pending: reviewComment.Pending,
			Reactions: reviewComment.Reactions, Muted: thread.IsOutdated,
		}
		out = append(out, ansi.Truncate(bar+comment.Header(theme, environment.Now, reviewComment), width, "…"))
		owner = append(owner, i)
		body, noted := localNote(theme, thread, i, bodyWidth)
		if !noted {
			body = comment.Body(theme, environment.Markdown, reviewComment, bodyWidth)
		}
		if body == "" {
			continue
		}
		for line := range strings.SplitSeq(body, "\n") {
			out = append(out, bar+line)
			owner = append(owner, i)
		}
		if i < len(thread.Comments)-1 {
			out = append(out, strings.TrimRight(bar, " "))
			owner = append(owner, -1) // the blank row between two comments belongs to neither
		}
	}
	return out, owner
}

func header(environment Environment, thread github.ReviewThread, width int, options Options) string {
	theme := environment.Theme
	glyph := "▸"
	if options.Expanded {
		glyph = "▾"
	}
	style := theme.ThreadMarker
	if Quiet(thread) {
		style = theme.Muted
	}
	parts := []string{style.Render(glyph)}
	if options.Location {
		parts = append(parts, theme.Normal.Render(location(thread)))
	}
	count := len(thread.Comments)
	if count > 0 {
		parts = append(parts, theme.Author.Render(comment.Author(thread.Comments[0].Author)))
	}
	summary := fmt.Sprintf("%d comment", count)
	if count != 1 {
		summary += "s"
	}
	if count > 0 && !thread.Local.IsLocal() { // a local thread has no times
		summary += " · " + comment.RelativeTime(environment.Now, thread.Comments[count-1].CreatedAt)
	}
	parts = append(parts, theme.Muted.Render(summary))
	if thread.IsResolved {
		parts = append(parts, theme.Muted.Render("[resolved]"))
	}
	if thread.IsOutdated {
		parts = append(parts, theme.Muted.Render("[outdated]"))
	}
	if thread.SubjectType == github.SubjectFile {
		parts = append(parts, theme.Muted.Render("[file]"))
	}
	line := strings.Join(parts, " ")
	if !options.Expanded && count > 0 {
		if path := preview(thread.Comments[0].Body); path != "" {
			line += theme.Muted.Render(" — " + path)
		}
	}
	return ansi.Truncate(line, width, "…")
}

// localNote draws a comment of a local thread as plain text, not Markdown:
// the question, exactly as typed; the waiting line, muted; or why there is no
// answer, in the error style. The text is the user's or the plugin's, so it is
// cleaned. ok is false for the answer and for every comment of other threads.
func localNote(activeTheme theme.Theme, thread github.ReviewThread, index, width int) (body string, ok bool) {
	if !thread.Local.IsLocal() {
		return "", false
	}
	style := activeTheme.Normal
	switch {
	case index == 0:
	case index == len(thread.Comments)-1 && thread.Local == github.LocalWaiting:
		style = activeTheme.Muted
	case index == len(thread.Comments)-1 && thread.Local == github.LocalFailed:
		style = activeTheme.ErrorText
	default:
		return "", false
	}
	if thread.IsOutdated {
		style = activeTheme.Muted
	}
	const indent = "  " // as the Markdown body of an answer is indented
	lines := strings.Split(ansi.Wrap(terminaltext.Clean(thread.Comments[index].Body), max(width-len(indent), 1), ""), "\n")
	for i, line := range lines {
		lines[i] = indent + style.Render(line)
	}
	return strings.Join(lines, "\n"), true
}

func location(thread github.ReviewThread) string {
	locationText := terminaltext.Clean(thread.Path)
	line := thread.Line
	if line == 0 {
		line = thread.OriginalLine
	}
	if line > 0 {
		locationText += ":" + strconv.Itoa(line)
	}
	return locationText
}

// preview is the first non-empty line of a body, cleaned, for collapsed threads.
func preview(body string) string {
	for line := range strings.SplitSeq(terminaltext.CleanBlock(body), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
