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

// formatVersion is the newest format version of the session and trust
// records that this ws reads and writes (ADR 0027).
const formatVersion = 1

const (
	// visibleVersion is the version of a visible workspace's
	// workspace.toml, the same as before hidden existed.
	visibleVersion = 1
	// hiddenVersion is the version of a hidden workspace's workspace.toml,
	// so an older ws leaves it alone (ADR 0043).
	hiddenVersion = 2
)

// Workspace is a workspace's state/<id>/workspace.toml (ADR 0012).
type Workspace struct {
	Version int    `toml:"version"`
	ID      string `toml:"id"`
	// Name is the --name the workspace was created with, for display; empty
	// for a generated id.
	Name      string    `toml:"name"`
	Path      string    `toml:"path"`
	CreatedAt time.Time `toml:"created_at"`
	// Hidden leaves the workspace out of ws list without -a, and out of
	// the desktop app's default views (ADR 0043).
	Hidden bool   `toml:"hidden,omitempty"`
	Repos  []Repo `toml:"repos"`
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

// SessionSubdir is the directory, relative to the workspace, that ws shell
// and ws claude start a session in: the repo's directory when the
// workspace has exactly one repo, else empty, for the workspace itself
// (ADR 0028).
func (ws *Workspace) SessionSubdir() string {
	if len(ws.Repos) != 1 || !filepath.IsLocal(ws.Repos[0].Path) {
		return ""
	}
	sub := filepath.Clean(ws.Repos[0].Path)
	if sub == "." {
		return ""
	}
	return sub
}

// SessionDir is the directory ws shell and ws claude start a session in:
// the workspace's directory joined with SessionSubdir.
func (ws *Workspace) SessionDir() string {
	return filepath.Join(ws.Path, ws.SessionSubdir())
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
	if err := checkVersion(ws.Version, hiddenVersion); err != nil {
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

// checkVersion accepts a record's version if this ws knows it, up to
// newest. A newer one was written by a newer ws, and the workspace is left
// alone until ws is upgraded (ADR 0027).
func checkVersion(version, newest int) error {
	if version > newest {
		return fmt.Errorf("written by a newer ws (version %d; this ws reads up to %d); upgrade ws", version, newest)
	}
	if version < 1 {
		return fmt.Errorf("unsupported version %d", version)
	}
	return nil
}

// writeRecord writes a workspace.toml through a temp file and a rename, so
// a reader never sees part of one. Its version follows Hidden (ADR 0043).
func writeRecord(path string, ws *Workspace) error {
	ws.Version = visibleVersion
	if ws.Hidden {
		ws.Version = hiddenVersion
	}
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
