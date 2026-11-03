package config

import (
	"fmt"
	"regexp"

	"go.yaml.in/yaml/v3"

	"github.com/MikePors/strigidae/internal/options"
)

// MaximumFileSize is the largest config file strig will read.
const MaximumFileSize = 1 << 20

// topLevel maps each top-level key to the function that decodes it. Another
// feature adding a key (a theme, merge defaults) adds an entry here and a
// field on Config. Every declared option is a key too.
var topLevel = buildTopLevel()

func buildTopLevel() map[string]func(nodeParser *parser, node *yaml.Node, config *Config) {
	keyParsers := map[string]func(nodeParser *parser, node *yaml.Node, config *Config){
		"plugins":  (*parser).plugins,
		"tabs":     (*parser).tabs,
		"theme":    (*parser).theme,
		"timeouts": (*parser).timeouts,
	}
	for _, option := range options.All() {
		keyParsers[option.Name] = func(nodeParser *parser, node *yaml.Node, config *Config) {
			value := nodeParser.boolean(node, option.Name)
			if nodeParser.err == nil {
				config.Options = config.Options.With(option.Name, value)
			}
		}
	}
	return keyParsers
}

var (
	login       = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\[bot\])?$`)
	placeholder = regexp.MustCompile(`\{[^}]*\}`)
)

// Parse validates data, read from the file named path (used only in errors).
// Empty data yields Default.
func Parse(path string, data []byte) (Config, error) {
	if len(data) > MaximumFileSize {
		return Config{}, &Error{Path: path, Message: fmt.Sprintf("config file is too large (limit %d bytes)", MaximumFileSize)}
	}
	document, err := decodeSingle(path, data)
	if err != nil {
		return Config{}, err
	}
	if document == nil {
		return Default(), nil
	}

	nodeParser := &parser{path: path}
	nodeParser.rejectAliases(document)
	var config Config
	if nodeParser.err == nil {
		nodeParser.mapping(document.Content[0], "the config file", func(key string, node *yaml.Node) {
			topLevel[key](nodeParser, node, &config)
		}, keys(topLevel))
	}
	if nodeParser.err != nil {
		return Config{}, nodeParser.err
	}
	if config.Tabs == nil {
		config.Tabs = Default().Tabs
	}
	return config, nil
}

// theme stores the value as written. Resolving it (bundled theme or file)
// needs the user interface's theme packages, so the main package does that.
func (parser *parser) theme(node *yaml.Node, config *Config) {
	config.Theme = parser.stringValue(node, "theme")
}

func (parser *parser) timeouts(node *yaml.Node, config *Config) {
	parser.mapping(node, "timeouts", func(key string, node *yaml.Node) {
		switch key {
		case "request":
			config.Timeouts.Request = parser.duration(node, "request timeout")
		case "load":
			config.Timeouts.Load = parser.duration(node, "load timeout")
		}
	}, []string{"request", "load"})
}

func (parser *parser) tabs(node *yaml.Node, config *Config) {
	config.Tabs = uniqueList(parser, node, "tabs", parser.tab, "duplicate tab name %q")
}

func (parser *parser) tab(node *yaml.Node) (Tab, string) {
	tab := Tab{User: Me}
	var sections *yaml.Node
	parser.mapping(node, "a tab", func(key string, node *yaml.Node) {
		switch key {
		case "name":
			tab.Name = parser.stringValue(node, "tab name")
		case "user":
			tab.User = parser.stringValue(node, "user")
			if parser.err == nil && tab.User != Me && !login.MatchString(tab.User) {
				parser.fail(node, "user %q is not a GitHub login (letters, digits and hyphens) or %q", tab.User, Me)
			}
		case "sections":
			sections = node
		}
	}, []string{"name", "user", "sections"})
	if parser.err == nil && tab.Name == "" {
		parser.fail(node, "tab is missing a name")
	}
	if parser.err != nil {
		return tab, tab.Name
	}
	if sections == nil {
		tab.Sections = defaultSections()
	} else {
		tab.Sections = uniqueList(parser, sections, "sections", parser.section, "duplicate section name %q in this tab")
	}
	tab.resolve()
	return tab, tab.Name
}

func (parser *parser) section(node *yaml.Node) (Section, string) {
	var section Section
	parser.mapping(node, "a section", func(key string, node *yaml.Node) {
		switch key {
		case "name":
			section.Name = parser.stringValue(node, "section name")
		case "query":
			query := parser.stringValue(node, "query")
			if parser.err != nil {
				return
			}
			normalized, err := normalizeQuery(query)
			if err != nil {
				parser.fail(node, "%s", err)
				return
			}
			section.Query = normalized
		case "limit":
			section.Limit = parser.integer(node, "limit", 1, MaximumLimit)
		case "sort":
			section.Sort = Sort(parser.oneOf(node, "sort", sortNames))
		case "order":
			section.Order = Order(parser.oneOf(node, "order", orderNames))
		}
	}, []string{"name", "query", "limit", "sort", "order"})
	if parser.err == nil && section.Name == "" {
		parser.fail(node, "section is missing a name")
	}
	if parser.err == nil && section.Query == "" {
		parser.fail(node, "section %q is missing a query", section.Name)
	}
	return section, section.Name
}
