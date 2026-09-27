// Package gh reads a repository's pull requests through GitHub's gh command
// (design §5.2).
package gh

import (
	"encoding/json"
	"fmt"

	"github.com/rkrysinski/hq/internal/proc"
)

// PR is a pull request as the list needs it.
type PR struct {
	Number int    `json:"number"`
	Branch string `json:"headRefName"`
	State  string `json:"state"` // OPEN, CLOSED or MERGED
	URL    string `json:"url"`
}

// listArgs ask gh for the repository's pull requests, all states, newest
// first, enough for every branch an agent works on.
var listArgs = []string{"pr", "list", "--state", "all", "--limit", "200", "--json", "number,headRefName,state,url"}

// PullRequests runs gh in the repository (run runs there) and gives each
// branch its newest pull request.
func PullRequests(run proc.Runner) (map[string]PR, error) {
	out, err := run.Run("gh", listArgs...)
	if err != nil {
		return nil, err
	}
	var prs []PR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("gh pr list: %v", err)
	}
	byBranch := map[string]PR{}
	for _, p := range prs {
		if q, ok := byBranch[p.Branch]; !ok || p.Number > q.Number {
			byBranch[p.Branch] = p
		}
	}
	return byBranch, nil
}
