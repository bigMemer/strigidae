package config

import (
	"strings"

	"go.yaml.in/yaml/v3"
)

// KeyMapping puts a command line on a key sequence: pressing it runs the line
// as if it had been typed after ":".
type KeyMapping struct {
	// Key is the sequence, its key names separated by one space ("g a",
	// "ctrl+a").
	Key string
	// Command is the command line, without the ":".
	Command string
	// Line is the mapping's line in the file.
	Line int
}

func (parser *parser) keys(node *yaml.Node, config *Config) {
	config.Keys = uniqueList(parser, node, "keys", parser.keyMapping, "duplicate key %q")
}

func (parser *parser) keyMapping(node *yaml.Node) (KeyMapping, string) {
	mapping := KeyMapping{Line: node.Line}
	parser.mapping(node, "a key mapping", func(name string, node *yaml.Node) {
		switch name {
		case "key":
			mapping.Key = strings.Join(strings.Fields(parser.stringValue(node, "key")), " ")
			if parser.err == nil && isCount(mapping.Key) {
				parser.fail(node, "key %q is a count; map another key", mapping.Key)
			}
		case "command":
			mapping.Command = strings.TrimPrefix(parser.stringValue(node, "command"), ":")
			if parser.err == nil && strings.TrimSpace(mapping.Command) == "" {
				parser.fail(node, "command must be a non-empty text value")
			}
		}
	}, []string{"key", "command"})
	if parser.err == nil && mapping.Key == "" {
		parser.fail(node, "key mapping is missing a key")
	}
	if parser.err == nil && mapping.Command == "" {
		parser.fail(node, "key mapping %q is missing a command", mapping.Key)
	}
	return mapping, mapping.Key
}

// isCount reports whether a sequence starts with a digit 1-9, which the key
// matcher reads as the start of a count.
func isCount(sequence string) bool {
	return sequence[0] >= '1' && sequence[0] <= '9' && (len(sequence) == 1 || sequence[1] == ' ')
}
