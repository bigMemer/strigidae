# CLAUDE.md

Guidance for anyone (human or agent) working on **strigidae**, a terminal UI for reviewing GitHub pull requests with vim keybindings. The binary is `strig`.

Read this file before starting an issue, plus the area guide for the code you'll touch:

- [`internal/userinterface/CLAUDE.md`](internal/userinterface/CLAUDE.md): Bubble Tea conventions, keybindings, rendering and golden tests
- [`internal/github/CLAUDE.md`](internal/github/CLAUDE.md): the GitHub API boundary, errors, fixtures

If something here is wrong or missing, say so in your PR rather than silently working around it.

## Ground rules

- **Clean-room.** Design from the issue's requirements. Do not copy code, docs, keymaps, or config formats from other GitHub TUIs, and do not name other GitHub TUIs in code, comments, docs, issues, or PRs.
- **One issue → one branch → one PR.** Stay inside the issue's scope. Unrelated problems you notice go in the PR description under "Follow-ups", not in the diff.
- **Never push to `main`.** Every change lands through a pull request, and the maintainer merges.
- **`make check` must pass** before you push. It is exactly what CI runs.
- If the issue is ambiguous in a way that changes user-visible behaviour (keys, layout, wording, config format), stop and ask rather than guess.
- **Simplest first version.** Build the smallest thing that meets the acceptance criteria. Special cases and polish go under "Follow-ups" in the PR; they become their own issues if wanted.

## Stack

| Concern | Choice |
|---|---|
| Language | Go (version pinned in `go.mod`; the `go` command fetches the toolchain) |
| TUI | Bubble Tea v2 `charm.land/bubbletea/v2`, widgets `charm.land/bubbles/v2`, styling `charm.land/lipgloss/v2`, markdown `charm.land/glamour/v2` |
| Syntax highlighting | `github.com/alecthomas/chroma/v2` |
| GitHub auth + API | `github.com/cli/go-gh/v2` (reuses the `gh` CLI's login; GraphQL + REST) |
| Test diffs | `github.com/google/go-cmp/cmp` |

The v1 Charm paths (`github.com/charmbracelet/{bubbletea,bubbles,lipgloss,glamour}`) are banned by the linter; `github.com/charmbracelet/x/...` helpers such as `x/ansi` are fine. Prefer the standard library and the libraries above; **any other dependency** needs a one-line justification in the PR description.

## Layout

```
cmd/strig/                              main: flag parsing, wiring real dependencies, starting the program
internal/version/                       build version
internal/github/                        domain types, the Client interface, typed errors     (see its CLAUDE.md)
internal/github/network/                real Client (go-gh); the only package that talks to the network
internal/github/fake/                   in-memory Client for tests and demos
internal/gitrepository/                 infer the GitHub repository (host + owner/name) from a checkout's remotes
internal/diff/                          unified-diff parsing and modelling (pure, no user interface)
internal/config/                        config file loading and validation
internal/options/                       yes/no options shared by the config file and `:set` (non-UI)
internal/editor/                        $EDITOR handoff for writing comments
internal/environment/                   environment for programs strig starts (token variables removed)
internal/beefis/                        strig as a beefis unit: `:ask` questions out, answers in (non-UI)
internal/analysis/                      review analyses (prreview/analysis@1): lenses of a pull request, parsed and validated (non-UI)
internal/logging/                       log file setup
internal/golden/                        golden-file test helper
internal/userinterface/                 app shell, View interface, status bar, command line (see its CLAUDE.md)
internal/userinterface/keys/            keymap registry and multi-key sequence handling
internal/userinterface/theme/           Lip Gloss styles; every colour lives here
internal/userinterface/terminaltext/    sanitising of untrusted text (Clean, CleanBlock); imports no other userinterface package
internal/userinterface/components/      reusable widgets (picker, markdown pane, diff pane, …)
internal/userinterface/views/<view>/    one package per screen: pullrequestlist, workspace, dashboard, …
```

Packages that don't exist yet are created by the issue that first needs them. Dependencies point one way, enforced by `depguard` (`make lint` fails on a violation):

```
cmd/strig ──► userinterface/views/* ──► userinterface/components ──► userinterface ──► userinterface/keys, userinterface/theme
    │              └──────► github, diff, config, editor, gitrepository   (non-UI)
    └──► github/network  (the only importer of the real client)
```

- `internal/userinterface`, `userinterface/keys`, `userinterface/theme` and `userinterface/components` **never import `userinterface/views/*`**. Views may import other views, without cycles.
- Non-UI packages (`github`, `beefis`, `analysis`, `diff`, `config`, `options`, `editor`, `environment`, `gitrepository`, `logging`, `version`) never import UI packages.
- Only `cmd/strig` imports `internal/github/network`.

## Process-wide rules

- **Never write to stdout or stderr while the TUI is running**; it corrupts the screen. Log with `log/slog` to the file under `$XDG_STATE_HOME/strig/` (fallback `~/.local/state/strig/`; `strig --log-path` prints it). Never log tokens, auth headers, or full API response bodies.
- **No global mutable state** apart from build-time values such as `version.Version` and two startup redirections: `internal/logging` points the standard `log`/`slog` default loggers at the log file (so a dependency that logs can't corrupt the screen), and `cmd/strig` sets `github.com/cli/browser`'s `Stdout`/`Stderr` to `io.Discard` (so `xdg-open` can't either). The one runtime exception is the Chroma style registry: `theme.Theme.ChromaStyleName` registers one generated style per theme (Glamour looks code-block styles up by name there). The registry is a plain map that Glamour reads without a lock, so **registry writes and all Glamour rendering must stay on the UI goroutine** (never in a `tea.Cmd`); a mutex does not make it safe.
- **Never hardcode `github.com` as the host.** Take it from the git remote, `-R`, or the auth/config, so GitHub Enterprise can work later.
- **Untrusted text.** Everything from GitHub or git (titles, names, labels, paths, branches, markdown, diff content, error messages) is attacker-controlled and can carry terminal escapes. It goes through `internal/userinterface/terminaltext` before reaching the screen; details in `internal/userinterface/CLAUDE.md`.

## Testing

- `go test ./...` must never touch the network or the real `gh` config. Use `internal/github/fake` or fixtures in a `testdata/` directory next to the test. Scrub tokens and personal data from anything recorded.
- **Table-driven tests** with the standard `testing` package; compare plain data with `cmp.Diff`. Models have unexported fields (which make `cmp.Diff` panic): test them through behaviour or exported accessors.
- What must be tested: parsing and mapping logic, keymap/sequence handling, state transitions in `Update`, and each view's main states (see `internal/userinterface/CLAUDE.md`).
- No `time.Sleep` in tests. Inject clocks and timers.

## Code style

- `make fmt` (gofumpt + goimports) and `make lint` (golangci-lint) are authoritative. A `//nolint` must name the linter and explain why (`//nolint:errcheck // best-effort write to stderr`).
- Handle errors from writes. `_, _ = fmt.Fprintln(stderr, ...)` is acceptable only for best-effort output to stderr.
- Error strings are lower-case with no trailing punctuation. Wrap with context: `fmt.Errorf("load pull request %d: %w", number, err)`.
- Doc comments on every exported identifier and every package. Otherwise comment *why*, not *what*; keep comments sparse.
- Keep functions small and names plain. No `utils`/`helpers`/`common` packages.

## Naming

Every name is spelled out in full English: variables, parameters, struct fields, method receivers, functions, types, constants, exported identifiers, test helpers, package directories and file names. Write `view`, not `v`; `model`, not `m`; `message`, not `msg`; `command`, not `cmd`; `test`, not `t`; `testCase`, not `tt`; `PullRequest`, not `PR`; `Identifier`, not `ID`; `ObjectIdentifier`, not `OID`; `Checks`, not `CI`; `userinterface`, not `ui`.

Exceptions, which keep their short form:
- the integer loop indices `i`, `j`, `k`;
- `err`, `ctx`, `ok`, and the `Err` prefix of sentinel errors and `Err` fields;
- names of formats and protocols: URL, HTTP, JSON, YAML, GraphQL, REST, API, ANSI, SGR, UTF-8, OSC 52, SHA, RGB, OKLCH;
- the words `diff` (and `diffview`) and `Info`, and the binary name `strig` (so `cmd/strig` stays);
- anything GitHub or a library defines: GraphQL field names (`headRefOid`) and the structs decoded from them, JSON and YAML tags, config keys, and identifiers from imported packages (`tea.Cmd`, `tea.Msg`, `key.Binding`);
- user-visible text (keys, commands, messages).

A local or parameter may take the name of its type's package (`theme theme.Theme`, `diff *diff.Diff`) when the function uses nothing else from that package. Where the function does use the package, pick a more specific name (`activeTheme`, `parsedDiff`, `colour`, `pressed`) rather than an abbreviation or an import alias.

## Workflow

1. **Branch** off an up-to-date `main`: `<type>/<issue#>-<short-slug>`, e.g. `feat/6-pr-list`. Types: `feat`, `fix`, `chore`, `docs`, `refactor`, `test`.
2. **Commit** in small, coherent steps with [Conventional Commits](https://www.conventionalcommits.org/) messages and PR titles: `feat(diff): add hunk navigation` (the scope is optional).
3. **Run `make check`** (fmt-check, lint, race tests, tidy-check, govulncheck, actionlint, build). Install `shellcheck` locally for full parity with CI.
4. **Open a PR** against `main` using the template: `Closes #N`; what changed and why; how to try it (exact `strig` invocation and keys) and test notes; screenshots for visible UI changes (every PR that changes what's on screen includes them); new keybindings and dependencies; follow-ups left out of scope.
   The body must start with `Closes #N` for the right issue: merging closes whatever it names, so check it again after editing the body.
5. **Review.** Every PR gets an independent review by a fresh agent with no stake in the code (`/review-pr <N>` in Claude Code; see `.claude/skills/review-pr`) on top of the author's own check. It reports in the terminal; `/review-pr <N> post` also puts the findings on the PR as one comment-only review. Fix findings in new commits. For posted findings, reply in each thread naming the fixing commit and resolve it; leave threads that need the maintainer's decision open.
6. CI must be green. Address review comments with new commits; don't rewrite history on a PR under review. The maintainer merges, with merge commits.

**Definition of done:** the issue's acceptance criteria are met; tests cover the new logic and main view states; `make check` passes; new keybindings appear in the help overlay; README/docs reflect user-facing changes; the PR description is complete. Checks that only the maintainer can do (a mutation against real GitHub, behaviour in a real terminal) are added to [`HUMAN.md`](HUMAN.md) with the issue, the steps and what to report; the maintainer removes them once done.

## Tooling notes

- Dev tools (golangci-lint, govulncheck, actionlint) are pinned in their own modules under `tools/<name>/` and run through the Makefile. Don't add tools to the main `go.mod`. Dependabot doesn't update them; a monthly workflow (`update-tools.yml`) runs `make update-tools` and opens a PR.
- After changing dependencies run `make tidy`.
- **Go toolchain vulnerabilities:** if `govulncheck` flags a **standard library** package fixed in a newer Go release, don't change `go.mod`'s `go`/`toolchain` lines. Stop and report it; the maintainer bumps the toolchain in a separate PR and you merge `main` afterwards. Third-party vulnerabilities are in scope only if your PR added or upgraded that module.
- `gh pr edit` fails with "Projects (classic) is being deprecated" on older `gh`. Update a PR body with `gh api -X PATCH repos/MikePors/strigidae/pulls/<N> -F body=@file.md`.
