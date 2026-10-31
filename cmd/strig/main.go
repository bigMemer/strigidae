// Command strig is a terminal UI for reviewing GitHub pull requests.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/MikePors/strigidae/internal/beefis"
	"github.com/MikePors/strigidae/internal/config"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/github/network"
	"github.com/MikePors/strigidae/internal/gitrepository"
	"github.com/MikePors/strigidae/internal/logging"
	"github.com/MikePors/strigidae/internal/userinterface/theme/scheme"
	"github.com/MikePors/strigidae/internal/version"
)

// dependencies are the side effects run needs, injected so tests exercise flag
// handling and repository inference without a checkout, the network or a
// terminal.
type dependencies struct {
	getWorkingDirectory func() (string, error)
	getEnvironment      func(string) string
	home                func() (string, error)
	runner              gitrepository.Runner
	// knownHost reports whether host is a GitHub host gh can talk to.
	knownHost func(host string) bool
	newClient func(host string) (github.Client, error)
	start     func(application application, stderr io.Writer) error
}

func realDependencies() dependencies {
	return dependencies{
		getWorkingDirectory: os.Getwd,
		getEnvironment:      os.Getenv,
		home:                os.UserHomeDir,
		runner:              gitrepository.ExecRunner,
		knownHost:           network.KnownHost,
		newClient: func(host string) (github.Client, error) {
			client, err := network.New(network.Options{Host: host})
			if err != nil {
				return nil, err // not client: a nil *Client would be a non-nil interface
			}
			return client, nil
		},
		start: runTerminalInterface,
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, realDependencies()))
}

func run(arguments []string, stdout, stderr io.Writer, dependencies dependencies) int {
	return report(stderr, execute(arguments, stdout, stderr, dependencies))
}

func execute(arguments []string, stdout, stderr io.Writer, dependencies dependencies) error {
	flags := flag.NewFlagSet("strig", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		_, _ = fmt.Fprintln(flags.Output(), "usage: strig [flags] [pull request number]")
		flags.PrintDefaults()
	}
	showVersion := flags.Bool("version", false, "print version and exit")
	showLogPath := flags.Bool("log-path", false, "print the log file path and exit")
	showConfigPath := flags.Bool("config-path", false, "print the config file path and exit")
	listThemes := flags.Bool("list-themes", false, "print the bundled theme names and exit")
	configFlag := flags.String("config", "", "config file `path` (default: $XDG_CONFIG_HOME/strig/config.yml)")
	beefisFlag := flags.Bool("beefis", false, "be a beefis unit: :ask sends questions about diff lines through beefis and shows the answers (run inside a beefis pane)")
	repositoryFlag := flags.String("R", "", "repository to show as `owner/repo` (default: the current checkout's upstream or origin remote)")
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return &usageError{} // the flag package has printed the problem and usage
	}
	pullRequest, err := pullRequestArgument(flags.Args())
	if err != nil {
		return err
	}

	configPath, configErr := configLocation(*configFlag, dependencies)

	switch {
	case *showVersion:
		return printLine(stdout, "strig "+version.String())
	case *showLogPath:
		path, err := logging.Path(os.Getenv, os.UserHomeDir)
		if err != nil {
			return err
		}
		return printLine(stdout, path)
	case *listThemes:
		return printLine(stdout, strings.Join(append(scheme.Bundled(), scheme.Terminal), "\n"))
	case *showConfigPath:
		if configErr != nil {
			return configErr
		}
		return printLine(stdout, configPath)
	}

	if configErr != nil {
		return configErr
	}
	if *beefisFlag && !beefis.InPane(dependencies.getEnvironment) {
		return &usageError{"--beefis: not inside a beefis pane ($" + beefis.SessionVariable + " is not set)"}
	}
	configuration, err := config.Load(configPath, *configFlag != "")
	if err != nil {
		return err
	}
	chosen, terminalTheme, err := resolveTheme(configuration.Theme, filepath.Dir(configPath), dependencies)
	if err != nil {
		return err
	}
	application, err := newApplication(*repositoryFlag, configuration, dependencies)
	if err != nil {
		return err
	}
	application.scheme = chosen
	application.terminalTheme = terminalTheme
	application.beefis = *beefisFlag
	if pullRequest > 0 && application.dashboard {
		return &usageError{fmt.Sprintf("pull request %d: no repository here (run strig in a checkout, or pass -R owner/repo)", pullRequest)}
	}
	application.pullRequest = pullRequest
	return dependencies.start(application, stderr)
}

// pullRequestArgument is the optional pull request number after the flags,
// 189 or #189; zero when there is none.
func pullRequestArgument(arguments []string) (int, error) {
	switch {
	case len(arguments) == 0:
		return 0, nil
	case len(arguments) > 1:
		return 0, &usageError{fmt.Sprintf("unexpected argument %q: give at most one pull request number", arguments[1])}
	}
	number, err := strconv.Atoi(strings.TrimPrefix(arguments[0], "#"))
	if err != nil || number < 1 {
		return 0, &usageError{fmt.Sprintf("unexpected argument %q (did you mean -R %s?)", arguments[0], arguments[0])}
	}
	return number, nil
}

// resolveTheme loads the theme the config names, or returns nil for the
// built-in one. terminal is set for `theme: terminal`, whose palette is asked
// for when the terminal interface starts. A theme that can't be loaded stops
// strig before the screen starts.
func resolveTheme(value, configDirectory string, dependencies dependencies) (loaded *scheme.Scheme, terminal bool, err error) {
	if value == "" {
		return nil, false, nil
	}
	location, err := scheme.Locate(value, configDirectory, dependencies.home)
	if err != nil {
		return nil, false, fmt.Errorf("theme %q: %w", value, err)
	}
	if location.Terminal {
		return nil, true, nil
	}
	result, err := scheme.Load(location)
	if err != nil {
		return nil, false, fmt.Errorf("theme %q: %w", value, err)
	}
	return &result, false, nil
}

// configLocation is the --config path if given, else the default location.
func configLocation(flagValue string, dependencies dependencies) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	return config.Path(dependencies.getEnvironment, dependencies.home)
}

func printLine(stdout io.Writer, text string) error {
	_, err := fmt.Fprintln(stdout, text)
	return err
}
