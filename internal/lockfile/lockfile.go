// Package lockfile takes advisory flock locks on files (ADR 0008).
package lockfile

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// pollInterval is how often a waiting lock retries.
const pollInterval = 50 * time.Millisecond

// mode is the kind of lock to take.
type mode int

const (
	shared mode = iota
	exclusive
)

// Exclusive waits until it holds path's lock exclusively, or ctx is done.
// It creates the file and its directory if needed. The returned function
// releases the lock.
func Exclusive(ctx context.Context, path string) (func(), error) {
	return acquire(ctx, path, exclusive)
}

// Shared waits until it holds path's lock shared with other readers, or ctx
// is done.
func Shared(ctx context.Context, path string) (func(), error) {
	return acquire(ctx, path, shared)
}

// TryExclusive takes path's lock exclusively without waiting. ok is false
// when someone else holds it.
func TryExclusive(path string) (unlock func(), ok bool, err error) {
	l, ok, err := TryExclusiveLock(path)
	if !ok || err != nil {
		return nil, ok, err
	}
	return l.Unlock, true, nil
}

// HeldExclusively reports whether someone holds path's lock exclusively.
// It never creates the file or its directory: a missing file is a free
// lock. It probes with a shared lock, so probes running at the same time
// don't see each other as holders.
func HeldExclusively(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("opening lock %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	held, err := tryLock(f, shared)
	if err != nil {
		return false, fmt.Errorf("locking %s: %w", path, err)
	}
	return held, nil
}

// Lock is a held lock and the open file it's held through.
type Lock struct {
	f *os.File
}

// TryExclusiveLock is TryExclusive, returning the held Lock so it can be
// handed to a child process.
func TryExclusiveLock(path string) (lock *Lock, ok bool, err error) {
	f, err := open(path)
	if err != nil {
		return nil, false, err
	}
	held, err := tryLock(f, exclusive)
	if err != nil {
		_ = f.Close()
		return nil, false, fmt.Errorf("locking %s: %w", path, err)
	}
	if held {
		_ = f.Close()
		return nil, false, nil
	}
	return &Lock{f: f}, true, nil
}

// File is the open file the lock is held through. A flock belongs to the
// open file, so a child process given File, for example in
// exec.Cmd.ExtraFiles, holds the same lock: it stays held until this
// process and every such child have closed their descriptors.
// On Windows a child given the file doesn't hold the lock (ADR 0081).
func (l *Lock) File() *os.File {
	return l.f
}

// Unlock closes this process's descriptor for the lock.
func (l *Lock) Unlock() {
	_ = l.f.Close()
}

func acquire(ctx context.Context, path string, m mode) (func(), error) {
	f, err := open(path)
	if err != nil {
		return nil, err
	}
	for {
		held, err := tryLock(f, m)
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("locking %s: %w", path, err)
		}
		if !held {
			return func() { _ = f.Close() }, nil
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("waiting for lock %s: %w", path, ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}

func open(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("creating lock directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening lock %s: %w", path, err)
	}
	return f, nil
}
