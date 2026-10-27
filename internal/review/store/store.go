// Package store keeps submitted reviews in CONFIG_DIR/reviews/, outside any
// workspace, so they outlive the review agent (ADR 0039):
//
//	<root>/<host>/<owner>/<name>/<sha>/<profile>.json
//
// is each profile's current submission, and a resubmission renames the one
// before it to <profile>.<n>.json, n counting up from 1. Writes happen
// under a lock per directory, through a temp file and a rename.
package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/repo"
)

// lockName is the directory lock's file. It starts with a dot, so it's
// never read as a submission.
const lockName = ".lock"

var (
	commitPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	profilePattern = regexp.MustCompile(`^[a-z0-9-]+$`)
	// filePattern is a submission's file name: its profile, and an ordinal
	// for a superseded one.
	filePattern = regexp.MustCompile(`^([a-z0-9-]+?)(?:\.([1-9]\d*))?\.json$`)
)

// Store is the reviews directory.
type Store struct {
	root string
}

// New returns the Store under root, normally CONFIG_DIR/reviews. Nothing is
// created until something is submitted.
func New(root string) *Store {
	return &Store{root: root}
}

// Dir is where submissions for id at commit live.
func (s *Store) Dir(id repo.ID, commit string) string {
	return filepath.Join(s.root, id.Host, filepath.FromSlash(id.Owner), id.Name, commit)
}

// Submitted is where a submission was stored.
type Submitted struct {
	// Path is the stored file, <profile>.json.
	Path string
	// Superseded is where the profile's previous submission was moved,
	// or empty when there was none.
	Superseded string
}

// Submit stores doc as profile's current submission for id at commit,
// superseding the one already there.
func (s *Store) Submit(ctx context.Context, id repo.ID, commit, profile string, doc []byte) (Submitted, error) {
	if err := checkKey(commit, profile); err != nil {
		return Submitted{}, err
	}
	dir := s.Dir(id, commit)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Submitted{}, fmt.Errorf("creating %s: %w", dir, err)
	}
	unlock, err := lockfile.Exclusive(ctx, filepath.Join(dir, lockName))
	if err != nil {
		return Submitted{}, fmt.Errorf("locking %s: %w", dir, err)
	}
	defer unlock()
	current := filepath.Join(dir, profile+".json")
	superseded, err := supersede(dir, profile, current)
	if err != nil {
		return Submitted{}, err
	}
	if err := writeFile(current, doc); err != nil {
		return Submitted{}, fmt.Errorf("storing %s: %w", current, err)
	}
	return Submitted{Path: current, Superseded: superseded}, nil
}

// supersede renames current, if it exists, to the profile's next ordinal.
func supersede(dir, profile, current string) (string, error) {
	if _, err := os.Lstat(current); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("checking %s: %w", current, err)
	}
	files, err := list(dir)
	if err != nil {
		return "", err
	}
	next := 1
	for _, f := range files {
		if f.Profile == profile && f.Ordinal >= next {
			next = f.Ordinal + 1
		}
	}
	target := filepath.Join(dir, profile+"."+strconv.Itoa(next)+".json")
	if err := os.Rename(current, target); err != nil {
		return "", fmt.Errorf("superseding %s: %w", current, err)
	}
	return target, nil
}

// File is one stored submission.
type File struct {
	Profile string
	// Ordinal is the superseded submission's number, 1 for the oldest; 0
	// for the profile's current submission.
	Ordinal int
	Path    string
}

// Current reports whether f is its profile's current submission.
func (f *File) Current() bool {
	return f.Ordinal == 0
}

// Files lists the submissions for id at commit: by profile, oldest first,
// each profile's current one last. A commit with none is an empty list.
func (s *Store) Files(ctx context.Context, id repo.ID, commit string) ([]File, error) {
	if !commitPattern.MatchString(commit) {
		return nil, fmt.Errorf("%q isn't a full commit", commit)
	}
	dir := s.Dir(id, commit)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return []File{}, nil
	}
	unlock, err := lockfile.Shared(ctx, filepath.Join(dir, lockName))
	if err != nil {
		return nil, fmt.Errorf("locking %s: %w", dir, err)
	}
	defer unlock()
	return list(dir)
}

// list reads dir's submissions, sorted as Files returns them. Other names
// are skipped.
func list(dir string) ([]File, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []File{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	files := []File{}
	for _, e := range entries {
		m := filePattern.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		ordinal := 0
		if m[2] != "" {
			if ordinal, err = strconv.Atoi(m[2]); err != nil {
				continue
			}
		}
		files = append(files, File{Profile: m[1], Ordinal: ordinal, Path: filepath.Join(dir, e.Name())})
	}
	slices.SortFunc(files, compareFiles)
	return files, nil
}

// compareFiles orders by profile, then oldest first with the current
// submission last.
func compareFiles(a, b File) int {
	if c := strings.Compare(a.Profile, b.Profile); c != 0 {
		return c
	}
	return rank(a) - rank(b)
}

func rank(f File) int {
	if f.Current() {
		return int(^uint(0) >> 1)
	}
	return f.Ordinal
}

func checkKey(commit, profile string) error {
	if !commitPattern.MatchString(commit) {
		return fmt.Errorf("%q isn't a full commit", commit)
	}
	if !profilePattern.MatchString(profile) {
		return fmt.Errorf("%q isn't a profile name", profile)
	}
	return nil
}

// writeFile writes data to path through a temp file in the same directory
// and a rename, so a reader never sees part of it.
func writeFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
