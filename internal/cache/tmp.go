package cache

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// tmpMaxAge is how old an entry in tmp/ must be before it's taken for a
// leftover of a ws that was killed. tmp/ also holds other processes' clones
// in progress, which can run for many minutes on a big repo or a slow link;
// a day is far beyond any clone, and a leftover only costs disk until then.
const tmpMaxAge = 24 * time.Hour

// startOp runs once per Cache, before its first operation that touches
// repos: it clears leftovers from tmp/.
func (c *Cache) startOp() {
	c.cleanOnce.Do(c.cleanTmp)
}

// cleanTmp removes entries in tmp/ last modified more than tmpMaxAge ago.
// It's best effort: a failure is a warning, like the Time Machine exclusion.
func (c *Cache) cleanTmp() {
	dir := c.tmpDir()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		c.warn("couldn't clean %s: %v", dir, err)
		return
	}
	cutoff := c.now().Add(-tmpMaxAge)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if err := os.RemoveAll(path); err != nil {
			c.warn("couldn't remove leftover %s: %v", path, err)
		}
	}
}
