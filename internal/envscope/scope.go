// Package envscope holds environment scopes: which environments a workspace may start children on (ADR 0082).
package envscope

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// Local is the id and name of this machine.
const Local = "local"

// Limits on scopes, scope files and the ledger (design §5).
const (
	// MaxEntries is the most environments in one scope, Self included.
	MaxEntries = 64
	// MaxFileBytes is the largest scope file.
	MaxFileBytes = 16 << 10
	// MaxLedgerEntries is the most entries in the controller's ledger.
	MaxLedgerEntries = 4096
)

// ErrInvalid marks a scope, scope file or ledger that doesn't validate.
var ErrInvalid = errors.New("invalid scope")

// ErrOutOfScope marks an environment a scope doesn't hold; see OutOfScopeError.
var ErrOutOfScope = errors.New("environment out of scope")

// Ref is one environment in a scope.
type Ref struct {
	ID   string `json:"id"`   // Local or envproto.ValidID
	Name string `json:"name"` // Local for Local
}

// Scope is a set of refs that always holds Self. The zero value is invalid.
type Scope struct {
	self string
	refs []Ref
}

// Only is the scope that holds just self.
func Only(self Ref) Scope {
	return Scope{self: self.ID, refs: []Ref{self}}
}

// New is the scope of self plus refs, deduplicated by ID and sorted with
// Local first, then by name.
func New(self Ref, refs []Ref) (Scope, error) {
	all := make([]Ref, 0, len(refs)+1)
	seen := make(map[string]bool, len(refs)+1)
	for _, ref := range append([]Ref{self}, refs...) {
		if !validID(ref.ID) {
			return Scope{}, fmt.Errorf("%w: environment id %q", ErrInvalid, ref.ID)
		}
		if seen[ref.ID] {
			continue
		}
		seen[ref.ID] = true
		all = append(all, ref)
	}
	if len(all) > MaxEntries {
		return Scope{}, fmt.Errorf("%w: %d environments, at most %d", ErrInvalid, len(all), MaxEntries)
	}
	slices.SortFunc(all, compareRefs)
	return Scope{self: self.ID, refs: all}, nil
}

// Self is the environment the scope belongs to.
func (s Scope) Self() Ref {
	ref, _ := s.ref(s.self)
	return ref
}

// Refs is a copy of the scope's environments, sorted.
func (s Scope) Refs() []Ref {
	return slices.Clone(s.refs)
}

// IDs are the environment ids, in the order of Refs.
func (s Scope) IDs() []string {
	ids := make([]string, 0, len(s.refs))
	for _, ref := range s.refs {
		ids = append(ids, ref.ID)
	}
	return ids
}

// Names are the environment names, in the order of Refs.
func (s Scope) Names() []string {
	names := make([]string, 0, len(s.refs))
	for _, ref := range s.refs {
		names = append(names, ref.Name)
	}
	return names
}

// Contains reports whether id is in the scope.
func (s Scope) Contains(id string) bool {
	_, ok := s.ref(id)
	return ok
}

// ByName returns the environment called name.
func (s Scope) ByName(name string) (Ref, bool) {
	i := slices.IndexFunc(s.refs, func(ref Ref) bool { return ref.Name == name })
	if i < 0 {
		return Ref{}, false
	}
	return s.refs[i], true
}

// WiderThanSelf reports whether the scope holds an environment besides Self.
func (s Scope) WiderThanSelf() bool {
	return len(s.refs) > 1
}

// Narrow returns {self} ∪ ids. self.ID and every id must be in s, else an
// *OutOfScopeError for the first that isn't (input order).
func (s Scope) Narrow(self Ref, ids []string) (Scope, error) {
	selfRef, ok := s.ref(self.ID)
	if !ok {
		return Scope{}, s.outOfScope(self.ID)
	}
	picked := make([]Ref, 0, len(ids))
	for _, id := range ids {
		ref, ok := s.ref(id)
		if !ok {
			return Scope{}, s.outOfScope(id)
		}
		picked = append(picked, ref)
	}
	return New(selfRef, picked)
}

// Intersect keeps the refs whose ID is in ids, and always self.
func (s Scope) Intersect(ids []string) Scope {
	kept := slices.DeleteFunc(s.Refs(), func(ref Ref) bool {
		return ref.ID != s.self && !slices.Contains(ids, ref.ID)
	})
	return Scope{self: s.self, refs: kept}
}

// OutOfScopeError names an environment that a scope doesn't hold.
type OutOfScopeError struct {
	Environment string   // the offending name (or id when no name is known)
	Allowed     []string // s.Names()
}

func (e *OutOfScopeError) Error() string {
	return fmt.Sprintf("environment %s isn't in your scope (%s)", e.Environment, strings.Join(e.Allowed, ", "))
}

func (*OutOfScopeError) Unwrap() error {
	return ErrOutOfScope
}

func (s Scope) outOfScope(id string) error {
	name := id
	if ref, ok := s.ref(id); ok {
		name = ref.Name
	}
	return &OutOfScopeError{Environment: name, Allowed: s.Names()}
}

func (s Scope) ref(id string) (Ref, bool) {
	i := slices.IndexFunc(s.refs, func(ref Ref) bool { return ref.ID == id })
	if i < 0 {
		return Ref{}, false
	}
	return s.refs[i], true
}

func validID(id string) bool {
	return id == Local || envproto.ValidID(id)
}

// compareRefs puts Local first, then sorts by name and then by id.
func compareRefs(a, b Ref) int {
	return cmp.Or(
		cmp.Compare(localRank(a), localRank(b)),
		strings.Compare(a.Name, b.Name),
		strings.Compare(a.ID, b.ID),
	)
}

func localRank(ref Ref) int {
	if ref.ID == Local {
		return 0
	}
	return 1
}
