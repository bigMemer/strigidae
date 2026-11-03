package main

import (
	"bytes"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/github/fake"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
	"github.com/MikePors/strigidae/internal/userinterface/views/dashboard"
	"github.com/MikePors/strigidae/internal/userinterface/views/workspace"
)

// ":create" belongs to the repository list: elsewhere the shell says "not
// available here" because the top view declines it or is no Commander.
func TestCreateIsNotAvailableOutsideTheList(test *testing.T) {
	if !slices.Contains(viewCommands(), "create") {
		test.Fatal(`the shell is not told about "create"`)
	}
	if _, ok := any(dashboard.New(fake.New(), nil, theme.Default(), dashboard.Options{})).(userinterface.Commander); ok {
		test.Error("the dashboard is a Commander, so it could take :create")
	}
	view := workspace.New(fake.New(), github.Repository{Owner: "o", Name: "r"}, 1)
	for _, name := range []string{"create", "create-draft"} {
		if command, handled := view.Command(name, ""); handled || command != nil {
			test.Errorf("the workspace handled :%s", name)
		}
	}
}

// The checkout's branch is the head the list preselects; with -R the checkout
// need not be the repository shown, so there is none.
func TestCurrentBranchOfTheCheckout(test *testing.T) {
	origin := "git@github.com:o/r.git"
	for _, testCase := range []struct {
		name      string
		arguments []string
		want      string
	}{
		{"inferred from the checkout", nil, "feat/current"},
		{"-R has none", []string{"-R", "o/r"}, ""},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			harness := newHarness(map[string]string{"origin": origin}, true, nil)
			var stdout, stderr bytes.Buffer
			if code := run(testCase.arguments, &stdout, &stderr, harness.dependencies); code != 0 {
				test.Fatalf("exit code = %d: %s", code, stderr.String())
			}
			if got := harness.application.currentBranch; got != testCase.want {
				test.Errorf("current branch = %q, want %q", got, testCase.want)
			}
		})
	}
}

// The shell is told the commands of every view, so ":merge" on the dashboard
// says "not available here" instead of "not a command".
func TestViewCommandsAreCollectedFromTheViews(test *testing.T) {
	got := viewCommands()
	for _, name := range []string{"merge", "approve", "copy-filename", "edit-comment", "resolve", "create", "create-draft"} {
		if !slices.Contains(got, name) {
			test.Errorf("viewCommands() lacks %q", name)
		}
	}
	// The shell's own commands win, so a view command with the same name
	// could never be reached.
	for _, name := range userinterface.CommandNames() {
		if slices.Contains(got, name) {
			test.Errorf("view command %q is also a shell command", name)
		}
	}
	seen := map[string]bool{}
	for _, name := range got {
		if seen[name] {
			test.Errorf("view command %q is declared twice", name)
		}
		seen[name] = true
	}
}

// The checkout strig starts in is kept, -R or not, so a plugin run on a pull
// request of its repository is told the directory.
func TestWorkingCheckout(test *testing.T) {
	for _, testCase := range []struct {
		name      string
		checkout  bool
		arguments []string
		want      userinterface.Checkout
	}{
		{"inferred", true, nil, userinterface.Checkout{Directory: "/work", Host: "github.com", Repository: github.Repository{Owner: "o", Name: "r"}}},
		{"kept with -R", true, []string{"-R", "x/y"}, userinterface.Checkout{Directory: "/work", Host: "github.com", Repository: github.Repository{Owner: "o", Name: "r"}}},
		{"outside a checkout", false, []string{"-R", "x/y"}, userinterface.Checkout{}},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			harness := newHarness(map[string]string{"origin": "git@github.com:o/r.git"}, testCase.checkout, nil)
			var stdout, stderr bytes.Buffer
			if code := run(testCase.arguments, &stdout, &stderr, harness.dependencies); code != 0 {
				test.Fatalf("exit code = %d: %s", code, stderr.String())
			}
			if diff := cmp.Diff(testCase.want, harness.application.checkout); diff != "" {
				test.Errorf("checkout (-want +got):\n%s", diff)
			}
		})
	}
}
