// Command testgate holds hq's CI gates: the shape of the test suite, its
// coverage, no private details in the public text, and platform-specific
// code in the platform package only (docs/hq/testing.md):
//
//	go run ./tools/testgate pyramid          # counts tests per level, checks the shape
//	go run ./tools/testgate coverage FILE    # checks a coverage profile against the threshold
//	go run ./tools/testgate private [PATH...] # no private details in tracked files (or PATHs)
//	go run ./tools/testgate prepush REPO     # no private details in what a push sends (the pre-push hook)
//	go run ./tools/testgate platform [ROOT]  # platform-specific names only in internal/platform
//
// private and prepush read the pattern list from $HQ_PRIVATE_PATTERNS or,
// without it, from the repository's .git/info/hq-private-patterns (see
// private.go). Without a list private passes with a notice and prepush
// refuses the push (#185).
//
// A test's level is its file's build constraint: none is unit, the
// "integration" tag is integration, the "e2e" tag is end-to-end.
//
// The two thresholds below only go up: a pull request that raises the actual
// figure raises the threshold in the same pull request. Never lower one.
package main

import (
	"fmt"
	"os"
)

const usage = "usage: testgate pyramid [ROOT] | coverage FILE | private [PATH...] | prepush REPO | platform [ROOT]"

const (
	// MinUnitShare is the lowest allowed unit share of unit + integration tests.
	MinUnitShare = 0.50
	// MinCoverage is the lowest allowed statement coverage of internal/, in
	// percent: 0.5 points under CI's figure, rounded down to a tenth, so a
	// PR that changes nothing about tests stays above it (#42).
	MinCoverage = 94.4
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	var problems []string
	refused := "" // what a failed check adds after its problems
	switch os.Args[1] {
	case "pyramid":
		root := "."
		if len(os.Args) > 2 {
			root = os.Args[2]
		}
		c, err := countTests(root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "testgate:", err)
			os.Exit(2)
		}
		fmt.Printf("unit %d, integration %d, e2e %d (unit share %.0f%%, minimum %.0f%%)\n",
			c.unit, c.integration, c.e2e, 100*c.unitShare(), 100*MinUnitShare)
		problems = c.problems(MinUnitShare)
	case "coverage":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		f, err := os.Open(os.Args[2])
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
		summary, p, err := checkPrivate(list, os.Args[2:])
		if err != nil {
			fmt.Fprintln(os.Stderr, "testgate:", err)
			os.Exit(2)
		}
		fmt.Println(summary)
		problems = p
	case "prepush":
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		p, err := runPrepush(os.Args[2])
		if err != nil {
			// Not checked, so not pushed: exit 2 fails the hook too.
			fmt.Fprintln(os.Stderr, "testgate:", err, "- push refused")
			os.Exit(2)
		}
		problems = p
		if len(p) > 0 && p[0] != noList { // the summary says why a push without a list is refused
			refused = "push refused: take the private details out of the commits that hold them (a later commit that removes one still pushes it), or fix the list"
		}
	case "platform":
		root := "."
		if len(os.Args) > 2 {
			root = os.Args[2]
		}
		hits, err := checkPlatform(root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "testgate:", err)
			os.Exit(2)
		}
		fmt.Printf("platform: %d names checked, uses outside internal/platform: %d\n", len(platformNames), len(hits))
		for _, h := range hits {
			problems = append(problems, h.String())
		}
	default:
		fmt.Fprintln(os.Stderr, usage)
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
