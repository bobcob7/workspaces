package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/bobcob7/workspaces/internal/lockfile"
)

// MemoryVersion is the newest memory.json version this ws reads and writes.
const MemoryVersion = 1

// Bounds of a loop's memory, ADR 0056.
const (
	MaxMemoryKeys  = 100
	MaxMemoryValue = 64 << 10
	MaxMemoryBytes = 256 << 10
)

// Errors that memory reads and writes return, wrapped with the key.
var (
	// ErrMemoryInvalid is a key that isn't a valid memory key.
	ErrMemoryInvalid = errors.New("invalid memory key")
	// ErrMemoryNotFound is a key that isn't set.
	ErrMemoryNotFound = errors.New("memory key not found")
	// ErrMemoryConflict is a key whose revision isn't the one expected.
	ErrMemoryConflict = errors.New("memory key changed")
	// ErrMemoryFull is a value or memory over its limit.
	ErrMemoryFull = errors.New("loop memory is full")
)

// AnyRev is the expectRev that skips the revision check.
const AnyRev = -1

// MemoryEntry is one value in a loop's memory.
type MemoryEntry struct {
	Value     string    `json:"value"`
	Rev       int       `json:"rev"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy string    `json:"updated_by"`
}

// Memory is a loop's memory, as memory.json holds it.
type Memory struct {
	Version int                    `json:"version"`
	Entries map[string]MemoryEntry `json:"entries"` // never nil after Read
}

// ValidateMemoryKey checks a memory key with ValidateVarKey's rule, and
// wraps ErrMemoryInvalid when it fails.
func ValidateMemoryKey(key string) error {
	if err := ValidateVarKey(key); err != nil {
		return fmt.Errorf("%w: %q", ErrMemoryInvalid, key)
	}
	return nil
}

// ReadMemory reads the loop's memory.json without locking. A missing file is
// an empty memory. A version newer than MemoryVersion is ErrNewerData.
func (d *DataStore) ReadMemory(name string) (Memory, error) {
	if err := ValidateName(name); err != nil {
		return Memory{}, err
	}
	path := d.MemoryPath(name)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return emptyMemory(), nil
	}
	if err != nil {
		return Memory{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var m Memory
	if err := json.Unmarshal(data, &m); err != nil {
		return Memory{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if m.Version > MemoryVersion {
		return Memory{}, fmt.Errorf("%s: %w", path, ErrNewerData)
	}
	if m.Entries == nil {
		m.Entries = map[string]MemoryEntry{}
	}
	return m, nil
}

// SetMemory sets key to value under the memory lock, and returns the stored
// entry. The rev goes up by one; updated_by is by. expectRev must be AnyRev
// or the key's current rev (0 when it is unset). A value over MaxMemoryValue,
// a new key past MaxMemoryKeys, or a total past MaxMemoryBytes is
// ErrMemoryFull.
func (d *DataStore) SetMemory(ctx context.Context, name, key, value, by string, expectRev int, now time.Time) (MemoryEntry, error) {
	if err := ValidateName(name); err != nil {
		return MemoryEntry{}, err
	}
	if err := ValidateMemoryKey(key); err != nil {
		return MemoryEntry{}, err
	}
	unlock, err := d.lockMemory(ctx, name)
	if err != nil {
		return MemoryEntry{}, err
	}
	defer unlock()
	m, err := d.ReadMemory(name)
	if err != nil {
		return MemoryEntry{}, err
	}
	old := m.Entries[key]
	if err := checkMemoryFit(&m, key, value); err != nil {
		return MemoryEntry{}, err
	}
	if err := checkRev(key, old.Rev, expectRev); err != nil {
		return MemoryEntry{}, err
	}
	entry := MemoryEntry{
		Value:     value,
		Rev:       old.Rev + 1,
		UpdatedAt: now.UTC().Truncate(time.Second),
		UpdatedBy: by,
	}
	m.Entries[key] = entry
	if err := d.writeMemory(name, m); err != nil {
		return MemoryEntry{}, err
	}
	return entry, nil
}

// RemoveMemory deletes key under the memory lock. A missing key is
// ErrMemoryNotFound; expectRev is checked as in SetMemory.
func (d *DataStore) RemoveMemory(ctx context.Context, name, key string, expectRev int) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if err := ValidateMemoryKey(key); err != nil {
		return err
	}
	unlock, err := d.lockMemory(ctx, name)
	if err != nil {
		return err
	}
	defer unlock()
	m, err := d.ReadMemory(name)
	if err != nil {
		return err
	}
	old, ok := m.Entries[key]
	if !ok {
		return fmt.Errorf("%w: %s", ErrMemoryNotFound, key)
	}
	if err := checkRev(key, old.Rev, expectRev); err != nil {
		return err
	}
	delete(m.Entries, key)
	return d.writeMemory(name, m)
}

// ClearMemory deletes every key under the memory lock, and returns how many
// there were.
func (d *DataStore) ClearMemory(ctx context.Context, name string) (int, error) {
	if err := ValidateName(name); err != nil {
		return 0, err
	}
	unlock, err := d.lockMemory(ctx, name)
	if err != nil {
		return 0, err
	}
	defer unlock()
	m, err := d.ReadMemory(name)
	if err != nil {
		return 0, err
	}
	removed := len(m.Entries)
	if removed == 0 {
		return 0, nil
	}
	m.Entries = map[string]MemoryEntry{}
	if err := d.writeMemory(name, m); err != nil {
		return 0, err
	}
	return removed, nil
}

// Values is key → value, never nil.
func (m *Memory) Values() map[string]string {
	values := make(map[string]string, len(m.Entries))
	for key, entry := range m.Entries {
		values[key] = entry.Value
	}
	return values
}

// Bytes is the sum of the lengths of all values.
func (m *Memory) Bytes() int {
	total := 0
	for _, entry := range m.Entries {
		total += len(entry.Value)
	}
	return total
}

// Keys is the keys, sorted.
func (m *Memory) Keys() []string {
	return slices.Sorted(maps.Keys(m.Entries))
}

func emptyMemory() Memory {
	return Memory{Version: MemoryVersion, Entries: map[string]MemoryEntry{}}
}

// lockMemory takes the loop's .memory.lock exclusively, creating the loop's
// data directory first.
func (d *DataStore) lockMemory(ctx context.Context, name string) (func(), error) {
	if err := os.MkdirAll(d.Dir(name), 0o700); err != nil {
		return nil, fmt.Errorf("creating data of loop %s: %w", name, err)
	}
	unlock, err := lockfile.Exclusive(ctx, filepath.Join(d.Dir(name), ".memory.lock"))
	if err != nil {
		return nil, fmt.Errorf("locking memory of loop %s: %w", name, err)
	}
	return unlock, nil
}

// checkMemoryFit is ErrMemoryFull when setting key to value would break a
// limit.
func checkMemoryFit(m *Memory, key, value string) error {
	if len(value) > MaxMemoryValue {
		return fmt.Errorf("%w: %s is over %d bytes", ErrMemoryFull, key, MaxMemoryValue)
	}
	_, exists := m.Entries[key]
	if !exists && len(m.Entries) >= MaxMemoryKeys {
		return fmt.Errorf("%w: more than %d keys", ErrMemoryFull, MaxMemoryKeys)
	}
	total := m.Bytes() - len(m.Entries[key].Value) + len(value)
	if total > MaxMemoryBytes {
		return fmt.Errorf("%w: more than %d bytes in all", ErrMemoryFull, MaxMemoryBytes)
	}
	return nil
}

// checkRev is ErrMemoryConflict when expectRev is set and isn't current.
func checkRev(key string, current, expectRev int) error {
	if expectRev == AnyRev || expectRev == current {
		return nil
	}
	return fmt.Errorf("%w: %s is at rev %d, not %d", ErrMemoryConflict, key, current, expectRev)
}

// writeMemory writes the loop's memory.json atomically, at the current
// version.
func (d *DataStore) writeMemory(name string, m Memory) error {
	m.Version = MemoryVersion
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding memory of loop %s: %w", name, err)
	}
	return writeFileAtomic(d.MemoryPath(name), append(data, '\n'))
}
