package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/lockfile"
)

const (
	tmpDirName   = ".tmp"
	lockFileName = ".lock"
	storeDirMode = 0o700
	tmpMaxAge    = time.Hour
	idAttempts   = 5
)

// Store is CONFIG_DIR/artifacts (config.Paths.Artifacts).
type Store struct {
	dir           string
	random        io.Reader
	now           func() time.Time
	maxStoreBytes int64
}

// NewStore returns a Store over dir. random is crypto/rand.Reader in ws.
func NewStore(dir string, random io.Reader, now func() time.Time) *Store {
	return &Store{dir: dir, random: random, now: now, maxStoreBytes: MaxStoreBytes}
}

// NewArtifact is what Add stores.
type NewArtifact struct {
	Name  string
	Title string
	Note  string
	Data  []byte
	By    Actor
	Root  string
}

// NewRevision is what Put appends.
type NewRevision struct {
	Data    []byte
	Note    string
	By      Actor
	BaseRev int
}

// PutResult is Put's record and how many threads were anchored and
// detached after moving anchors (both 0 until T2).
type PutResult struct {
	Record   Record
	Anchored int
	Detached int
}

// Entry is one artifact in List, with its thread counts.
type Entry struct {
	Record  Record
	Threads ThreadCounts
}

// Add stores a new artifact with its first revision.
func (s *Store) Add(ctx context.Context, n *NewArtifact) (Record, error) {
	kind, ct, err := validateNew(n)
	if err != nil {
		return Record{}, err
	}
	if err = s.prepare(); err != nil {
		return Record{}, err
	}
	if err = s.checkQuota(int64(len(n.Data))); err != nil {
		return Record{}, err
	}
	id, dir, err := s.createDir()
	if err != nil {
		return Record{}, err
	}
	rec := newRecord(id, kind, ct, n, s.now().UTC().Truncate(time.Second))
	if err := s.writeNew(ctx, dir, &rec, n.Data); err != nil {
		_ = os.RemoveAll(dir)
		return Record{}, err
	}
	return rec, nil
}

// Put appends a revision to artifact id, moving its threads' anchors.
func (s *Store) Put(ctx context.Context, id string, n *NewRevision) (PutResult, error) {
	dir, err := s.dirOf(id)
	if err != nil {
		return PutResult{}, err
	}
	if err = s.prepare(); err != nil {
		return PutResult{}, err
	}
	if err = CheckLine("note", n.Note); err != nil {
		return PutResult{}, err
	}
	unlock, err := lockfile.Exclusive(ctx, filepath.Join(dir, lockFileName))
	if err != nil {
		return PutResult{}, err
	}
	defer unlock()
	rec, err := readRecord(dir)
	if err != nil {
		return PutResult{}, err
	}
	return s.appendRevision(dir, &rec, n)
}

// Show is the record and its thread counts.
func (s *Store) Show(id string) (Entry, error) {
	dir, err := s.dirOf(id)
	if err != nil {
		return Entry{}, err
	}
	rec, err := readRecord(dir)
	if err != nil {
		return Entry{}, err
	}
	threads, err := readThreads(dir)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Record: rec, Threads: Counts(threads)}, nil
}

// Read is the record, revision rev (0 = latest) and its bytes.
func (s *Store) Read(id string, rev int) (Record, Revision, []byte, error) {
	dir, err := s.dirOf(id)
	if err != nil {
		return Record{}, Revision{}, nil, err
	}
	rec, err := readRecord(dir)
	if err != nil {
		return Record{}, Revision{}, nil, err
	}
	r, ok := findRevision(&rec, rev)
	if !ok {
		return Record{}, Revision{}, nil, fmt.Errorf("%w: %s has no revision r%d", ErrNotFound, id, rev)
	}
	data, err := os.ReadFile(revisionPath(dir, r.Rev))
	if err != nil {
		return Record{}, Revision{}, nil, fmt.Errorf("reading %s r%d: %w", id, r.Rev, err)
	}
	return rec, r, data, nil
}

// List is every artifact, latest revision's created_at first (ties by
// id); an artifact whose record can't be read is skipped and named in
// the warnings.
func (s *Store) List() ([]Entry, []string, error) {
	ids, err := s.ids()
	if err != nil {
		return nil, nil, err
	}
	var found []*Entry
	var warnings []string
	for _, id := range ids {
		e, err := s.Show(id)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		found = append(found, &e)
	}
	slices.SortFunc(found, compareEntries)
	entries := make([]Entry, 0, len(found))
	for _, e := range found {
		entries = append(entries, *e)
	}
	return entries, warnings, nil
}

// Remove deletes the artifact: it is renamed into .tmp under its lock,
// then removed.
func (s *Store) Remove(ctx context.Context, id string) error {
	dir, err := s.dirOf(id)
	if err != nil {
		return err
	}
	if err = s.prepare(); err != nil {
		return err
	}
	unlock, err := lockfile.Exclusive(ctx, filepath.Join(dir, lockFileName))
	if err != nil {
		return err
	}
	trash := filepath.Join(s.tmpDir(), fmt.Sprintf("%s-%d", id, s.now().UnixNano()))
	err = os.Rename(dir, trash)
	unlock()
	if err != nil {
		return fmt.Errorf("removing %s: %w", id, err)
	}
	return os.RemoveAll(trash)
}

// Threads is the artifact's threads (for T2 and the CLI).
func (s *Store) Threads(id string) ([]Thread, error) {
	dir, err := s.dirOf(id)
	if err != nil {
		return nil, err
	}
	return readThreads(dir)
}

func (s *Store) tmpDir() string {
	return filepath.Join(s.dir, tmpDirName)
}

// prepare creates the store's directories and clears stale temporary files.
func (s *Store) prepare() error {
	if err := os.MkdirAll(s.tmpDir(), storeDirMode); err != nil {
		return fmt.Errorf("creating %s: %w", s.tmpDir(), err)
	}
	s.sweepTmp()
	return nil
}

func (s *Store) sweepTmp() {
	entries, err := os.ReadDir(s.tmpDir())
	if err != nil {
		return
	}
	cutoff := s.now().Add(-tmpMaxAge)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(s.tmpDir(), e.Name()))
	}
}

// dirOf is the directory of artifact id, or ErrNotFound.
func (s *Store) dirOf(id string) (string, error) {
	if !ValidID(id) {
		return "", fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	dir := filepath.Join(s.dir, id)
	_, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", dir, err)
	}
	return dir, nil
}

// ids lists the artifact directories in the store.
func (s *Store) ids() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.dir, err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && ValidID(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// createDir draws an id and makes its directory, retrying when the id is
// taken.
func (s *Store) createDir() (string, string, error) {
	for range idAttempts {
		id, err := NewID(s.random)
		if err != nil {
			return "", "", err
		}
		dir := filepath.Join(s.dir, id)
		err = os.Mkdir(dir, storeDirMode)
		if err == nil {
			return id, dir, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", "", fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	return "", "", fmt.Errorf("creating an artifact directory: %w", fs.ErrExist)
}

func (s *Store) writeNew(ctx context.Context, dir string, rec *Record, data []byte) error {
	unlock, err := lockfile.Exclusive(ctx, filepath.Join(dir, lockFileName))
	if err != nil {
		return err
	}
	defer unlock()
	if err := writeAtomic(s.tmpDir(), revisionPath(dir, 1), data); err != nil {
		return err
	}
	return writeRecord(s.tmpDir(), dir, rec)
}

func (s *Store) appendRevision(dir string, rec *Record, n *NewRevision) (PutResult, error) {
	latest := rec.Latest().Rev
	if n.BaseRev != 0 && n.BaseRev != latest {
		return PutResult{}, &ConflictError{Base: n.BaseRev, Latest: latest}
	}
	if len(rec.Revisions) >= MaxRevisions {
		return PutResult{}, fmt.Errorf("%w: an artifact keeps at most %d revisions", ErrFull, MaxRevisions)
	}
	kind, ct, err := Detect(rec.Name, n.Data)
	if err != nil {
		return PutResult{}, err
	}
	if kind != rec.Kind {
		return PutResult{}, fmt.Errorf("%w: r%d must be %s like the artifact", ErrInvalid, latest+1, rec.Kind)
	}
	if err := s.checkQuota(int64(len(n.Data))); err != nil {
		return PutResult{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	rev := newRevision(latest+1, ct, n.Data, n.Note, n.By, now)
	if err := writeAtomic(s.tmpDir(), revisionPath(dir, rev.Rev), n.Data); err != nil {
		return PutResult{}, err
	}
	rec.Revisions = append(rec.Revisions, rev)
	rec.ContentType = ct
	if err := writeRecord(s.tmpDir(), dir, rec); err != nil {
		return PutResult{}, err
	}
	return PutResult{Record: *rec}, nil
}

// checkQuota is ErrFull when extra more bytes would take the store past its
// limit.
func (s *Store) checkQuota(extra int64) error {
	used, err := s.storedBytes()
	if err != nil {
		return err
	}
	if used+extra > s.maxStoreBytes {
		return fmt.Errorf("%w: the store holds at most %d GiB", ErrFull, s.maxStoreBytes>>30)
	}
	return nil
}

// storedBytes sums the sizes of every revision in the store. Records that
// can't be read count as empty; List reports them.
func (s *Store) storedBytes() (int64, error) {
	ids, err := s.ids()
	if err != nil {
		return 0, err
	}
	var total int64
	for _, id := range ids {
		rec, err := readRecord(filepath.Join(s.dir, id))
		if err != nil {
			continue
		}
		for _, rev := range rec.Revisions {
			total += rev.Size
		}
	}
	return total, nil
}

func validateNew(n *NewArtifact) (Kind, string, error) {
	if err := CheckName(n.Name); err != nil {
		return "", "", err
	}
	if err := CheckLine("title", n.Title); err != nil {
		return "", "", err
	}
	if err := CheckLine("note", n.Note); err != nil {
		return "", "", err
	}
	return Detect(n.Name, n.Data)
}

func newRecord(id string, kind Kind, ct string, n *NewArtifact, now time.Time) Record {
	return Record{
		Version:     FormatVersion,
		ID:          id,
		Name:        n.Name,
		Title:       optionalString(n.Title),
		Kind:        kind,
		ContentType: ct,
		CreatedAt:   now,
		CreatedBy:   n.By,
		Root:        optionalString(n.Root),
		Revisions:   []Revision{newRevision(1, ct, n.Data, n.Note, n.By, now)},
	}
}

func newRevision(rev int, ct string, data []byte, note string, by Actor, now time.Time) Revision {
	sum := sha256.Sum256(data)
	return Revision{
		Rev:         rev,
		ContentType: ct,
		Size:        int64(len(data)),
		SHA256:      hex.EncodeToString(sum[:]),
		CreatedAt:   now,
		CreatedBy:   by,
		Note:        optionalString(note),
	}
}

func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func findRevision(rec *Record, rev int) (Revision, bool) {
	if rev == 0 {
		return rec.Latest(), true
	}
	for _, r := range rec.Revisions {
		if r.Rev == rev {
			return r, true
		}
	}
	return Revision{}, false
}

func compareEntries(a, b *Entry) int {
	ta, tb := a.Record.Latest().CreatedAt, b.Record.Latest().CreatedAt
	switch {
	case ta.After(tb):
		return -1
	case tb.After(ta):
		return 1
	}
	return strings.Compare(a.Record.ID, b.Record.ID)
}
