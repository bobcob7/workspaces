package runner

import (
	"time"

	"github.com/bobcob7/workspaces/internal/agent"
	"github.com/bobcob7/workspaces/internal/mcp"
)

const (
	statusVersion = 1
	// startGrace is how long a run without status.json counts as starting.
	startGrace = 10 * time.Second
	// restartWindow is how long after a resume a dead runner counts as starting.
	restartWindow = 3 * time.Minute
	// startTolerance absorbs status.json's whole-second start times.
	startTolerance = time.Second
)

// Status is status.json (reference.md). Exit and ExitMessage are nil while
// the run is going; Verdict is nil until there is one.
type Status struct {
	Version          int                   `json:"version"`
	Session          string                `json:"session"`
	PID              int                   `json:"pid"`
	ProcessStartedAt time.Time             `json:"process_started_at"`
	State            string                `json:"state"`
	Step             int                   `json:"step"`
	MaxSteps         int                   `json:"max_steps"`
	Restarts         int                   `json:"restarts"`
	Usage            agent.Usage           `json:"usage"`
	MCP              []mcp.Status          `json:"mcp"`
	Exit             *string               `json:"exit"`
	ExitMessage      *string               `json:"exit_message"`
	Verdict          *string               `json:"verdict"`
	UpdatedAt        time.Time             `json:"updated_at"`
	Endpoint         string                `json:"endpoint"`
	Model            string                `json:"model"`
	EndpointChanges  int                   `json:"endpoint_changes"`
	Endpoints        []agent.EndpointUsage `json:"endpoints"`
}

// State is ADR 0063's state of the run in f: starting, working, idle,
// stopped or unknown. recordedAt is when the run was started; it decides
// starting against stopped while there is no status.json yet. It returns
// the live pid for working, else 0, and the status it read, if any.
func (f Files) State(now, recordedAt time.Time, procs processReader) (state string, pid int, st *Status) {
	status, err := f.ReadStatus()
	if isMissing(err) {
		return startingOrStopped(now.Sub(recordedAt) < startGrace), 0, nil
	}
	if err != nil {
		return "unknown", 0, nil
	}
	if status.Exit != nil {
		return "idle", 0, &status
	}
	start, err := procs.StartTime(status.PID)
	if err == nil && sameStart(start, status.ProcessStartedAt) {
		return "working", status.PID, &status
	}
	if err == nil {
		return "stopped", 0, &status
	}
	resumed := status.Restarts > 0 && now.Sub(status.UpdatedAt) < restartWindow
	return startingOrStopped(resumed), 0, &status
}

func startingOrStopped(starting bool) string {
	if starting {
		return "starting"
	}
	return "stopped"
}

func sameStart(live, recorded time.Time) bool {
	diff := live.Sub(recorded)
	return diff < startTolerance && diff > -startTolerance
}
