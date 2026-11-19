package runner

import (
	"os"
	"time"
)

// Reader reads the runner sessions under a state directory, for the
// lifetime pass and ws list (ADR 0063). It never starts or stops anything.
type Reader struct {
	stateDir string
	procs    processReader
	now      func() time.Time
}

// NewReader returns a Reader of the runner sessions under stateDir, asking
// the operating system about processes with procs.
func NewReader(stateDir string, procs processReader) Reader {
	return Reader{stateDir: stateDir, procs: procs, now: time.Now}
}

// Exists reports whether workspace id has its runner directory.
func (r Reader) Exists(id string) bool {
	_, err := os.Stat(FilesFor(r.stateDir, id).Dir)
	return err == nil
}

// State is workspace id's runner state (ADR 0063), its live pid, and the
// status and run it read: each is nil when its file can't be read, and
// recordedAt is when the session was recorded.
func (r Reader) State(id string, recordedAt time.Time) (string, int, *Status, *Run) {
	f := FilesFor(r.stateDir, id)
	state, pid, st := f.State(r.now(), recordedAt, r.procs)
	run, err := f.ReadRun()
	if err != nil {
		return state, pid, st, nil
	}
	return state, pid, st, &run
}
