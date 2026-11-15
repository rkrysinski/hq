package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/prefs"
	"github.com/rkrysinski/hq/internal/update"
	"github.com/rkrysinski/hq/internal/version"
)

// updateCheckEvery is how often hq asks GitHub for the latest release
// (design §3.9).
const updateCheckEvery = 24 * time.Hour

// updateCheckCommand is the hidden command a user command starts, detached,
// when the daily update check is due: it asks GitHub for the latest release
// and keeps the answer in the preferences file, where the next command reads
// it (design §3.9).
const updateCheckCommand = "__update-check"

func runUpdate(env Env, d deps, args []string) error {
	if len(args) != 0 {
		return usageErr("usage: hq update")
	}
	addProfile(env, d)
	latest, err := d.releases.Latest()
	if err != nil {
		return envErr("could not read hq's releases: %v", err)
	}
	d.rememberLatest(latest)
	current := version.Version
	if !update.Newer(latest, current) {
		fmt.Fprintf(env.Stdout, "hq %s is up to date (latest release %s)\n", current, latest)
		return nil
	}
	gen, published, err := d.releases.Generation(latest)
	if err != nil {
		return envErr("could not read the generation of hq %s: %v; nothing was replaced", latest, err)
	}
	if err := mayUpdate(d, latest, gen, published); err != nil {
		return err
	}
	exe, err := d.executable()
	if err != nil {
		return envErr("cannot find the running hq: %v", err)
	}
	dir, err := os.MkdirTemp("", "hq-update-")
	if err != nil {
		return envErr("%v", err)
	}
	defer os.RemoveAll(dir)
	if err := d.releases.Download(latest, dir, d.asset, update.SumsFile); err != nil {
		return envErr("could not download hq %s: %v", latest, err)
	}
	bin, err := os.ReadFile(filepath.Join(dir, d.asset))
	if err != nil {
		return envErr("%v", err)
	}
	sums, err := os.ReadFile(filepath.Join(dir, update.SumsFile))
	if err != nil {
		return envErr("%v", err)
	}
	if err := update.Verify(sums, d.asset, bin); err != nil {
		return usageErr("%v; nothing was replaced", err)
	}
	if err := update.Replace(exe, bin); err != nil {
		return envErr("could not replace %s: %v", exe, err)
	}
	fmt.Fprintf(env.Stdout, "updated hq %s -> %s\n", current, latest)
	return nil
}

// mayUpdateCommand is the hidden command install.sh asks the installed hq
// before it replaces it: may it be replaced by release TAG of generation
// GEN (empty when the release publishes none)? It answers as hq update
// does, refusing with its line and exit code 1 (ADR 0020).
const mayUpdateCommand = "__may-update"

// generationCommand is the hidden command that prints this hq's
// generation; an hq from before generations does not know it, which is
// how install.sh tells one from the other (ADR 0020).
const generationCommand = "__generation"

func runMayUpdate(_ Env, d deps, args []string) error {
	usage := usageErr("usage: hq %s TAG GENERATION", mayUpdateCommand)
	if len(args) != 2 || args[0] == "" {
		return usage
	}
	gen, published := 0, args[1] != ""
	if published {
		var err error
		if gen, err = strconv.Atoi(args[1]); err != nil || gen < 1 {
			return usage
		}
	}
	return mayUpdate(d, args[0], gen, published)
}

func runGeneration(env Env, _ deps, args []string) error {
	if len(args) != 0 {
		return usageErr("usage: hq %s", generationCommand)
	}
	fmt.Fprintln(env.Stdout, version.Generation)
	return nil
}

// mayUpdate applies the update rule to release tag, of generation gen
// (published false when it publishes none, which counts as another): an
// update within this hq's generation goes through whatever agents exist;
// one across generations is refused while any agent hq knows exists, or
// while hq cannot tell whether one does, with exit code 1 (spec §4.1, ADR
// 0020). No option lets it through.
func mayUpdate(d deps, tag string, gen int, published bool) error {
	if published && gen == version.Generation {
		return nil
	}
	theirs := "it names none"
	if published {
		theirs = strconv.Itoa(gen)
	}
	refused := fmt.Sprintf("not updated to %s: it is of another generation (%s, this hq %d)", tag, theirs, version.Generation)
	in, err := agentsInTheWay(d)
	if err != nil {
		return usageErr("%s and hq cannot tell whether agents exist: %s", refused, err.Msg)
	}
	if len(in) == 0 {
		return nil
	}
	return usageErr("%s and agents exist; end them first with hq stop or hq kill NAME: %s", refused, strings.Join(in, ", "))
}

// agentsInTheWay lists every agent hq knows, each with its state, as an
// update across generations names them: the agents of this tmux server in
// any state, ended ones included, as hq ls shows them, then its lost agents
// in every repository hq knows (lostAgents). Asking sbx is not optional
// here: when it fails, does not answer in time or is too old, hq cannot
// know the lost agents, and that is the error.
func agentsInTheWay(d deps) ([]string, *Error) {
	asked := d.now()
	all, err := d.pollSandboxes()
	if err != nil {
		return nil, asError(sbxErr(err))
	}
	ws, err := d.tmux.Windows()
	if err != nil {
		return nil, asError(tmuxErr(err))
	}
	running := agent.Sandboxes{Running: map[string]bool{}, Asked: asked}
	for _, s := range all {
		running.Running[s.Name] = s.Running()
	}
	var in []string
	for _, a := range agent.Look(ws, d.readAgent, d.takeQueued, running, d.now(), d.store()) {
		in = append(in, a.Name+" ("+a.State+")")
	}
	for _, a := range lostAgents(d, ws, knownRepositories(d, all)) {
		in = append(in, a.Name+" (lost)")
	}
	return in, nil
}

func (d deps) rememberLatest(latest string) {
	now := d.now().Unix()
	_ = d.updatePrefs(func(p *prefs.Prefs) bool {
		p.UpdateChecked, p.LatestRelease = now, latest
		return true
	})
}

// checkDue reports whether the daily update check is due and, when it is,
// takes it: the check's moment is stored first, under the preferences' lock,
// so of the hq processes that find it due at once only one checks, and a
// failed check waits a day like a successful one. A dev build never checks.
func checkDue(d deps) bool {
	if !update.IsRelease(version.Version) {
		return false
	}
	now := d.now()
	if !due(d.loadPrefs().UpdateChecked, now) {
		return false
	}
	took := false
	_ = d.updatePrefs(func(p *prefs.Prefs) bool {
		if !due(p.UpdateChecked, now) {
			return false
		}
		p.UpdateChecked, took = now.Unix(), true
		return true
	})
	return took
}

// due reports whether a check made at checked (unix seconds) is a day old at
// now; one dated after now (the clock was set back) is due too.
func due(checked int64, now time.Time) bool {
	age := now.Sub(time.Unix(checked, 0))
	return age >= updateCheckEvery || age < 0
}

// checkUpdate asks for the latest release and keeps it; a failure is silent
// and the next check comes a day later.
func checkUpdate(d deps) {
	latest, err := d.releases.Latest()
	if err != nil {
		return
	}
	_ = d.updatePrefs(func(p *prefs.Prefs) bool {
		p.LatestRelease = latest
		return true
	})
}

// checkLater starts the update check in a detached hq.
func checkLater(d deps) error {
	exe, err := d.executable()
	if err != nil {
		return err
	}
	return d.detach([]string{exe, updateCheckCommand})
}

func runUpdateCheck(_ Env, d deps, args []string) error {
	if len(args) != 0 {
		return usageErr("usage: hq %s", updateCheckCommand)
	}
	checkUpdate(d)
	return nil
}

// newerRelease is the release newer than this hq that the last check found,
// or empty; a dev build knows none.
func newerRelease(d deps) string {
	if !update.IsRelease(version.Version) {
		return ""
	}
	if p := d.loadPrefs(); update.Newer(p.LatestRelease, version.Version) {
		return p.LatestRelease
	}
	return ""
}

// updateHint is the line hq --version adds when a newer release is known.
func updateHint(d deps) string {
	if r := newerRelease(d); r != "" {
		return r + " available - hq update\n"
	}
	return ""
}

// updateDuties tells from a command line whether that command starts the
// update check when it is due, and whether it ends with the update notice
// (spec §4.2). The user's own commands do both; --version shows the hint in
// its own output. The dashboard checks from its list and shows the hint in
// its header; hq mcp speaks JSON-RPC on its output, hq update checks itself,
// and the hidden commands hq runs for itself, --json output and unknown
// commands get neither.
func updateDuties(args []string) (check, notice bool) {
	if len(args) == 0 {
		return false, false
	}
	switch args[0] {
	case "--version", "-V":
		return true, false
	case "--help", "-h", "help":
		return true, true
	case "dash", "mcp", "update":
		return false, false
	}
	for _, c := range commands() {
		if c.name == args[0] {
			return true, !slices.Contains(args[1:], "--json")
		}
	}
	return false, false
}

// afterCommand does a command's update duties once it is done: it starts
// the update check in the background when it is due, so no command waits on
// GitHub, and, when stderr is a terminal and the last check found a newer
// release, says so in one line.
func afterCommand(env Env, d deps, args []string) {
	check, notice := updateDuties(args)
	if check && checkDue(d) {
		_ = checkLater(d)
	}
	if notice && d.stderrTerminal(env.Stderr) {
		if r := newerRelease(d); r != "" {
			fmt.Fprintf(env.Stderr, "hq %s is available - run hq update\n", r)
		}
	}
}
