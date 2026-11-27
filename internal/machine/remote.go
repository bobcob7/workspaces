package machine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// remoteDirName is the directory under a root's machine directory that holds
// the copies of children on other environments (ADR 0082).
const remoteDirName = "remote"

// RemoteDir is <stateDir>/<root>/machine/remote/<envID>/<child>, where the
// copy of a child on another environment is kept. Each part is checked as
// Dir's are, and envID must be a valid environment id.
func (s *Store) RemoteDir(root, envID, child string) (string, error) {
	if err := checkComponents(root, child); err != nil {
		return "", err
	}
	if !envproto.ValidID(envID) {
		return "", fmt.Errorf("%w: environment %q", ErrInvalid, envID)
	}
	return filepath.Join(s.stateDir, root, "machine", remoteDirName, envID, child), nil
}

// WriteRemote copies a finished child's record from environment envID into
// its remote directory: spawn.toml when sp is set, then report.md, then
// result.toml last, so a result file means the copy is complete. A copy is
// replaced only by a result recorded after it; an older or equal one leaves
// everything as it is.
func (s *Store) WriteRemote(root, envID, child string, sp *Spawn, r *Result, report []byte) error {
	dir, err := s.remoteDirMade(root, envID, child)
	if err != nil {
		return err
	}
	current, err := s.ReadRemote(root, envID, child)
	if err != nil {
		return err
	}
	if current.Result != nil && !r.RecordedAt.After(current.Result.RecordedAt) {
		return nil
	}
	if sp != nil {
		if err := writeTOML(filepath.Join(dir, spawnFile), sp); err != nil {
			return err
		}
	}
	if err := writeReport(filepath.Join(dir, reportFile), report); err != nil {
		return err
	}
	r.Version = FormatVersion
	r.Report = report != nil
	return writeTOML(filepath.Join(dir, resultFile), r)
}

// ReadRemote reads the copy of child from environment envID.
func (s *Store) ReadRemote(root, envID, child string) (Record, error) {
	dir, err := s.RemoteDir(root, envID, child)
	if err != nil {
		return Record{}, err
	}
	return readDir(dir, child)
}

// FindRemote is the environment whose complete copy of child under root is
// kept, and whether there is one. It is how a child removed from its
// environment is read again without its environment's id.
func (s *Store) FindRemote(root, child string) (string, bool, error) {
	if err := checkComponents(root, child); err != nil {
		return "", false, err
	}
	base := filepath.Join(s.stateDir, root, "machine", remoteDirName)
	entries, err := os.ReadDir(base)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading %s: %w", base, err)
	}
	for _, e := range entries {
		if !e.IsDir() || !envproto.ValidID(e.Name()) {
			continue
		}
		exists, err := fileExists(filepath.Join(base, e.Name(), child, resultFile))
		if err != nil {
			return "", false, err
		}
		if exists {
			return e.Name(), true, nil
		}
	}
	return "", false, nil
}

// remoteDirMade is RemoteDir, created with the mode of prepareDir.
func (s *Store) remoteDirMade(root, envID, child string) (string, error) {
	dir, err := s.RemoteDir(root, envID, child)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	return dir, nil
}

// fileExists reports whether path exists; any error but not-exist is one.
func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	}
	return false, fmt.Errorf("checking %s: %w", path, err)
}
