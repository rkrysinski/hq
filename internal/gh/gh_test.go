package gh

import (
	"errors"
	"strings"
	"testing"
)

type fakeGh struct {
	out  string
	err  error
	args []string
}

func (f *fakeGh) Run(name string, args ...string) ([]byte, error) {
	f.args = append([]string{name}, args...)
	return []byte(f.out), f.err
}

func TestEachBranchGetsItsNewestPullRequest(t *testing.T) {
	f := &fakeGh{out: `[
		{"number": 12, "headRefName": "feat/a", "state": "OPEN", "url": "https://github.com/o/r/pull/12"},
		{"number": 9, "headRefName": "feat/a", "state": "CLOSED", "url": "https://github.com/o/r/pull/9"},
		{"number": 10, "headRefName": "fix/b", "state": "MERGED", "url": "https://github.com/o/r/pull/10"},
		{"number": 14, "headRefName": "fix/b", "state": "OPEN", "url": "https://github.com/o/r/pull/14"}
	]`}
	prs, err := PullRequests(f)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.args, " "); got != "gh pr list --state all --limit 200 --json number,headRefName,state,url" {
		t.Errorf("ran %q", got)
	}
	if len(prs) != 2 || prs["feat/a"].Number != 12 || prs["fix/b"].URL != "https://github.com/o/r/pull/14" || prs["fix/b"].State != "OPEN" {
		t.Fatalf("%+v", prs)
	}
}

func TestGhFailingIsAnError(t *testing.T) {
	if _, err := PullRequests(&fakeGh{err: errors.New("gh: not logged in")}); err == nil {
		t.Error("gh's failure lost")
	}
	if _, err := PullRequests(&fakeGh{out: "not json"}); err == nil {
		t.Error("bad answer accepted")
	}
	if prs, err := PullRequests(&fakeGh{out: "[]"}); err != nil || len(prs) != 0 {
		t.Errorf("no pull requests: %v %v", prs, err)
	}
}
