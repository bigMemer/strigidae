package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/MikePors/strigidae/internal/config"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/gitrepository"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
	"github.com/MikePors/strigidae/internal/userinterface/theme/scheme"
	"github.com/MikePors/strigidae/internal/userinterface/theme/terminalpalette"
)

// application is everything the TUI needs, resolved before it starts.
type application struct {
	client     github.Client
	repository github.Repository
	// scheme is the theme the config chose, or nil for the built-in one.
	scheme *scheme.Scheme
	// terminalTheme is set for `theme: terminal`: the palette is asked of
	// the terminal when the terminal interface starts.
	terminalTheme bool
	// theme is built from scheme when the terminal interface starts, after
	// logging is set up, because building a theme can log contrast warnings.
	theme theme.Theme
	// dashboard is set when strig runs without -R outside a checkout, or in one
	// with no GitHub remote: there is no repository to list, so the first view
	// is the dashboard of config.
	dashboard bool
	// pullRequest is the number given on the command line, opened on start;
	// zero for none.
	pullRequest int
	// notice is shown in the status bar when the dashboard starts, to say why
	// it is showing.
	notice string
	// config is the loaded config file: the dashboard's tabs and sections, and
	// the yes/no options it sets.
	config config.Config
	// checkout is the checkout strig was started in, for plugin requests.
	checkout userinterface.Checkout
	// currentBranch is the checkout's branch, empty with -R, on a detached
	// head or outside a checkout.
	currentBranch string
}

// newApplication resolves the repository to show and a client for its host.
func newApplication(repositoryFlag string, configuration config.Config, dependencies dependencies) (application, error) {
	target, err := resolveTarget(repositoryFlag, dependencies)
	// With no repository there is no host either: the client uses the gh default.
	// A checkout with no GitHub remote has nothing to list either, so it gets
	// the dashboard too, with a note saying why.
	noRemote := repositoryFlag == "" && errors.Is(err, gitrepository.ErrNoGitHubRemote)
	dashboard := repositoryFlag == "" && (errors.Is(err, gitrepository.ErrNotCheckout) || noRemote)
	notice := ""
	if noRemote {
		notice = "no GitHub remote here; showing the dashboard"
	}
	if err != nil && !dashboard {
		return application{}, err
	}
	client, err := dependencies.newClient(target.Host)
	if errors.Is(err, github.ErrNotAuthenticated) {
		return application{}, &signInError{host: target.Host, err: err}
	}
	if err != nil {
		return application{}, err
	}
	return application{client: client, repository: target.Repository, theme: theme.Default(), dashboard: dashboard, notice: notice, config: configuration, currentBranch: checkoutBranch(repositoryFlag, dashboard, dependencies), checkout: workingCheckout(dependencies)}, nil
}

// workingCheckout is the checkout strig was started in, for plugin requests
// about its repository; the zero value outside one. Unlike the current branch
// it is kept with -R: the root compares it with each request's repository.
func workingCheckout(dependencies dependencies) userinterface.Checkout {
	directory, err := dependencies.getWorkingDirectory()
	if err != nil {
		return userinterface.Checkout{}
	}
	remote, err := gitrepository.Infer(context.Background(), dependencies.runner, directory, dependencies.knownHost)
	if err != nil {
		return userinterface.Checkout{}
	}
	top, err := gitrepository.TopLevel(context.Background(), dependencies.runner, directory)
	if err != nil {
		return userinterface.Checkout{}
	}
	return userinterface.Checkout{Directory: top, Host: remote.Host, Repository: remote.Repository}
}

// checkoutBranch is the branch of the checkout strig was started in, which
// the pull request list preselects as the head of a new pull request. With -R
// the checkout need not be the repository shown, so there is none.
func checkoutBranch(repositoryFlag string, dashboard bool, dependencies dependencies) string {
	if repositoryFlag != "" || dashboard {
		return ""
	}
	directory, err := dependencies.getWorkingDirectory()
	if err != nil {
		return ""
	}
	branch, err := gitrepository.CurrentBranch(context.Background(), dependencies.runner, directory)
	if err != nil {
		return ""
	}
	return branch
}

// resolveTarget decides which repository to show: -R if given, else the
// current checkout's remote. Outside a checkout it fails with
// gitrepository.ErrNotCheckout.
func resolveTarget(repositoryFlag string, dependencies dependencies) (gitrepository.Remote, error) {
	if repositoryFlag != "" {
		repository, err := github.ParseRepository(repositoryFlag)
		if err != nil {
			return gitrepository.Remote{}, &usageError{fmt.Sprintf("-R: %v", err)}
		}
		// No host: the client uses the gh default (GH_HOST or github.com).
		return gitrepository.Remote{Repository: repository}, nil
	}
	directory, err := dependencies.getWorkingDirectory()
	if err != nil {
		return gitrepository.Remote{}, err
	}
	return gitrepository.Infer(context.Background(), dependencies.runner, directory, dependencies.knownHost)
}

// buildTheme makes the theme to draw with. Problems that don't stop a theme
// loading are logged here. For `theme: terminal` it asks the terminal for its
// palette; when that fails it falls back to the built-in theme and returns a
// notice for the status bar saying why.
func (application application) buildTheme(logger *slog.Logger, askTerminal func() (terminalpalette.Reply, error)) (built theme.Theme, notice string) {
	if application.terminalTheme {
		built, err := terminalTheme(askTerminal)
		if err != nil {
			logger.Warn("theme terminal: using the built-in theme", "err", err)
			return theme.Default(), "theme terminal: " + err.Error() + "; using the built-in theme"
		}
		return built, ""
	}
	if application.scheme == nil {
		return theme.Default(), ""
	}
	for _, warning := range application.scheme.Warnings {
		logger.Warn("theme", "name", application.scheme.Name, "warning", warning)
	}
	return application.scheme.Theme(), ""
}

func terminalTheme(askTerminal func() (terminalpalette.Reply, error)) (theme.Theme, error) {
	reply, err := askTerminal()
	if err != nil {
		return theme.Theme{}, err
	}
	return reply.Theme()
}
