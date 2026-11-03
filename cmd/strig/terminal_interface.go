package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"
	cliBrowser "github.com/cli/browser"

	"github.com/MikePors/strigidae/internal/clipboard"
	"github.com/MikePors/strigidae/internal/environment"
	"github.com/MikePors/strigidae/internal/logging"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/theme/terminalpalette"
	"github.com/MikePors/strigidae/internal/userinterface/views/dashboard"
	"github.com/MikePors/strigidae/internal/userinterface/views/pullrequestlist"
	"github.com/MikePors/strigidae/internal/userinterface/views/workspace"
	"github.com/MikePors/strigidae/internal/version"
)

// runTerminalInterface starts the interactive program. Logs go to a file because the TUI
// owns the terminal; if the file can't be opened we warn once and carry on
// with logging discarded.
func runTerminalInterface(application application, stderr io.Writer) error {
	logger, closeLog, err := logging.Start(os.Getenv, os.UserHomeDir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "strig: logging disabled: %v\n", err) // best effort, before the TUI starts
	}
	defer closeLog()
	logging.Install(logger)
	silenceBrowser()
	logger.Info("starting", "version", version.String(), "repo", application.repository.String())

	built, notice := application.buildTheme(logger, askTerminal)
	application.theme = built
	model := userinterface.New(firstView(application), application.theme, application.userInterfaceOptions(logger, notice))
	if err := runProgram(tea.NewProgram(model), model); err != nil {
		logger.Error("program failed", "err", err)
		return fmt.Errorf("run: %w", err)
	}
	logger.Info("exiting")
	return nil
}

// userInterfaceOptions is what the shell starts with: the logger, the startup
// notice, and the options the config file set.
func (application application) userInterfaceOptions(logger *slog.Logger, notice string) userinterface.Options {
	runner, commands, names := application.pluginOptions(logger)
	// Without a state directory local threads live for the session only.
	stateDirectory, err := logging.StateDirectory(os.Getenv, os.UserHomeDir)
	if err != nil {
		logger.Warn("local threads are not kept between runs", "err", err)
	}
	return userinterface.Options{
		StateDirectory: stateDirectory,
		Logger:         logger, Notice: notice, StartingOptions: application.config.Options,
		ViewCommands: append(viewCommands(), names...),
		PluginRunner: runner, PluginCommands: commands,
	}
}

// viewCommands collects the command names every view package declares, so the
// shell can tell a command another view owns from a word that is no command.
// A view with commands adds its Commands here.
func viewCommands() []string {
	return append(workspace.Commands(), pullrequestlist.Commands()...)
}

// askTerminal asks the controlling terminal for its palette, before the
// program takes over the terminal.
func askTerminal() (terminalpalette.Reply, error) {
	controllingTerminal, err := terminalpalette.Open()
	if err != nil {
		return terminalpalette.Reply{}, err
	}
	reply, err := terminalpalette.Ask(controllingTerminal, os.Getenv("TMUX") != "", time.Now)
	return reply, errors.Join(err, controllingTerminal.Close())
}

// firstView is the dashboard when there is no repository (outside a checkout,
// or in one with no GitHub remote), else the repository's pull request list.
func firstView(application application) userinterface.View {
	if application.dashboard {
		return dashboard.New(application.client, application.config.Tabs, application.theme, dashboard.Options{Notice: application.notice, Timeouts: application.timeouts(), Clipboard: systemClipboard()})
	}
	return pullrequestlist.New(application.client, application.repository, application.config.Tabs, application.theme, pullrequestlist.Options{PullRequest: application.pullRequest, Timeouts: application.timeouts(), Clipboard: systemClipboard(), CurrentBranch: application.currentBranch})
}

// systemClipboard is the real clipboard tool lookup.
func systemClipboard() clipboard.Copier {
	return clipboard.New(clipboardOptions(os.Environ()))
}

// clipboardOptions gives the tool the same scrubbed environment as every
// program strig starts.
func clipboardOptions(variables []string) clipboard.Options {
	return clipboard.Options{
		Platform:       runtime.GOOS,
		GetEnvironment: os.Getenv,
		LookPath:       exec.LookPath,
		Environment:    environment.Scrub(variables),
	}
}

// timeouts hands the config file's deadlines to the views; a field the file
// left out is zero, which selects the view's default.
func (application application) timeouts() userinterface.Timeouts {
	return userinterface.Timeouts{Request: application.config.Timeouts.Request, Load: application.config.Timeouts.Load}
}

// silenceBrowser stops browser launchers writing to the terminal the TUI owns.
// When no launcher is configured, go-gh falls back to cli/browser, which wires
// xdg-open and friends to these package globals (os.Stdout/os.Stderr by
// default) regardless of the writers given to browser.New.
func silenceBrowser() {
	cliBrowser.Stdout = io.Discard
	cliBrowser.Stderr = io.Discard
}

// runProgram runs the program and then ends the plugin runs, on every path
// out of Run: a signal ends Bubble Tea without the model seeing a quit.
func runProgram(program *tea.Program, model *userinterface.Model) error {
	model.SetSend(program.Send)
	_, err := program.Run()
	model.Close()
	return err
}
