package loop

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// ErrNewerData is a history, memory, or run-mark file from a newer ws.
var ErrNewerData = errors.New("loop data written by a newer ws; upgrade ws")

// DataStore keeps what ws records about loops besides their records:
// <stateDir>/.loops/<name>/ (history, reports, memory) and
// <stateDir>/<workspace>/loop.toml (the run mark), ADR 0056.
type DataStore struct{ stateDir string }

// NewDataStore returns the loop data kept under stateDir. It does not touch
// stateDir until it is used.
func NewDataStore(stateDir string) *DataStore {
	return &DataStore{stateDir: stateDir}
}

// StateDir is the directory the data is kept under.
func (d *DataStore) StateDir() string {
	return d.stateDir
}

// Dir is the directory of a loop's data: <stateDir>/.loops/<name>.
func (d *DataStore) Dir(name string) string {
	return filepath.Join(d.stateDir, ".loops", name)
}

// HistoryPath is the loop's history.json.
func (d *DataStore) HistoryPath(name string) string {
	return filepath.Join(d.Dir(name), "history.json")
}

// MemoryPath is the loop's memory.json.
func (d *DataStore) MemoryPath(name string) string {
	return filepath.Join(d.Dir(name), "memory.json")
}

// ReportPath is the saved report of run seq: reports/<seq>.md.
func (d *DataStore) ReportPath(name string, seq int) string {
	return filepath.Join(d.Dir(name), "reports", fmt.Sprintf("%d.md", seq))
}

// RemoveAll deletes the loop's data directory; a missing one is not an
// error.
func (d *DataStore) RemoveAll(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if err := os.RemoveAll(d.Dir(name)); err != nil {
		return fmt.Errorf("removing data of loop %s: %w", name, err)
	}
	return nil
}

// RunMarkVersion is the newest run mark version this ws reads and writes.
const RunMarkVersion = 1

// RunMark is <stateDir>/<workspace>/loop.toml: which loop started the run.
type RunMark struct {
	Version   int       `toml:"version"`
	Loop      string    `toml:"loop"`
	StartedAt time.Time `toml:"started_at"`
}

// WriteRunMark writes the workspace's run mark atomically, with Version set
// to RunMarkVersion.
func (d *DataStore) WriteRunMark(workspaceID string, m RunMark) error {
	if err := validateWorkspaceID(workspaceID); err != nil {
		return err
	}
	m.Version = RunMarkVersion
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(m); err != nil {
		return fmt.Errorf("encoding run mark of %s: %w", workspaceID, err)
	}
	return writeFileAtomic(d.runMarkPath(workspaceID), buf.Bytes())
}

// ReadRunMark reads the workspace's run mark. found is false when there is
// no mark; a mark of a newer version is ErrNewerData.
func (d *DataStore) ReadRunMark(workspaceID string) (m RunMark, found bool, err error) {
	if err = validateWorkspaceID(workspaceID); err != nil {
		return RunMark{}, false, err
	}
	path := d.runMarkPath(workspaceID)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return RunMark{}, false, nil
	}
	if err != nil {
		return RunMark{}, false, fmt.Errorf("reading %s: %w", path, err)
	}
	if _, err := toml.Decode(string(data), &m); err != nil {
		return RunMark{}, false, fmt.Errorf("parsing %s: %w", path, err)
	}
	if m.Version > RunMarkVersion {
		return RunMark{}, false, fmt.Errorf("%s: %w", path, ErrNewerData)
	}
	return m, true, nil
}

func (d *DataStore) runMarkPath(workspaceID string) string {
	return filepath.Join(d.stateDir, workspaceID, "loop.toml")
}

// validateWorkspaceID checks that id is one path element, so it can name a
// directory under the state dir.
func validateWorkspaceID(id string) error {
	if id == "" || strings.HasPrefix(id, ".") || strings.Contains(id, "/") {
		return fmt.Errorf("%w: workspace id %q is not one path element", ErrInvalid, id)
	}
	return nil
}

// writeFileAtomic writes data to a temp file in path's directory, then
// renames it into place, so a reader never sees part of the file. The
// directory is created 0o700 and the file is 0o600.
func writeFileAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", path, err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", path, err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("renaming into %s: %w", path, err)
	}
	return nil
}
