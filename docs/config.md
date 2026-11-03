# Configuration

strig reads a YAML file at `$XDG_CONFIG_HOME/strig/config.yml` (falling back to
`~/.config/strig/config.yml`). `strig --config <path>` uses another file, and
`strig --config-path` prints the location that would be used.

A missing file is fine: strig uses the defaults below. A file named with
`--config` must exist. A file with a mistake stops strig before the screen
starts, with the file, line and problem, for example:

    strig: /home/me/.config/strig/config.yml:7: unknown key "limt" in a section (valid keys: name, query, limit, sort, order)

Unknown keys are errors so typos don't pass silently.

Other rules:

- The file must be a single YAML document and at most 1 MiB.
- YAML anchors, aliases (`*name`) and merge keys (`<<`) are not supported and
  are errors.
- `name`, `user` and `query` accept any plain text value, so `user: 12345`
  works; they are trimmed of surrounding spaces. `limit` must be an unquoted
  integer.
- Tab names must be unique, and section names must be unique within a tab.
- For YAML syntax errors the line comes from the YAML parser and can point at
  the start of the enclosing block rather than the exact line of the mistake. A commented example is in
[`config.example.yml`](config.example.yml).

## Shape

```yaml
tabs:
  - name: Me
  - name: Alice
    user: alice
    sections:
      - name: Needs review
        query: is:pr is:open review-requested:{user}
        limit: 50
        sort: updated
        order: desc
```

### `theme`

Optional. A bundled theme's name (`strig --list-themes`) or the path of a
Base16/Base24 scheme file; unset means the built-in theme. A relative path is
taken from the config file's directory, and `~` is expanded. See
[themes.md](themes.md).

### `timeouts`

Optional. How long strig waits for GitHub before giving up on a call.

```yaml
timeouts:
  request: 10s
  load: 30s
```

| Key | Default | Applies to |
|---|---|---|
| `request` | `10s` | Small calls and every write |
| `load` | `30s` | Bigger reads, paged ones as a whole: the diff, a pull request's details, its threads and the pull request lists |

Either key may be left out and keeps its default. Values are Go durations
(`10s`, `1m`, `1m30s`); the unit is required, so `10` is an error. Zero,
negative or unparseable values are a config error at startup, and so is an
empty `timeouts:` (use `{}` or leave the key out). There is no `:set` for these.

### `plugins`

Optional. Commands that run a program of your own, each with a `command` name
(a plain word), an `on_run` list (the program and its arguments), a `target`
(`lines` or `pull-request`), an optional `show` (`thread`, only with
`target: lines`: the answer is drawn in the diff as a local thread, see
[plugins.md](plugins.md)) and an optional `timeout` (default `60s`).
Command names must be unique. Plugins are read only from this file, never from
a repository.

```yaml
plugins:
  - command: ask
    on_run: [strig-ask]
    target: lines
    timeout: 120s
```

Each plugin becomes a command in the pull request workspace (`:ask why is this
slow?`); how to run one is described in [plugins.md](plugins.md). A `command`
that is already a command of strig is an error. The first `on_run` item must be a bare program name,
an absolute path or a `~/` path. The protocol and the rules of a run are in
[plugins.md](plugins.md).

### Options

Optional yes/no keys, written at the top level next to `theme`. Each is also
a `:set` option (`:set nowrapscan`, `:set wrap`, `:set` alone to show
them), which changes it for the running session without touching the file.
The value must be an unquoted YAML boolean: `true` or `false` (`True` and
`TRUE` also work). Quoted values and `yes` / `no` are errors naming the line, for example
`config.yml:3: wrapscan must be true or false`.

| Key | Default | Meaning |
|---|---|---|
| `wrapscan` | `true` | `n` / `N` continue past the last or first match of a search; with `false` they stop and say `no more matches below` / `above` |
| `hlsearch` | `true` | rows matching a search are marked; with `false` they are not (the search and `n` / `N` still work) |
| `wrap` | `false` | long diff lines continue on the next screen rows instead of being cut off (see below) |
| `hideviewed` | `false` | files marked viewed (`v`) leave the Files pane, so what is left still needs review (see below) |
| `hidereviewed` | `false` | hunks marked reviewed (`m`) leave the diff pane, each leaving a one-line placeholder (see below) |
| `autoviewed` | `false` | marking a file's last unreviewed hunk with `m` also marks the file viewed, as `v` would (see below) |
| `hideresolved` | `false` | resolved comment threads leave the diff pane, one you resolve after a short fade (see below) |
| `skipresolved` | `false` | `)` / `(` jump over resolved comment threads, which stay shown and reachable with `j` / `k` (see below) |
| `relativenumber` | `false` | the diff pane numbers every row by its distance from the cursor, for counts with `j` / `k` (see below) |

#### `wrap`

With `wrap: true`, a diff line longer than the diff pane continues on the next
screen rows instead of being cut off at the pane's edge. Continuation rows have
blank line numbers with `↪` in the gutter and keep the line's added or removed
colour. Lines break at the pane's edge,
never inside a wide character. `j`/`k`, counts, `G`, visual selection and
comments still move by diff lines, and `z h` / `z l` / `z H` / `z L` do nothing
while wrapping is on. Only the diff pane wraps.

A line taller than the whole pane (a minified file, say) shows only as much of
itself as fits; the rest cannot be scrolled to while wrapping is on. A pane too
narrow to hold a wide character (a code area of one cell) does not wrap.

#### `hideviewed`

With `hideviewed: true`, files marked viewed are not listed in the workspace's
Files pane; the Overview row always is. Turning it on or off with `:set` takes
effect at once.

Pressing `v` marks the file under the cursor viewed at once (and on GitHub, as
without the option) and moves the cursor to the next listed file. The marked
file stays in its place for 8 seconds, fading step by step into the background,
then disappears. To take it back, press `k` to return to it and `v` again while
it is fading: it is un-marked and stays. Leaving the pull request, or
`:set nohideviewed`, ends the fading; the files stay marked viewed.

The title still counts every file (`Files 3/5 viewed`), `}` and `{` skip hidden
files, and the file picker (`f`) still lists them all. Choosing a hidden file
shows its diff while the Files pane cursor waits on the nearest listed file.

#### `hidereviewed`

With `hidereviewed: true`, hunks marked reviewed with `m` are hidden in the
diff pane. Each leaves one muted row in its place, such as
`▸ reviewed hunk hidden  @@ -40,5 +42,7 @@`. Turning the option on or off with
`:set` takes effect at once.

Pressing `m` marks the hunk at once, and the cursor stays where it is. The
hunk stays for 8 seconds, fading step by step into the background, and then
collapses to its placeholder. A cursor still inside it moves to the
placeholder. To take a mark back, press `m` again while the hunk is fading,
or press `m` on the placeholder: the hunk is un-marked and shown again.
Leaving the pull request, or `:set nohidereviewed`, ends the fading; the hunks
stay marked reviewed.

`]` and `[` (and counts with them) skip hidden hunks. `G` with a count aimed
at a line of a hidden hunk lands on its placeholder. The comment threads on a
hidden hunk's lines are hidden with it, and `)` / `(` skip them; the
Overview's "Review threads" list still shows them. `enter` on a placeholder
does nothing.

#### `autoviewed`

With `autoviewed: true` (or `:set autoviewed`), reviewing the last unreviewed
hunk of a file with `m` marks the file viewed, exactly as pressing `v` would: it
is synced with GitHub and asks nothing. The status bar says
`Marked <path> viewed (all hunks reviewed)`. Unreviewing a hunk of a file strig
marked this way unmarks the file again, on GitHub too, and says
`Marked <path> not viewed (a hunk is no longer reviewed)`. A file you marked
with `v` yourself is never unmarked automatically, and pressing `v` on a file
strig marked hands it back to you. Files with no hunks (binary files, pure
renames) can't have hunks reviewed, so they are never marked this way.

With `hideviewed` on, a file marked this way fades and disappears like any
viewed file, and pressing `v` on it while it fades takes the mark back.

#### `hideresolved`

With `hideresolved: true`, resolved comment threads are left out of the diff
pane, so a review with many settled threads stays readable. Turning the option
on or off with `:set` takes effect at once, for every pull request.

A line whose only threads are hidden keeps its hollow `○` marker in the gutter,
so you can tell a settled discussion was there. `)` / `(` skip hidden threads,
and `z a`, `z o`, `z c` and `enter` on such a line do nothing. The Overview's
"Review threads" list still shows every thread.

A thread you resolve with `x` (or `:resolve`) stays in place for 8 seconds,
fading step by step into the background, then leaves; a cursor still inside
it moves to its line. To take it back, press `x` on it while it is fading and
answer `y` to unresolve it: it stops fading and stays. Threads that were
already resolved when they loaded (by you earlier, or by someone else) are
hidden at once. Leaving the pull request, or `:set nohideresolved`, ends the
fading.

#### `skipresolved`

With `skipresolved: true`, `)` and `(` jump over resolved comment threads and
land only on unresolved ones. A count counts only the threads they land on, so
`3)` goes to the third unresolved thread below. When there is no unresolved
thread in that direction, they show `no unresolved threads` and the cursor
stays. Resolved threads still show, folded, and stay reachable with `j` / `k`.

Only resolved threads are skipped: an outdated thread that is still unresolved
is visited. With `hideresolved` on, resolved threads are hidden and `)` / `(`
already skip them. Turning the option on or off with `:set` takes effect at
once in an open pull request.

#### `relativenumber`

With `relativenumber: true`, the diff pane's gutter gains a column, left of
the old and new line numbers, that numbers every row by its distance from the
cursor: `0` on the cursor row, `1` on the rows next to it, and so on. Every row
the cursor stops on is numbered: diff lines, hunk headers, the placeholders of
hidden hunks, notices and each line of a comment thread. So `5j` moves to the
row marked `5` below. With `wrap` on, a long line's continuation rows have no
number. Rows further than 9999 rows away (the largest count) are left blank.
The numbers are for `j` / `k` outside a line selection: in visual mode (`V`)
`j` / `k` move one diff line at a time and skip thread rows.
The column takes five cells from the code area. `G` with a count still goes to
a new-side line number.

### `tabs`

A non-empty list. Tab names must be unique.

| Key | Required | Meaning |
|---|---|---|
| `name` | yes | Label shown on the tab |
| `user` | no | GitHub login the sections are about. Default `@me`, the signed-in user |
| `sections` | no | The tab's sections. Default: the default sections below |

The same list also gives the tabs of the repository pull request list (shown
inside a checkout, or with `-R`): **All** first, then one tab per entry, in
order, with the repository's open pull requests whose author is the tab's
`user` (`@me` when it has none). Only `name` and `user` are used there;
`sections` are not.

### `sections`

Each section is one PR list driven by a GitHub search query.

| Key | Required | Meaning |
|---|---|---|
| `name` | yes | Section heading |
| `query` | yes | GitHub search query. `{user}` is replaced by the tab's `user` |
| `limit` | no | Most PRs to fetch, 1 to 100. Default 30 |
| `sort` | no | `updated` (default), `created`, `comments`, `reactions` or `interactions` |
| `order` | no | `desc` (default) or `asc` |

`{user}` is the only variable; any other `{...}` in a query is an error.

Queries list pull requests only. If a query has no `is:pr` (or `type:pr`),
strig adds `is:pr` when it loads the file. A query must not contain a `sort:`
qualifier (an error); use the `sort` and `order` keys instead.

## Defaults

With no config file, or a file without `tabs`, strig has one tab, **Me**, with
these sections (`{user}` is `@me`):

| Section | Query |
|---|---|
| Review requested | `is:pr is:open review-requested:{user}` |
| My PRs | `is:pr is:open author:{user}` |
| Involved | `is:pr is:open involves:{user} -author:{user}` |

**Tabs you list replace the default Me tab.** If `tabs` has only a teammate,
you won't get your own dashboard. To keep it, list it first: `- name: Me`.

A tab with no `sections` gets the same three, so a teammate's tab is just
`- name: Alice` plus `user: alice`.
