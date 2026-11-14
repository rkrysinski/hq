# Releasing

How a change that is already on `dev` becomes a version: a git tag `vX.Y.Z`, a
GitHub release with notes, and a milestone recording what shipped in it.

hq's branches, version, release binaries, installer and checksums, and releasing without GitHub Actions: `docs/hq/releasing.md`, which holds for hq where it differs from this file.

Deploying a version is the project's own procedure. When the project has one,
it is described in `docs/deployment.md`, and the release notes link to it.

## 1. Cut a release (developer machine)

```bash
git checkout dev && git pull
scripts/cut-release.sh 0.1.0 --dry-run   # prints every step, changes nothing
scripts/cut-release.sh 0.1.0
```

That is the whole procedure. It refuses to start unless you are on a clean `dev`
that is level with `origin/dev`, the version is higher than every version
already tagged, and that tag does not exist yet. Then it bumps the version files,
commits `Release 0.1.0` (skipped when the files already carry that version),
writes an annotated tag whose message lists the issues in the release, and pushes
`dev` and the tag together.

Anything that fails unwinds: no release commit, no local tag, nothing pushed.

### Release branch

A release built on a `release-vX.Y.Z` branch (`docs/agents/issue-tracker.md`,
*Working a release*) is bumped by the branch's own last commit
(`scripts/bump-version.sh X.Y.Z`), and that commit is the candidate Release QA
tests. On such a branch the script runs in release mode:

```bash
scripts/cut-release.sh 1.2.3 --no-push --dry-run
scripts/cut-release.sh 1.2.3 --no-push   # tags HEAD as v1.2.3, pushes nothing
git push origin v1.2.3                   # the operator's step: publishes the release
```

The branch must be clean and level with its origin counterpart, as `dev` is.
It makes no release commit: `bump-version.sh` must find nothing to change, and
when it would change a file the branch was not bumped and the script refuses.
It tags `HEAD`. A local `v1.2.3` already at `HEAD` is kept, so a second run
succeeds; one at another commit is an old candidate and is refused with both
commits named: delete it (`git tag -d v1.2.3`) to tag the new one. Without
`--no-push` it pushes the branch and the tag together, as on `dev`.

The issue list in the tag message comes from the pull requests merged since the
previous tag, via `scripts/lib/release-notes.sh` — the same code
`scripts/release.sh` uses for the release notes, so a tag and its release can
never describe different things. A release holds at least one merged pull
request; with none in the range the script stops. The first release takes
everything from the first commit on. A spec (label `prd`) gets no line in the
tag message, as in the notes; its tickets do.

<details>
<summary>What it does, if you ever need to do it by hand</summary>

```bash
scripts/bump-version.sh 0.1.0          # VERSION, and the stack's own version files
git commit -am "Release 0.1.0"
git tag -a v0.1.0 -m "Release v0.1.0: <one line per change>"
git push --atomic origin dev v0.1.0
```

</details>

**If the process is killed outright** — SIGKILL, a closed laptop — it cannot roll
itself back. Check `git log` and `git tag -l`. If a `Release X.Y.Z` commit or a
`vX.Y.Z` tag is there but `git ls-remote origin refs/tags/vX.Y.Z` prints nothing,
the cut never reached origin; undo it with `git tag -d vX.Y.Z` followed by
`git reset --hard origin/dev`. If origin does have the tag, the release stands —
`git pull` and carry on.

### Version order

`scripts/version.sh` is the definition of version order and of the next version
number: `sort` (tags on stdin, in version order, anything that is not
`vX.Y.Z` dropped), `latest [--remote <name>]` and `next-patch|next-minor|next-major <tag>`.
`cut-release.sh`, `release.sh` and the release skills call it; nothing compares
versions inline, because BSD `sort` on macOS and GNU `sort` differ.

### Version files

`VERSION` at the repository root is the single source of truth, whatever the
stack: `scripts/bump-version.sh` writes it and the running app reads it. When the
stack records the version in its own files too (`pyproject.toml` and `uv.lock`
on the Python stack, `package.json`, `pom.xml`), `scripts/sync-version.sh`
copies it there; `bump-version.sh` runs it when it exists. A project that
records its version in another file adds that file to `scripts/sync-version.sh`
(creating the script if the stack has none).

## 2. Release notes

Pushing the tag is the last thing you do by hand. `.github/workflows/release.yml`
fires on any `v*` tag and runs `scripts/release.sh`, which writes the GitHub
release: the issues closed by the pull requests merged since the previous tag,
grouped by their label. A pull request closes the issues its description names
with a closing keyword (`Closes #<n>`, `Fixes #<n>`, ...), as GitHub reads it:
a keyword inside an HTML comment, a code span or a fenced code block closes
nothing, here and in `scripts/check-pr.sh`.

| Issue label | Section in the notes |
|-------------|----------------------|
| `enhancement` | New features |
| `bug` | Fixes |
| `internal` | Internal, together with pull requests that closed no issue |
| none | Other |

The notes are in English, like everything hq writes on GitHub (`docs/hq/releasing.md`). The wording sits in one
block at the top of `scripts/release.sh`.

A pull request may close several issues (a spec pull request closes the spec and
every ticket): each ticket lands under its own type label, and an issue without a
type label appears under *Other*. The spec itself (label `prd`) gets no line in
the notes, even with a type label; it still gets the milestone and the comment.
The master pull request of a supervised release (head `release-vX.Y.Z`) is left
out, with the issues it closes: it is merged into `dev` after its tag, so its merge
commit would otherwise land in the next release; the ticket pull requests merged
into the release branch are listed as usual. Any other branch whose name starts
with `release-` is an ordinary branch.

**Deployment prerequisites come from the pull requests.** A pull request whose
change needs work outside the code states it in its own description, in one or
both of these sections (each runs to the next `## ` heading, a `---` rule, or the
`🤖 Generated with` attribution line, whichever comes first):

```markdown
## Before deployment
- Admin: grant the service account read access to the new queue.

## After deployment
- `.env`: `EXAMPLE_FEATURE_ENABLED=1`, restart.
```

These sections hold only steps the operator or IT performs to deploy, because
the script copies them verbatim into the release notes. Follow-up work, plans and
"next steps" go in issues, never in these sections.

A ticked step (`- [x]`) is done: it was performed before the tag, under
supervision, so it is not a step of the deploy and the script leaves it out of
the notes, with the lines indented under it. A pull request whose steps are all
ticked adds nothing to the section. Untick a step to bring it back.

The script collects the sections from every pull request in the release and places them
in the notes' **Deployment** part: *Before deployment* first, *After deployment* after
it, each block headed by its pull request. The console summary counts them
(`prerequisites : N before deployment, M after`), so review them in the
`--dry-run` output. A prerequisite missing from a merged pull request is fixed by
editing that pull request's description and re-running the script; there is no
separate list to maintain. A release with no prerequisites and no
`docs/deployment.md` has no **Deployment** part.

The `Pull request` workflow guards these sections before merge: `scripts/check-pr.sh`
fails a pull request whose prerequisite section is empty, commented out, or under a
heading this script would not read (another level, another case, a trailing colon).
A pull request with a prerequisite section also needs `## Rollback`: one line per
rollback step, or `None: one-way door`. On a branch named
`release-vX.Y.Z` the check also allows a closing keyword (`Closes #<n>`) only under `## Closes`
(locally: `scripts/check-pr.sh --release-pr ...`).

The release also creates the milestone `vX.Y.Z`, puts every issue and pull
request in it, closes it, and comments `Released in vX.Y.Z - <link>` on each issue.
Milestones are the record of what shipped in which version, so nobody needs to
assign them by hand. A supervised release is the exception: its plan creates the
milestone first (`docs/agents/issue-tracker.md`, *Working a release*, *Milestone*).

**To re-run it**, from a checkout with the tag:

```bash
scripts/release.sh v0.1.0 --dry-run   # prints every write, changes nothing
scripts/release.sh v0.1.0
```

Re-running is safe: it never comments twice, never creates a second milestone,
and skips the release entirely when the notes have not changed.

**Before a supervised release**, check the script on real pull requests: dry-run
it over the last published release. `/release-prepare` runs it as a preflight
check: a row of the *Preflight* table in `docs/agents/supervisor.md`.

```bash
scripts/release.sh v1.2.2 --dry-run   # the last published tag
```

The dry run rebuilds that release's notes from today's pull request bodies and
compares them with the published ones. `release v1.2.2 is already up to date`
means they agree. `would update release v1.2.2 with:` means they differ: compare
the printed notes with the published notes before you tag. When an issue or pull
request is listed in one and not the other, find out why before you release,
because the next notes, milestone and "Released in" comments would repeat a defect.
Three causes are not defects:

- text someone wrote by hand between the `generated` markers (move it outside them)
- a description edited after that release
- a rule the template's release notes announce as changed since then

What is left is a defect in the script or in how a description reads: report it
against the template, or fix the description of a pull request that is not
released yet. The check reads only that one release, so it can miss a defect
whose case is not in it.

**If you edit the notes in the GitHub UI**, keep your text outside the
`<!-- generated -->` … `<!-- /generated -->` markers (the browser saves the
notes with Windows line endings; the script handles that). Everything between them is
replaced on the next run; everything around them is kept. When you re-run only to
refresh the notes after an edit, use `--no-issue-writes` so it publishes the
release and leaves the milestone and the issue comments alone.

**If the workflow fails**, the tag is already pushed — nothing is lost. Read the
run log, fix the cause, and re-run `scripts/release.sh` locally or re-run the
workflow; the result is the same either way.
