package pullrequestrows

import (
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

var testNow = time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)

func TestRelativeAge(test *testing.T) {
	tests := []struct {
		ago  time.Duration
		want string
	}{
		{10 * time.Second, "now"},
		{-time.Hour, "now"},
		{5 * time.Minute, "5m"},
		{3 * time.Hour, "3h"},
		{49 * time.Hour, "2d"},
		{45 * 24 * time.Hour, "1mo"},
		{400 * 24 * time.Hour, "1y"},
	}
	for _, testCase := range tests {
		if got := relativeAge(testNow, testNow.Add(-testCase.ago)); got != testCase.want {
			test.Errorf("age %v = %q, want %q", testCase.ago, got, testCase.want)
		}
	}
}

func TestChecksMark(test *testing.T) {
	activeTheme := theme.Default()
	table := New(activeTheme, time.Now, Options{})
	tests := []struct {
		state github.CheckState
		glyph string
		style lipgloss.Style
	}{
		{github.ChecksPassing, "✓", activeTheme.PullRequestList.ChecksPassing},
		{github.ChecksFailing, "✗", activeTheme.PullRequestList.ChecksFailing},
		{github.ChecksRunning, "●", activeTheme.PullRequestList.ChecksRunning},
		{github.ChecksNone, "", activeTheme.PullRequestList.Comments},
	}
	for _, testCase := range tests {
		test.Run(string(testCase.state), func(test *testing.T) {
			glyph, style := table.checksMark(testCase.state)
			if glyph != testCase.glyph {
				test.Errorf("glyph = %q, want %q", glyph, testCase.glyph)
			}
			if got, want := style.Render("x"), testCase.style.Render("x"); got != want {
				test.Errorf("style renders %q, want %q", got, want)
			}
		})
	}
}
