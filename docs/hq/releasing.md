# Releasing hq

hq's own release facts, beside the template's `docs/releasing.md`; where the two differ, this file holds for hq.

## Branches

- `dev` is the default branch. Work lands on it through pull requests, and a cut from `dev` tags it.
- A supervised release is built on `release-vX.Y.Z`, forked from `dev`, or from the latest tag for a hotfix release (`docs/agents/issue-tracker.md`, *Working a release*, *Branches*). Its tag is on that branch's candidate, and the master pull request brings the branch back into `dev` with a merge commit after the tag push, so the tagged commit is an ancestor of `dev`.
- A version is its tag `vX.Y.Z` and its GitHub release. The latest version is the release GitHub marks latest, which the install one-liner and `hq update` take (*Binaries, installer and checksums*).

## Two ways to release

| | Cut from `dev` | Supervised release |
|---|---|---|
| When | A small fix: one pull request, or a few unrelated ones, already merged on `dev` | A release of several issues planned together, built as a whole on one release branch |
| Who merges the work | The user, each pull request into `dev` | The skills' merger, each ticket pull request into `release-vX.Y.Z`; the user, the master pull request into `dev`, with a merge commit after the tag push (`gh pr merge <pr> --merge`) |
| Where the tag is | The release commit `scripts/cut-release.sh` makes on `dev`, or `dev`'s head when the version files already read the version (`docs/releasing.md`, *1. Cut a release*) | The candidate, the release branch's tip after its version bump (`docs/releasing.md`, *Release branch*) |
| Who tags | The user: `scripts/cut-release.sh X.Y.Z` on `dev` | The supervisor, in `/release-ship`: `scripts/cut-release.sh X.Y.Z --no-push` on the release branch tags the candidate locally |
| Who pushes the tag | `scripts/cut-release.sh`, with `dev`, when the user runs it | The user: `git push origin vX.Y.Z`, the go of the go/no-go |

On both, the pushed tag also runs `.github/workflows/release.yml`: the binaries, the notes and the milestone (*Binaries, installer and checksums*).

## In English

Every release of hq is in English (the owner's decision of 2026-11-14, #186): the notes and their headings, the milestone and the comments `scripts/release.sh` writes, the release issue, the master pull request, and the sections of a ticket pull request that a release reads. The template writes them in Polish, and hq has no setting to change that, so hq translates the strings in the template's own files. Each of the lines below conflicts at an update from the template that changes it: keep hq's English and take the rest of the template's change.

| Template (Polish) | hq (English) | Where |
|---|---|---|
| `Nowe funkcje`, `Poprawki`, `Pozostałe`, `Wewnętrzne` | `New features`, `Fixes`, `Other`, `Internal` | `scripts/release.sh` (headings), `docs/releasing.md`, `docs/agents/triage-labels.md`, the note of `scripts/check-pr.sh`, the revert in `docs/agents/issue-tracker.md` |
| `Wdrożenie` | `Deployment` | `scripts/release.sh`, `docs/releasing.md`, `docs/workflow.md` |
| anchors `nowe-funkcje`, `poprawki`, `pozostale`, `wewnetrzne`, `wdrozenie`, `przed-wdrozeniem`, `polecenie`, `po-wdrozeniu` | `new-features`, `fixes`, `other`, `internal`, `deployment`, `before-deployment`, `command`, `after-deployment` | `scripts/release.sh` |
| `Do zrobienia, zanim wdrożysz tę wersję:`, `Do zrobienia na produkcji po wdrożeniu:`, `Instrukcja` | `To do before you deploy this version:`, `To do after deploying this version:`, `Instructions` | `scripts/release.sh` |
| `Wydanie` (milestone), `Wydane w` (comment) | `Release`, `Released in` | `scripts/release.sh`, `docs/releasing.md`, `docs/workflow.md`, `.github/workflows/release.yml` |
| `## Przed wdrożeniem`, `## Po wdrożeniu` | `## Before deployment`, `## After deployment` | `scripts/lib/release-notes.sh`, `scripts/check-pr.sh`, both pull request templates, `docs/releasing.md`, `docs/workflow.md`, `docs/agents/issue-tracker.md`, `docs/agents/implementer.md` |
| `## Wycofanie`, `Brak: drzwi jednokierunkowe` | `## Rollback`, `None: one-way door` | `scripts/check-pr.sh`, `.github/PULL_REQUEST_TEMPLATE.md`, `docs/releasing.md`, `docs/agents/issue-tracker.md`, `docs/agents/implementer.md` |
| `## Zamyka` | `## Closes` | `.github/PULL_REQUEST_TEMPLATE/release.md`, `scripts/check-pr.sh`, `docs/releasing.md`, `docs/agents/issue-tracker.md` |
| `## Plan wdrożenia` | `## Rollout plan` | `.github/PULL_REQUEST_TEMPLATE/release.md`, `docs/agents/issue-tracker.md` |
| `Wydanie vX.Y.Z` (release issue title) | `Release vX.Y.Z` | `.github/ISSUE_TEMPLATE/release.md`, `docs/agents/issue-tracker.md` |
| `## Wycofane kroki wdrożenia`, `Wycofane:` (a reverted ticket) | `## Reverted deployment steps`, `Reverted:` | `docs/agents/issue-tracker.md` |
| "in Polish" (the language of the notes and of a pull request's deployment steps) | "in English" | `scripts/release.sh`, `scripts/lib/release-notes.sh`, both pull request templates, `docs/releasing.md`, `docs/workflow.md`, `docs/agents/issue-tracker.md` |
| a misspelt heading has "no Polish letters" | "another case" | `scripts/check-pr.sh` (header), `docs/releasing.md` |

Three more differences come with the translation:

- `scripts/check-pr.sh` names a section under the template's Polish heading (`## Przed wdrożeniem`, `## Po wdrożeniu`) as misspelt and asks for the English one, so its steps are not left out of the notes without a word (the template has no such case).
- The header of `scripts/check-pr.sh` points its cases at hq's tests, `scripts/checkpr_integration_test.go`, which run it and `scripts/release.sh` against these strings (the template's point at its own test file).
- The row of `internal` in `docs/agents/triage-labels.md` has `-` where the template has an em dash (`docs/hq/agents.md`, *Rules*).

`scripts/check-pr.sh` still rejects the Polish generic titles (`poprawki`, `zmiany`) as the template does; that writes nothing to GitHub.

**Releases before v0.14.0** are in Polish and stay so. Do not re-run `scripts/release.sh` on one of them except with `--dry-run`: it would rewrite its notes in English, leave out the deployment steps of pull requests written under the Polish headings, and post a second comment beside each `Wydane w` one.

**The release skills** write some text of their own, which is not hq's to change: in their current version they look up and create the release issue as `Wydanie vX.Y.Z`, write `## Zamyka` and `## Plan wdrożenia` in the master pull request, check a ticket pull request for `## Wycofanie`, and rename the Polish deployment headings for a revert. Until they follow hq's English, a supervised release meets those differences: `scripts/check-pr.sh` requires the master pull request's closing keywords under `## Closes`, and a ticket pull request has `## Rollback`.

## Version

`VERSION` records the version beside the tag (`scripts/bump-version.sh` writes it, *Version files*), but hq's binary does not read it: the release build bakes the tag into the binary (`hq --version`), and a build from a checkout is a dev build.

## Binaries, installer and checksums

hq deploys nothing: a release is installed by people, with the install one-liner of the README or `hq update`. `docs/deployment.md` does not exist.

The release workflow, `.github/workflows/release.yml`, builds hq for macOS (arm64, amd64) and Linux (amd64, arm64) with `scripts/build-release.sh` before it writes the notes, baking the tag in as the version, and attaches the binaries (`hq-<os>-<arch>`), `install.sh` and `SHA256SUMS` to the release after them. `install.sh` and `hq update` take the latest release and verify the binary against `SHA256SUMS` before installing it. To check the build without a tag: `scripts/build-release.sh v0.0.1 /tmp/dist`.

## Without GitHub Actions

When GitHub Actions starts no runs on the repository (for example, the account's Actions are blocked), a pushed tag builds nothing and writes no notes: the tag's run is missing from `gh run list`, and the tag has no release. Then, once the tag is pushed, do by hand what the workflow does, with the same scripts:

```bash
git checkout v0.3.0
scripts/build-release.sh v0.3.0 /tmp/dist     # hq for every platform, install.sh, SHA256SUMS
git checkout -                                # back to the branch you came from
scripts/release.sh v0.3.0                     # the release, its notes, the milestone, the comments
gh release upload v0.3.0 /tmp/dist/* --clobber
```

Build from the tag, so the binaries are exactly what was tagged. Check the result before announcing it: the release is marked latest, it has six files, and the install one-liner from the README installs the new version (`hq --version`).
