# strigidae

A terminal UI for reviewing GitHub pull requests, with vim keybindings.

The binary is called `strig`.

> **Status:** pre-alpha. See the [milestones](https://github.com/MikePors/strigidae/milestones) for what is planned, and [CONTRIBUTING.md](CONTRIBUTING.md) to help.

## Building from source

Requires Go (any recent version; the `go` command fetches the exact toolchain pinned in `go.mod`).

```sh
make build        # builds ./bin/strig
./bin/strig --version
make test         # go test -race ./...
make check        # everything CI runs: fmt, lint, test, tidy, govulncheck, actionlint
make lint         # golangci-lint (tools are pinned in tools/<name>/go.mod)
```

Or install directly:

```sh
go install github.com/MikePors/strigidae/cmd/strig@latest
```

## Dashboard

Run `strig` outside a git checkout, or in one with no GitHub remote (and without `-R`), to see the dashboard: one tab per entry in the config (by default a single "Me" tab), each tab showing its sections stacked, with pull requests from any repository. `g t` / `g T` switch tabs, `]` / `[` jump between sections, `enter` opens the pull request, `o` opens it in the browser and `R` refreshes the tab, and `/` searches the rows: type to jump to the first match, `enter` keeps the search (matching rows get a marker), `n` / `N` move to the next / previous match and `esc` clears it. The repository's pull request list and the Files pane of a pull request search the same way. In a pull request, `/` searches whichever pane has focus: the Files pane's paths, or the shown file's diff lines and comment threads (only what is drawn; the cursor row shows `>` instead of the marker); each pane keeps its own query, and showing another file clears the diff's. Inside a checkout with a GitHub remote, or with `-R owner/repo`, strig shows that repository's pull requests instead. That list has tabs too: **All** (every open pull request), then one per entry in the config's `tabs:` (by default **Me**) with the repository's open pull requests by that tab's `user`; the tabs' sections are not used there. `g t` / `g T` switch tabs; each tab loads when first shown, keeps its own cursor and search, and `R` refreshes the tab on screen. A checkout whose remote is on a host strig doesn't know (another forge, or a GitHub Enterprise host you are not signed in to) still exits with an error; pass `-R owner/repo` there.

## Checks, merge state and reviewers

In a pull request, the Overview lists the head commit's checks (failing first) in a "Checks (N)" section, reached with `]` / `[`. `:checks` opens the same list as a picker; `enter` opens the selected check in the browser. The header shows conflicts, "behind base" or "blocked" when one stands in the way of merging, and each reviewer with a mark: `✓` approved, `✗` changes requested, `•` commented, `?` requested and not yet reviewed.

## Editing the title and body

`:edit-pull-request` (in a pull request, whatever pane has focus) opens `$VISUAL` / `$EDITOR` on the pull request's current title and body: the title on the first line, then the body (one blank line after the title is optional). When you close the editor it asks `y/N`, then updates both on GitHub. An empty title is an error, unchanged text says `no changes`, and GitHub must allow you to edit the pull request (`you can't edit this pull request` otherwise).

## Clipboard

`:copy-url` copies the open pull request's URL, and `:copy-filename` the selected file's path, in two ways. It sends the OSC 52 terminal escape, which also works over SSH, and it pipes the text to the first clipboard tool found on `PATH`: `pbcopy` on macOS; on Linux `wl-copy` (when `WAYLAND_DISPLAY` is set), then, when `DISPLAY` is set, `xclip -selection clipboard`, then `xsel --clipboard --input`. With no tool (or no display), only OSC 52 is used; a terminal that ignores OSC 52 then copies nothing. Over SSH the remote machine's tool fills the remote clipboard, so OSC 52 is what reaches your local one: inside tmux add `set -g set-clipboard on` to `~/.tmux.conf`, and allow OSC 52 in your terminal.

## Asking about a line (beefis)

Run inside a beefis pane (a terminal multiplexer whose templates wire panes together) with `strig --beefis` and strig becomes a beefis unit. In a pull request's diff, `:ask <question>` sends the question about the line under the cursor (or the visual selection) to beefis, together with the repository, pull request number, head commit, path, side, line numbers and the diff lines around them. A beefis template routes it to whatever answers (typically a Claude pane) and routes the reply back. The question shows at once below its line as a thread, waiting; the answer fills it in when it arrives, even if the pull request was closed in the meantime. These threads are local to strig: nothing goes to GitHub, and replying, resolving, reacting, editing and deleting refuse them. A question asked on an older head shows as outdated. Answers last until strig quits. The protocol (`ask` event, `answer` command) is described in [docs/beefis.md](docs/beefis.md).

## Review analyses

A review analysis is a JSON file that regroups a pull request's changes for a reader: lenses (ways of looking at the change, such as following the call path, or checking what it claims), each holding segments of related spans of lines, with notes. strig loads `.strig/analysis/<number>.json` (relative to where strig runs) when you open pull request `<number>`, or any file with `:analysis <path>`. `:lens` then shows the first lens in place of the Files list: its segments in reading order, with their spans; moving between spans (`j`/`k`, or `:next-span` and `:previous-span` from the diff) takes the diff to each one. `:lens <id>` switches lens, `:lens` closes it. The format is described in [docs/analysis.md](docs/analysis.md).

## Configuration

strig reads an optional YAML config (tabs and the search queries behind their sections) from `$XDG_CONFIG_HOME/strig/config.yml` (default `~/.config/strig/config.yml`); `--config <path>` overrides it and `strig --config-path` prints the location in use. See [docs/config.md](docs/config.md) and [docs/config.example.yml](docs/config.example.yml). The yes/no options (`wrapscan`, `hlsearch`, `wrap` for long diff lines, `hideviewed` for the Files pane, `hidereviewed` for reviewed hunks, `autoviewed` to mark a file viewed once all its hunks are reviewed, `hideresolved` to leave resolved comment threads out of the diff, `skipresolved` so `)` / `(` jump over resolved threads, and `relativenumber` to number diff rows by distance from the cursor) can be set there or with `:set` while strig runs. The `timeouts` key sets how long strig waits for GitHub: `request` (default 10s) for small calls and writes, `load` (default 30s) for diffs, details, threads and PR lists. Colour themes (bundled ones, or any Base16/Base24 scheme file; `strig --list-themes`; `:theme <name or file>` switches while strig runs) are described in [docs/themes.md](docs/themes.md).

## License

[MIT](LICENSE)
