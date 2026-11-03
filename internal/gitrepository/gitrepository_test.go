package gitrepository

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/github"
)

func TestParseURL(test *testing.T) {
	repository := func(owner, name string) github.Repository { return github.Repository{Owner: owner, Name: name} }
	tests := []struct {
		raw     string
		want    Remote
		wantErr bool
	}{
		{"git@github.com:MikePors/strigidae.git", Remote{"github.com", repository("MikePors", "strigidae")}, false},
		{"git@github.com:MikePors/strigidae", Remote{"github.com", repository("MikePors", "strigidae")}, false},
		{"ghe.example.com:team/app.git", Remote{"ghe.example.com", repository("team", "app")}, false},
		{"ssh://git@github.com/o/r.git", Remote{"github.com", repository("o", "r")}, false},
		{"ssh://git@ghe.example.com:2222/o/r", Remote{"ghe.example.com", repository("o", "r")}, false},
		{"ssh://git@ssh.github.com:443/o/r.git", Remote{"github.com", repository("o", "r")}, false},
		{"https://github.com/o/r.git", Remote{"github.com", repository("o", "r")}, false},
		{"https://github.com/o/r/", Remote{"github.com", repository("o", "r")}, false},
		{"https://user:tok@ghe.example.com/o/r.git", Remote{"ghe.example.com", repository("o", "r")}, false},
		{"git://github.com/o/r.git", Remote{"github.com", repository("o", "r")}, false},
		{"  https://github.com/o/r.git\n", Remote{"github.com", repository("o", "r")}, false},
		{"https://GitHub.com/o/r", Remote{"github.com", repository("o", "r")}, false},
		{"https://github.com:443/o/r.git", Remote{"github.com", repository("o", "r")}, false},
		{"git@ssh.github.com:o/r.git", Remote{"github.com", repository("o", "r")}, false},
		{"git@GitHub.com:o/r.git", Remote{"github.com", repository("o", "r")}, false},
		{"", Remote{}, true},
		{"/home/me/repo", Remote{}, true},
		{"../repo", Remote{}, true},
		{"https://github.com/onlyowner", Remote{}, true},
		{"https://github.com/a/b/c", Remote{}, true},
		{"file:///srv/git/r.git", Remote{}, true},
		{"ftp://github.com/o/r", Remote{}, true},
	}
	for _, testCase := range tests {
		test.Run(testCase.raw, func(test *testing.T) {
			got, err := ParseURL(testCase.raw)
			if (err != nil) != testCase.wantErr {
				test.Fatalf("err = %v, wantErr %v", err, testCase.wantErr)
			}
			if diff := cmp.Diff(testCase.want, got); diff != "" {
				test.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

// fakeGit answers rev-parse and remote get-url from a table.
func fakeGit(hasTopLevel bool, remotes map[string]string) Runner {
	return func(_ context.Context, _ string, arguments ...string) (string, error) {
		switch {
		case len(arguments) > 0 && arguments[0] == "rev-parse":
			if !hasTopLevel {
				return "", errors.New("not a git repository")
			}
			return "/work/repo", nil
		case len(arguments) == 3 && arguments[0] == "remote" && arguments[1] == "get-url":
			if address, ok := remotes[arguments[2]]; ok {
				return address, nil
			}
			return "", errors.New("no such remote")
		}
		return "", errors.New("unexpected git call")
	}
}

func TestUnknownHostErrorQuotesEachHost(test *testing.T) {
	err := &UnknownHostError{Hosts: []string{"gitlab.com", "github-work"}}
	want := `remote host "gitlab.com", "github-work" is not a known GitHub host`
	if got := err.Error(); got != want {
		test.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestInferKnownHosts(test *testing.T) {
	known := func(text string) bool { return text == "github.com" || text == "ghe.example.com" }
	tests := []struct {
		name      string
		remotes   map[string]string
		want      Remote
		wantHosts []string // set when an UnknownHostError is expected
	}{
		{"known origin", map[string]string{"origin": "git@github.com:o/r.git"}, Remote{"github.com", github.Repository{Owner: "o", Name: "r"}}, nil},
		{
			"gitlab upstream falls back to github origin",
			map[string]string{"upstream": "git@gitlab.com:o/r.git", "origin": "git@github.com:me/r.git"},
			Remote{"github.com", github.Repository{Owner: "me", Name: "r"}},
			nil,
		},
		{"ssh alias only", map[string]string{"origin": "github-work:acme/app.git"}, Remote{}, []string{"github-work"}},
		{"both unknown", map[string]string{"upstream": "git@gitlab.com:o/r.git", "origin": "github-work:acme/app.git"}, Remote{}, []string{"gitlab.com", "github-work"}},
		{"enterprise host", map[string]string{"origin": "https://ghe.example.com/o/r"}, Remote{"ghe.example.com", github.Repository{Owner: "o", Name: "r"}}, nil},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			got, err := Infer(context.Background(), fakeGit(true, testCase.remotes), "/work", known)
			if testCase.wantHosts != nil {
				var unknownHostError *UnknownHostError
				if !errors.As(err, &unknownHostError) {
					test.Fatalf("err = %v, want *UnknownHostError", err)
				}
				if diff := cmp.Diff(testCase.wantHosts, unknownHostError.Hosts); diff != "" {
					test.Errorf("hosts (-want +got):\n%s", diff)
				}
				return
			}
			if err != nil {
				test.Fatal(err)
			}
			if diff := cmp.Diff(testCase.want, got); diff != "" {
				test.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

func TestInfer(test *testing.T) {
	up := "git@github.com:upstream-owner/repo.git"
	origin := "https://github.com/fork-owner/repo.git"
	tests := []struct {
		name        string
		hasTopLevel bool
		remotes     map[string]string
		want        github.Repository
		wantErr     error
	}{
		{"origin only", true, map[string]string{"origin": origin}, github.Repository{Owner: "fork-owner", Name: "repo"}, nil},
		{"upstream preferred", true, map[string]string{"origin": origin, "upstream": up}, github.Repository{Owner: "upstream-owner", Name: "repo"}, nil},
		{"unparsable upstream falls back", true, map[string]string{"origin": origin, "upstream": "/local/path"}, github.Repository{Owner: "fork-owner", Name: "repo"}, nil},
		{"no remotes", true, nil, github.Repository{}, ErrNoGitHubRemote},
		{"other remote names ignored", true, map[string]string{"fork": origin}, github.Repository{}, ErrNoGitHubRemote},
		{"outside checkout", false, map[string]string{"origin": origin}, github.Repository{}, ErrNotCheckout},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			got, err := Infer(context.Background(), fakeGit(testCase.hasTopLevel, testCase.remotes), "/work", nil)
			if !errors.Is(err, testCase.wantErr) {
				test.Fatalf("err = %v, want %v", err, testCase.wantErr)
			}
			if got.Repository != testCase.want {
				test.Errorf("repo = %v, want %v", got.Repository, testCase.want)
			}
		})
	}
}

func TestCurrentBranch(test *testing.T) {
	for _, testCase := range []struct {
		name    string
		output  string
		err     error
		want    string
		wantErr bool
	}{
		{"on a branch", "feat/x", nil, "feat/x", false},
		{"detached head", "", nil, "", false},
		{"not a checkout", "", errors.New("fatal"), "", true},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			run := func(_ context.Context, _ string, arguments ...string) (string, error) {
				if strings.Join(arguments, " ") != "branch --show-current" {
					test.Errorf("arguments = %v", arguments)
				}
				return testCase.output, testCase.err
			}
			got, err := CurrentBranch(context.Background(), run, "/work")
			if (err != nil) != testCase.wantErr || got != testCase.want {
				test.Errorf("got %q, %v; want %q, error %v", got, err, testCase.want, testCase.wantErr)
			}
		})
	}
}

func TestExecRunnerScrubsTokens(test *testing.T) {
	test.Setenv("GH_TOKEN", "secret")
	test.Setenv("GITHUB_TOKEN", "secret")
	test.Setenv("GH_ENTERPRISE_TOKEN", "secret")
	test.Setenv("GITHUB_ENTERPRISE_TOKEN", "secret")
	test.Setenv("STRIG_TEST_KEPT", "kept")

	// A shell alias prints the environment git itself passes to its children.
	directory := test.TempDir()
	output, err := ExecRunner(context.Background(), directory, "-c", "alias.printenvironment=!env", "printenvironment")
	if err != nil {
		test.Fatal(err)
	}
	lines := strings.Split(output, "\n")
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"} {
		for _, line := range lines {
			if strings.HasPrefix(line, name+"=") {
				test.Errorf("%s reached git", name)
			}
		}
	}
	// Setting Env by hand would otherwise leave the inherited, stale PWD.
	if !slices.Contains(lines, "PWD="+directory) {
		test.Errorf("PWD is not %q:\n%s", directory, output)
	}
	if !slices.Contains(lines, "STRIG_TEST_KEPT=kept") {
		test.Errorf("STRIG_TEST_KEPT was dropped:\n%s", output)
	}
}
