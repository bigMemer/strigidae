package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MikePors/strigidae/internal/config"
	"github.com/MikePors/strigidae/internal/userinterface"
)

func writeConfig(test *testing.T, text string) string {
	test.Helper()
	path := filepath.Join(test.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		test.Fatal(err)
	}
	return path
}

// Every command strig handles is refused as a plugin name, with the line of
// the plugin. The names come from the real registries, so a command added to
// the shell or to any view later is covered without touching this test.
func TestPluginNamedLikeABuiltInIsRefused(test *testing.T) {
	names := builtinCommands()
	for _, expected := range []string{"q", "quit", "set", "theme", "approve", "resolve", "merge", "create", "copy-url"} {
		if !slices.Contains(names, expected) {
			test.Fatalf("the built-in commands lack %q: %v", expected, names)
		}
	}
	for _, name := range names {
		test.Run(name, func(test *testing.T) {
			path := writeConfig(test, "plugins:\n  - command: "+name+"\n    on_run: [tool]\n    target: lines\n")
			harness := newHarness(map[string]string{"origin": "git@github.com:o/r.git"}, true, nil)
			var stdout, stderr bytes.Buffer
			if code := run([]string{"--config", path}, &stdout, &stderr, harness.dependencies); code == 0 {
				test.Fatalf("strig started with a plugin named %q", name)
			}
			want := path + ":2: plugin command \"" + name + "\" is already a built-in command"
			if !strings.Contains(stderr.String(), want) {
				test.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
			}
			if harness.application != nil {
				test.Error("the interface was started")
			}
		})
	}
}

func TestPluginsReachTheUserInterface(test *testing.T) {
	path := writeConfig(test, "plugins:\n  - {command: ask, on_run: [tool], target: lines}\n  - {command: summarize, on_run: [tool], target: pull-request}\n")
	harness := newHarness(map[string]string{"origin": "git@github.com:o/r.git"}, true, nil)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--config", path}, &stdout, &stderr, harness.dependencies); code != 0 {
		test.Fatalf("exit code = %d: %s", code, stderr.String())
	}
	got := harness.application.userInterfaceOptions(nil, "")
	if got.PluginRunner == nil || len(got.PluginCommands) != 2 {
		test.Fatalf("runner = %v, commands = %v", got.PluginRunner, got.PluginCommands)
	}
	if got.PluginCommands[0] != (userinterface.PluginCommand{Name: "ask", Target: config.TargetLines}) {
		test.Errorf("first command = %+v", got.PluginCommands[0])
	}
	for _, name := range []string{"ask", "summarize", "merge"} {
		if !slices.Contains(got.ViewCommands, name) {
			test.Errorf("view commands lack %q, so the shell would call it no command", name)
		}
	}
}

// With no plugins nothing changes: no runner (a nil interface, not a typed
// nil), no commands and the same view commands as before.
func TestNoPluginsChangeNothing(test *testing.T) {
	got := application{config: config.Default()}.userInterfaceOptions(nil, "")
	if got.PluginRunner != nil || got.PluginCommands != nil {
		test.Errorf("runner = %v, commands = %v, want none", got.PluginRunner, got.PluginCommands)
	}
	if want := viewCommands(); !slices.Equal(got.ViewCommands, want) {
		test.Errorf("view commands = %v, want %v", got.ViewCommands, want)
	}
}
