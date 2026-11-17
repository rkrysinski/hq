# Supervised releases: this project's facts

Read by the release skills (`/release-prepare`, `/release-implement`, `/release-ship`). The procedure is theirs; this file holds only what differs in this project. A section marked *optional* may be left out, and the skill then uses its own default; a skill that finds a required section missing says so and stops.

## Agent policy

| Setting | Value |
| --- | --- |
| Model for the supervisor | `fable` |
| Effort for the supervisor | `high` |
| Cap per release, USD | `not capped` |
| Cap per agent, USD | `not capped` |

The rows for implementation and the release tester are left out: the skills' defaults apply. Spend is not capped because hq's agents run on a subscription and hq calls no paid API.

## Environment

- The supervisor runs as an hq agent `sup-X.Y.Z` (the release's version) in the repository's sandbox, with hq loaded there (`hq mcp load REPO`).
- The project accepts that every agent in that sandbox sees hq's tools while hq is loaded there.
- The release tester runs in that sandbox too and reaches no real sandbox: its steps use the stub `sbx` (`docs/agents/release-qa.md`).
- A ticket pull request gets the `Pull request tests` run, the full build on macOS and Ubuntu (`docs/hq/testing.md`, *CI*): the supervisor holds its merge until that run is green on the pull request's head, unless the change touches only the paths the workflow's header lists as skipped, which get no run.
- The user pushes the tag.
- A release deploys nothing: people install it with the README's one-liner or `hq update` (`docs/hq/releasing.md`).

## What the operator rules on

Beside the decisions the release skills already send to the operator, three rulings in a supervised release are the operator's (the user's), not the supervisor's:

- **The scope of a review finding.** A review's findings go where the release skills send them; the supervisor moves none of them to "later", a follow-up issue or "not this branch". A finding it thinks should wait (outside this release's code, needing a decision, a risk to the release) goes to the operator, named with that reason, and only the operator's ruling moves it out of the release.
- **An exemption from a written rule.** A rule of the repository's docs holds as written. Waiving it for a case is the operator's ruling: for example a mutation block left out after a docs-only commit, or a surviving mutant let through with a reason that `docs/hq/testing.md`, *What passes*, does not accept.
- **A method that departs from the wording of a decision.** A brief carries out a decision (an option the operator chose, an ADR) as its words say. A method that differs from those words, or narrows or widens them, is the operator's ruling.

The supervisor posts each as an operator decision on the release issue, the way the release skills post an approval, before any brief that acts on it goes out. Only what acts on the ruling waits for the answer; the rest of the brief goes out.

## Preflight

Checks `/release-prepare` runs before the readiness table is built, in this order; each is a command and the condition under which it passes. The identity check (`gh` authenticated with push permission on the repository, the login recorded with the preflight results) is the skills' own and is not repeated here.

| Check | Command | Passes when |
| --- | --- | --- |
| Release notes on the last release | `scripts/release.sh "$(scripts/version.sh latest --remote origin)" --dry-run` | it prints `already up to date`, or each difference from the published notes has a cause that is not a defect (`docs/releasing.md`, *Before a supervised release*). One known cause that is not a defect: origin's history was rewritten on 2026-11-14, and GitHub records the pull requests of a release cut before that date by their old merge commits, so for a last release whose tag was created before 2026-11-14 (`git for-each-ref --format='%(creatordate:short)' refs/tags/vX.Y.Z`) the check also passes when it prints `no merged pull requests found`. For every later release it passes only as above |
| Full build on the `dev` head | `git fetch origin dev && d=$(mktemp -d) && git worktree add --detach "$d" origin/dev && "$d/scripts/check.sh"`, then `git worktree remove --force "$d"` | `scripts/check.sh` exits 0 and its last line names the sha `git rev-parse origin/dev` prints, without ` with uncommitted changes` |
| hq answers | hq's MCP tool `list`, called by the supervisor | it answers with the list of agents, without an error |
| `Tests` on `dev` | `gh run list --branch dev --workflow Tests --limit 1 --json conclusion,headSha,url` | `conclusion` is `success` |
