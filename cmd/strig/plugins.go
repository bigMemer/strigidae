package main

import (
	"fmt"
	"log/slog"
	"os"
	"slices"

	"github.com/MikePors/strigidae/internal/config"
	"github.com/MikePors/strigidae/internal/plugins"
	"github.com/MikePors/strigidae/internal/userinterface"
)

// builtinCommands are every command name strig itself handles, the shell's
// and each view's. A plugin may not take one. It is built from the same lists
// the shell is given, so a command added to any of them is covered.
func builtinCommands() []string {
	return append(userinterface.CommandNames(), viewCommands()...)
}

// checkPluginCommands refuses a plugin whose command is a built-in one: the
// built-in would always win, so the plugin could never run. The error is
// located at the plugin's line, like the other config errors.
func checkPluginCommands(path string, configured []config.Plugin) error {
	builtin := builtinCommands()
	for _, plugin := range configured {
		if slices.Contains(builtin, plugin.Command) {
			return &config.Error{Path: path, Line: plugin.Line, Message: fmt.Sprintf("plugin command %q is already a built-in command", plugin.Command)}
		}
	}
	return nil
}

// pluginOptions hands the configured plugins to the shell: the runner, the
// commands the views are told about, and their names for the shell's list of
// view commands. With no plugins configured all of it is empty.
func (application application) pluginOptions(logger *slog.Logger) (runner userinterface.PluginRunner, commands []userinterface.PluginCommand, names []string) {
	if len(application.config.Plugins) == 0 {
		return nil, nil, nil
	}
	for _, plugin := range application.config.Plugins {
		commands = append(commands, userinterface.PluginCommand{Name: plugin.Command, Target: plugin.Target, Show: plugin.Show})
		names = append(names, plugin.Command)
	}
	runner = plugins.New(application.config.Plugins, plugins.Options{
		Environment: os.Environ(),
		Logger:      logger,
		Home:        os.UserHomeDir,
	})
	return runner, commands, names
}
