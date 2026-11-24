//go:build unix

package daemon

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// socketBaseDir is the directory of the socket when $XDG_RUNTIME_DIR is unset.
func socketBaseDir(getenv func(string) string, uid int) string {
	tmp := getenv("TMPDIR")
	if tmp == "" {
		tmp = "/tmp"
	}
	return filepath.Join(tmp, fmt.Sprintf("ws-%d", uid))
}

// prepareSocketDir creates the socket's directory, which must be private to
// the user: 0700 and owned by them.
func prepareSocketDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating the socket directory: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("checking the socket directory: %w", err)
	}
	owner, ok := ownerUID(info)
	if !ok || owner != os.Getuid() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("socket directory %s must be 0700 and owned by you", dir)
	}
	return nil
}

func ownerUID(info fs.FileInfo) (int, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}
