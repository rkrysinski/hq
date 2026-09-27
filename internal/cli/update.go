package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/update"
	"github.com/rkrysinski/hq/internal/version"
)

// updateCheckEvery is how often hq --version asks GitHub for the latest
// release (design §3.9).
const updateCheckEvery = 24 * time.Hour

func ghErr(err error) error {
	var pe *proc.Error
	if errors.As(err, &pe) && pe.NotFound {
		return envErr("gh not found; install GitHub CLI and run gh auth login")
	}
	return envErr("could not read hq's releases: %v (see gh auth status)", err)
}

func runUpdate(env Env, d deps, args []string) error {
	if len(args) != 0 {
		return usageErr("usage: hq update")
	}
	addProfile(env, d)
	latest, err := d.releases.Latest()
	if err != nil {
		return ghErr(err)
	}
	d.rememberLatest(latest)
	current := version.Version
	if !update.Newer(latest, current) {
		fmt.Fprintf(env.Stdout, "hq %s is up to date (latest release %s)\n", current, latest)
		return nil
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
		return ghErr(err)
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

func (d deps) rememberLatest(latest string) {
	p := d.loadPrefs()
	p.UpdateChecked = d.now().Unix()
	p.LatestRelease = latest
	_ = d.savePrefs(p)
}

// updateHint returns the line hq --version adds when a newer release exists,
// asking GitHub at most once a day. A dev build gets no hint, and a failed
// check is silent (the next one comes a day later).
func updateHint(d deps) string {
	if !update.IsRelease(version.Version) {
		return ""
	}
	p := d.loadPrefs()
	if d.now().Sub(time.Unix(p.UpdateChecked, 0)) >= updateCheckEvery {
		latest, err := d.releases.Latest()
		if err == nil {
			p.LatestRelease = latest
		}
		p.UpdateChecked = d.now().Unix()
		_ = d.savePrefs(p)
	}
	if update.Newer(p.LatestRelease, version.Version) {
		return fmt.Sprintf("%s available - hq update\n", p.LatestRelease)
	}
	return ""
}
