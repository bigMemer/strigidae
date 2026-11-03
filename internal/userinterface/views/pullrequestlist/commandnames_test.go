package pullrequestlist

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// CommandNames is what Tab offers, and Command handles each of them.
func TestCommandNamesAreTheCommandsTheViewHandles(test *testing.T) {
	view := &View{}
	if diff := cmp.Diff(Commands(), view.CommandNames()); diff != "" {
		test.Errorf("command names (-want +got):\n%s", diff)
	}
}
