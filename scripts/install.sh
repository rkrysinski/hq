#!/bin/sh
# Install hq from its GitHub release (design §3.9). Published with every
# release; the first install is one command, with no GitHub login:
#
#   curl -fsSL https://github.com/rkrysinski/hq/releases/latest/download/install.sh | sh
#
# It checks the prerequisites (spec §11), downloads the binary for this
# platform over HTTPS from the release's public URLs, verifies it against the
# release's SHA256SUMS and puts it in ~/.local/bin/hq. On macOS with iTerm2 it
# also adds the iTerm2 profile `hq` (Option as Esc+, design §3.7), changing no
# other profile. HQ_VERSION=vX.Y.Z installs that release instead of the
# latest; HQ_RELEASES_URL reads the releases from another place that answers
# as GitHub's release URLs do (tests, QA). Exit codes: 0 installed, 1 failed
# download or checksum, 3 missing prerequisite.
set -eu

RELEASES=${HQ_RELEASES_URL:-https://github.com/rkrysinski/hq/releases}
RELEASES=${RELEASES%/}
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
command -v curl >/dev/null 2>&1 || need "curl: install curl"
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

# The latest release is where RELEASES/latest redirects: RELEASES/tag/TAG.
# Its tag is read once, so the binary and SHA256SUMS come from one release
# even while another is published.
tag=${HQ_VERSION:-}
if [ -z "$tag" ]; then
    location=$(curl -fsS -o /dev/null -w '%{redirect_url}' "$RELEASES/latest") ||
        fail 1 "could not read the latest release from $RELEASES"
    case $location in
        */tag/?*) tag=${location##*/tag/} ;;
        *) fail 1 "no release found at $RELEASES" ;;
    esac
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
for f in "$asset" SHA256SUMS; do
    curl -fsSL -o "$tmp/$f" "$RELEASES/download/$tag/$f" ||
        fail 1 "could not download $f of $tag from $RELEASES"
done

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

# On macOS with iTerm2, the profile hq (Option as Esc+, design §3.7); the
# binary holds it, so install and hq update write the same file.
"$BIN_DIR/hq" __iterm-profile | while IFS= read -r line; do say "$line"; done

case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *) say "$(tilde "$BIN_DIR") is not on PATH; add it in your shell's profile: export PATH=\"\$HOME/.local/bin:\$PATH\"" ;;
esac
