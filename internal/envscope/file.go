package envscope

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

const (
	fileName    = "scope.json"
	fileVersion = 1
)

// Parent is the workspace that started a cross-environment child.
type Parent struct {
	Environment string `json:"environment"`
	Workspace   string `json:"workspace"`
}

// File is state/<id>/scope.json: the scope a workspace was given.
type File struct {
	Version      int       `json:"version"` // 1
	Environments []Ref     `json:"environments"`
	Self         string    `json:"self"`
	Source       string    `json:"source"` // "granted" | "inherited" | "narrowed"
	Parent       *Parent   `json:"parent"`
	WrittenAt    time.Time `json:"written_at"`
}

// Path is the scope file of workspace id under stateDir.
func Path(stateDir, id string) string {
	return filepath.Join(stateDir, id, fileName)
}

// FileFor is the scope file for s, written by source at now.
func FileFor(s Scope, source string, parent *Parent, now time.Time) *File {
	return &File{
		Version:      fileVersion,
		Environments: s.Refs(),
		Self:         s.self,
		Source:       source,
		Parent:       parent,
		WrittenAt:    now.UTC(),
	}
}

// Read returns Only(self) and nil, nil when the file is missing; Only(self)
// and an error wrapping ErrInvalid when it is malformed, too big, has
// unknown fields, version != 1, > MaxEntries, a bad id, self.ID absent, or
// File.Self != self.ID. Never a wider scope on any error.
func Read(stateDir, id string, self Ref) (Scope, *File, error) {
	fallback := Only(self)
	if err := checkWorkspaceDir(id); err != nil {
		return fallback, nil, err
	}
	data, err := readScopeFile(Path(stateDir, id))
	if errors.Is(err, fs.ErrNotExist) {
		return fallback, nil, nil
	}
	if err != nil {
		return fallback, nil, err
	}
	f, err := decodeFile(data)
	if err != nil {
		return fallback, nil, err
	}
	if f.Self != self.ID {
		return fallback, nil, fmt.Errorf("%w: scope file's self %q isn't %q", ErrInvalid, f.Self, self.ID)
	}
	scope, err := scopeOf(f)
	if err != nil {
		return fallback, nil, err
	}
	return scope, f, nil
}

// Write writes f atomically (temp file + rename) with mode 0600, creating
// state/<id>/ (0700) if needed. f is validated as Read would.
func Write(stateDir, id string, f *File) error {
	if err := checkWorkspaceDir(id); err != nil {
		return err
	}
	if err := validateFile(f); err != nil {
		return err
	}
	data, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("encoding scope file: %w", err)
	}
	if len(data) > MaxFileBytes {
		return fmt.Errorf("%w: scope file is over %d bytes", ErrInvalid, MaxFileBytes)
	}
	dir := filepath.Join(stateDir, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	return writeAtomic(Path(stateDir, id), data)
}

// readScopeFile reads path, refusing a file that is too big or whose mode is
// wider than 0600 (on platforms with Unix modes).
func readScopeFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading scope file: %w", err)
	}
	if info.Size() > MaxFileBytes {
		return nil, fmt.Errorf("%w: scope file is over %d bytes", ErrInvalid, MaxFileBytes)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&^os.FileMode(0o600) != 0 {
		return nil, fmt.Errorf("%w: scope file mode %v is wider than 0600", ErrInvalid, info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading scope file: %w", err)
	}
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("%w: scope file is over %d bytes", ErrInvalid, MaxFileBytes)
	}
	return data, nil
}

// decodeFile parses and validates a scope file: no unknown fields, no data
// after the object.
func decodeFile(data []byte) (*File, error) {
	var f File
	if err := decodeStrict(data, &f); err != nil {
		return nil, err
	}
	if err := validateFile(&f); err != nil {
		return nil, err
	}
	return &f, nil
}

// decodeStrict decodes exactly one JSON object into v, refusing unknown fields.
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: data after the object", ErrInvalid)
	}
	return nil
}

func validateFile(f *File) error {
	if f.Version != fileVersion {
		return fmt.Errorf("%w: version %d isn't %d", ErrInvalid, f.Version, fileVersion)
	}
	if len(f.Environments) > MaxEntries {
		return fmt.Errorf("%w: %d environments, at most %d", ErrInvalid, len(f.Environments), MaxEntries)
	}
	if !validSource(f.Source) {
		return fmt.Errorf("%w: source %q", ErrInvalid, f.Source)
	}
	if f.Parent != nil && !validID(f.Parent.Environment) {
		return fmt.Errorf("%w: parent environment %q", ErrInvalid, f.Parent.Environment)
	}
	selfListed := false
	for _, ref := range f.Environments {
		if err := validRef(ref); err != nil {
			return err
		}
		selfListed = selfListed || ref.ID == f.Self
	}
	if !selfListed {
		return fmt.Errorf("%w: self %q isn't among the environments", ErrInvalid, f.Self)
	}
	return nil
}

// validSource is granted, inherited or narrowed.
func validSource(source string) bool {
	switch source {
	case "granted", "inherited", "narrowed":
		return true
	}
	return false
}

// scopeOf builds the scope of a validated file.
func scopeOf(f *File) (Scope, error) {
	i := slices.IndexFunc(f.Environments, func(ref Ref) bool { return ref.ID == f.Self })
	if i < 0 {
		return Scope{}, fmt.Errorf("%w: self %q isn't among the environments", ErrInvalid, f.Self)
	}
	return New(f.Environments[i], f.Environments)
}

// checkWorkspaceDir refuses an id that isn't one path element, before any path is built from it.
func checkWorkspaceDir(id string) error {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("%w: workspace id %q isn't one path element", ErrInvalid, id)
	}
	return nil
}

// writeAtomic replaces path with data through a temp file in the same
// directory, mode 0600.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(tmp.Name()) // best effort: the write already failed
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name()) // best effort: the rename already failed
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}
