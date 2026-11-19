package runner

import (
	"time"

	"github.com/bobcob7/workspaces/internal/machine"
)

//go:generate go tool moq -out moq_test.go . processReader resultStore

// processReader reads a process's start time, to tell a live runner from a
// reused pid.
type processReader interface {
	StartTime(pid int) (time.Time, error)
}

// resultStore is the part of machine.Store a runner records through, plus
// the lineage ws machine result computes for its caller.
type resultStore interface {
	Dir(root, child string) string
	Read(root, child string) (machine.Record, error)
	WriteResult(root, child string, r *machine.Result, report []byte) error
	// Lineage returns child's root and its ancestors, as ws machine result does.
	Lineage(child string) (root string, ancestors []string, err error)
}
