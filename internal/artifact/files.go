package artifact

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	recordFile   = "artifact.json"
	threadsFile  = "comments.json"
	revisionFile = "r"
	fileMode     = 0o600
)

// threadsDocument is comments.json.
type threadsDocument struct {
	Version int      `json:"version"`
	Threads []Thread `json:"threads"`
}

// writeAtomic writes data to a new file in tmpDir, syncs it, sets mode
// 0600, renames it to path, and syncs path's directory so the rename
// survives a crash. Only a failure before or during the rename is an error:
// once path has been replaced, a failed directory sync is ignored, so a caller
// never rolls back a write that has already taken effect.
func writeAtomic(tmpDir, path string, data []byte) error {
	return writeAtomicSync(tmpDir, path, data, syncDirectory)
}

// writeAtomicSync is writeAtomic with the directory sync injected, so tests
// can make it fail.
func writeAtomicSync(tmpDir, path string, data []byte, syncDir func(string) error) error {
	tmp, err := os.CreateTemp(tmpDir, "write-*")
	if err != nil {
		return fmt.Errorf("creating a temporary file for %s: %w", path, err)
	}
	if err := finishTemp(tmp, data); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	_ = syncDir(filepath.Dir(path))
	return nil
}

func finishTemp(tmp *os.File, data []byte) error {
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(fileMode); err != nil {
		_ = tmp.Close()
		return err
	}
	return tmp.Close()
}

// readRecord reads <dir>/artifact.json; a version above FormatVersion is
// ErrNewer, bad JSON ErrInvalid, both wrapped with the path.
func readRecord(dir string) (Record, error) {
	path := filepath.Join(dir, recordFile)
	var r Record
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return r, fmt.Errorf("reading %s: %w", path, ErrNotFound)
	}
	if err != nil {
		return r, fmt.Errorf("reading %s: %w", path, err)
	}
	if err = json.Unmarshal(data, &r); err != nil {
		return r, fmt.Errorf("reading %s: %w: %w", path, ErrInvalid, err)
	}
	err = checkRecord(path, &r)
	return r, err
}

func checkRecord(path string, r *Record) error {
	if r.Version > FormatVersion {
		return fmt.Errorf("reading %s: %w", path, ErrNewer)
	}
	if len(r.Revisions) == 0 {
		return fmt.Errorf("reading %s: %w: no revisions", path, ErrInvalid)
	}
	return nil
}

// readThreads reads <dir>/comments.json; a missing file is no threads.
func readThreads(dir string) ([]Thread, error) {
	path := filepath.Join(dir, threadsFile)
	data, err := readFileIfExists(path)
	if err != nil {
		return nil, err
	}
	return parseThreads(path, data)
}

// parseThreads decodes the bytes of the comments.json at path; nil bytes
// (no file) are no threads.
func parseThreads(path string, data []byte) ([]Thread, error) {
	if data == nil {
		return nil, nil
	}
	var doc threadsDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("reading %s: %w: %w", path, ErrInvalid, err)
	}
	if doc.Version > FormatVersion {
		return nil, fmt.Errorf("reading %s: %w", path, ErrNewer)
	}
	return doc.Threads, nil
}

// writeRecord writes artifact.json in dir.
func writeRecord(tmpDir, dir string, r *Record) error {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", recordFile, err)
	}
	return writeAtomic(tmpDir, filepath.Join(dir, recordFile), data)
}

// writeThreads writes comments.json in dir.
func writeThreads(tmpDir, dir string, threads []Thread) error {
	data, err := json.Marshal(threadsDocument{Version: FormatVersion, Threads: threads})
	if err != nil {
		return fmt.Errorf("encoding %s: %w", threadsFile, err)
	}
	return writeAtomic(tmpDir, filepath.Join(dir, threadsFile), data)
}

// revisionPath is where revision rev's bytes live in dir.
func revisionPath(dir string, rev int) string {
	return filepath.Join(dir, revisionFile+strconv.Itoa(rev))
}

// isRevisionName reports whether name is a revision file, r<n>.
func isRevisionName(name string) bool {
	digits, ok := strings.CutPrefix(name, revisionFile)
	if !ok {
		return false
	}
	_, err := strconv.ParseUint(digits, 10, 0)
	return err == nil
}
