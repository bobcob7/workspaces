package envscope

import (
	"encoding/json"
	"fmt"
	"unicode"
)

const (
	// MaxRollupSteps caps a remote base and every merged sum (ADR 0083).
	MaxRollupSteps = 1_000_000_000
	// MaxMergeDepth is the most ledger hops a merge follows.
	MaxMergeDepth = 64
	// MaxListedPerEnvironment is the most snapshot workspaces read per environment.
	MaxListedPerEnvironment = 4096
	// maxListedBytes bounds the snapshot array ListedOf decodes.
	maxListedBytes = 1 << 20
	// maxListedID is the longest workspace id ListedOf keeps.
	maxListedID = 128
)

// Steps is a rollup: the steps planned and done in a workspace and below it.
type Steps struct {
	Total int `json:"total"`
	Done  int `json:"done"`
}

// Add is s plus o, saturating at MaxRollupSteps, with Done never above Total.
func (s Steps) Add(o Steps) Steps {
	total := min(s.Total+o.Total, MaxRollupSteps)
	return Steps{Total: total, Done: min(s.Done+o.Done, total)}
}

// Valid reports whether 0 ≤ Done ≤ Total ≤ MaxRollupSteps.
func (s Steps) Valid() bool {
	return 0 <= s.Done && s.Done <= s.Total && s.Total <= MaxRollupSteps
}

// Listed is one workspace of an environment: its same-environment parent
// ("" for none) and its base rollup, as its own environment counted it.
type Listed struct {
	Environment string // "local" or an environment id
	Workspace   string
	Parent      string
	Base        Steps
}

// ListedOf decodes a snapshot's workspaces array (ws list objects) for
// environment envID: id, parent and rollup only. Over maxListedBytes is an
// error; entries past MaxListedPerEnvironment, with an empty, over-128-byte
// or control-character id, are skipped; a rollup that isn't Valid is zero and
// its id is returned in invalid.
func ListedOf(envID string, workspaces json.RawMessage) (listed []Listed, invalid []string, err error) {
	if len(workspaces) > maxListedBytes {
		return nil, nil, fmt.Errorf("%w: snapshot workspaces over %d bytes", ErrInvalid, maxListedBytes)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(workspaces, &items); err != nil {
		return nil, nil, fmt.Errorf("decoding snapshot workspaces: %w", err)
	}
	for _, raw := range items {
		if len(listed) == MaxListedPerEnvironment {
			break
		}
		var item struct {
			ID     string          `json:"id"`
			Parent string          `json:"parent"`
			Rollup json.RawMessage `json:"rollup"`
		}
		if err := json.Unmarshal(raw, &item); err != nil || !validWorkspaceID(item.ID) {
			continue
		}
		base, ok := decodeBase(item.Rollup)
		if !ok {
			invalid = append(invalid, item.ID)
		}
		listed = append(listed, Listed{Environment: envID, Workspace: item.ID, Parent: validParent(item.Parent), Base: base})
	}
	return listed, invalid, nil
}

// decodeBase is the rollup in raw, or zero when raw is null or absent; ok is
// false when raw isn't a Valid rollup.
func decodeBase(raw json.RawMessage) (base Steps, ok bool) {
	if len(raw) == 0 {
		return Steps{}, true
	}
	if err := json.Unmarshal(raw, &base); err != nil || !base.Valid() {
		return Steps{}, false
	}
	return base, true
}

// validParent is parent when it is a workspace id, and "" otherwise.
func validParent(parent string) string {
	if validWorkspaceID(parent) {
		return parent
	}
	return ""
}

// validWorkspaceID reports whether id is 1 to maxListedID bytes with no control characters.
func validWorkspaceID(id string) bool {
	if id == "" || len(id) > maxListedID {
		return false
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// Merger merges ledger children into rollups (ADR 0083 D1).
type Merger struct {
	listed   map[key]Listed
	kids     map[key][]string // same-environment children by parent workspace
	children map[key][]Entry  // ledger children by parent workspace
}

// key names a workspace on an environment; ids are map keys only.
type key struct {
	env string
	ws  string
}

// NewMerger indexes listed (by environment and workspace; a later duplicate
// key is ignored) and the ledger entries that have a parent.
func NewMerger(listed []Listed, ledger []Entry) *Merger {
	m := &Merger{
		listed:   make(map[key]Listed, len(listed)),
		kids:     make(map[key][]string),
		children: make(map[key][]Entry),
	}
	for _, l := range listed {
		k := key{l.Environment, l.Workspace}
		if _, dup := m.listed[k]; dup {
			continue
		}
		m.listed[k] = l
		if l.Parent != "" {
			parent := key{l.Environment, l.Parent}
			m.kids[parent] = append(m.kids[parent], l.Workspace)
		}
	}
	for i := range ledger {
		e := &ledger[i]
		if e.Parent == nil || e.Parent.Environment == e.Environment {
			continue
		}
		parent := key{e.Parent.Environment, e.Parent.Workspace}
		m.children[parent] = append(m.children[parent], *e)
	}
	return m
}

// Of is the merged rollup of a listed workspace: its base plus Below over
// itself and its same-environment descendants. ok is false when it isn't
// listed.
func (m *Merger) Of(env, workspace string) (Steps, bool) {
	self := key{env, workspace}
	base, ok := m.listed[self]
	if !ok {
		return Steps{}, false
	}
	g := m.newMerge()
	g.visited[self] = true
	return base.Base.Add(g.below(env, m.tree(env, workspace), 0)), true
}

// Below is the sum of the merged rollups of the ledger entries whose parent
// is (env, w) for any w in workspaces. A ledger entry that isn't listed adds
// only its own Below (its base is zero).
func (m *Merger) Below(env string, workspaces []string) Steps {
	return m.newMerge().below(env, workspaces, 0)
}

// tree is ws and its same-environment descendants, breadth-first; a seen set ends cycles.
func (m *Merger) tree(env, ws string) []string {
	nodes := []string{ws}
	seen := map[string]bool{ws: true}
	for i := 0; i < len(nodes); i++ {
		for _, child := range m.kids[key{env, nodes[i]}] {
			if !seen[child] {
				seen[child] = true
				nodes = append(nodes, child)
			}
		}
	}
	return nodes
}

// merge is the state of one Of or Below call: each ledger key is merged once.
type merge struct {
	m       *Merger
	visited map[key]bool
}

func (m *Merger) newMerge() *merge {
	return &merge{m: m, visited: make(map[key]bool)}
}

// below sums the merged rollups of the ledger children of workspaces on env; depth is the hops taken so far.
func (g *merge) below(env string, workspaces []string, depth int) Steps {
	var sum Steps
	for _, ws := range workspaces {
		for _, e := range g.m.children[key{env, ws}] {
			sum = sum.Add(g.child(&e, depth+1))
		}
	}
	return sum
}

// child is the merged rollup of ledger entry e reached at hop depth, or zero when it was merged already or is too deep.
func (g *merge) child(e *Entry, depth int) Steps {
	k := key{e.Environment, e.Workspace}
	if depth > MaxMergeDepth || g.visited[k] {
		return Steps{}
	}
	g.visited[k] = true
	base := g.m.listed[k].Base
	return base.Add(g.below(e.Environment, g.m.tree(e.Environment, e.Workspace), depth))
}
