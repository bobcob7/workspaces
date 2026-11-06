package loop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/bobcob7/workspaces/internal/lockfile"
)

// Store keeps loop records in dir, one <name>.toml each.
type Store struct{ dir string }

// NewStore returns a store of loop records in dir. It does not touch dir
// until it is used.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// Lock takes dir/.lock exclusively; RLock takes it shared. Both create dir
// (0o700) first and return the unlock function.
func (s *Store) Lock(ctx context.Context) (func(), error) {
	return s.lock(ctx, lockfile.Exclusive)
}

// RLock takes dir/.lock shared; see Lock.
func (s *Store) RLock(ctx context.Context) (func(), error) {
	return s.lock(ctx, lockfile.Shared)
}

func (s *Store) lock(ctx context.Context, take func(context.Context, string) (func(), error)) (func(), error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating loops directory: %w", err)
	}
	unlock, err := take(ctx, filepath.Join(s.dir, ".lock"))
	if err != nil {
		return nil, fmt.Errorf("locking loops: %w", err)
	}
	return unlock, nil
}

// List reads every <name>.toml, sorted by name. Names starting with "."
// and files not ending in ".toml" are ignored. A missing dir is no loops.
// A file that doesn't parse, or whose Version > FormatVersion, is skipped
// and described in a warning that names the file.
func (s *Store) List() (loops []Loop, warnings []string, err error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("reading loops: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".toml") {
			continue
		}
		loop, err := readLoop(filepath.Join(s.dir, name))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipping %s: %v", name, err))
			continue
		}
		loops = append(loops, loop)
	}
	slices.SortFunc(loops, func(a, b Loop) int {
		return strings.Compare(a.Name, b.Name)
	})
	return loops, warnings, nil
}

// Get reads one loop; ErrNotFound when the file doesn't exist.
func (s *Store) Get(name string) (Loop, error) {
	path, err := s.path(name)
	if err != nil {
		return Loop{}, err
	}
	loop, err := readLoop(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Loop{}, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return loop, err
}

// Create writes a new record; ErrExists when the file exists.
func (s *Store) Create(l *Loop) error {
	path, err := s.path(l.Name)
	if err != nil {
		return err
	}
	_, err = os.Stat(path)
	if err == nil {
		return fmt.Errorf("%w: %s", ErrExists, l.Name)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("checking loop %s: %w", l.Name, err)
	}
	return s.Save(l)
}

// Save overwrites a record. Both Create and Save set Version to
// FormatVersion, then write <name>.toml.tmp and rename it into place.
func (s *Store) Save(l *Loop) error {
	path, err := s.path(l.Name)
	if err != nil {
		return err
	}
	l.Version = FormatVersion
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(l); err != nil {
		return fmt.Errorf("encoding loop %s: %w", l.Name, err)
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("creating loops directory: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("writing loop %s: %w", l.Name, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("writing loop %s: %w", l.Name, err)
	}
	return nil
}

// Remove deletes the record; ErrNotFound when it doesn't exist.
func (s *Store) Remove(name string) error {
	path, err := s.path(name)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if err != nil {
		return fmt.Errorf("removing loop %s: %w", name, err)
	}
	return nil
}

// path is the record file for name. ValidateName keeps a name from being a
// path.
func (s *Store) path(name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	return filepath.Join(s.dir, name+".toml"), nil
}

// readLoop reads and checks one record file. A record of a newer version
// is refused, so it is never rewritten by an older ws (ADR 0027).
func readLoop(path string) (Loop, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Loop{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var l Loop
	meta, err := toml.Decode(string(data), &l)
	if err != nil {
		return Loop{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if l.Version > FormatVersion {
		return Loop{}, fmt.Errorf("written by a newer ws (version %d; this ws reads up to %d); upgrade ws", l.Version, FormatVersion)
	}
	if l.Version < 1 {
		return Loop{}, fmt.Errorf("parsing %s: unsupported version %d", path, l.Version)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return Loop{}, fmt.Errorf("parsing %s: unknown key %s", path, undecoded[0])
	}
	return l, nil
}
