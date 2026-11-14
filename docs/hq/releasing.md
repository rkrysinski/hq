# Releasing hq

hq's own release facts, beside the template's `docs/releasing.md`; where the two differ, this file holds for hq.

## Branches

- `dev` is the default branch. Work lands on it through pull requests.
- `main` holds the latest released version and nothing else; nobody commits or merges into it by hand.

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
