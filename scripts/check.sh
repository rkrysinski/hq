#!/bin/sh
# The full build: every step the test workflows run, in their order, and they
# run nothing else (#176; private.yml checks private details apart). An
# agent, a merger and GitHub run the same steps, and the last line is the
# test evidence of a merge. Runs from any directory.
#
# Its output is a contract the team's release skills read, the same one the
# project template's full build prints:
#   check: ok   <step>                       one line per passed step
#   check: skip <step> <package> <test>: <reason>
#                                            after a passed test step's ok
#                                            line, one per test it skipped
#   check: FAIL <step>                       at the first failing step, then
#   <that step's output>                     its output, then
#   check: stopped at <step> on <commit>     and exit 1; no later step runs
#   check: all <N> steps passed on <commit>  the last line when all passed, exit 0
# <commit> is the full sha of HEAD, followed by " with uncommitted changes"
# when the working tree is not clean (untracked files count, ignored ones not).
#
# The steps: vet (for every build tag), format, pyramid (tools/testgate),
# platform (tools/testgate), timebound (tools/testgate: no test bounds real
# elapsed time under 1s), unit, integration (with coverage of internal/),
# coverage (tools/testgate's threshold), e2e. A passed step's output is not
# printed; the figures of pyramid and coverage, and the skip lines, go to the
# GitHub job summary when GITHUB_STEP_SUMMARY is set. The test steps (unit,
# integration, e2e) run go test -json: of a passed one, testgate skips names
# each test it skipped, a subtest under its full name, with the test's reason
# on one line (#270), and a run that skipped nothing prints no such line; a
# skipped test does not fail the build, and testgate failing to list them
# fails the step. Of a failed one, testgate plain prints the output as go
# test prints it without -json. See docs/hq/testing.md.

set -u

cd "$(dirname "$0")/.." || exit 1

# Every temporary file of the run, the tests' among them, lives in a
# directory of this checkout's own outside the tree, in the caller's
# TMPDIR or /tmp (#226): a test run leaves no file loose in the shared
# temporary directory, and removing what this checkout's runs left there
# never touches another checkout's run. A run that ends leaves it empty. A
# run from a shell whose TMPDIR is already that directory stays in it.
base=${TMPDIR:-/tmp}
base=${base%/}
name=hq-check-$(printf %s "$(pwd)" | cksum | cut -d' ' -f1)
case $base in
*/"$name") TMPDIR=$base ;;
*) TMPDIR=$base/$name ;;
esac
mkdir -p "$TMPDIR" || exit 1
export TMPDIR

commit=$(git rev-parse HEAD) || exit 1
if [ -n "$(git status --porcelain)" ]; then
	commit="$commit with uncommitted changes"
fi

# The coverage profile and each step's output live outside the tree, so a run
# never leaves the tree dirty.
tmp=$(mktemp -d) || exit 1
trap 'rm -rf "$tmp"' EXIT
trap 'exit 1' HUP INT TERM

step_vet() {
	go vet ./... && go vet -tags integration ./... && go vet -tags e2e ./...
}

step_format() {
	files=$(gofmt -l .) || return 1
	[ -z "$files" ] && return 0
	echo "$files"
	return 1
}

step_pyramid() { go run ./tools/testgate pyramid; }

step_platform() { go run ./tools/testgate platform; }

step_timebound() { go run ./tools/testgate timebound; }

# gotest STEP ARGS... runs go test -json with ARGS for the test step STEP.
# When the tests pass it writes the step's skip lines to $tmp/skips and fails
# when testgate cannot list them; when they fail it prints their output as go
# test prints it without -json, or as go test -json printed it when testgate
# cannot read it.
gotest() {
	gotest_step=$1
	shift
	if go test -json "$@" >"$tmp/json" 2>&1; then
		go run ./tools/testgate skips "$gotest_step" "$tmp/json" >"$tmp/skips" && return
		echo "the tests passed, but testgate could not list the tests they skipped"
		return 1
	fi
	go run ./tools/testgate plain "$tmp/json" 2>/dev/null || cat "$tmp/json"
	return 1
}

step_unit() { gotest unit ./...; }

# -count=1: a cached package's profile describes the code of the run that
# filled the cache, not this one's (#53).
step_integration() {
	gotest integration -count=1 -tags integration -coverpkg=./internal/... -coverprofile="$tmp/cover.out" ./...
}

step_coverage() { go run ./tools/testgate coverage "$tmp/cover.out"; }

step_e2e() {
	if [ -d e2e ]; then gotest e2e -tags e2e ./e2e/...; else echo "no journeys yet"; fi
}

n=0
for step in vet format pyramid platform timebound unit integration coverage e2e; do
	: >"$tmp/skips"
	if ! "step_$step" >"$tmp/out" 2>&1; then
		echo "check: FAIL $step"
		cat "$tmp/out"
		echo "check: stopped at $step on $commit"
		exit 1
	fi
	echo "check: ok   $step"
	cat "$tmp/skips"
	n=$((n + 1))
	[ -n "${GITHUB_STEP_SUMMARY:-}" ] || continue
	# The two gates' figures, which the thresholds follow (testing.md), and
	# the tests a step skipped, as they are, in a block of their own.
	case $step in pyramid | coverage) cat "$tmp/out" >>"$GITHUB_STEP_SUMMARY" ;; esac
	if [ -s "$tmp/skips" ]; then
		{
			echo '```text'
			cat "$tmp/skips"
			echo '```'
		} >>"$GITHUB_STEP_SUMMARY"
	fi
done
echo "check: all $n steps passed on $commit"
