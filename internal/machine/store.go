package machine

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	spawnFile  = "spawn.toml"
	resultFile = "result.toml"
	reportFile = "report.md"
)

// Store reads and writes CONFIG_DIR/state/<root>/machine/<child>/.
type Store struct{ stateDir string }

// NewStore returns a Store over stateDir, normally config.Paths.State.
func NewStore(stateDir string) *Store {
	return &Store{stateDir: stateDir}
}

// Dir is <stateDir>/<root>/machine/<child>.
func (s *Store) Dir(root, child string) string {
	return filepath.Join(s.stateDir, root, "machine", child)
}

// WriteSpawn writes spawn.toml, creating the directory (0o700), with
// sp.Version set to FormatVersion.
func (s *Store) WriteSpawn(root, child string, sp *Spawn) error {
	dir, err := s.prepareDir(root, child)
	if err != nil {
		return err
	}
	sp.Version = FormatVersion
	return writeTOML(filepath.Join(dir, spawnFile), sp)
}

// WriteResult writes report.md when report is non-nil, or deletes it (a
// missing file is fine) when report is nil, then result.toml with
// r.Version = FormatVersion and r.Report = report != nil. It creates the
// directory (0o700).
func (s *Store) WriteResult(root, child string, r *Result, report []byte) error {
	dir, err := s.prepareDir(root, child)
	if err != nil {
		return err
	}
	if err := writeReport(filepath.Join(dir, reportFile), report); err != nil {
		return err
	}
	r.Version = FormatVersion
	r.Report = report != nil
	return writeTOML(filepath.Join(dir, resultFile), r)
}

// Read reads both files; a missing file leaves its field nil, and a
// missing directory is an empty Record with Child set. A version above
// FormatVersion is ErrNewer, a file that doesn't parse ErrInvalid, both
// wrapped with the file's path.
func (s *Store) Read(root, child string) (Record, error) {
	if err := checkComponents(root, child); err != nil {
		return Record{}, err
	}
	dir := s.Dir(root, child)
	rec := Record{Child: child}
	var sp Spawn
	if found, err := readTOML(filepath.Join(dir, spawnFile), &sp); err != nil {
		return Record{}, err
	} else if found {
		rec.Spawn = &sp
	}
	var r Result
	if found, err := readTOML(filepath.Join(dir, resultFile), &r); err != nil {
		return Record{}, err
	} else if found {
		rec.Result = &r
	}
	if rec.Result != nil && rec.Result.Report {
		path, err := existingReportPath(dir)
		if err != nil {
			return Record{}, err
		}
		rec.ReportPath = path
	}
	return rec, nil
}

// prepareDir checks root and child, then creates their directory.
func (s *Store) prepareDir(root, child string) (string, error) {
	if err := checkComponents(root, child); err != nil {
		return "", err
	}
	dir := s.Dir(root, child)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	return dir, nil
}

func checkComponents(root, child string) error {
	if err := checkComponent("root", root); err != nil {
		return err
	}
	return checkComponent("child", child)
}

// checkComponent requires name to be a single, non-hidden path element.
func checkComponent(what, name string) error {
	separator := func(r rune) bool { return r == '/' || r == filepath.Separator }
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, ".") || strings.ContainsFunc(name, separator) {
		return fmt.Errorf("%w: %s %q", ErrInvalid, what, name)
	}
	return nil
}

// readTOML decodes path into v and reports whether the file exists. A
// version above FormatVersion, read from the file's own version key, is
// ErrNewer; a file that doesn't parse is ErrInvalid.
func readTOML(path string, v versioned) (found bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", path, err)
	}
	if _, decodeErr := toml.Decode(string(data), v); decodeErr != nil {
		return false, fmt.Errorf("%s: %w: %w", path, ErrInvalid, decodeErr)
	}
	if v.fileVersion() > FormatVersion {
		return false, fmt.Errorf("%s: %w", path, ErrNewer)
	}
	return true, nil
}

// versioned is a record file: spawn.toml or result.toml.
type versioned interface {
	fileVersion() int
}

func (s *Spawn) fileVersion() int { return s.Version }

func (r *Result) fileVersion() int { return r.Version }

// existingReportPath is report.md's absolute path in dir, or "" when the
// file is gone.
func existingReportPath(dir string) (string, error) {
	path := filepath.Join(dir, reportFile)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("checking %s: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", path, err)
	}
	return abs, nil
}

// writeReport replaces path with report, or removes it when report is nil.
func writeReport(path string, report []byte) error {
	if report == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("removing %s: %w", path, err)
		}
		return nil
	}
	return writeAtomic(path, func(w io.Writer) error {
		_, err := w.Write(report)
		return err
	})
}

// writeTOML writes v to path as TOML.
func writeTOML(path string, v any) error {
	return writeAtomic(path, func(w io.Writer) error {
		return toml.NewEncoder(w).Encode(v)
	})
}

// writeAtomic writes through a temp file in path's directory, then renames
// it into place, so a reader never sees part of the file. The file is 0o600.
func writeAtomic(path string, write func(io.Writer) error) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", path, err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if err = write(tmp); err != nil {
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
