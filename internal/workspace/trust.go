package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Trust is a workspace's state/<id>/trust record: the Claude Code trust
// ws added for the workspace's repo, which removal takes back (ADR 0028).
type Trust struct {
	Version int `toml:"version"`
	// Dir is the trusted directory, as Claude Code's config keys it.
	Dir string `toml:"dir"`
	// CreatedEntry reports whether ws created Dir's whole project entry,
	// rather than adding the trust key to one already there.
	CreatedEntry bool `toml:"created_entry"`
}

// RecordTrust writes workspace id's trust record, before ws adds the
// trust to Claude Code's config, so no removal can miss it.
func (s *Store) RecordTrust(id, dir string, createdEntry bool) error {
	if !validID(id) {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	t := Trust{Version: formatVersion, Dir: dir, CreatedEntry: createdEntry}
	if err := writeTOML(s.trustPath(id), &t); err != nil {
		return fmt.Errorf("writing trust record for %s: %w", id, err)
	}
	return nil
}

// untrust takes back the trust ws added for workspace id, if it recorded
// any. A failure is a warning, never a reason to keep the workspace.
func (s *Store) untrust(id string) {
	t, err := s.readTrust(id)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		s.warn("couldn't read the Claude Code trust ws added for workspace %s, so it stays in Claude Code's config: %v", id, err)
		return
	}
	if err := s.trust.Untrust(t.Dir, t.CreatedEntry); err != nil {
		s.warn("couldn't remove the Claude Code trust ws added for %s; remove projects[%q] from Claude Code's config by hand: %v", t.Dir, t.Dir, err)
	}
}

func (s *Store) readTrust(id string) (Trust, error) {
	path := s.trustPath(id)
	data, err := os.ReadFile(path)
	if err != nil {
		return Trust{}, err
	}
	var t Trust
	meta, err := toml.Decode(string(data), &t)
	if err != nil {
		return Trust{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := checkVersion(t.Version); err != nil {
		return Trust{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return Trust{}, fmt.Errorf("parsing %s: unknown key %s", path, undecoded[0])
	}
	if !filepath.IsAbs(t.Dir) {
		return Trust{}, fmt.Errorf("parsing %s: dir %q isn't absolute", path, t.Dir)
	}
	return t, nil
}

func (s *Store) trustPath(id string) string {
	return filepath.Join(s.stateOf(id), "trust")
}
