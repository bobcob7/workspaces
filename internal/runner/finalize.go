package runner

import (
	"fmt"
	"os"
	"strings"
	"time"

	json "encoding/json/v2"

	"github.com/bobcob7/workspaces/internal/agent"
	"github.com/bobcob7/workspaces/internal/machine"
)

// Exit reasons the runner writes itself (reference.md, "Runner exit reasons").
const (
	exitStopped = agent.ExitStopped
	// defaultFail is the verdict a run without a role records.
	defaultFail = "FAIL"
	// reportEvents is how many transcript events a finalizer's report keeps.
	reportEvents = 20
)

// Usage is usage.json (reference.md): written once, at exit.
type Usage struct {
	Version     int             `json:"version"`
	Runner      string          `json:"runner"`
	Model       string          `json:"model"`
	ModelAlias  string          `json:"model_alias"`
	Endpoint    string          `json:"endpoint"`
	URL         string          `json:"url"`
	API         string          `json:"api"`
	Role        string          `json:"role"`
	Exit        string          `json:"exit"`
	Steps       int             `json:"steps"`
	Restarts    int             `json:"restarts"`
	Usage       agent.Usage     `json:"usage"`
	MCPCalls    map[string]int  `json:"mcp_calls"`
	Endpoints   []UsageEndpoint `json:"endpoints"`
	StartedAt   time.Time       `json:"started_at"`
	EndedAt     time.Time       `json:"ended_at"`
	WallSeconds int             `json:"wall_seconds"`
	TraceID     string          `json:"trace_id"`
}

// UsageEndpoint is one endpoint's usage, with the history's url and api.
type UsageEndpoint struct {
	Endpoint string      `json:"endpoint"`
	URL      string      `json:"url"`
	API      string      `json:"api"`
	Model    string      `json:"model"`
	Usage    agent.Usage `json:"usage"`
}

// FinalizeInput is what a run leaves behind for usage.json and status.json.
type FinalizeInput struct {
	// Verdict is the verdict already recorded, "" when none was.
	Verdict   string
	Steps     int
	Restarts  int
	Usage     agent.Usage
	Endpoints []agent.EndpointUsage
	MCPCalls  map[string]int
	StartedAt time.Time
	EndedAt   time.Time
}

// Finalize ends a run that has no verdict from the model: unless exit is
// "stopped" and the run has no verdict, it records the role's first fail
// verdict with a generated report. It then writes usage.json and the final
// status.json. The runner calls it at every exit; the daemon calls it for
// "crashed".
func Finalize(f Files, run *Run, exit, message string, out *FinalizeInput, store resultStore) error {
	if out.Verdict == "" && exit != exitStopped {
		verdict := failVerdict(run)
		report, err := f.report(exit, message)
		if err != nil {
			return err
		}
		if err := f.Record(run, store, verdict, report); err != nil {
			return err
		}
		out.Verdict = verdict
	}
	if err := f.writeUsage(run, exit, out, store); err != nil {
		return err
	}
	return f.finishStatus(run, exit, message, out)
}

// Record writes verdict and report: through store, as ws machine result
// does, when run has a parent, else to runner/report.md. Either way the
// verdict goes to status.json.
func (f Files) Record(run *Run, store resultStore, verdict string, report []byte) error {
	if run.Parent == "" {
		if err := writeAtomic(f.path(reportFile), report); err != nil {
			return fmt.Errorf("writing report: %w", err)
		}
	} else if err := f.recordThroughStore(store, verdict, report); err != nil {
		return err
	}
	return f.setVerdict(verdict)
}

func (f Files) recordThroughStore(store resultStore, verdict string, report []byte) error {
	root, ancestors, err := store.Lineage(f.ID)
	if err != nil {
		return fmt.Errorf("finding %s's root: %w", f.ID, err)
	}
	result := machine.Result{Verdict: verdict, Ancestors: ancestors, RecordedAt: time.Now().UTC().Truncate(time.Second)}
	if err := store.WriteResult(root, f.ID, &result, report); err != nil {
		return fmt.Errorf("recording %s's result: %w", f.ID, err)
	}
	return nil
}

// recordedVerdict is the verdict the run recorded: the model's own or the
// finalizer's, from result.toml with a parent, else from status.json. ""
// when there is none.
func (f Files) recordedVerdict(run *Run, store resultStore) (string, error) {
	if run.Parent != "" {
		root, _, err := store.Lineage(f.ID)
		if err != nil {
			return "", fmt.Errorf("finding %s's root: %w", f.ID, err)
		}
		rec, err := store.Read(root, f.ID)
		if err != nil {
			return "", fmt.Errorf("reading %s's result: %w", f.ID, err)
		}
		if rec.Result == nil {
			return "", nil
		}
		return rec.Result.Verdict, nil
	}
	st, err := f.ReadStatus()
	if isMissing(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if st.Verdict == nil {
		return "", nil
	}
	return *st.Verdict, nil
}

func (f Files) setVerdict(verdict string) error {
	st, err := f.ReadStatus()
	if isMissing(err) {
		st = Status{}
	} else if err != nil {
		return err
	}
	st.Verdict = &verdict
	return f.WriteStatus(&st)
}

func (f Files) writeUsage(run *Run, exit string, out *FinalizeInput, store resultStore) error {
	path, err := f.UsagePath(run, store)
	if err != nil {
		return err
	}
	usage := usageOf(run, exit, out)
	data, err := json.Marshal(usage)
	if err != nil {
		return fmt.Errorf("encoding usage.json: %w", err)
	}
	return writeAtomic(path, data)
}

// usageOf is usage.json's content. The endpoint fields are the run's last
// history entry; endpoints pairs each usage entry with its history entry.
func usageOf(run *Run, exit string, out *FinalizeInput) Usage {
	current := run.Current()
	mcpCalls := out.MCPCalls
	if mcpCalls == nil {
		mcpCalls = map[string]int{}
	}
	return Usage{
		Version:     statusVersion,
		Runner:      "ws",
		Model:       current.Model,
		ModelAlias:  run.ModelAlias,
		Endpoint:    current.Endpoint,
		URL:         current.URL,
		API:         current.API,
		Role:        run.Role,
		Exit:        exit,
		Steps:       out.Steps,
		Restarts:    out.Restarts,
		Usage:       out.Usage,
		MCPCalls:    mcpCalls,
		Endpoints:   usageEndpoints(run, out.Endpoints),
		StartedAt:   out.StartedAt,
		EndedAt:     out.EndedAt,
		WallSeconds: int(out.EndedAt.Sub(out.StartedAt).Seconds()),
		TraceID:     run.TraceID,
	}
}

func usageEndpoints(run *Run, endpoints []agent.EndpointUsage) []UsageEndpoint {
	out := make([]UsageEndpoint, 0, len(endpoints))
	for i, e := range endpoints {
		entry := UsageEndpoint{Endpoint: e.Endpoint, Model: e.Model, Usage: e.Usage}
		if i < len(run.EndpointHistory) {
			entry.URL = run.EndpointHistory[i].URL
			entry.API = run.EndpointHistory[i].API
		}
		out = append(out, entry)
	}
	return out
}

// finishStatus writes the final status.json: the exit, its message, the
// verdict and the totals.
func (f Files) finishStatus(run *Run, exit, message string, out *FinalizeInput) error {
	st, err := f.ReadStatus()
	if isMissing(err) {
		st = Status{Session: run.Session, PID: os.Getpid(), MaxSteps: run.MaxSteps}
	} else if err != nil {
		return err
	}
	current := run.Current()
	st.State = "exited"
	st.Step = out.Steps
	st.Restarts = out.Restarts
	st.Usage = out.Usage
	st.Endpoints = out.Endpoints
	st.Endpoint = current.Endpoint
	st.Model = current.Model
	st.Exit = &exit
	st.ExitMessage = &message
	st.UpdatedAt = out.EndedAt
	if out.Verdict != "" {
		st.Verdict = &out.Verdict
	}
	return f.WriteStatus(&st)
}

// report is the generated report: its first line names the exit, then the
// message and the last transcript events.
func (f Files) report(exit, message string) ([]byte, error) {
	events, err := f.lastEvents()
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "ws runner: %s\n\n%s\n\n## Last events\n\n```json\n", exit, message)
	for _, line := range events {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString("```\n")
	return []byte(b.String()), nil
}

// lastEvents returns the last reportEvents lines of transcript.jsonl; a missing file
// has none.
func (f Files) lastEvents() ([]string, error) {
	data, err := os.ReadFile(f.Transcript())
	if isMissing(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading transcript: %w", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > reportEvents {
		lines = lines[len(lines)-reportEvents:]
	}
	return lines, nil
}

// failVerdict is the role's first fail verdict, FAIL without a role.
func failVerdict(run *Run) string {
	if len(run.Fail) == 0 {
		return defaultFail
	}
	return run.Fail[0]
}
