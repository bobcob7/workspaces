package machine

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/bobcob7/workspaces/internal/envscope"
	"github.com/bobcob7/workspaces/internal/workspace"
)

const progressFile = "progress.toml"

// Limits on a progress record, so a UI can lay it out.
const (
	MaxProgressName = 40
	MaxProgressNote = 200
	MaxProgressStep = 1_000_000
)

// ErrProgressInvalid is a progress record that breaks the rules of Progress.Check.
var ErrProgressInvalid = errors.New("invalid progress")

// Progress is progress.toml: what one workspace says about its own task. Its
// steps are the ones it does itself; work it hands to children is theirs
// (ADR 0077).
type Progress struct {
	Version   int       `toml:"version"`
	Name      string    `toml:"name,omitempty"`
	Total     int       `toml:"total"`
	Done      int       `toml:"done"`
	Note      string    `toml:"note,omitempty"`
	UpdatedAt time.Time `toml:"updated_at"`
}

func (p *Progress) fileVersion() int { return p.Version }

// Check is nil when p's name and note are short single-line text, and its
// steps satisfy 0 <= Done <= Total <= MaxProgressStep.
func (p *Progress) Check() error {
	if err := checkLine("name", p.Name, MaxProgressName); err != nil {
		return err
	}
	if err := checkLine("note", p.Note, MaxProgressNote); err != nil {
		return err
	}
	switch {
	case p.Total < 0 || p.Total > MaxProgressStep:
		return fmt.Errorf("%w: total %d must be 0 to %d", ErrProgressInvalid, p.Total, MaxProgressStep)
	case p.Done < 0:
		return fmt.Errorf("%w: done %d is negative", ErrProgressInvalid, p.Done)
	case p.Done > p.Total:
		return fmt.Errorf("%w: done %d is more than total %d; raise total first", ErrProgressInvalid, p.Done, p.Total)
	}
	return nil
}

func checkLine(what, s string, limit int) error {
	if utf8.RuneCountInString(s) > limit {
		return fmt.Errorf("%w: %s is over %d characters", ErrProgressInvalid, what, limit)
	}
	if strings.ContainsFunc(s, unicode.IsControl) {
		return fmt.Errorf("%w: %s must be one line of plain text", ErrProgressInvalid, what)
	}
	return nil
}

// WriteProgress writes child's progress.toml with p.Version set to
// FormatVersion, creating the directory (0o700).
func (s *Store) WriteProgress(root, child string, p *Progress) error {
	dir, err := s.prepareDir(root, child)
	if err != nil {
		return err
	}
	p.Version = FormatVersion
	return writeTOML(filepath.Join(dir, progressFile), p)
}

// ReadProgress reads child's progress.toml; a missing file is nil. A version
// above FormatVersion is ErrNewer, a file that doesn't parse or breaks the
// rules of Progress.Check ErrInvalid, so a hand-edited record can't reach a
// rollup.
func (s *Store) ReadProgress(root, child string) (*Progress, error) {
	if err := checkComponents(root, child); err != nil {
		return nil, err
	}
	var p Progress
	path := filepath.Join(s.Dir(root, child), progressFile)
	found, err := readTOML(path, &p)
	if err != nil || !found {
		return nil, err
	}
	if err = p.Check(); err != nil {
		return nil, fmt.Errorf("%s: %w: %w", path, ErrInvalid, err)
	}
	return &p, nil
}

// ReadProgressOf is ReadProgress for every id; an id with no record is
// absent from the map.
func (s *Store) ReadProgressOf(root string, ids []string) (map[string]*Progress, error) {
	out := make(map[string]*Progress, len(ids))
	for _, id := range ids {
		p, err := s.ReadProgress(root, id)
		if err != nil {
			return nil, err
		}
		if p != nil {
			out[id] = p
		}
	}
	return out, nil
}

// Rollup is the steps done and planned in a workspace and everything below it.
type Rollup struct {
	Total int
	Done  int
}

// Fraction is Done/Total, and false when no step is planned.
func (r Rollup) Fraction() (float64, bool) {
	if r.Total == 0 {
		return 0, false
	}
	return float64(r.Done) / float64(r.Total), true
}

// ProgressNode is one workspace in a progress tree.
type ProgressNode struct {
	ID       string
	Progress *Progress
	Rollup   Rollup
	Children []*ProgressNode
}

// ProgressTree is id and everything below it in list, in list order, with each
// node's Rollup the sum of its own steps and its children's rollups. A
// workspace with no record has no steps of its own, except that one with a
// result in outcomes counts as a single step, done when it passed (ADR 0077).
// Either way it passes its children's steps up. Ids already placed are
// skipped, so a cycle in hand-edited parents ends the tree rather than looping.
func ProgressTree(list []workspace.Workspace, id string, progress map[string]*Progress, outcomes map[string]Outcome) *ProgressNode {
	kids := map[string][]string{}
	for i := range list {
		kids[list[i].Parent] = append(kids[list[i].Parent], list[i].ID)
	}
	seen := map[string]bool{}
	var build func(string) *ProgressNode
	build = func(cur string) *ProgressNode {
		seen[cur] = true
		n := &ProgressNode{ID: cur, Progress: progress[cur]}
		switch outcome, finished := outcomes[cur]; {
		case n.Progress != nil:
			n.Rollup = Rollup{Total: n.Progress.Total, Done: n.Progress.Done}
		case finished:
			n.Rollup = Rollup{Total: 1}
			if outcome == OutcomePass {
				n.Rollup.Done = 1
			}
		}
		for _, k := range kids[cur] {
			if seen[k] {
				continue
			}
			c := build(k)
			n.Children = append(n.Children, c)
			n.Rollup.Total += c.Rollup.Total
			n.Rollup.Done += c.Rollup.Done
		}
		return n
	}
	return build(id)
}

// Add is r plus o, saturating like the merged rollups of ADR 0083.
func (r Rollup) Add(o Rollup) Rollup {
	sum := envscope.Steps{Total: r.Total, Done: r.Done}.Add(envscope.Steps{Total: o.Total, Done: o.Done})
	return Rollup{Total: sum.Total, Done: sum.Done}
}

// AddRemote adds steps to the node with id parent and to each of its ancestors
// in n, as a child on another environment (ADR 0083). It reports whether it
// found parent.
func (n *ProgressNode) AddRemote(parent string, steps Rollup) bool {
	if n.ID == parent {
		n.Rollup = n.Rollup.Add(steps)
		return true
	}
	for _, c := range n.Children {
		if c.AddRemote(parent, steps) {
			n.Rollup = n.Rollup.Add(steps)
			return true
		}
	}
	return false
}

// IDs is every id in the tree, the root first.
func (n *ProgressNode) IDs() []string {
	ids := []string{n.ID}
	for _, c := range n.Children {
		ids = append(ids, c.IDs()...)
	}
	return ids
}

// Complete is p with every step done, after a pass. It reports whether p
// changed: a record with no steps planned, or all done, is left alone.
func (p *Progress) Complete() bool {
	if p.Total == 0 || p.Done == p.Total {
		return false
	}
	p.Done = p.Total
	return true
}
