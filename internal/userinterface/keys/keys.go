// Package keys is the keymap registry: every key binding the app responds to
// is declared here (globals) or in the view that owns it, using bubbles/key,
// so the help overlay can be generated from one source of truth.
//
// Multi-key sequences are written as space-separated key names in a binding,
// for example key.WithKeys("g g") or key.WithKeys("g f"). The root model
// resolves key presses with a Matcher and delivers the result to the active
// view as a Msg; views test it with key.Matches(msg, binding) and never look
// at raw key strings.
package keys

import "charm.land/bubbles/v2/key"

// Message is delivered to the active view when a key press, or a complete
// sequence, matches a binding that is active in that view. Seq is the key name
// ("j", "ctrl+d") or the space-joined sequence ("g g"). Msg implements
// fmt.Stringer so it works with key.Matches.
//
// Message is not a tea.KeyPressMsg: embedded bubbles components (viewport, ...)
// ignore it. Views drive them through their methods (ScrollDown, GotoTop, ...)
// in response to a Msg and never forward it to the component's Update.
//
// Count is the number typed before the key ("11j" has Count 11); 0 means no
// count. Views read it for movement keys and ignore it elsewhere, so other
// keys act once. Use Repeat to read it.
type Message struct {
	Sequence string
	Count    int
}

// Each runs step once per count (once without a count). It stops early when a
// step leaves position() unchanged, so a count of 9999 against the end of a
// list costs one step, not 9999.
func (message Message) Each(position func() int, step func()) {
	for range message.Repeat() {
		before := position()
		step()
		if position() == before {
			return
		}
	}
}

// Repeat returns how many times the key should act: the count, or 1 without one.
func (message Message) Repeat() int { return max(message.Count, 1) }

// String returns the matched key or sequence.
func (message Message) String() string { return message.Sequence }

// Global holds the bindings reserved across the whole app. Views use them
// through the Msg they receive; the root model itself handles Help and Command.
type Global struct {
	Down, Up         key.Binding
	Top, Bottom      key.Binding
	HalfDown, HalfUp key.Binding
	PageDown, PageUp key.Binding
	// ScrollCursorTop, ScrollCursorMiddle and ScrollCursorBottom scroll the
	// view so the cursor row sits at its top, middle or bottom. They take no count.
	ScrollCursorTop, ScrollCursorMiddle, ScrollCursorBottom key.Binding
	Select                                                  key.Binding
	Open                                                    key.Binding
	Refresh                                                 key.Binding
	Search                                                  key.Binding
	NextMatch, PreviousMatch                                key.Binding
	Cancel                                                  key.Binding
	Help                                                    key.Binding
	// Command opens the command line (":q", ":qa"). The root handles it.
	Command key.Binding
	// Count documents the count prefix ("11j"). It exists only for the help
	// overlay: the Matcher parses counts itself, and Bindings leaves it out.
	Count key.Binding
	// Complete documents Tab on the command line. It exists only for the help
	// overlay: the root takes the key itself while the command line is open,
	// and Bindings leaves it out so tab stays unbound elsewhere.
	Complete key.Binding
}

// DefaultGlobal returns the default vim-style global bindings.
func DefaultGlobal() Global {
	bind := func(keys, helpKey, description string) key.Binding {
		return key.NewBinding(key.WithKeys(keys), key.WithHelp(helpKey, description))
	}
	return Global{
		Down:               bind("j", "j", "down"),
		Up:                 bind("k", "k", "up"),
		Top:                bind("g g", "gg", "top"),
		Bottom:             bind("G", "G", "bottom"),
		HalfDown:           bind("ctrl+d", "ctrl+d", "half page down"),
		HalfUp:             bind("ctrl+u", "ctrl+u", "half page up"),
		PageDown:           bind("ctrl+f", "ctrl+f", "page down"),
		PageUp:             bind("ctrl+b", "ctrl+b", "page up"),
		ScrollCursorTop:    bind("z t", "zt", "cursor line to the top"),
		ScrollCursorMiddle: bind("z z", "zz", "cursor line to the middle"),
		ScrollCursorBottom: bind("z b", "zb", "cursor line to the bottom"),
		Select:             bind("enter", "enter", "open / select"),
		Open:               bind("o", "o", "open in browser"),
		Refresh:            bind("R", "R", "refresh"),
		Search:             bind("/", "/", "search"),
		NextMatch:          bind("n", "n", "next match"),
		PreviousMatch:      bind("N", "N", "previous match"),
		Cancel:             bind("esc", "esc", "cancel / clear"),
		Help:               bind("?", "?", "toggle help"),
		Command:            bind(":", ":", "command line (:q close, :qa quit)"),
		Count:              bind("1", "1-9", "count: 11j, 3], 42G"),
		Complete:           bind("tab", "tab", "complete a word, shift+tab back"),
	}
}

// Bindings returns every global binding as a flat list, for sequence matching.
func (global Global) Bindings() []key.Binding {
	// Search, NextMatch and PreviousMatch are bindings of every view that
	// searches, but they stay reserved here so the matcher and the reserved-key
	// test always know them.
	out := []key.Binding{global.Search, global.NextMatch, global.PreviousMatch}
	for _, bindings := range global.FullHelp() {
		for _, binding := range bindings {
			// Count is documentation only; a bare digit is never a binding.
			if helpKey := binding.Help().Key; helpKey != global.Count.Help().Key && helpKey != global.Complete.Help().Key {
				out = append(out, binding)
			}
		}
	}
	return out
}

// FullHelp groups the global bindings for the help overlay. Search, NextMatch
// and PreviousMatch are left out: only views that support searching list them
// in their own FullHelp.
func (global Global) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{global.Down, global.Up, global.Top, global.Bottom, global.HalfDown, global.HalfUp, global.PageDown, global.PageUp},
		{global.ScrollCursorTop, global.ScrollCursorMiddle, global.ScrollCursorBottom},
		{global.Select, global.Open, global.Refresh},
		{global.Cancel, global.Help, global.Command, global.Complete, global.Count},
	}
}
