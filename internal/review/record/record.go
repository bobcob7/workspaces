// Package record reads and writes a review workspace's record,
// state/<id>/review.toml: what the review covers, which profiles review it
// with which models, and who asked for it (ADR 0039).
package record

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/BurntSushi/toml"
)

// FileName is the record's name in a workspace's state directory.
const FileName = "review.toml"

// formatVersion is the newest version of review.toml this ws reads and
// writes (ADR 0027).
const formatVersion = 1

// ErrNotFound means a workspace has no review record: it isn't a review
// workspace.
var ErrNotFound = errors.New("no review record")

var (
	commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	namePattern   = regexp.MustCompile(`^[a-z0-9-]+$`)
)

// Record is state/<id>/review.toml, version 1 (ADR 0039).
type Record struct {
	Version int `toml:"version" json:"version"`
	// Repo is the reviewed repo's id, host/owner/name.
	Repo string `toml:"repo" json:"repo"`
	// Ref is the reviewed commit, a full SHA.
	Ref string `toml:"ref" json:"ref"`
	// Requester is the id of the workspace that asked for the review, or
	// empty for none.
	Requester string    `toml:"requester" json:"requester"`
	StartedAt time.Time `toml:"started_at" json:"started_at"`
	Profiles  []Profile `toml:"profiles" json:"profiles"`
	// PR is the pull request reviewed, or nil for a review of a commit.
	PR *PR `toml:"pr,omitempty" json:"pr"`
}

// Profile is one profile the review runs.
type Profile struct {
	Name string `toml:"name" json:"name"`
	// Model is the profile's model, or empty for the session's.
	Model string `toml:"model" json:"model"`
	// Auto is set when the profile joined because the review's files
	// matched it.
	Auto bool `toml:"auto" json:"auto"`
}

// PR is the pull request a review covers.
type PR struct {
	Number          int    `toml:"number" json:"number"`
	URL             string `toml:"url" json:"url"`
	Base            string `toml:"base" json:"base"`
	HeadBranch      string `toml:"head_branch" json:"head_branch"`
	CrossRepository bool   `toml:"cross_repository" json:"cross_repository"`
}

// Path is the record of workspace id under stateDir.
func Path(stateDir, id string) string {
	return filepath.Join(stateDir, id, FileName)
}

// HasProfile reports whether the review runs the profile named name.
func (r *Record) HasProfile(name string) bool {
	for _, p := range r.Profiles {
		if p.Name == name {
			return true
		}
	}
	return false
}

// ProfileNames are the names of the review's profiles, in order.
func (r *Record) ProfileNames() []string {
	names := make([]string, 0, len(r.Profiles))
	for _, p := range r.Profiles {
		names = append(names, p.Name)
	}
	return names
}

// Read reads and checks the record at path. A missing file is ErrNotFound.
func Read(path string) (Record, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Record{}, fmt.Errorf("%w: %s", ErrNotFound, path)
	}
	if err != nil {
		return Record{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var rec Record
	meta, err := toml.Decode(string(data), &rec)
	if err != nil {
		return Record{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := checkVersion(rec.Version); err != nil {
		return Record{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return Record{}, fmt.Errorf("parsing %s: unknown key %s", path, undecoded[0])
	}
	if err := rec.check(); err != nil {
		return Record{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return rec, nil
}

// Write writes rec to path as the current version, through a temp file in
// the same directory and a rename, so a reader never sees part of one.
func Write(path string, rec *Record) error {
	out := *rec
	out.Version = formatVersion
	out.StartedAt = out.StartedAt.UTC().Truncate(time.Second)
	if err := out.check(); err != nil {
		return fmt.Errorf("writing review record: %w", err)
	}
	if err := writeTOML(path, &out); err != nil {
		return fmt.Errorf("writing review record: %w", err)
	}
	return nil
}

// checkVersion accepts a record's version if this ws knows it. A newer one
// was written by a newer ws, and is left alone until ws is upgraded
// (ADR 0027).
func checkVersion(version int) error {
	if version > formatVersion {
		return fmt.Errorf("written by a newer ws (version %d; this ws reads up to %d); upgrade ws", version, formatVersion)
	}
	if version < 1 {
		return fmt.Errorf("unsupported version %d", version)
	}
	return nil
}

// check rejects a record ws couldn't have written.
func (r *Record) check() error {
	if r.Repo == "" {
		return errors.New("no repo")
	}
	if !commitPattern.MatchString(r.Ref) {
		return fmt.Errorf("ref %q isn't a full commit", r.Ref)
	}
	if len(r.Profiles) == 0 {
		return errors.New("no profiles")
	}
	seen := map[string]bool{}
	for _, p := range r.Profiles {
		if !namePattern.MatchString(p.Name) {
			return fmt.Errorf("profile name %q is invalid", p.Name)
		}
		if seen[p.Name] {
			return fmt.Errorf("profile %q listed twice", p.Name)
		}
		seen[p.Name] = true
	}
	if r.PR != nil && r.PR.Number < 1 {
		return fmt.Errorf("pr number %d is invalid", r.PR.Number)
	}
	return nil
}

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
