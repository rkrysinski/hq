// Command testgate holds hq's CI gates: the shape of the test suite, its
// coverage, no private details in the public text, platform-specific code
// in the platform package only, and no test bounding real elapsed time under
// one second (docs/hq/testing.md):
//
//	go run ./tools/testgate pyramid [ROOT]   # counts tests per level, checks the shape
//	go run ./tools/testgate coverage FILE    # checks a coverage profile against the threshold
//	go run ./tools/testgate private [PATH...] # no private details in tracked files (or PATHs)
//	go run ./tools/testgate prepush REPO     # no private details in what a push sends (the pre-push hook)
//	go run ./tools/testgate prepost          # no private details in what a gh command posts (Claude Code's hook)
//	go run ./tools/testgate platform [ROOT]  # platform-specific names only in internal/platform
//	go run ./tools/testgate timebound [ROOT] # no test bounds real elapsed time under 1s
//	go run ./tools/testgate mutation BASE [PATH...] # mutation tests of the lines changed against BASE, part by part
//
// A command line in none of these forms (no command, another command, an
// argument missing or one too many) is refused before any gate runs: the
// usage line on stderr and exit 2 (#284, args.go).
//
// private, prepush and prepost read the pattern list from
// $HQ_PRIVATE_PATTERNS or, without it, from the repository's
// .git/info/hq-private-patterns (see private.go). Without a list private
// passes with a notice, prepush refuses the push (#185) and prepost a
// command that posts a text (#231). prepost reads the PreToolUse hook's
// input on stdin (prepost.go).
//
// mutation prints one line per mutant left alive or timed out and a last
// line with the counts, and exits 1 when there is such a mutant (#204,
// mutation.go). Each PATH, a package's directory or a Go file, is a part:
// the parts run one after another, each with a build cache of its own, and
// the changed production files no part covers get a line each (#233).
// Like every gate it exits 2 on an error of its own, unit tests that failed
// with a mutant and fail without it too included: no mutant is judged
// (#234, mutationgo.go).
//
// A test's level is its file's build constraint: none is unit, the
// "integration" tag is integration, the "e2e" tag is end-to-end.
//
// The two thresholds below only go up: a pull request that raises the actual
// figure raises the threshold in the same pull request. Never lower one.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

const usage = "usage: testgate pyramid [ROOT] | coverage FILE | private [PATH...] | prepush REPO | prepost | platform [ROOT] | timebound [ROOT] | mutation BASE [PATH...]"

const (
	// MinUnitShare is the lowest allowed unit share of unit + integration tests.
	MinUnitShare = 0.75
	// MinCoverage is the lowest allowed statement coverage of internal/, in
	// percent: 0.5 points under CI's figure, rounded down to a tenth, so a
	// PR that changes nothing about tests stays above it (#42).
	MinCoverage = 94.7
)

func main() {
	if isMutationGo(os.Args[0], os.Getenv(envRealGo)) {
		os.Exit(mutationGo(os.Args[1:]))
	}
	inv, err := readArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var problems []string
	refused := "" // what a failed check adds after its problems
	switch inv.name {
	case "pyramid":
		c, err := countTests(inv.args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "testgate:", err)
			os.Exit(2)
		}
		fmt.Printf("unit %d, integration %d, e2e %d (unit share %.0f%%, minimum %.0f%%)\n",
			c.unit, c.integration, c.e2e, 100*c.unitShare(), 100*MinUnitShare)
		problems = c.problems(MinUnitShare)
	case "coverage":
		f, err := os.Open(inv.args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "testgate:", err)
			os.Exit(2)
		}
		pct, err := coverage(f)
		f.Close()
		if err != nil {
			fmt.Fprintln(os.Stderr, "testgate:", err)
			os.Exit(2)
		}
		fmt.Printf("coverage %.2f%% (minimum %.1f%%)\n", pct, MinCoverage)
		if pct < MinCoverage {
			problems = append(problems, fmt.Sprintf("coverage %.1f%% is below %.1f%%", pct, MinCoverage))
		}
	case "private":
		list, err := privateList(os.Getenv(PrivatePatternsEnv), gitCommonDir("."))
		if err != nil {
			fmt.Fprintln(os.Stderr, "testgate:", err)
			os.Exit(2)
		}
		summary, p, err := checkPrivate(list, inv.args)
		if err != nil {
			fmt.Fprintln(os.Stderr, "testgate:", err)
			os.Exit(2)
		}
		fmt.Println(summary)
		problems = p
	case "prepush":
		p, err := runPrepush(inv.args[0])
		if err != nil {
			// Not checked, so not pushed: exit 2 fails the hook too.
			fmt.Fprintln(os.Stderr, "testgate:", err, "- push refused")
			os.Exit(2)
		}
		problems = p
		if len(p) > 0 && p[0] != noList { // the summary says why a push without a list is refused
			refused = "push refused: take the private details out of the commits that hold them (a later commit that removes one still pushes it), or fix the list"
		}
	case "prepost":
		p, err := runPrepost()
		if err != nil {
			// Not checked, so not posted: the hook refuses on any failure.
			fmt.Fprintln(os.Stderr, "testgate: prepost:", err, "- refused")
			os.Exit(2)
		}
		problems = p
		if len(p) > 0 && p[0] != noPostList { // the summary says why a command without a list is refused
			refused = postRefused
		}
	case "platform":
		hits, err := checkPlatform(inv.args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "testgate:", err)
			os.Exit(2)
		}
		fmt.Printf("platform: %d names checked, uses outside internal/platform: %d\n", len(platformNames), len(hits))
		for _, h := range hits {
			problems = append(problems, h.String())
		}
	case "timebound":
		hits, checked, err := checkTimebound(inv.args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "testgate:", err)
			os.Exit(2)
		}
		fmt.Printf("timebound: %d test files checked, upper bounds under %s on real elapsed time: %d\n", checked, timeboundLimit, len(hits))
		for _, h := range hits {
			problems = append(problems, h.String())
		}
	case "mutation":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		failed, err := runMutationParts(ctx, ".", inv.args[0], inv.args[1:], os.Stdout, os.Stderr)
		stop()
		if err != nil {
			fmt.Fprintln(os.Stderr, "testgate: mutation:", err)
			os.Exit(2)
		}
		if failed {
			os.Exit(1)
		}
	default: // a row of forms with no case here: fail, never pass unchecked
		fmt.Fprintln(os.Stderr, "testgate:", inv.name, "is read but no gate runs it")
		os.Exit(2)
	}
	for _, p := range problems {
		fmt.Println("FAIL:", p)
	}
	if len(problems) > 0 {
		if refused != "" {
			fmt.Println(refused)
		}
		os.Exit(1)
	}
}

// runPrepush checks the push the pre-push hook is given on stdin from the
// repository repo, prints its summary and returns its problems.
func runPrepush(repo string) ([]string, error) {
	list, err := privateList(os.Getenv(PrivatePatternsEnv), gitCommonDir(repo))
	if err != nil {
		return nil, err
	}
	summary, problems, err := checkPush(list, repo, os.Stdin)
	if err != nil {
		return nil, err
	}
	fmt.Println(summary)
	return problems, nil
}

// runPrepost checks the Bash command of the PreToolUse hook's input on
// stdin, prints its summary when it posts a text, and returns its problems.
func runPrepost() ([]string, error) {
	list, err := privateList(os.Getenv(PrivatePatternsEnv), gitCommonDir("."))
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	summary, problems, err := checkPost(list, os.Stdin, home)
	if err != nil {
		return nil, err
	}
	if summary != "" {
		fmt.Println(summary)
	}
	return problems, nil
}
