package workspace

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

func TestSortedChecksCancelledIsInactive(test *testing.T) {
	checks := []github.Check{
		{Name: "a-cancelled", Result: github.CheckCancelled},
		{Name: "b-passed", Result: github.CheckPassed},
		{Name: "c-failed", Result: github.CheckFailed},
		{Name: "d-skipped", Result: github.CheckSkipped},
	}
	var names []string
	for _, check := range sortedChecks(checks) {
		names = append(names, check.Name)
	}
	want := []string{"c-failed", "b-passed", "a-cancelled", "d-skipped"}
	if diff := cmp.Diff(want, names); diff != "" {
		test.Errorf("order (-want +got):\n%s", diff)
	}
	glyph, _ := checkGlyph(theme.New(true), github.CheckCancelled)
	if glyph != "-" {
		test.Errorf("cancelled glyph = %q, want %q", glyph, "-")
	}
}
