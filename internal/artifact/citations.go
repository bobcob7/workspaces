package artifact

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/lockfile"
)

// Scan limits (ADR 0084).
const (
	MaxScanCitations = 1000
	MaxScanKeyBytes  = 128
	MaxSeen          = 4096
	citationsFile    = "citations.json"
	citationsLock    = "citations.lock"
	workspaceRecord  = "workspace.toml"
	maxWorkspaceID   = 128
)

// ErrNoWorkspace is a scan for a workspace whose record is gone.
var ErrNoWorkspace = errors.New("no such workspace")

// Ref is one artifact a citation names, and the revision it names (0: none).
type Ref struct {
	ID  string
	Rev int
}

// Citation is one message of a transcript that names artifacts.
type Citation struct {
	Key  string
	At   time.Time
	Refs []Ref
}

// Scan is what the desktop found in a workspace's transcript since its
// cursor: the citing messages, and where it read up to.
type Scan struct {
	Workspace  string
	Name       string
	Root       string
	Transcript string
	Offset     int64
	Citations  []Citation
}

// ScanResult is what RecordScan did: the stored offset, how many
// (message, artifact) pairs it recorded, and how many it skipped because
// they were recorded before or name no stored artifact.
type ScanResult struct {
	Offset   int64
	Recorded int
	Skipped  int
	Warnings []string
}

// Cursor is a workspace's stored scan position.
type Cursor struct {
	Workspace  string
	Transcript string
	Offset     int64
	UpdatedAt  time.Time
}

// scanState is state/<id>/citations.json.
type scanState struct {
	Version    int       `json:"version"`
	Transcript string    `json:"transcript"`
	Offset     int64     `json:"offset"`
	Seen       []string  `json:"seen"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ValidWorkspaceID reports whether id could name a workspace: one path
// element, not hidden, at most 128 bytes, no control characters.
func ValidWorkspaceID(id string) bool {
	if id == "" || len(id) > maxWorkspaceID || strings.HasPrefix(id, ".") {
		return false
	}
	return !strings.ContainsFunc(id, func(r rune) bool { return r == '/' || r == '\\' || r < ' ' || r == 0x7f })
}

// CheckScan is ErrInvalid unless s is a scan RecordScan accepts.
func CheckScan(s *Scan) error {
	switch {
	case !ValidWorkspaceID(s.Workspace):
		return fmt.Errorf("%w: %q is not a workspace id", ErrInvalid, s.Workspace)
	case s.Offset < 0:
		return fmt.Errorf("%w: offset %d is negative", ErrInvalid, s.Offset)
	case len(s.Citations) > MaxScanCitations:
		return fmt.Errorf("%w: at most %d citations at once, not %d", ErrInvalid, MaxScanCitations, len(s.Citations))
	}
	for i := range s.Citations {
		k := s.Citations[i].Key
		if k == "" || len(k) > MaxScanKeyBytes {
			return fmt.Errorf("%w: citation %d's key must be 1 to %d bytes", ErrInvalid, i, MaxScanKeyBytes)
		}
	}
	return nil
}

// RecordScan records the citations in scan that weren't recorded before, as
// cited links, and stores the cursor and the pairs it recorded in
// <stateDir>/<workspace>/citations.json, under that workspace's
// citations.lock. A pair is added to seen only once its link is written.
func (s *Store) RecordScan(ctx context.Context, stateDir string, scan *Scan) (ScanResult, error) {
	if err := CheckScan(scan); err != nil {
		return ScanResult{}, err
	}
	dir := filepath.Join(stateDir, scan.Workspace)
	if err := requireWorkspace(dir, scan.Workspace); err != nil {
		return ScanResult{}, err
	}
	unlock, err := lockfile.Exclusive(ctx, filepath.Join(dir, citationsLock))
	if err != nil {
		return ScanResult{}, err
	}
	defer unlock()
	if err = requireWorkspace(dir, scan.Workspace); err != nil {
		return ScanResult{}, err
	}
	state, err := readScanState(dir)
	if err != nil {
		return ScanResult{}, err
	}
	res := s.recordPairs(ctx, scan, &state)
	advanceCursor(&state, scan, s.now().UTC().Truncate(time.Second))
	if err := writeScanState(dir, &state); err != nil {
		return ScanResult{}, err
	}
	res.Offset = state.Offset
	return res, nil
}

// recordPairs records the unseen (message, artifact) pairs of scan, one
// RecordLinks per artifact, and adds the recorded ones to state.Seen.
func (s *Store) recordPairs(ctx context.Context, scan *Scan, state *scanState) ScanResult {
	var res ScanResult
	pending, order := unseenPairs(scan, state.Seen, &res)
	for _, id := range order {
		p := pending[id]
		err := s.RecordLinks(ctx, id, p.links...)
		switch {
		case errors.Is(err, ErrNotFound):
			res.Skipped += len(p.keys)
		case err != nil:
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: citation not recorded: %v", id, err))
		default:
			res.Recorded += len(p.keys)
			state.Seen = append(state.Seen, p.keys...)
		}
	}
	return res
}

// artifactPairs is what one artifact gets from a scan: a link per (message,
// revision), and the seen keys of its messages.
type artifactPairs struct {
	links []NewLink
	keys  []string
}

// unseenPairs groups scan's citations by artifact, leaving out pairs in seen
// and repeats within the scan; skipped ones are counted in res.
func unseenPairs(scan *Scan, seen []string, res *ScanResult) (map[string]*artifactPairs, []string) {
	pending := make(map[string]*artifactPairs)
	var order []string
	done := make(map[string]bool, len(seen))
	for _, k := range seen {
		done[k] = true
	}
	for i := range scan.Citations {
		c := &scan.Citations[i]
		for _, id := range citedIDs(c.Refs) {
			key := c.Key + ":" + id
			if done[key] {
				res.Skipped++
				continue
			}
			done[key] = true
			p, ok := pending[id]
			if !ok {
				p = &artifactPairs{}
				pending[id] = p
				order = append(order, id)
			}
			p.keys = append(p.keys, key)
			p.links = append(p.links, citedLinks(scan, c, id)...)
		}
	}
	return pending, order
}

// citedIDs is the distinct valid artifact ids in refs, in order.
func citedIDs(refs []Ref) []string {
	var ids []string
	for _, r := range refs {
		if ValidID(r.ID) && !slices.Contains(ids, r.ID) {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

// citedLinks is one cited link per distinct revision c names of artifact id.
func citedLinks(scan *Scan, c *Citation, id string) []NewLink {
	var revs []int
	for _, r := range c.Refs {
		if r.ID == id && !slices.Contains(revs, r.Rev) {
			revs = append(revs, r.Rev)
		}
	}
	links := make([]NewLink, 0, len(revs))
	for _, rev := range revs {
		links = append(links, NewLink{Workspace: scan.Workspace, Name: scan.Name, Root: scan.Root, Action: ActionCited, Rev: rev, At: c.At})
	}
	return links
}

// advanceCursor keeps the larger offset for the same transcript, and takes
// scan's for a new one; seen keeps its newest MaxSeen pairs.
func advanceCursor(state *scanState, scan *Scan, now time.Time) {
	if scan.Transcript != state.Transcript || scan.Offset > state.Offset {
		state.Transcript, state.Offset = scan.Transcript, scan.Offset
	}
	if over := len(state.Seen) - MaxSeen; over > 0 {
		state.Seen = slices.Clone(state.Seen[over:])
	}
	state.Version = FormatVersion
	state.UpdatedAt = now
}

// requireWorkspace is ErrNoWorkspace unless dir holds a workspace record.
func requireWorkspace(dir, id string) error {
	_, err := os.Stat(filepath.Join(dir, workspaceRecord))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrNoWorkspace, id)
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", dir, err)
	}
	return nil
}

// Cursors is every workspace's stored scan cursor under stateDir; a state
// that can't be read is skipped and named in the warnings.
func Cursors(stateDir string) ([]Cursor, []string, error) {
	entries, err := os.ReadDir(stateDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", stateDir, err)
	}
	var cursors []Cursor
	var warnings []string
	for _, e := range entries {
		if !e.IsDir() || !ValidWorkspaceID(e.Name()) {
			continue
		}
		c, ok, err := readCursor(filepath.Join(stateDir, e.Name()), e.Name())
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", e.Name(), err))
		}
		if ok {
			cursors = append(cursors, c)
		}
	}
	return cursors, warnings, nil
}

func readCursor(dir, id string) (Cursor, bool, error) {
	data, err := readFileIfExists(filepath.Join(dir, citationsFile))
	if err != nil || data == nil {
		return Cursor{}, false, err
	}
	state, err := parseScanState(filepath.Join(dir, citationsFile), data)
	if err != nil {
		return Cursor{}, false, err
	}
	return Cursor{Workspace: id, Transcript: state.Transcript, Offset: state.Offset, UpdatedAt: state.UpdatedAt}, true, nil
}

// readScanState reads <dir>/citations.json; a missing file is a new state.
// A newer version is ErrNewer, so an older ws never rewrites it.
func readScanState(dir string) (scanState, error) {
	path := filepath.Join(dir, citationsFile)
	data, err := readFileIfExists(path)
	if err != nil || data == nil {
		return scanState{Version: FormatVersion}, err
	}
	return parseScanState(path, data)
}

func parseScanState(path string, data []byte) (scanState, error) {
	var state scanState
	if err := json.Unmarshal(data, &state); err != nil {
		return scanState{}, fmt.Errorf("reading %s: %w: %w", path, ErrInvalid, err)
	}
	if state.Version > FormatVersion {
		return scanState{}, fmt.Errorf("reading %s: %w", path, ErrNewer)
	}
	return state, nil
}

// writeScanState writes <dir>/citations.json through a temporary file in
// dir.
func writeScanState(dir string, state *scanState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", citationsFile, err)
	}
	return writeAtomic(dir, filepath.Join(dir, citationsFile), data)
}
