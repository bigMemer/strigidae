package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	cliBrowser "github.com/cli/browser"
	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/config"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/github/fake"
	"github.com/MikePors/strigidae/internal/userinterface/keys"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

// harness records what run did with its injected dependencies.
type harness struct {
	dependencies dependencies
	clientHost   *string
	started      *github.Repository
	// application is what start received; nil until the TUI would start.
	application *application
}

// newHarness returns deps whose checkout has the given remotes ("" key
// "toplevel" absent means not a checkout).
func newHarness(remotes map[string]string, checkout bool, clientErr error) *harness {
	testHarness := &harness{clientHost: new(string), started: new(github.Repository)}
	testHarness.dependencies = dependencies{
		getWorkingDirectory: func() (string, error) { return "/work", nil },
		// Directories that don't exist, so tests never read a real config.
		getEnvironment: func(string) string { return "/nonexistent-xdg" },
		home:           func() (string, error) { return "/nonexistent-home", nil },
		runner: func(_ context.Context, _ string, arguments ...string) (string, error) {
			if arguments[0] == "rev-parse" {
				if !checkout {
					return "", errors.New("not a git repository")
				}
				return "/work", nil
			}
			if address, ok := remotes[arguments[2]]; ok {
				return address, nil
			}
			return "", errors.New("no such remote")
		},
		knownHost: func(host string) bool { return host == "github.com" || host == "ghe.example.com" },
		newClient: func(host string) (github.Client, error) {
			*testHarness.clientHost = host
			if clientErr != nil {
				return nil, fmt.Errorf("host %s: %w", host, clientErr)
			}
			return fake.New(), nil
		},
		start: func(application application, _ io.Writer) error {
			*testHarness.started = application.repository
			testHarness.application = &application
			return nil
		},
	}
	return testHarness
}

func TestRunVersion(test *testing.T) {
	var stdout, stderr bytes.Buffer
	harness := newHarness(nil, false, nil)
	if code := run([]string{"--version"}, &stdout, &stderr, harness.dependencies); code != 0 {
		test.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "strig ") {
		test.Fatalf("stdout = %q, want prefix %q", stdout.String(), "strig ")
	}
}

func TestRunUnknownFlag(test *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--nope"}, &stdout, &stderr, newHarness(nil, false, nil).dependencies); code != 2 {
		test.Fatalf("exit code = %d, want 2", code)
	}
}

func TestRunPullRequestNumber(test *testing.T) {
	remotes := map[string]string{"origin": "git@github.com:o/r.git"}
	for _, testCase := range []struct {
		name      string
		arguments []string
		checkout  bool
		want      int
		wantCode  int
		wantError string
	}{
		{"none", nil, true, 0, 0, ""},
		{"number", []string{"189"}, true, 189, 0, ""},
		{"with a hash", []string{"#189"}, true, 189, 0, ""},
		{"after -R outside a checkout", []string{"-R", "o/r", "7"}, false, 7, 0, ""},
		{"zero", []string{"0"}, true, 0, 2, `unexpected argument "0"`},
		{"negative", []string{"-1"}, true, 0, 2, ""},
		{"two numbers", []string{"1", "2"}, true, 0, 2, `unexpected argument "2": give at most one pull request number`},
		{"no repository", []string{"7"}, false, 0, 2, "pull request 7: no repository here (run strig in a checkout, or pass -R owner/repo)"},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			var stdout, stderr bytes.Buffer
			harness := newHarness(remotes, testCase.checkout, nil)
			if code := run(testCase.arguments, &stdout, &stderr, harness.dependencies); code != testCase.wantCode {
				test.Fatalf("exit code = %d, want %d (stderr: %s)", code, testCase.wantCode, stderr.String())
			}
			if testCase.wantCode != 0 {
				if !strings.Contains(stderr.String(), testCase.wantError) || harness.application != nil {
					test.Errorf("stderr = %q, want %q; started = %v", stderr.String(), testCase.wantError, harness.application != nil)
				}
				return
			}
			if harness.application.pullRequest != testCase.want {
				test.Errorf("pull request = %d, want %d", harness.application.pullRequest, testCase.want)
			}
		})
	}
}

func TestRunPositionalArgument(test *testing.T) {
	var stdout, stderr bytes.Buffer
	harness := newHarness(map[string]string{"origin": "git@github.com:o/r.git"}, true, nil)
	if code := run([]string{"cli/cli"}, &stdout, &stderr, harness.dependencies); code != 2 {
		test.Fatalf("exit code = %d, want 2", code)
	}
	if want := `strig: unexpected argument "cli/cli" (did you mean -R cli/cli?)`; !strings.Contains(stderr.String(), want) {
		test.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	if !harness.started.IsZero() {
		test.Error("TUI started despite the stray argument")
	}
}

func TestSilenceBrowser(test *testing.T) {
	oldOut, oldErr := cliBrowser.Stdout, cliBrowser.Stderr
	test.Cleanup(func() { cliBrowser.Stdout, cliBrowser.Stderr = oldOut, oldErr })
	var out, errOut bytes.Buffer
	cliBrowser.Stdout, cliBrowser.Stderr = &out, &errOut

	silenceBrowser()
	if cliBrowser.Stdout != io.Discard || cliBrowser.Stderr != io.Discard {
		test.Fatal("browser output not discarded")
	}
}

func TestRunHelp(test *testing.T) {
	var stdout, stderr bytes.Buffer
	harness := newHarness(nil, false, nil)
	for _, flagName := range []string{"-h", "--help"} {
		if code := run([]string{flagName}, &stdout, &stderr, harness.dependencies); code != 0 {
			test.Errorf("%s exit code = %d, want 0", flagName, code)
		}
	}
	for _, want := range []string{"-log-path", "-config-path", "-config path", "-R owner/repo"} {
		if !strings.Contains(stderr.String(), want) {
			test.Errorf("usage should mention %q: %q", want, stderr.String())
		}
	}
}

func TestRunLogPath(test *testing.T) {
	test.Setenv("XDG_STATE_HOME", "/xdg/state")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--log-path"}, &stdout, &stderr, newHarness(nil, false, nil).dependencies); code != 0 {
		test.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if got, want := stdout.String(), "/xdg/state/strig/strig.log\n"; got != want {
		test.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunRepositorySelection(test *testing.T) {
	origin := "git@github.com:fork/widgets.git"
	upstream := "https://ghe.example.com/acme/widgets.git"
	tests := []struct {
		name           string
		arguments      []string
		remotes        map[string]string
		checkout       bool
		clientErr      error
		wantCode       int
		wantRepository string
		wantHost       string
		wantClient     bool // newClient should have been called
		wantStderr     string
		wantDashboard  bool // the first view is the dashboard, with no repository
		wantNotice     string
	}{
		{
			name: "inferred from origin", remotes: map[string]string{"origin": origin}, checkout: true,
			wantRepository: "fork/widgets", wantHost: "github.com", wantClient: true,
		},
		{
			name: "upstream wins and sets host", remotes: map[string]string{"origin": origin, "upstream": upstream}, checkout: true,
			wantRepository: "acme/widgets", wantHost: "ghe.example.com", wantClient: true,
		},
		{
			name: "gitlab upstream falls back to github origin", remotes: map[string]string{"origin": origin, "upstream": "git@gitlab.com:acme/widgets.git"}, checkout: true,
			wantRepository: "fork/widgets", wantHost: "github.com", wantClient: true,
		},
		{
			name: "ssh alias host is named in the error", remotes: map[string]string{"origin": "github-work:acme/app.git"}, checkout: true,
			wantCode: 1, wantStderr: `remote host "github-work" isn't a GitHub host gh is signed in to; pass -R owner/repo or run gh auth login --hostname <host>`,
		},
		{
			name: "two unknown hosts are quoted individually", remotes: map[string]string{"upstream": "git@gitlab.com:o/r.git", "origin": "github-work:acme/app.git"}, checkout: true,
			wantCode: 1, wantStderr: `remote host "gitlab.com", "github-work" isn't a GitHub host`,
		},
		{
			name: "-R overrides inference", arguments: []string{"-R", "o/r"}, remotes: map[string]string{"origin": origin}, checkout: true,
			wantRepository: "o/r", wantHost: "", wantClient: true,
		},
		{
			name: "-R works outside a checkout", arguments: []string{"-R", "o/r"},
			wantRepository: "o/r", wantClient: true,
		},
		{
			name: "-R invalid", arguments: []string{"-R", "nonsense"}, checkout: true,
			wantCode: 2, wantStderr: "invalid repository",
		},
		{
			name:       "outside a checkout starts the dashboard",
			wantClient: true, wantDashboard: true,
		},
		{
			name: "checkout without a remote starts the dashboard with a note", checkout: true,
			wantClient: true, wantDashboard: true, wantNotice: "no GitHub remote here; showing the dashboard",
		},
		{
			name: "-R in a checkout without a remote shows that repository", arguments: []string{"-R", "o/r"}, checkout: true,
			wantRepository: "o/r", wantClient: true,
		},
		{
			name: "not authenticated", remotes: map[string]string{"origin": origin}, checkout: true,
			clientErr: github.ErrNotAuthenticated,
			wantCode:  1, wantHost: "github.com", wantClient: true, wantStderr: "not signed in to github.com: run `gh auth login --hostname github.com`",
		},
		{
			name: "client failure", remotes: map[string]string{"origin": origin}, checkout: true,
			clientErr: errors.New("boom"),
			wantCode:  1, wantHost: "github.com", wantClient: true, wantStderr: "boom",
		},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			harness := newHarness(testCase.remotes, testCase.checkout, testCase.clientErr)
			var clientCalled bool
			inner := harness.dependencies.newClient
			harness.dependencies.newClient = func(host string) (github.Client, error) { clientCalled = true; return inner(host) }

			var stdout, stderr bytes.Buffer
			code := run(testCase.arguments, &stdout, &stderr, harness.dependencies)
			if code != testCase.wantCode {
				test.Fatalf("exit code = %d, want %d (stderr: %q)", code, testCase.wantCode, stderr.String())
			}
			if !strings.Contains(stderr.String(), testCase.wantStderr) {
				test.Errorf("stderr = %q, want it to contain %q", stderr.String(), testCase.wantStderr)
			}
			if clientCalled != testCase.wantClient {
				test.Errorf("client constructed = %v, want %v", clientCalled, testCase.wantClient)
			}
			if testCase.wantClient && *harness.clientHost != testCase.wantHost {
				test.Errorf("client host = %q, want %q", *harness.clientHost, testCase.wantHost)
			}
			if got := harness.started.String(); testCase.wantRepository != "" && got != testCase.wantRepository {
				test.Errorf("started on %q, want %q", got, testCase.wantRepository)
			}
			if testCase.wantDashboard {
				if harness.application == nil || !harness.application.dashboard || !harness.started.IsZero() {
					test.Errorf("want the dashboard without a repository, got %+v", harness.application)
				}
				if got := harness.application.notice; got != testCase.wantNotice {
					test.Errorf("notice = %q, want %q", got, testCase.wantNotice)
				}
				return
			}
			if harness.application != nil && harness.application.dashboard {
				test.Error("dashboard chosen although a repository was resolved")
			}
			if testCase.wantRepository == "" && !harness.started.IsZero() {
				test.Errorf("TUI started on %v despite failure", *harness.started)
			}
		})
	}
}

func TestRunConfig(test *testing.T) {
	directory := test.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			test.Fatal(err)
		}
		return path
	}
	good := write("good.yml", "tabs:\n  - name: Alice\n    user: alice\n")
	bad := write("bad.yml", "tabs:\n  - name: A\n    usr: x\n")
	repository := []string{"-R", "o/r"}

	tests := []struct {
		name       string
		arguments  []string
		wantCode   int
		wantStdout string
		wantStderr string
		wantTabs   []string
	}{
		{"config-path default", []string{"--config-path"}, 0, "/nonexistent-xdg/strig/config.yml\n", "", nil},
		{"config-path honours --config", []string{"--config", good, "--config-path"}, 0, good + "\n", "", nil},
		{"no file means defaults", repository, 0, "", "", []string{"Me"}},
		{"valid file", append([]string{"--config", good}, repository...), 0, "", "", []string{"Alice"}},
		{"explicit missing file", append([]string{"--config", filepath.Join(directory, "nope.yml")}, repository...), 1, "", "no such file", nil},
		{"invalid file names file, line and key", append([]string{"--config", bad}, repository...), 1, "", bad + `:3: unknown key "usr"`, nil},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			harness := newHarness(nil, false, nil)
			var tabs []string
			harness.dependencies.start = func(application application, _ io.Writer) error {
				for _, tab := range application.config.Tabs {
					tabs = append(tabs, tab.Name)
				}
				return nil
			}
			var stdout, stderr bytes.Buffer
			if code := run(testCase.arguments, &stdout, &stderr, harness.dependencies); code != testCase.wantCode {
				test.Errorf("exit code = %d, want %d (stderr: %s)", code, testCase.wantCode, stderr.String())
			}
			if stdout.String() != testCase.wantStdout {
				test.Errorf("stdout = %q, want %q", stdout.String(), testCase.wantStdout)
			}
			if !strings.Contains(stderr.String(), testCase.wantStderr) {
				test.Errorf("stderr = %q, want it to contain %q", stderr.String(), testCase.wantStderr)
			}
			if diff := cmp.Diff(testCase.wantTabs, tabs); diff != "" {
				test.Errorf("tabs (-want +got):\n%s", diff)
			}
		})
	}
}

// deadlineClient records the deadline of each pull request load it receives.
type deadlineClient struct {
	*fake.Client
	deadlines *[]time.Time
}

func (client deadlineClient) record(ctx context.Context) {
	if deadline, ok := ctx.Deadline(); ok {
		*client.deadlines = append(*client.deadlines, deadline)
	}
}

func (client deadlineClient) ListPullRequests(ctx context.Context, repository github.Repository, pageRequest github.PageRequest) (github.Page[github.PullRequest], error) {
	client.record(ctx)
	return client.Client.ListPullRequests(ctx, repository, pageRequest)
}

func (client deadlineClient) SearchPullRequests(ctx context.Context, query string, pageRequest github.PageRequest) (github.Page[github.PullRequest], error) {
	client.record(ctx)
	return client.Client.SearchPullRequests(ctx, query, pageRequest)
}

// runCommand runs command and any commands batched inside it.
func runCommand(command tea.Cmd) {
	if command == nil {
		return
	}
	if batch, ok := command().(tea.BatchMsg); ok {
		for _, inner := range batch {
			runCommand(inner)
		}
	}
}

func TestFirstViewUsesConfiguredTimeouts(test *testing.T) {
	// The default load timeout is 30s, so an hour proves the override arrived.
	configured := config.Config{Timeouts: config.Timeouts{Load: time.Hour}}
	tests := []struct {
		name      string
		dashboard bool
	}{
		{"pull request list", false},
		{"dashboard", true},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			var deadlines []time.Time
			configured.Tabs = config.Default().Tabs
			view := firstView(application{
				client:     deadlineClient{Client: fake.New(), deadlines: &deadlines},
				repository: github.Repository{Owner: "o", Name: "r"},
				dashboard:  testCase.dashboard,
				config:     configured,
				theme:      theme.Default(),
			})
			started := time.Now()
			runCommand(view.Init())
			if len(deadlines) == 0 {
				test.Fatal("no pull request load was made")
			}
			for _, deadline := range deadlines {
				if deadline.Sub(started) < 30*time.Minute {
					test.Errorf("load deadline is %v away, want about an hour", deadline.Sub(started))
				}
			}
		})
	}
}

func TestFirstView(test *testing.T) {
	tests := []struct {
		name        string
		application application
		want        string
	}{
		{"dashboard outside a checkout", application{client: fake.New(), dashboard: true, config: config.Default()}, "Dashboard"},
		{"pull request list inside one", application{client: fake.New(), repository: github.Repository{Owner: "o", Name: "r"}}, "o/r"},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			testCase.application.theme = theme.Default()
			if got := firstView(testCase.application).Title(); got != testCase.want {
				test.Errorf("first view title = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestDashboardShowsConfiguredTeammateTab(test *testing.T) {
	path := filepath.Join(test.TempDir(), "config.yml")
	body := "tabs:\n  - name: Me\n  - name: Rivka\n    user: rivka\n    sections:\n      - {name: Needs review, query: 'review-requested:{user}'}\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		test.Fatal(err)
	}
	harness := newHarness(nil, false, nil) // not a checkout
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--config", path}, &stdout, &stderr, harness.dependencies); code != 0 {
		test.Fatalf("exit code = %d (stderr: %s)", code, stderr.String())
	}
	view := firstView(*harness.application)
	view.Update(keys.Message{Sequence: "g t"})
	if out := ansi.Strip(view.Render(80, 10)); !strings.Contains(out, "Needs review") || !strings.Contains(out, "review-requested:rivka") {
		test.Errorf("teammate tab does not show its section:\n%s", out)
	}
}

func TestRunBeefis(test *testing.T) {
	remotes := map[string]string{"origin": "git@github.com:o/r.git"}
	for _, testCase := range []struct {
		name     string
		session  string
		wantCode int
	}{
		{"outside a beefis pane", "", 2},
		{"in a beefis pane", "0b5c-session", 0},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			var stdout, stderr bytes.Buffer
			harness := newHarness(remotes, true, nil)
			getEnvironment := harness.dependencies.getEnvironment
			harness.dependencies.getEnvironment = func(name string) string {
				if name == "BEEFIS_SESSION" {
					return testCase.session
				}
				return getEnvironment(name)
			}
			if code := run([]string{"--beefis"}, &stdout, &stderr, harness.dependencies); code != testCase.wantCode {
				test.Fatalf("exit code = %d, want %d (stderr: %s)", code, testCase.wantCode, stderr.String())
			}
			if testCase.wantCode != 0 {
				if !strings.Contains(stderr.String(), "not inside a beefis pane") || harness.application != nil {
					test.Errorf("stderr = %q, started = %v", stderr.String(), harness.application != nil)
				}
				return
			}
			if harness.application == nil || !harness.application.beefis {
				test.Error("started without beefis on")
			}
		})
	}
}

func TestClipboardToolGetsScrubbedEnvironment(test *testing.T) {
	options := clipboardOptions([]string{"PATH=/bin", "GH_TOKEN=secret", "GITHUB_TOKEN=secret", "HOME=/h"})
	if diff := cmp.Diff([]string{"PATH=/bin", "HOME=/h"}, options.Environment); diff != "" {
		test.Errorf("environment (-want +got):\n%s", diff)
	}
}
