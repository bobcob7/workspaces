package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// formatVersion is the newest format version of workspace.toml and the
// session record that this ws reads and writes (ADR 0027).
const formatVersion = 1

// Workspace is a workspace's state/<id>/workspace.toml (ADR 0012).
type Workspace struct {
	Version int    `toml:"version"`
	ID      string `toml:"id"`
	// Name is the --name the workspace was created with, for display; empty
	// for a generated id.
	Name      string    `toml:"name"`
	Path      string    `toml:"path"`
	CreatedAt time.Time `toml:"created_at"`
	Repos     []Repo    `toml:"repos"`
}

// Repo is one repo checked out in a workspace.
type Repo struct {
	// ID is the canonical repo id, host/owner/name.
	ID string `toml:"id"`
	// Spec is the spec as given.
	Spec string `toml:"spec"`
	// Path is the repo's directory, relative to the workspace.
	Path     string `toml:"path"`
	Upstream string `toml:"upstream"`
	// Ref is the branch, tag, or commit checked out; the default branch's
	// name when the spec named none.
	Ref     string `toml:"ref"`
	RefKind string `toml:"ref_kind"`
	Commit  string `toml:"commit"`
}

var (
	// errNoRecord means a state directory has no workspace.toml: a
	// creation still cloning, or interrupted while it was.
	errNoRecord = errors.New("no workspace.toml")
	// errNoDir means a record's workspace directory doesn't exist: a
	// creation not yet moved into place, or interrupted before it was, or a
	// workspace deleted by hand.
	errNoDir = errors.New("workspace directory doesn't exist")
)

// readRecord reads and checks a workspace.toml.
func readRecord(path string) (Workspace, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Workspace{}, fmt.Errorf("%w: %s", errNoRecord, path)
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var ws Workspace
	meta, err := toml.Decode(string(data), &ws)
	if err != nil {
		return Workspace{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := checkVersion(ws.Version); err != nil {
		return Workspace{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return Workspace{}, fmt.Errorf("parsing %s: unknown key %s", path, undecoded[0])
	}
	if want := filepath.Base(filepath.Dir(path)); ws.ID != want {
		return Workspace{}, fmt.Errorf("parsing %s: id %q doesn't match its directory %q", path, ws.ID, want)
	}
	if ws.Path == "" || !filepath.IsAbs(ws.Path) {
		return Workspace{}, fmt.Errorf("parsing %s: path %q isn't absolute", path, ws.Path)
	}
	return ws, nil
}

// checkVersion accepts a record's version if this ws knows it. A newer one
// was written by a newer ws, and the workspace is left alone until ws is
// upgraded (ADR 0027).
func checkVersion(version int) error {
	if version > formatVersion {
		return fmt.Errorf("written by a newer ws (version %d; this ws reads up to %d); upgrade ws", version, formatVersion)
	}
	if version < 1 {
		return fmt.Errorf("unsupported version %d", version)
	}
	return nil
}

// writeRecord writes a workspace.toml through a temp file and a rename, so
// a reader never sees part of one.
func writeRecord(path string, ws *Workspace) error {
	if err := writeTOML(path, ws); err != nil {
		return fmt.Errorf("writing workspace record: %w", err)
	}
	return nil
}

// writeTOML writes v to path as TOML through a temp file in the same
// directory and a rename.
func writeTOML(path string, v any) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := toml.NewEncoder(tmp).Encode(v); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
