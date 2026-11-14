//go:build integration

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The sections of a pull request that scripts/check-pr.sh guards and
// scripts/release.sh copies are in English, in the checks and in the pull
// request templates alike (#186).

// repoRoot is the checkout this test file belongs to.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(file))
}

// checkPR runs scripts/check-pr.sh with the title and the description on
// stdin, and returns its output and exit status.
func checkPR(t *testing.T, body string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(filepath.Join(repoRoot(t), "scripts", "check-pr.sh"),
		append(args, "Show the release notes in English")...)
	cmd.Stdin = strings.NewReader(body)
	out, err := cmd.CombinedOutput()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

// fillTemplate reads a pull request template and writes text under each named
// `## ` heading, the way an author fills it in.
func fillTemplate(t *testing.T, path string, fill map[string]string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), path))
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	for heading, text := range fill {
		line := "## " + heading + "\n"
		if !strings.Contains(body, line) {
			t.Fatalf("%s has no %q heading", path, line)
		}
		body = strings.Replace(body, line, line+"\n"+text+"\n", 1)
	}
	return body
}

const ticketSections = "## Summary\n\nThe notes are in English.\n\n" +
	"## QA\n\nN/A: wording only.\n\n" +
	"## Merge Danger\n\n**Door:** two-way.\n\n"

func TestCheckPRAsksForTheRollbackOfADeploymentStep(t *testing.T) {
	body := "Closes #5\n\n" + ticketSections + "## After deployment\n\n- [ ] Restart the service.\n"
	out, code := checkPR(t, body)
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	want := "check-pr: `## Rollback` is missing: the description has deployment steps, so add one line per rollback step, or `None: one-way door`"
	if !strings.Contains(out, want) {
		t.Fatalf("output:\n%s\nwant the line:\n%s", out, want)
	}
}

func TestCheckPRNamesTheEnglishHeadingOfAMisspeltSection(t *testing.T) {
	body := "Closes #5\n\n" + ticketSections + "## before deployment:\n\n- [ ] Set the variable.\n"
	out, code := checkPR(t, body)
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	want := "check-pr: heading \"## before deployment:\" is not read by release.sh: write exactly `## Before deployment`"
	if !strings.Contains(out, want) {
		t.Fatalf("output:\n%s\nwant the line:\n%s", out, want)
	}
}

func TestCheckPRPassesTheTicketTemplateFilledIn(t *testing.T) {
	body := fillTemplate(t, ".github/PULL_REQUEST_TEMPLATE.md", map[string]string{
		"Summary":      "The notes are in English.",
		"QA":           "N/A: wording only.",
		"Merge Danger": "**Door:** two-way.",
		"Rollback":     "- Before deployment: None: one-way door",
	})
	body = strings.Replace(body, "Closes #\n", "Closes #5\n", 1)
	body = strings.Replace(body, "## Rollback\n", "## Before deployment\n\n- [ ] Grant the token.\n\n## Rollback\n", 1)
	if out, code := checkPR(t, body); code != 0 || !strings.Contains(out, "check-pr: ok") {
		t.Fatalf("exit %d, want the filled template to pass:\n%s\n--- description:\n%s", code, out, body)
	}
}

func TestCheckPRReadsTheClosingSectionOfTheReleaseTemplate(t *testing.T) {
	fill := map[string]string{
		"Summary":      "Ship.",
		"QA":           "Release QA green.",
		"Merge Danger": "**Door:** two-way.",
		"Closes":       "Closes #1",
	}
	body := fillTemplate(t, ".github/PULL_REQUEST_TEMPLATE/release.md", fill)
	if out, code := checkPR(t, body, "--release-pr"); code != 0 || !strings.Contains(out, "check-pr: ok") {
		t.Fatalf("exit %d, want the filled release template to pass:\n%s", code, out)
	}

	fill["Summary"] = "Ship. Fixes #2"
	body = fillTemplate(t, ".github/PULL_REQUEST_TEMPLATE/release.md", fill)
	out, code := checkPR(t, body, "--release-pr")
	want := "check-pr: a release pull request lists the issues it closes only under `## Closes`"
	if code != 1 || !strings.Contains(out, want) {
		t.Fatalf("exit %d, output:\n%s\nwant exit 1 and the line:\n%s", code, out, want)
	}
}
