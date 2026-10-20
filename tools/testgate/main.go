// Command testgate enforces the shape of hq's test suite in CI
// (docs/agents/testing.md):
//
//	go run ./tools/testgate pyramid          # counts tests per level, checks the shape
//	go run ./tools/testgate coverage FILE    # checks a coverage profile against the threshold
//	go run ./tools/testgate private [PATH...] # no private details in tracked files (or PATHs)
//
// private reads its patterns from $HQ_PRIVATE_PATTERNS (see private.go)
// and passes with a notice when the variable is unset.
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

const usage = "usage: testgate pyramid [ROOT] | coverage FILE | private [PATH...]"

const (
	// MinUnitShare is the lowest allowed unit share of unit + integration tests.
	MinUnitShare = 0.50
	// MinCoverage is the lowest allowed statement coverage of internal/, in
	// percent: 0.5 points under CI's figure, rounded down to a tenth, so a
	// PR that changes nothing about tests stays above it (#42).
	MinCoverage = 94.1
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	var problems []string
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
			fmt.Fprintln(os.Stderr, "usage: testgate coverage FILE")
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
		pats, err := parsePatterns(os.Getenv(PrivatePatternsEnv))
		if err != nil {
			fmt.Fprintln(os.Stderr, "testgate:", PrivatePatternsEnv, err)
			os.Exit(2)
		}
		if len(pats) == 0 {
			fmt.Printf("private: %s is not set, nothing checked\n", PrivatePatternsEnv)
			return
		}
		paths := os.Args[2:]
		if len(paths) == 0 {
			if paths, err = trackedFiles("."); err != nil {
				fmt.Fprintln(os.Stderr, "testgate:", err)
				os.Exit(2)
			}
		}
		hits, err := scanPaths(paths, pats)
		if err != nil {
			fmt.Fprintln(os.Stderr, "testgate:", err)
			os.Exit(2)
		}
		fmt.Printf("private: %d patterns, %d matching lines\n", len(pats), len(hits))
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
		os.Exit(1)
	}
}
