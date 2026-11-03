# Human tasks

Checks only the maintainer can do, such as live verification against real GitHub. Each item is removed once it's done.

## Live checks

- [ ] **`Tab` completes on the command line in a real terminal** (#208). Only synthetic key events are tested; terminals differ in what they send for `shift+tab`.
  1. In a pull request list, press `:` then `Tab`. The first command fills in and a one-line list of every command appears just above the command line, the current one highlighted. `Tab` and `shift+tab` should move through it and wrap.
  2. Type `:set no` then `Tab`, and `:theme ` then `Tab`. Option and theme names should cycle. Type a character: the list closes and the match stays.
  3. Open a pull request: `:` `Tab` should now offer its commands (`:approve`, `:merge`, …) and not `:create`. Shrink the terminal to under 30 columns and check the list scrolls with `…` and never wraps.
  4. Report any terminal where `shift+tab` does nothing or types a character.

- [ ] **`:create` and `:create-draft` create pull requests on GitHub** (#173). The `createPullRequest` mutation and the branch listing have only been tested against the fake client and fixtures.
  1. In a test repository, push a branch with at least one commit that differs from `main`. Run `strig` in the checkout and open the pull request list.
  2. Run `:create`. The head picker should open with your current branch under the cursor; `enter`. The base picker should not list that branch and should have the default branch under the cursor; `enter`. The editor opens empty: write a title, a blank line and a body, close it, answer `y`. The new pull request should open in strig and appear in the list; check title, body, head, base and that it is not a draft on github.com.
  3. Push a second branch and run `:create-draft`. No `y/N` should be asked; check on github.com that it is a draft.
  4. Run `strig -R <owner>/<repo>` from outside the checkout (or with `-R`) and run `:create`: no head should be preselected.
  5. `esc` in each picker should cancel quietly; an empty title in the editor should show an error and create nothing.
  6. **Error case:** run `:create` again with the same head as step 2 (a pull request is already open) and, separately, a head branch that has no commits beyond the base (create one with `git branch no-commits main` and push it). The status bar should show GitHub's message. Report the wording.

- [ ] **`:edit-pull-request` edits a pull request's title and body on GitHub** (#174). The `updatePullRequest` mutation and the `viewerCanUpdate` field have only been tested against the fake client and fixtures.
  1. Open a test PR you may edit in strig and run `:edit-pull-request`. The editor should open with the title, a blank line, then the body.
  2. Change both, close the editor, and answer `y`. strig should say `Pull request updated` and the Overview should show the new title and description; check both on github.com.
  3. Run it again and close the editor without changes. It should say `no changes`.
  4. Delete the title line's text and close. It should show an error and send nothing.
  5. Open a pull request you can't edit (someone else's, in a repo where you only have read access) and run the command. It should say `you can't edit this pull request` before opening an editor.

- [ ] **`:copy-filename` and `:copy-url` reach the system clipboard** (#172, #171). Only the fake copier is tested.
  1. On macOS, inside tmux with no `set-clipboard` setting in your tmux config, open a pull request, select a file and run `:copy-filename` and `:copy-url`, then paste elsewhere. Report whether it pasted.
  2. On Linux (Wayland with `wl-copy`, and X11 with `xclip` or `xsel` if you can), do the same inside and outside tmux. Report which tool was used and whether it pasted.
  3. Check nothing was printed on the screen, and `strig --log-path` shows no clipboard error.

- [ ] **`:discard-review` deletes a pending review on GitHub** (#139, merged in #143). The delete call (GraphQL `deletePullRequestReview`) has only been tested against the fake client.
  1. On a test PR, add two or three inline draft comments, so you have a pending review.
  2. Run `:discard-review`. The question should name the number of drafts. Answer `y`.
  3. strig should say `Pending review discarded`, the drafts should disappear from strig, and the pending review should be gone on github.com.
  4. Run `:discard-review` again. It should say `no pending review`.
  5. **Submitted elsewhere:** start a pending review again, run `:discard-review`, and leave the `y/N` open. Submit the review on github.com, then answer `y` in strig. strig re-reads before deleting, so it should say `no pending review` and delete nothing.
  6. **GitHub's answer for a submitted review** (outside strig). The code assumes deleting a review that is no longer pending gives an error other than not-found. Take the node ID of the review you just submitted (`gh api graphql -f query='{repository(owner:"<owner>",name:"<repo>"){pullRequest(number:<N>){reviews(last:1){nodes{id state}}}}}'`), then run `gh api graphql -f query='mutation{deletePullRequestReview(input:{pullRequestReviewId:"<id>"}){clientMutationId}}'`. Report the error GitHub returns.

- [ ] **`:react` adds and removes a reaction on GitHub** (#156). The `addReaction` / `removeReaction` calls have only been tested against the fake client.
  1. On a test PR with a review comment in a thread, open it in strig and move the diff cursor onto the comment's text (not the thread's first line).
  2. Run `:react`. The picker should be titled with the comment's author and list the 8 reactions with their counts. Choose `+1` with `enter`.
  3. strig should say `reacted 👍`, and the comment should show 👍 with a count in strig and on github.com.
  4. Run `:react +1` on the same comment. strig should say `removed 👍`, and the reaction should be gone on github.com.
  5. Run `:react` with the cursor on a diff line. It should say `move onto a comment to react to it`.

- [ ] **Plugin commands in a real terminal** (#200). The runs, the selection and the quit are tested with fake programs and key events, never in a real terminal.
  1. Put a small plugin in your config, for example a program `~/bin/strig-echo` (`chmod +x`) that starts with `#!/bin/sh` and holds `cat > /tmp/strig-request.json; echo '{"name":"notice","data":{"text":"got it"}}'`, and `plugins: [{command: echo, on_run: ["~/bin/strig-echo"], target: lines}]`. Open a pull request, select a file, move onto a diff line and run `:echo hello there`. The status bar should show a spinner briefly and then `got it`. `/tmp/strig-request.json` should have `"arguments":"hello there"`, your repository (with the right `host`), the pull request number and head, the file path, `side`, `line`, `startLine` and an `excerpt` of the line with three lines either side, each starting with `+`, `-` or a space.
  2. Press `V`, `j` twice, then `:` and run `:echo`. The command line should open over the selection; `esc` should close it and leave the selection (try that first). The request should hold `startLine` and `line` of the selection, and the selection should end after the run. Report anything odd about how the selection looks while the command line is open.
  3. Add a second plugin with `target: pull-request` that is a `#!/bin/sh` script ending in `exec sleep 30` (the `exec` matters: strig kills only the plugin process, so a plain `sleep 30` child would outlive it and keep running after you quit); it prints its notice before the `exec`. Run it from the Files pane and the Overview: the spinner should run for the whole time. Run it a second time while the first is still going: it waits its turn (the spinner stays on until both are done). Close the pull request with `:q` meanwhile: nothing should be shown when it finishes, and the plugin process should keep running until it ends (`pgrep -f` the program).
  4. Start the slow plugin and quit with `:qa` during the run: strig should exit at once (well under two seconds) and the plugin process should be gone (`pgrep`). Start two runs (one waiting) and quit: the second must never start.
  5. Make a plugin that runs `cat /dev/tty` (and one that prints `hi` to `/dev/tty`). Run it: it should fail at once (no such device or address), type nothing into strig, and nothing should be drawn over the screen. Report the status bar text.
  6. Make a plugin that prints `{"name":"notice","data":{"text":"\u001b[2J\u001b]0;pwned\u0007x"}}`: the status bar should show harmless text, the screen must not clear and the terminal title must not change.

- [ ] **Plugin answers as local threads in a real terminal** (#201). The thread, its states and its refusals are tested with fake runners and key events, never in a real terminal.
  1. Put a plugin in your config, for example `plugins: [{command: ask, on_run: ["~/bin/strig-ask"], target: lines, show: thread}]`, where `~/bin/strig-ask` is a `#!/bin/sh` script that runs `cat >/dev/null; sleep 3; echo '{"name":"answer","data":{"text":"It is **bounded** by `Retry`."}}'`. Open a pull request, move onto a diff line and run `:ask why does this retry?`. A thread should appear under the line at once: `you` with your question, then `answer (local, not on GitHub)` with `waiting for an answer…` in a muted colour. After three seconds the answer replaces the waiting line, drawn as Markdown. Report how it looks next to a real thread.
  2. Make the script print `{"name":"error","data":{"text":"no model"}}` instead: the second comment should read `no answer: no model` in the error colour. Run with `timeout: 1s` and the sleep: it should read `no answer:` followed by the timeout text.
  3. With the cursor on the thread (header, then each comment), try `r`, `x`, `e`, `D`, `:react +1`, `:resolve`: each says `this answer is local to strig, not on GitHub: nothing to <action>`, asks nothing and opens no editor. On a real thread in the same file, `x` still asks `y/N`.
  4. Run `:ask` while the sleep is running, then `:q` before it ends, and open the same pull request again from the list: the thread should be there and, once the run ends, answered. Quit strig: the answer is gone on the next start (it is kept in memory only).
  5. Push a new commit to the pull request's branch (or use another pull request head), press `R`: the thread should show `[outdated]`, muted, after the last row of its file. Report whether that reads clearly.
  6. Make the script print an answer with terminal escapes (`\u001b[2J\u001b]0;pwned\u0007x`): the screen must not clear and the terminal title must not change.
