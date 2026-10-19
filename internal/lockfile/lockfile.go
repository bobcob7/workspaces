// Package lockfile takes advisory flock locks on files (ADR 0008).
package lockfile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// pollInterval is how often a waiting lock retries.
const pollInterval = 50 * time.Millisecond

// Exclusive waits until it holds path's lock exclusively, or ctx is done.
// It creates the file and its directory if needed. The returned function
// releases the lock.
func Exclusive(ctx context.Context, path string) (func(), error) {
	return acquire(ctx, path, unix.LOCK_EX)
}

// Shared waits until it holds path's lock shared with other readers, or ctx
// is done.
func Shared(ctx context.Context, path string) (func(), error) {
	return acquire(ctx, path, unix.LOCK_SH)
}

// TryExclusive takes path's lock exclusively without waiting. ok is false
// when someone else holds it.
func TryExclusive(path string) (unlock func(), ok bool, err error) {
	f, err := open(path)
	if err != nil {
		return nil, false, err
	}
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		return func() { _ = f.Close() }, true, nil
	}
	_ = f.Close()
	if errors.Is(err, unix.EWOULDBLOCK) {
		return nil, false, nil
	}
	return nil, false, fmt.Errorf("locking %s: %w", path, err)
}

func acquire(ctx context.Context, path string, how int) (func(), error) {
	f, err := open(path)
	if err != nil {
		return nil, err
	}
	for {
		err = unix.Flock(int(f.Fd()), how|unix.LOCK_NB)
		if err == nil {
			return func() { _ = f.Close() }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("locking %s: %w", path, err)
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
