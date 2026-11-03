package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rkrysinski/hq/internal/kills"
)

const killedUsage = "usage: hq killed [NAME]"

// runKilled shows the trace of kills (#147), oldest first: every agent hq
// killed, when, and who asked for it; with NAME, the kills of agents of
// that name.
func runKilled(env Env, d deps, args []string) error {
	if len(args) > 1 || len(args) == 1 && strings.HasPrefix(args[0], "-") {
		return usageErr(killedUsage)
	}
	ks, err := d.readKills()
	if err != nil {
		return envErr("cannot read the trace of kills: %v", err)
	}
	n := 0
	for _, k := range ks {
		if len(args) == 1 && k.Name != args[0] {
			continue
		}
		n++
		fmt.Fprintf(env.Stdout, "%s  %s in %s (sandbox %s), by %s\n", k.At.In(d.now().Location()).Format("2006-01-02 15:04:05"), k.Name, filepath.Base(k.Repo), k.Sandbox, killedBy(k))
	}
	switch {
	case n > 0:
	case len(args) == 1:
		fmt.Fprintf(env.Stdout, "no kill of '%s' in the trace\n", args[0])
	default:
		fmt.Fprintln(env.Stdout, "no kills in the trace")
	}
	return nil
}

// killedBy says who asked for a kill.
func killedBy(k kills.Kill) string {
	switch k.By {
	case kills.ByCLI:
		return "hq " + k.Command
	case kills.ByDashboard:
		return "the dashboard"
	case kills.ByMCP:
		s := "MCP: client " + k.Client
		if k.App != "" {
			s += " (" + k.App + ")"
		}
		if k.Caller != "" {
			s += ", agent " + k.Caller
		}
		return s
	}
	return k.By
}
