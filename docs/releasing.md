# Releasing

How a change that is already on `main` becomes a version: a git tag `vX.Y.Z`, a
GitHub release with notes, and a milestone recording what shipped in it.

Deploying a version is the project's own procedure. When the project has one,
it is described in `docs/deployment.md`, and the release notes link to it.

## 1. Cut a release (developer machine)

```bash
git checkout main && git pull
scripts/cut-release.sh 0.1.0 --dry-run   # prints every step, changes nothing
scripts/cut-release.sh 0.1.0
```

That is the whole procedure. It refuses to start unless you are on a clean `main`
that is level with `origin/main`, the version is higher than every version
already tagged, and that tag does not exist yet. Then it bumps the version files,
commits `Release 0.1.0` (skipped when the files already carry that version),
writes an annotated tag whose message lists the issues in the release, and pushes
`main` and the tag together.

Anything that fails unwinds: no release commit, no local tag, nothing pushed.

The issue list in the tag message comes from the pull requests merged since the
previous tag, via `scripts/lib/release-notes.sh` - the same code
`scripts/release.sh` uses for the release notes, so a tag and its release can
never describe different things. A release holds at least one merged pull
request; with none in the range the script stops. The first release takes
everything from the first commit on.

<details>
<summary>What it does, if you ever need to do it by hand</summary>

```bash
scripts/bump-version.sh 0.1.0          # the version files, none yet
git commit -am "Release 0.1.0"
git tag -a v0.1.0 -m "Release v0.1.0: <one line per change>"
git push --atomic origin main v0.1.0
```

</details>

**If the process is killed outright** - SIGKILL, a closed laptop - it cannot roll
itself back. Check `git log` and `git tag -l`. If a `Release X.Y.Z` commit or a
`vX.Y.Z` tag is there but `git ls-remote origin refs/tags/vX.Y.Z` prints nothing,
the cut never reached origin; undo it with `git tag -d vX.Y.Z` followed by
`git reset --hard origin/main`. If origin does have the tag, the release stands -
`git pull` and carry on.

### Version files

hq has no implementation yet, so no file records the version: the tag is the
only record and `scripts/bump-version.sh` changes nothing. When the stack is
chosen, its manifest becomes the single source of truth and
`scripts/bump-version.sh` writes the version into it.

## 2. Release notes

Pushing the tag is the last thing you do by hand. `.github/workflows/release.yml`
fires on any `v*` tag and runs `scripts/release.sh`, which writes the GitHub
release: the issues closed by the pull requests merged since the previous tag,
grouped by their label.

| Issue label | Section in the notes |
|-------------|----------------------|
| `enhancement` | Nowe funkcje |
| `bug` | Poprawki |
| `internal` | Wewnętrzne, together with pull requests that closed no issue |
| none | Pozostałe |

The notes are in Polish, for the people who read them. The wording sits in one
block at the top of `scripts/release.sh`.

**Deployment prerequisites come from the pull requests.** A pull request whose
change needs work outside the code states it in its own description, in one or
both of these sections (each runs to the next `## ` heading):

```markdown
## Przed wdrożeniem
- IT: consent to `User.Read.All` for the app registration.

## Po wdrożeniu
- `.env`: `DIRECTORY_SYNC_ENABLED=1`, restart.
```

The script collects them from every pull request in the release and places them
in the notes' **Wdrożenie** part: *Przed wdrożeniem* first, *Po wdrożeniu* after
it, each block headed by its pull request. The console summary counts them
(`prerequisites : N before deployment, M after`), so review them in the
`--dry-run` output. A prerequisite missing from a merged pull request is fixed by
editing that pull request's description and re-running the script; there is no
separate list to maintain. A release with no prerequisites and no
`docs/deployment.md` has no **Wdrożenie** part.

The release also creates the milestone `vX.Y.Z`, puts every issue and pull
request in it, closes it, and comments `Wydane w vX.Y.Z - <link>` on each issue.
Milestones are the record of what shipped in which version, so nobody needs to
assign them by hand.

**To re-run it**, from a checkout with the tag:

```bash
scripts/release.sh v0.1.0 --dry-run   # prints every write, changes nothing
scripts/release.sh v0.1.0
```

Re-running is safe: it never comments twice, never creates a second milestone,
and skips the release entirely when the notes have not changed.

**If you edit the notes in the GitHub UI**, keep your text outside the
`<!-- generated -->` … `<!-- /generated -->` markers (the browser saves the
notes with Windows line endings; the script handles that). Everything between them is
replaced on the next run; everything around them is kept. When you re-run only to
refresh the notes after an edit, use `--no-issue-writes` so it publishes the
release and leaves the milestone and the issue comments alone.

**If the workflow fails**, the tag is already pushed - nothing is lost. Read the
run log, fix the cause, and re-run `scripts/release.sh` locally or re-run the
workflow; the result is the same either way.
