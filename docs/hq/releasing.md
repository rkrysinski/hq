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
