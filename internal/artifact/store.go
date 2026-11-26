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
	linkLockWait  time.Duration
	// recordWriter and threadsWriter write artifact.json and comments.json
	// for Put; tests replace them to make a write fail.
	recordWriter  func(tmpDir, dir string, r *Record) error
	threadsWriter func(tmpDir, dir string, threads []Thread) error
}

// NewStore returns a Store over dir. random is crypto/rand.Reader in ws.
func NewStore(dir string, random io.Reader, now func() time.Time) *Store {
	return &Store{
		dir:           dir,
		random:        random,
		now:           now,
		maxStoreBytes: MaxStoreBytes,
		linkLockWait:  LinkLockWait,
		recordWriter:  writeRecord,
		threadsWriter: writeThreads,
	}
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

// PutResult is Put's record and how many threads' anchors were moved to the
// new revision, and how many were left detached.
type PutResult struct {
	Record   Record
	Anchored int
	Detached int
}

// Entry is one artifact in List, with its thread counts and the
// conversations it was part of (ADR 0084). Warnings name a links.json that
// was ignored.
type Entry struct {
	Record        Record
	Threads       ThreadCounts
	Conversations []Conversation
	Warnings      []string
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
	unlock, err := lockArtifact(ctx, dir)
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
	setAnchorStates(threads, rec.Latest().Rev)
	links, warnings := linksOrWarning(dir, id)
	return Entry{Record: rec, Threads: Counts(threads), Conversations: Conversations(&rec, threads, links), Warnings: warnings}, nil
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
		warnings = append(warnings, e.Warnings...)
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
	unlock, err := lockArtifact(ctx, dir)
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

// lockArtifact takes the lock in dir. Taking it recreates dir, so a Remove
// that renamed the artifact away after dirOf would leave a directory holding
// only the lock: the record is checked under the lock, and such a directory
// is cleared with ErrNotFound.
func lockArtifact(ctx context.Context, dir string) (func(), error) {
	unlock, err := lockfile.Exclusive(ctx, filepath.Join(dir, lockFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, filepath.Base(dir))
	}
	if err != nil {
		return nil, err
	}
	_, err = os.Stat(filepath.Join(dir, recordFile))
	switch {
	case err == nil:
		return unlock, nil
	case errors.Is(err, fs.ErrNotExist):
		_ = os.RemoveAll(dir)
		unlock()
		return nil, fmt.Errorf("%w: %s", ErrNotFound, filepath.Base(dir))
	default:
		unlock()
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
}

// Threads is the artifact's threads, their anchors' states set for the
// latest revision.
func (s *Store) Threads(id string) ([]Thread, error) {
	dir, err := s.dirOf(id)
	if err != nil {
		return nil, err
	}
	rec, err := readRecord(dir)
	if err != nil {
		return nil, err
	}
	threads, err := readThreads(dir)
	if err != nil {
		return nil, err
	}
	setAnchorStates(threads, rec.Latest().Rev)
	return threads, nil
}

// setAnchorStates sets each anchor's State from the latest revision, so a
// stored state can't say "anchored" for an older revision.
func setAnchorStates(threads []Thread, latest int) {
	for i := range threads {
		a := threads[i].Anchor
		if a == nil {
			continue
		}
		a.State = stateDetached
		if a.Rev == latest {
			a.State = stateAnchored
		}
	}
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

// ids lists the entries in the store named like an artifact id. A stray file
// with such a name is listed too, so List warns about it instead of hiding it.
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
		if ValidID(e.Name()) {
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
	next := *rec
	next.Revisions = append(slices.Clone(rec.Revisions), rev)
	next.ContentType = ct
	return s.commitRevision(dir, &next, n.Data)
}

// commitRevision writes rec's new latest revision, whose bytes are data. The
// record write is the commit point: the revision file and the moved anchors
// are written first, and undone if either they or the record can't be
// written. comments.json is restored byte for byte.
func (s *Store) commitRevision(dir string, rec *Record, data []byte) (PutResult, error) {
	raw, err := readFileIfExists(filepath.Join(dir, threadsFile))
	if err != nil {
		return PutResult{}, err
	}
	old, err := parseThreads(filepath.Join(dir, threadsFile), raw)
	if err != nil {
		return PutResult{}, err
	}
	threads, anchored, detached := moveAnchors(rec, data, old)
	path := revisionPath(dir, rec.Latest().Rev)
	if err = writeAtomic(s.tmpDir(), path, data); err != nil {
		return PutResult{}, err
	}
	if threads != nil {
		if err = s.threadsWriter(s.tmpDir(), dir, threads); err != nil {
			_ = os.Remove(path)
			s.restoreThreads(dir, raw)
			return PutResult{}, err
		}
	}
	if err = s.recordWriter(s.tmpDir(), dir, rec); err != nil {
		_ = os.Remove(path)
		s.restoreThreads(dir, raw)
		return PutResult{}, err
	}
	return PutResult{Record: *rec, Anchored: anchored, Detached: detached}, nil
}

// restoreThreads puts comments.json back to raw, its bytes before the write,
// or removes it when it did not exist (raw is nil). It is best effort: the
// error that made the write fail is the one to report.
func (s *Store) restoreThreads(dir string, raw []byte) {
	path := filepath.Join(dir, threadsFile)
	if raw == nil {
		_ = os.Remove(path)
		return
	}
	_ = writeAtomic(s.tmpDir(), path, raw)
}

// readFileIfExists is path's bytes, or nil when it does not exist. An
// existing empty file is a non-nil empty slice.
func readFileIfExists(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if data == nil {
		data = []byte{}
	}
	return data, nil
}

// moveAnchors is old as re-anchored to the latest revision of rec, whose bytes
// are data, with how many anchors moved and how many were left detached.
// Nothing is written; the moved threads are nil when there is nothing to
// write. A quote that is gone leaves the thread detached at its last anchor.
func moveAnchors(rec *Record, data []byte, old []Thread) ([]Thread, int, int) {
	if rec.Kind == KindImage || len(old) == 0 {
		return nil, 0, 0
	}
	threads := slices.Clone(old)
	anchored, detached := 0, 0
	text := string(data)
	for i := range threads {
		if threads[i].Anchor == nil {
			continue
		}
		a := *threads[i].Anchor
		if moved, ok := Move(text, rec.Latest().Rev, a); ok {
			threads[i].Anchor = &moved
			anchored++
			continue
		}
		a.State = stateDetached
		threads[i].Anchor = &a
		detached++
	}
	return threads, anchored, detached
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

// storedBytes sums the revision files in the store. Quota policy: they count
// whether or not their record can be read, so a corrupt artifact.json does not
// hide its bytes. A directory that can't be read at all contributes 0, so one
// bad entry never blocks Add or Put of healthy artifacts; List reports it as
// a warning.
func (s *Store) storedBytes() (int64, error) {
	ids, err := s.ids()
	if err != nil {
		return 0, err
	}
	var total int64
	for _, id := range ids {
		total += revisionBytes(filepath.Join(s.dir, id))
	}
	return total, nil
}

// revisionBytes sums the sizes of dir's revision files; a directory that can't
// be read, or a file that vanished since it was listed, counts for nothing.
func revisionBytes(dir string) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var total int64
	for _, e := range entries {
		if !isRevisionName(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		total += info.Size()
	}
	return total
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
