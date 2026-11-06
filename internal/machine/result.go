// Package machine keeps the records of ws machine children under the root
// of their tree: what each was spawned as, and the verdict it recorded
// (ADR 0050).
package machine

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/workspace"
)

// FormatVersion is the newest spawn.toml and result.toml version this ws
// reads and writes.
const FormatVersion = 1

// MaxReportSize is the largest report ws machine result keeps.
const MaxReportSize = 1 << 20

const maxVerdictLength = 32

var (
	// ErrInvalid is a malformed verdict, id, or record.
	ErrInvalid = errors.New("invalid machine record")
	// ErrNewer is a record whose version is above FormatVersion.
	ErrNewer = errors.New("machine record from a newer ws")
)

// Outcome is what a verdict means: pass or fail.
type Outcome string

// The two outcomes of a result.
const (
	OutcomePass Outcome = "pass"
	OutcomeFail Outcome = "fail"
)

// DefaultPass and DefaultFail judge a child spawned without a role. They
// are read-only: callers must not modify them.
//
//nolint:gochecknoglobals // read-only defaults, specified by ADR 0050
var (
	DefaultPass = []string{"PASS", "APPROVED"}
	DefaultFail = []string{"FAIL", "CHANGES_REQUESTED"}
)

// NormalizeVerdict upper-cases s and checks it is 1-32 characters of
// [A-Za-z0-9_] starting with a letter; anything else is ErrInvalid.
func NormalizeVerdict(s string) (string, error) {
	v := strings.ToUpper(s)
	if !validVerdict(v) {
		return "", fmt.Errorf("%w: verdict %q", ErrInvalid, s)
	}
	return v, nil
}

func validVerdict(v string) bool {
	if v == "" || len(v) > maxVerdictLength {
		return false
	}
	for i := range len(v) {
		c := v[i]
		isLetter := 'A' <= c && c <= 'Z'
		isDigit := '0' <= c && c <= '9'
		if i == 0 && !isLetter {
			return false
		}
		if !isLetter && !isDigit && c != '_' {
			return false
		}
	}
	return true
}

// Spawn is spawn.toml: what ws machine spawn --role started a child as.
type Spawn struct {
	Version   int               `toml:"version"`
	Role      string            `toml:"role"`
	Model     string            `toml:"model,omitempty"`
	Pass      []string          `toml:"pass"`
	Fail      []string          `toml:"fail"`
	Vars      map[string]string `toml:"vars,omitempty"`
	Ancestors []string          `toml:"ancestors"`
	SpawnedAt time.Time         `toml:"spawned_at"`
}

// Allows reports whether verdict is one of the spawn's pass or fail verdicts.
func (s *Spawn) Allows(verdict string) bool {
	return slices.Contains(s.Pass, verdict) || slices.Contains(s.Fail, verdict)
}

// Result is result.toml: the verdict a child recorded.
type Result struct {
	Version    int       `toml:"version"`
	Verdict    string    `toml:"verdict"`
	Ancestors  []string  `toml:"ancestors"`
	RecordedAt time.Time `toml:"recorded_at"`
	// Report is set when report.md was kept with the result.
	Report bool `toml:"report"`
}

// OutcomeOf is pass when verdict is in spawn's Pass list, or with a nil
// spawn in DefaultPass; every other verdict is fail.
func OutcomeOf(spawn *Spawn, verdict string) Outcome {
	passes := DefaultPass
	if spawn != nil {
		passes = spawn.Pass
	}
	if slices.Contains(passes, verdict) {
		return OutcomePass
	}
	return OutcomeFail
}

// Record is what the store keeps for one child. Spawn and Result are nil
// when their file doesn't exist.
type Record struct {
	Child  string
	Spawn  *Spawn
	Result *Result
	// ReportPath is report.md's absolute path, or "" when Result is nil or
	// has no report.
	ReportPath string
}

// Found reports whether either file exists.
func (r *Record) Found() bool {
	return r.Spawn != nil || r.Result != nil
}

// Role is Spawn.Role, or "" without a spawn record.
func (r *Record) Role() string {
	if r.Spawn == nil {
		return ""
	}
	return r.Spawn.Role
}

// Ancestors is Result's ancestors, else Spawn's, else nil.
func (r *Record) Ancestors() []string {
	switch {
	case r.Result != nil:
		return r.Result.Ancestors
	case r.Spawn != nil:
		return r.Spawn.Ancestors
	default:
		return nil
	}
}

// Outcome is OutcomeOf(r.Spawn, r.Result.Verdict); ok is false when Result is nil.
func (r *Record) Outcome() (outcome Outcome, ok bool) {
	if r.Result == nil {
		return "", false
	}
	return OutcomeOf(r.Spawn, r.Result.Verdict), true
}

// Ancestors is id's parent, its parent, and so on up to the root, from
// list. It stops at a workspace with no parent, a parent not in list, or
// an id already seen (a cycle). An id not in list has none.
func Ancestors(list []workspace.Workspace, id string) []string {
	parents := make(map[string]string, len(list))
	for i := range list {
		parents[list[i].ID] = list[i].Parent
	}
	var out []string
	seen := map[string]bool{id: true}
	for cur := id; ; {
		parent, ok := parents[cur]
		if !ok || parent == "" || seen[parent] {
			return out
		}
		if _, known := parents[parent]; !known {
			return out
		}
		seen[parent] = true
		out = append(out, parent)
		cur = parent
	}
}

// Root is the last of Ancestors(list, id), or id itself when it has none.
func Root(list []workspace.Workspace, id string) string {
	ancestors := Ancestors(list, id)
	if len(ancestors) == 0 {
		return id
	}
	return ancestors[len(ancestors)-1]
}
