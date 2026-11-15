# QA evidence for hq

hq's own QA evidence rules, beside the template's `docs/agents/qa-evidence.md`; where the two differ, this file holds for hq. hq is a terminal program, so its checks run against hq as it runs in a terminal, not in a browser.

## Running the checks (steps 2 and 3)

Capture the before state by running hq in the issue worktree before changing code, or in a `dev` worktree. Then run each check against hq started from the issue worktree, driving it in a real terminal the way a person would (`docs/hq/agents.md`, *Local run*). The evidence:

- **Screenshots, in every PR**: the reviewer sees how hq looks, not only what it prints. Every check whose result shows in a terminal (command output, prompts, errors, the dashboard) gets a terminal screenshot, embedded in the PR body. Run those checks in a tmux pane that serves as the terminal (for example an outer server, `tmux -L qa-<n>-term`, whose pane runs a shell) and render the pane with `scripts/qa-screenshot.sh [-L SOCKET] TARGET OUT.png`, colours included. A PR with nothing to show in a terminal (docs, CI only) says so instead.
- **An issue about Windows/WSL, without a Windows machine**: run the checks with `scripts/local-dev.sh --stub native|wsl-linux|wsl-exe|none` (skill `local-dev`), which puts the stub `sbx`, `wslpath`, `cmd.exe` and `powershell.exe` in place of the real ones, in a neutral `HOME` under `/tmp`, and with `--before` builds the before binary from `origin/dev` at the same path as the after one.
- **Dashboard change**: a screenshot of every screen and state the change touches, paired with the before screenshot where the look changed. Name files `<check>-<screen>-before.png` / `-after.png`.
- **File** hq writes: the file itself.
- **Exact output and non-visual behaviour** (command output, exit codes, what hq does to tmux or `sbx`): the command and its output, saved as a `.txt` file or quoted in the PR, next to the screenshot where there is one.

Done when every check has an outcome and evidence, and every check visible in a terminal has a screenshot.

## Before publishing (step 4)

The repository is public, and so is the evidence: before the commit, run `go run ./tools/testgate private "$qa/issue-<n>"` (from the checkout, with the pattern list in place, see `docs/hq/testing.md`, *Private details*) and look at every image yourself. Nothing on the branch may name a private organisation, repository or host, or a local user's path: run QA in a neutral directory, or replace such a detail with a placeholder in a `.txt` and render its image again. CI checks the branch only on its next run on `dev` or a pull request, or the daily one: too late to keep a detail out. The pre-push hook does not guard this push either: the `qa-artifacts` worktree has no `scripts/git-hooks`, so git finds no hook there.

## In the `## QA` section (step 5)

Below the check table, quote `go run ./tools/testgate pyramid` from before and after the change (`docs/hq/testing.md`). A pull request into `dev` also has a row for its CI run (every job green, with a link to the run); a red run blocks the merge. A PR that changes only files the test workflows skip (the header of `.github/workflows/pull-request-tests.yml` lists them) has no run, and the row says so.

Above the full build block stands the mutation block (`docs/hq/testing.md`, *Mutation tests*), under a heading of its own: the command with its base, then its lines as it printed them, every alive or timed-out line followed by its one-line reason, and its last line. The last line names the same commit as the full build's, without ` with uncommitted changes`; after review fixes or a rebase both blocks are replaced with the new runs'. The master pull request of a release and a revert have none.

~~~markdown
### Mutation tests

`go run ./tools/testgate mutation origin/dev`

```text
alive      tools/testgate/push.go:94:14 conditionals boundary
mutation: 23 killed, 1 alive, 0 timed out, 2 not covered against origin/dev on 4f0c2a9e1b7d3c5a8e6f9b0d2c4a6e8f1b3d5c7a
```

- `push.go:94:14`: equivalent: `len(sha) > 7` and `>= 7` both return `sha` unchanged for a sha of 7 characters.
~~~

The full build block ends the section as the template's step 5 says.
