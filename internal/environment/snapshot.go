package environment

import (
	"encoding/json"
	"fmt"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// SnapshotFile is state/.environments/<name>/snapshot.json: the snapshot an
// environment sent, with the environment's id and when the controller got it.
type SnapshotFile struct {
	Version    int             `json:"version"`
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	ReceivedAt string          `json:"received_at"`
	Snapshot   json.RawMessage `json:"snapshot"`
}

// Snapshot reads and decodes the snapshot of name: the file and the
// envproto.Snapshot inside it. A snapshot from before protocol 1 has
// protocol 0, and one without agents has []. fs.ErrNotExist when there is none.
func (s *Store) Snapshot(name string) (SnapshotFile, envproto.Snapshot, error) {
	data, err := s.ReadSnapshot(name)
	if err != nil {
		return SnapshotFile{}, envproto.Snapshot{}, err
	}
	var file SnapshotFile
	if err := json.Unmarshal(data, &file); err != nil {
		return SnapshotFile{}, envproto.Snapshot{}, fmt.Errorf("decoding the snapshot file of %s: %w", name, err)
	}
	var snap envproto.Snapshot
	if err := json.Unmarshal(file.Snapshot, &snap); err != nil {
		return SnapshotFile{}, envproto.Snapshot{}, fmt.Errorf("decoding the snapshot of %s: %w", name, err)
	}
	if snap.Agents == nil {
		snap.Agents = []envproto.SnapshotAgent{}
	}
	return file, snap, nil
}
