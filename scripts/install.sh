#!/bin/sh
# Install hq from its GitHub release (design §3.9). Published with every
# release; the first install is one command:
#
#   gh release download -R rkrysinski/hq -p install.sh -O - | sh
#
# It checks the prerequisites (spec §11), downloads the binary for this
# platform, verifies it against the release's SHA256SUMS and puts it in
# ~/.local/bin/hq. On macOS it also adds the iTerm2 profile `hq` (Option as
# Esc+, design §3.7). HQ_VERSION=vX.Y.Z installs that release instead of the
# latest. Exit codes: 0 installed, 1 failed download or checksum, 3 missing
# prerequisite.
set -eu

REPO=rkrysinski/hq
BIN_DIR=$HOME/.local/bin

say() { echo "hq install: $*"; }
# tilde prints a path under the home directory as ~/...
tilde() { case $1 in "$HOME"/*) echo "~${1#"$HOME"}" ;; *) echo "$1" ;; esac; }
fail() { code=$1; shift; echo "hq install: $*" >&2; exit "$code"; }

# Prerequisites: all of them are reported at once, each with its remedy.
missing=
need() { missing="$missing
  - $1"; }
command -v git >/dev/null 2>&1 || need "git: install git"
command -v gh >/dev/null 2>&1 || need "gh: install GitHub CLI and run gh auth login"
command -v sbx >/dev/null 2>&1 || command -v sbx.exe >/dev/null 2>&1 ||
    need "sbx: install Docker Sandboxes"
command -v code >/dev/null 2>&1 || need "code: install VS Code and put code on PATH (Command Palette: Shell Command)"
if command -v tmux >/dev/null 2>&1; then
    v=$(tmux -V | sed 's/^tmux //; s/^next-//')
    major=${v%%.*}
    minor=$(echo "${v#*.}" | sed 's/[^0-9].*//')
    if [ "${major:-0}" -lt 3 ] || { [ "$major" -eq 3 ] && [ "${minor:-0}" -lt 4 ]; }; then
        need "tmux $v is too old: install tmux 3.4 or newer (Ubuntu 24.04 ships it)"
    fi
else
    need "tmux: install tmux 3.4 or newer"
fi
[ -z "$missing" ] || fail 3 "missing prerequisites:$missing"

case $(uname -s) in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) fail 3 "unsupported system $(uname -s): hq runs on macOS and on WSL" ;;
esac
case $(uname -m) in
    arm64 | aarch64) arch=arm64 ;;
    x86_64 | amd64) arch=amd64 ;;
    *) fail 3 "unsupported processor $(uname -m)" ;;
esac
asset=hq-$os-$arch

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
# Without a tag, gh downloads the latest release.
gh release download ${HQ_VERSION:+"$HQ_VERSION"} -R "$REPO" -p "$asset" -p SHA256SUMS -D "$tmp" ||
    fail 1 "could not download $asset (check gh auth status)"

want=$(awk -v f="$asset" '$2 == f || $2 == "*" f { print $1 }' "$tmp/SHA256SUMS")
if command -v sha256sum >/dev/null 2>&1; then
    got=$(sha256sum "$tmp/$asset" | cut -d' ' -f1)
else
    got=$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1)
fi
[ -n "$want" ] && [ "$want" = "$got" ] || fail 1 "checksum mismatch for $asset; nothing was installed"

mkdir -p "$BIN_DIR"
cp "$tmp/$asset" "$BIN_DIR/.hq.new"
chmod 755 "$BIN_DIR/.hq.new"
mv -f "$BIN_DIR/.hq.new" "$BIN_DIR/hq"
say "installed $("$BIN_DIR/hq" --version | head -n 1) to $(tilde "$BIN_DIR/hq")"

if [ "$os" = darwin ]; then
    profiles="$HOME/Library/Application Support/iTerm2/DynamicProfiles"
    mkdir -p "$profiles"
    cat >"$profiles/hq.json" <<'PROFILE'
{
  "Profiles": [
    {
      "Name": "hq",
      "Guid": "hq-dashboard",
      "Dynamic Profile Parent Name": "Default",
      "Option Key Sends": 2,
      "Right Option Key Sends": 2
    }
  ]
}
PROFILE
    say "added the iTerm2 profile hq (Option as Esc+) for the dashboard"
fi

case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *) say "$(tilde "$BIN_DIR") is not on PATH; add it in your shell's profile: export PATH=\"\$HOME/.local/bin:\$PATH\"" ;;
esac
