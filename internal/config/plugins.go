package config

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// DefaultPluginTimeout is how long a plugin may run when the file sets no
// timeout for it.
const DefaultPluginTimeout = 60 * time.Second

// Target is what a plugin's command acts on.
type Target string

// Supported plugin targets.
const (
	// TargetLines is the diff lines under the cursor or selection.
	TargetLines Target = "lines"
	// TargetPullRequest is the pull request as a whole.
	TargetPullRequest Target = "pull-request"
)

var targetNames = []string{string(TargetLines), string(TargetPullRequest)}

// Show is where a plugin's answer is shown, besides the status bar.
type Show string

// Supported plugin show values.
const (
	// ShowThread draws the question and the answer as a local thread on the
	// asked lines. It needs TargetLines.
	ShowThread Show = "thread"
)

var showNames = []string{string(ShowThread)}

// commandName is a plain word starting with a letter: it becomes a `:command`, so it holds no
// spaces or punctuation.
var commandName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`)

// Plugin is a command that runs a program of the user's choosing. Plugins are
// read only from the user's own config file, never from a repository.
type Plugin struct {
	// Command is the plugin's name; it becomes a command of that name.
	Command string
	// OnRun is the program and its arguments, started once per run.
	OnRun  []string
	Target Target
	// Show, if set, is where the answer is drawn; empty keeps it in the
	// status bar.
	Show Show
	// Timeout bounds one run, from the moment it starts.
	Timeout time.Duration
	// Service, if set, names the long-running process that serves the
	// plugin's runs. Plugins with the same Service share it, and their OnRun.
	Service string
	// Line is the line of the command in the file, for errors about it found
	// after parsing (a name that is also a built-in command).
	Line int
}

func (parser *parser) plugins(node *yaml.Node, config *Config) {
	config.Plugins = uniqueList(parser, node, "plugins", parser.plugin, "duplicate plugin command %q")
	parser.sameServicePrograms(node, config.Plugins)
}

// sameServicePrograms refuses two plugins of one service with different
// programs: the service is one process, started from one on_run.
func (parser *parser) sameServicePrograms(node *yaml.Node, plugins []Plugin) {
	first := map[string]Plugin{}
	for i, plugin := range plugins {
		if plugin.Service == "" {
			continue
		}
		earlier, seen := first[plugin.Service]
		if !seen {
			first[plugin.Service] = plugin
			continue
		}
		if !slices.Equal(earlier.OnRun, plugin.OnRun) {
			parser.fail(node.Content[i], "plugin %q: on_run differs from plugin %q of service %q; a service is one program", plugin.Command, earlier.Command, plugin.Service)
			return
		}
	}
}

func (parser *parser) plugin(node *yaml.Node) (Plugin, string) {
	plugin := Plugin{Timeout: DefaultPluginTimeout}
	var showNode *yaml.Node
	parser.mapping(node, "a plugin", func(key string, node *yaml.Node) {
		switch key {
		case "command":
			plugin.Command, plugin.Line = parser.stringValue(node, "plugin command"), node.Line
			if parser.err == nil && !commandName.MatchString(plugin.Command) {
				parser.fail(node, "plugin command %q must be a plain word (a letter, then letters, digits and hyphens)", plugin.Command)
			}
		case "on_run":
			plugin.OnRun = parser.program(node, "on_run")
			if parser.err == nil && !allowedProgram(plugin.OnRun[0]) {
				parser.fail(node, "on_run program %q must be a bare name found on PATH, an absolute path, or start with ~/", plugin.OnRun[0])
			}
		case "target":
			plugin.Target = Target(parser.oneOf(node, "target", targetNames))
		case "show":
			plugin.Show, showNode = Show(parser.oneOf(node, "show", showNames)), node
		case "timeout":
			plugin.Timeout = parser.duration(node, "plugin timeout")
		case "service":
			plugin.Service = parser.stringValue(node, "plugin service")
			if parser.err == nil && !commandName.MatchString(plugin.Service) {
				parser.fail(node, "plugin service %q must be a plain word (a letter, then letters, digits and hyphens)", plugin.Service)
			}
		}
	}, []string{"command", "on_run", "target", "show", "timeout", "service"})
	if parser.err == nil && plugin.Command == "" {
		parser.fail(node, "plugin is missing a command")
	}
	if parser.err == nil && plugin.OnRun == nil {
		parser.fail(node, "plugin %q is missing on_run", plugin.Command)
	}
	if parser.err == nil && plugin.Target == "" {
		parser.fail(node, "plugin %q is missing a target", plugin.Command)
	}
	if parser.err == nil && plugin.Show != "" && plugin.Target != TargetLines {
		parser.fail(showNode, "plugin %q: show %s needs target lines", plugin.Command, plugin.Show)
	}
	return plugin, plugin.Command
}

// allowedProgram reports whether a program is a bare name (looked up on PATH),
// an absolute path, or under the home directory ("~/..."). A relative path with
// a separator would resolve against the working directory, which is usually
// the checkout under review, so a repository could supply the program.
func allowedProgram(program string) bool {
	if rest, isHome := strings.CutPrefix(program, "~/"); isHome {
		return rest != ""
	}
	return !strings.Contains(program, "/") || filepath.IsAbs(program)
}

// program reads a non-empty list of text items: a program and its arguments.
// The first item, the program, must not be empty.
func (parser *parser) program(node *yaml.Node, what string) []string {
	if node.Kind != yaml.SequenceNode || len(node.Content) == 0 {
		parser.fail(node, "%s must be a non-empty list", what)
		return nil
	}
	items := make([]string, 0, len(node.Content))
	for i, item := range node.Content {
		if i == 0 {
			items = append(items, parser.stringValue(item, what+" program"))
		} else {
			items = append(items, parser.argument(item, what))
		}
		if parser.err != nil {
			return nil
		}
	}
	return items
}

// argument reads one scalar as written. Unlike stringValue it is not trimmed
// and may be empty: an argument is data for the program.
func (parser *parser) argument(node *yaml.Node, what string) string {
	if node.Kind != yaml.ScalarNode || node.Tag == "!!null" {
		parser.fail(node, "every item of %s must be text", what)
		return ""
	}
	return node.Value
}
