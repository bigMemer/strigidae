package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// PullRequestListStyles are the styles for pull request tables (the PR list view).
type PullRequestListStyles struct {
	Header           lipgloss.Style // column headings
	Selected         lipgloss.Style // the whole highlighted row
	Number           lipgloss.Style
	Author           lipgloss.Style
	Repository       lipgloss.Style // owner/name where a list spans repositories
	Branch           lipgloss.Style // head and base branch names
	Age              lipgloss.Style
	Draft            lipgloss.Style // the draft marker
	Approved         lipgloss.Style // review decision glyphs
	ChangesRequested lipgloss.Style
	ReviewRequired   lipgloss.Style
	ChecksPassing    lipgloss.Style // check status glyphs (also used by the PR workspace header)
	ChecksFailing    lipgloss.Style
	ChecksRunning    lipgloss.Style
	Comments         lipgloss.Style
}

func newPullRequestListStyles(roles Roles) PullRequestListStyles {
	foreground := func(colour color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(colour) }
	muted := foreground(roles.Muted)
	return PullRequestListStyles{
		Header:           muted.Bold(true),
		Selected:         lipgloss.NewStyle().Bold(true).Foreground(roles.OnAccent).Background(roles.Accent),
		Number:           foreground(roles.Warning),
		Author:           muted,
		Repository:       muted,
		Branch:           muted,
		Age:              muted,
		Draft:            muted.Italic(true),
		Approved:         foreground(roles.Success),
		ChangesRequested: foreground(roles.Error),
		ReviewRequired:   foreground(roles.Warning),
		ChecksPassing:    foreground(roles.Added),
		ChecksFailing:    foreground(roles.Removed),
		ChecksRunning:    foreground(roles.Warning),
		Comments:         muted,
	}
}
