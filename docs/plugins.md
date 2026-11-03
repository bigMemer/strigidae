# Command plugins

A plugin adds a command to strig that runs a program of your own. strig sends
the program a description of what you are pointing at, and the program replies
with what it would like strig to show or do. Plugins suggest and strig
decides: strig gives a plugin no GitHub token and writes nothing for it, and
anything it asks for goes through strig's own rules. A plugin is still your own
program, running as you, so it can do what you can (it could run `gh` itself,
for example). Add only programs you trust.

This page describes the configuration, how to run a plugin, and the protocol
between strig and the program.

## Where plugins come from

Plugins are read **only** from your own config file: the default location, or
the file named with `--config` (see [config.md](config.md)). strig never reads
plugin definitions from a repository, a checkout or a pull request, so what you
review cannot make strig run a command.

## Configuration

```yaml
plugins:
  - command: ask            # becomes :ask
    on_run: [strig-ask]     # the program and its arguments
    target: lines           # lines or pull-request
    show: thread            # optional: draw the answer in the diff
    timeout: 120s           # optional, default 60s
```

| Key | Required | Meaning |
|---|---|---|
| `command` | yes | The plugin's name, which becomes a command of that name. A plain word: a letter, then letters, digits and `-`. Names must be unique |
| `on_run` | yes | A non-empty list: the program, then its arguments. There is no shell, so quoting and variables are not interpreted. The program is a bare name found on `PATH`, an absolute path, or a path starting with `~/` (your home directory); any other path with a `/` is a config error (see below) |
| `target` | yes | What the command acts on: `lines` or `pull-request` |
| `show` | no | Where the answer is drawn. The only value is `thread`, and only with `target: lines`: see "Answers as local threads". Without it the answer is a notice |
| `timeout` | no | How long one run may take, as a Go duration such as `90s` or `2m`. Above zero. Default `60s` |

Unknown keys are errors, like everywhere in the config. So is a `command` that
is already a command of strig itself (`approve`, `merge`, `q`, ...): the
built-in would always win, so the plugin could never run. strig stops at
startup and names the file and line.

A program written as a relative path such as `scripts/ask.sh` or `./ask` would
resolve against strig's working directory, usually the checkout under review,
so a file from that repository could run. That is why the config does not
accept it: use a bare name on `PATH`, or an absolute or `~/` path. Arguments
after the program are not restricted.

## Running a plugin

Each plugin is a `:` command in the pull request workspace, named by its
`command`, with whatever you type after the name as its arguments:

```
:ask why does this retry forever?
:summarize
```

Plugin commands exist only in the workspace; on a list or the dashboard strig
says `not available here: ask`. They have no keys.

- `target: pull-request` works from any pane, as soon as the pull request has
  loaded.
- `target: lines` acts on the diff lines. With the diff pane on screen it uses
  the line under the cursor, or the lines of a `V` selection: press `:` while
  the selection is open (cancelling the command line with `esc` leaves the
  selection as it was). The selection ends when the plugin takes it. During a
  selection the commands of the workspace (the built-ins such as `:resolve`
  and `:review`, and `pull-request` plugins) are refused with `end the
  selection to run :<name>`; strig's own `:q`, `:qa`, `:set` and `:theme`
  still run. `:` opens the command line during a selection only if a `lines`
  plugin is configured. Without a
  diff line under the cursor the command says `move onto a diff line to run
  :ask`, and in a narrow terminal showing the Files pane, `focus the diff to
  run :ask`. Until the diff has loaded: `diff not loaded`.

Until the pull request has loaded, every plugin command says `pull request not
loaded yet`, as the built-in commands do.

While a run of the pull request on screen is under way, or waiting behind
another run of the same plugin, the status bar shows its usual spinner. What
the plugin prints appears in the status bar when it finishes: a `notice` as
information, an `error` (and a failed run) as an error, and an `answer` as a
notice. A plugin with `show: thread` (below) differs: its answer, and its
failure (an `error` effect, a failed run or a timeout), go to the thread, and
an `error` printed beside an `answer` also goes to the status bar. A result
belongs to the pull request it was asked from: if that pull request
(repository and number) is no longer the one on screen, the result is dropped
and only noted in the log (a local thread is still filled, and keeps the
result for when the pull request is opened again). A new head commit does not
drop it: the
result still shows and `reload` still runs; the request carries the `head` the
plugin was run at.
Closing the pull request does not stop a run; quitting strig does: it kills
the plugin processes, waits at most two seconds for them to end, and exits.
This holds however strig ends (`:qa`, SIGTERM, SIGINT, or an error).

A plugin runs in a session of its own, so it has no controlling terminal: a
program that opens `/dev/tty` fails instead of reading your keys or drawing on
the screen. Its standard input is the request, its standard output the
effects, and its standard error goes to strig's log.

## Answers as local threads

A `lines` plugin with `show: thread` draws its question and answer in the diff
on the lines it was asked about, as a comment thread. It is a configuration
error to give `show` to a `pull-request` plugin.

- **When you run it** the thread appears at once, even if the run waits behind
  another of the same plugin. The first comment, by `you`, is what you typed
  after the command name, shown exactly as typed and not as Markdown (just
  `:ask` when you typed nothing). The second, by
  `answer (local, not on GitHub)`, reads `waiting for an answer…`, muted.
- **An `answer` effect** fills the second comment as Markdown, drawn like any
  comment on GitHub. Several `answer` effects in one run are joined with a
  blank line.
- **No answer.** An `error` effect (with no `answer`), a failed run or a
  timeout replace the waiting line with `no answer: <the reason>`, in the error
  style. A run that prints neither says `no answer: the plugin gave no answer`.
  If it prints both an `answer` and an `error`, the thread shows the answer.
- `notice` and `reload` effects of the run work as for any plugin, and an
  `error` printed beside an `answer` also goes to the status bar.
- The thread is **local to strig**. It is never sent to GitHub and is gone when
  strig quits. `r`, `x`, `:resolve`, `:unresolve`, `e`, `D`, `:edit-comment`,
  `:delete-comment` and `:react` on it say `this answer is local to strig, not
  on GitHub: nothing to <action>` and do nothing. It is never resolved, never a
  draft, and not counted among the pull request's threads or unresolved
  threads. Folding (`z a`), `)` / `(`, `:set hideresolved`, `skipresolved` and
  search treat it like any other thread.
- Threads are kept by repository and pull request number for as long as strig
  runs, so an answer that arrives while the pull request is closed is there
  when you open it again.
- **Outdated.** A thread remembers the head commit it was asked on. When the
  diff on screen is of another head, it is marked `[outdated]`, muted, and
  listed after the last row of its file with its location, as GitHub's
  outdated threads are; when the diff is of that head again it is back on its
  lines.
- The answer and the reason are the plugin's text: they are cleaned of
  terminal escapes, like any text from a plugin.

## What strig sends

strig starts the program, writes one JSON object to its standard input, followed
by a newline, and closes it. The newline means a shell script can read the
whole request with a plain `read -r line`, also under `set -e`. The object
looks like this for a `lines` plugin:

```json
{
  "protocol": 1,
  "command": "ask",
  "arguments": "why does this retry forever?",
  "repository": {"host": "github.com", "owner": "acme", "name": "widgets"},
  "number": 7,
  "head": "4f9c0e1d...",
  "target": {
    "path": "internal/sync/client.go",
    "side": "RIGHT",
    "line": 16,
    "startLine": 14,
    "excerpt": " ctx := ...\n+func (c *Client) Sync() {\n+\treturn nil\n+}"
  }
}
```

| Field | Meaning |
|---|---|
| `protocol` | The version of this protocol, `1` |
| `command` | The plugin's `command`, as typed |
| `arguments` | The rest of the command line, one string, trimmed at both ends (empty when there is none) |
| `repository` | `host` (taken from the pull request's address, so GitHub Enterprise works), `owner` and `name` |
| `number` | The pull request number |
| `head` | The head commit the pull request had when the command was run. For `lines`, the commit the diff on screen belongs to |
| `target` | Absent for `target: pull-request` plugins. For `lines`: |
| `target.path` | The file's path in the pull request |
| `target.side` | `RIGHT` for lines of the new file (added and unchanged lines), `LEFT` for the old file (a selection containing a deleted line) |
| `target.line` | The last selected line, a line number on `side` |
| `target.startLine` | The first selected line; equal to `line` for one line |
| `target.excerpt` | The selected lines and up to three lines of the same hunk before and after them, joined by `\n`. Each line starts with `+` (added), `-` (deleted) or a space (unchanged), then the line's text |

For a `target: pull-request` plugin the object is the same without `target`:

```json
{"protocol": 1, "command": "summarize", "arguments": "", "repository": {"host": "github.com", "owner": "acme", "name": "widgets"}, "number": 7, "head": "4f9c0e1d..."}
```

A plugin should ignore fields it does not know. A plugin does not have to read
its input; one that exits without reading it is not an error in itself.

## What the plugin prints

The plugin prints JSON lines on standard output. Each line is one *effect*:

```json
{"name": "notice", "data": {"text": "Looks fine"}}
```

| Effect | Data | Meaning |
|---|---|---|
| `notice` | `{"text": ...}` | A short message for the status bar |
| `error` | `{"text": ...}` | A failure and its reason. With a failure exit status the run fails with this text; with exit 0 the effect is returned like any other |
| `answer` | `{"text": ...}` | An answer to show to the user: a notice, or the answer of a local thread with `show: thread` |
| `reload` | `{}` | Reload the pull request on screen, as `R` does |

Rules:

- Effects are handled in the order they were printed. Blank lines are skipped.
- An effect with a name strig does not know is logged and skipped, so a newer
  plugin works with an older strig.
- A line that is not a JSON object with a `name` fails the run. So does a known
  effect with bad `data`: `data` may be left out (or `null`) and otherwise must
  be an object, and `notice`, `error` and `answer` need a non-empty string
  `text` in it. `reload` needs no data.
- `error` is an effect like the others: a run can print one and still exit 0.
- Text in effects can hold anything the program printed, including terminal
  escape sequences. strig cleans it where it is shown.
- At most 1 MiB of output is read. Past that the program is killed and the run
  fails.

Standard error is written to strig's log file (`strig --log-path` prints where)
and never shown on the screen, one log record per line with the plugin's name,
so the text cannot pass as a log line of strig's own. Only the first 64 KiB of
a run's standard error is kept, followed by a note; the rest is dropped.

## Exit status

A program that exits 0 has succeeded, and its effects are used. A program that
exits with any other status has failed: if it printed an `error` effect (the
first one, before any line that is not an effect) the run fails with that
text, otherwise with a short fixed message. Its other effects are discarded.

## Timeout

The timeout is firm. It starts when the program starts, not while the run waits
its turn (see below), and when it passes the program is killed and the run
fails.

Only the program strig started is killed. Anything it started in turn keeps
running, so a plugin that wants to leave a helper running (a daemon, a
background session) can. That also means a child the plugin started outlives
it when strig quits or kills it (a shell script that runs `sleep 30` and exits
leaves the `sleep`; use `exec sleep 30` to have the program itself be killed).
Because the plugin has a session of its own, closing the terminal (SIGHUP) does
not stop a running plugin either.

## One run at a time

Runs of one plugin go one at a time, in the order they were asked for; the
others wait. A run that is cancelled while it waits leaves without running.
Different plugins do not wait for each other.

## The environment

The program inherits strig's environment and working directory, with the GitHub
token variables (`GH_TOKEN`, `GITHUB_TOKEN`, `GH_ENTERPRISE_TOKEN`,
`GITHUB_ENTERPRISE_TOKEN`) removed.

## Daemons a plugin starts

strig stops reading a moment after the program exits, even if something the
program started still has its standard output open, so a leftover helper cannot
hang strig. That wait lasts about a second, and whatever is printed to standard
output during it still counts: a line that is not an effect fails the run, and
too much output hits the limit. So **a daemon a plugin starts must redirect its
output and detach** (for example `nohup helper >/dev/null 2>&1 &`, or `setsid`),
so it neither keeps strig's pipes open nor adds output to the run. Output
printed after that wait is not read.

Once the program has exited by itself, its exit status decides the run, even if
the timeout or a cancel arrives during that wait.
