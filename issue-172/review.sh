#!/bin/sh
# review.sh SBX: each sbx call hq makes, its flags given to the binary's
# parser with --help; "accepted" when the parser knows every flag.
bin=$1
export HOME=$(mktemp -d)
check() {
    out=$("$bin" "$@" --help 2>&1 </dev/null)
    if printf '%s' "$out" | grep -qi 'unknown flag\|unknown shorthand\|unknown command'; then
        printf '%-95s %s\n' "sbx $*" "REJECTED: $(printf '%s' "$out" | grep -i 'unknown' | head -1)"
    elif printf '%s' "$out" | grep -q 'not signed in'; then
        # --help after -- or a command goes to the agent or the command:
        # the parser took every flag and sbx went on to need a sign-in.
        printf '%-95s %s\n' "sbx $*" "accepted (parsed, then: not signed in)"
    else
        printf '%-95s %s\n' "sbx $*" "accepted"
    fi
}
"$bin" version 2>&1 | head -1
check ls --json
check create --no-progress --name hq-local-app --template img:x --static-mcp a,b claude /w/app
check run --detach --name hq-local-app --template img:x --static-mcp a,b --model m --provider p claude /w/app
check run --name claude-app -- --settings '{}' hi
check version
check stop claude-app
check rm --force claude-app
check exec claude-app true
check mcp inspect hq --json
check mcp add hq --command /bin/hq --args mcp,serve
check mcp load hq --sandbox claude-app
echo "-- spellings before 0.48, as a control:"
check create --quiet claude /w/app
check create -q claude /w/app
check run --detached --name x claude /w/app
