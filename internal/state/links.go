package state

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// LinkedAt is the first of the directories under root's .git/hq down to
// dir, a directory there such as an agent's events directory or inbox, that
// is a link, or "" when none is. A sandbox can leave one there: hq lists,
// reads, writes and removes nothing through it, as it reads no file the
// sandbox writes through a link (design §7.3). A directory that is not
// there ends the walk: there is nothing under it to reach.
func LinkedAt(root, dir string) string {
	base := filepath.Join(root, ".git")
	rel, err := filepath.Rel(base, dir)
	if err != nil {
		return dir
	}
	p := base
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		p = filepath.Join(p, part)
		info, err := os.Lstat(p)
		if err != nil {
			return ""
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return p
		}
	}
	return ""
}

// LinkedErr is the error of a directory reached through the link at.
func LinkedErr(at string) error {
	return fmt.Errorf("%s is a link: hq reads no files through it", at)
}
