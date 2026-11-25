package envscope

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/lockfile"
)

const (
	ledgerName     = "scopes.json"
	ledgerLockName = "scopes.lock"
	ledgerVersion  = 1
)

// Entry is what the controller granted a workspace on one environment.
type Entry struct {
	Environment string    `json:"environment"` // ValidID
	Workspace   string    `json:"workspace"`
	Scope       []string  `json:"scope"` // ids
	Parent      *Parent   `json:"parent"`
	Role        *string   `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}

// ledgerFile is CONFIG_DIR/environments/scopes.json.
type ledgerFile struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

// Ledger is the controller's record of scopes it granted.
type Ledger struct {
	path string
	lock string
}

// NewLedger is the ledger in environmentsDir (scopes.json, scopes.lock).
func NewLedger(environmentsDir string) *Ledger {
	return &Ledger{
		path: filepath.Join(environmentsDir, ledgerName),
		lock: filepath.Join(environmentsDir, ledgerLockName),
	}
}

// Put adds e, or replaces the entry with the same environment and workspace.
func (l *Ledger) Put(ctx context.Context, e *Entry) error {
	if err := validateEntry(e); err != nil {
		return err
	}
	unlock, err := lockfile.Exclusive(ctx, l.lock)
	if err != nil {
		return fmt.Errorf("locking the scope ledger: %w", err)
	}
	defer unlock()
	entries, err := l.read()
	if err != nil {
		return err
	}
	entries = upsert(entries, e)
	if len(entries) > MaxLedgerEntries {
		return fmt.Errorf("scope ledger full (%d entries)", MaxLedgerEntries)
	}
	return l.write(entries)
}

// Remove deletes the entry for env and ws. A missing entry is not an error.
func (l *Ledger) Remove(ctx context.Context, env, ws string) error {
	unlock, err := lockfile.Exclusive(ctx, l.lock)
	if err != nil {
		return fmt.Errorf("locking the scope ledger: %w", err)
	}
	defer unlock()
	entries, err := l.read()
	if err != nil {
		return err
	}
	kept := slices.DeleteFunc(entries, func(e Entry) bool { return e.Environment == env && e.Workspace == ws })
	if len(kept) == len(entries) {
		return nil
	}
	return l.write(kept)
}

// List returns every entry; a missing file is empty.
func (l *Ledger) List() ([]Entry, error) {
	unlock, err := lockfile.Shared(context.Background(), l.lock)
	if err != nil {
		return nil, fmt.Errorf("locking the scope ledger: %w", err)
	}
	defer unlock()
	return l.read()
}

// Get returns the entry for env and ws.
func (l *Ledger) Get(env, ws string) (Entry, bool, error) {
	entries, err := l.List()
	if err != nil {
		return Entry{}, false, err
	}
	i := slices.IndexFunc(entries, func(e Entry) bool { return e.Environment == env && e.Workspace == ws })
	if i < 0 {
		return Entry{}, false, nil
	}
	return entries[i], true, nil
}

// Ceiling returns the Scope ids of the first entry, in chain order, whose
// Environment == env and Workspace == chain[i]; nil when none.
func (l *Ledger) Ceiling(env string, chain []string) ([]string, error) {
	entries, err := l.List()
	if err != nil {
		return nil, err
	}
	for _, ws := range chain {
		i := slices.IndexFunc(entries, func(e Entry) bool { return e.Environment == env && e.Workspace == ws })
		if i >= 0 {
			return entries[i].Scope, nil
		}
	}
	return nil, nil
}

// read decodes the ledger; the caller holds the lock.
func (l *Ledger) read() ([]Entry, error) {
	data, err := os.ReadFile(l.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the scope ledger: %w", err)
	}
	var f ledgerFile
	if err := decodeStrict(data, &f); err != nil {
		return nil, err
	}
	if f.Version != ledgerVersion {
		return nil, fmt.Errorf("%w: ledger version %d isn't %d", ErrInvalid, f.Version, ledgerVersion)
	}
	if len(f.Entries) > MaxLedgerEntries {
		return nil, fmt.Errorf("%w: %d ledger entries, at most %d", ErrInvalid, len(f.Entries), MaxLedgerEntries)
	}
	for i := range f.Entries {
		if err := validateEntry(&f.Entries[i]); err != nil {
			return nil, err
		}
	}
	return f.Entries, nil
}

// write replaces the ledger; the caller holds the exclusive lock.
func (l *Ledger) write(entries []Entry) error {
	if entries == nil {
		entries = []Entry{}
	}
	data, err := json.Marshal(ledgerFile{Version: ledgerVersion, Entries: entries})
	if err != nil {
		return fmt.Errorf("encoding the scope ledger: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return fmt.Errorf("creating the scope ledger directory: %w", err)
	}
	return writeAtomic(l.path, data)
}

// upsert replaces the entry with e's environment and workspace, or appends e.
func upsert(entries []Entry, e *Entry) []Entry {
	stored := *e
	stored.Scope = slices.Clone(e.Scope)
	i := slices.IndexFunc(entries, func(old Entry) bool {
		return old.Environment == e.Environment && old.Workspace == e.Workspace
	})
	if i >= 0 {
		entries[i] = stored
		return entries
	}
	return append(entries, stored)
}

func validateEntry(e *Entry) error {
	if !envproto.ValidID(e.Environment) {
		return fmt.Errorf("%w: ledger environment %q", ErrInvalid, e.Environment)
	}
	if e.Workspace == "" {
		return fmt.Errorf("%w: ledger entry without a workspace", ErrInvalid)
	}
	for _, id := range e.Scope {
		if !validID(id) {
			return fmt.Errorf("%w: ledger scope id %q", ErrInvalid, id)
		}
	}
	if e.Parent != nil && (!validID(e.Parent.Environment) || e.Parent.Workspace == "") {
		return fmt.Errorf("%w: ledger parent %q/%q", ErrInvalid, e.Parent.Environment, e.Parent.Workspace)
	}
	return nil
}
