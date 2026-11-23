package environment

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const revokedFile = "revoked"

// Revoked returns the set of revoked certificate serials. A missing file is
// an empty set.
func (s *Store) Revoked() (map[string]bool, error) {
	revoked := map[string]bool{}
	data, err := os.ReadFile(filepath.Join(s.dir, revokedFile))
	if errors.Is(err, fs.ErrNotExist) {
		return revoked, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the revoked list: %w", err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		revoked[fields[0]] = true
	}
	return revoked, nil
}

// revoke appends a line for the certificate with serial, issued to id, and
// rewrites the list. The caller holds the lock.
func (s *Store) revoke(serial, id string) error {
	path := filepath.Join(s.dir, revokedFile)
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("reading the revoked list: %w", err)
	}
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	line := fmt.Sprintf("%s %s %s\n", serial, id, s.now().UTC().Format(time.RFC3339))
	return writeFileAtomic(path, append(data, line...), 0o600)
}
