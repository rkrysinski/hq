#!/bin/sh
# Turn the URL of a remote into the repository gh knows it by: `owner/name`.
# Sourced by the scripts that take the repository from the `origin` remote, so
# all of them read a remote the same way.
#
# Usage after sourcing:
#   repo_from_url <url>
#
# Understands the three forms a GitHub remote has, with or without `.git`:
#   git@host:owner/name
#   http(s)://host/owner/name
#   ssh://[user@]host[:port]/owner/name
#
# Any other value is printed as it is, less a final `.git`: whether the result
# is owner/name is for the caller to check.

repo_from_url() {
    printf '%s\n' "$1" | sed \
        -e 's#^git@[^:]*:##' \
        -e 's#^https\{0,1\}://[^/]*/##' \
        -e 's#^ssh://[^/]*/##' \
        -e 's#\.git$##'
}
