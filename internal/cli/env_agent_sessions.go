package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/bobcob7/workspaces/internal/agent"
	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/runner"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// agentSession resolves a workspace reference to its workspace and Claude
// Code session: workspaces.Find(ref), then lifetime.List for its entry.
// No session or a shell: usage "<ref> has no Claude Code agent". Not found:
// workspace_not_found.
func (o *agentOperations) agentSession(ctx context.Context, ref string) (workspace.Workspace, *lifetime.Session, error) {
	ws, err := o.app.workspaces.Find(ref)
	if errors.Is(err, workspace.ErrNotFound) {
		return workspace.Workspace{}, nil, &envproto.ReplyError{Code: string(codeWorkspaceNotFound), Message: err.Error()}
	}
	if err != nil {
		return workspace.Workspace{}, nil, err
	}
	listing, err := o.app.lifetime.List(ctx)
	if err != nil {
		return workspace.Workspace{}, nil, err
	}
	for i := range listing.Workspaces {
		entry := &listing.Workspaces[i]
		if entry.Workspace.ID != ws.ID {
			continue
		}
		if s := entry.Session; s != nil && s.Kind == lifetime.KindAgent {
			return ws, s, nil
		}
		break
	}
	return workspace.Workspace{}, nil, &envproto.ReplyError{Code: string(codeUsage), Message: ref + " has no Claude Code agent"}
}

// claudeAgentSession is agentSession for the ops that talk to Claude Code
// (send, stop): a ws runner session is no Claude Code agent.
func (o *agentOperations) claudeAgentSession(ctx context.Context, ref string) (workspace.Workspace, *lifetime.Session, error) {
	ws, s, err := o.agentSession(ctx, ref)
	if err != nil {
		return ws, nil, err
	}
	if s.Runner == lifetime.RunnerWS {
		return workspace.Workspace{}, nil, &envproto.ReplyError{Code: string(codeUsage), Message: ref + " has no Claude Code agent"}
	}
	return ws, s, nil
}

// agentSessions lists Claude Code's sessions, finished ones included.
func (o *agentOperations) agentSessions(ctx context.Context) ([]claude.Session, error) {
	return o.app.claude.Agents(ctx, claude.ListOptions{All: true})
}

// findAgent is the Claude Code session with id in sessions, or nil.
func findAgent(sessions []claude.Session, id string) *claude.Session {
	for i := range sessions {
		if sessions[i].ID == id {
			return &sessions[i]
		}
	}
	return nil
}

// agentStatus is the workspace's session and its agent. A Claude Code agent
// is null when Claude Code doesn't list it; a ws runner reports its lifetime
// state.
func (o *agentOperations) agentStatus(ctx context.Context, args *envproto.WorkspaceArgs) (json.RawMessage, error) {
	ws, s, err := o.agentSession(ctx, args.Workspace)
	if err != nil {
		return nil, err
	}
	result := envproto.AgentStatusResult{Workspace: ws.ID, Session: s.ID}
	if s.Runner == lifetime.RunnerWS {
		result.Agent = runnerSnapshot(ws.ID, s)
		return json.Marshal(result)
	}
	sessions, err := o.agentSessions(ctx)
	if err != nil {
		return nil, err
	}
	if found := findAgent(sessions, s.ID); found != nil {
		result.Agent = new(snapshotAgent(found))
	}
	return json.Marshal(result)
}

// runnerSnapshot is a ws runner's agent: its lifetime state, under the
// workspace's name, with its process when it has one.
func runnerSnapshot(name string, s *lifetime.Session) *envproto.SnapshotAgent {
	snap := envproto.SnapshotAgent{ID: s.ID, Name: name, State: string(s.State)}
	if s.PID > 0 {
		snap.PID = new(s.PID)
	}
	return &snap
}

// agentLogs keeps the last Tail lines (DefaultTail for 0) of the agent's log,
// then drops lines from the front until the JSON fits MaxLogBytes. A ws
// runner's log is its transcript events, one JSON line each. Truncated is
// set when lines were cut, by the tail or the size limit.
func (o *agentOperations) agentLogs(ctx context.Context, args *envproto.LogsArgs) (json.RawMessage, error) {
	ws, s, err := o.agentSession(ctx, args.Workspace)
	if err != nil {
		return nil, err
	}
	if s.Runner == lifetime.RunnerWS {
		return o.runnerLogs(ws.ID, s, args.Tail)
	}
	text, err := o.app.claude.Logs(ctx, s.ID)
	if err != nil {
		return nil, err
	}
	lines, cut := tailLines(text, args.Tail)
	result := envproto.AgentLogsResult{Workspace: ws.ID, Session: s.ID, Lines: lines}
	fitLogs(&result)
	result.Truncated = result.Truncated || cut
	return json.Marshal(result)
}

// runnerLogs is the last tail transcript events of the ws runner session s
// of workspace id, one JSON line each. A transcript that doesn't exist yet
// has no events.
func (o *agentOperations) runnerLogs(id string, s *lifetime.Session, tail int) (json.RawMessage, error) {
	if tail == 0 {
		tail = envproto.DefaultTail
	}
	events, more, err := agent.Tail(runner.FilesFor(o.app.paths.State, id).Transcript(), tail)
	if errors.Is(err, fs.ErrNotExist) {
		events, more, err = nil, false, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s's transcript: %w", id, err)
	}
	lines := make([]string, 0, len(events))
	for _, ev := range events {
		line, err := json.Marshal(ev)
		if err != nil {
			return nil, fmt.Errorf("encoding %s's transcript event: %w", id, err)
		}
		lines = append(lines, string(line))
	}
	result := envproto.AgentLogsResult{Workspace: id, Session: s.ID, Lines: lines, Truncated: more}
	fitLogs(&result)
	result.Truncated = result.Truncated || more
	return json.Marshal(result)
}

// tailLines is the last tail lines of text without their carriage returns,
// DefaultTail when tail is 0, and whether it dropped any lines.
func tailLines(text string, tail int) ([]string, bool) {
	if text == "" {
		return []string{}, false
	}
	if tail == 0 {
		tail = envproto.DefaultTail
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	kept := lines[max(0, len(lines)-tail):]
	for i := range kept {
		kept[i] = strings.TrimSuffix(kept[i], "\r")
	}
	return kept, len(lines) > tail
}

// fitLogs drops the fewest lines from the front of result that bring its
// JSON within MaxLogBytes, and sets Truncated when it drops any. A last line
// that is too long on its own is cut from its front instead, at a rune
// boundary, so the result keeps its end.
func fitLogs(result *envproto.AgentLogsResult) {
	lines := result.Lines
	if len(lines) == 0 {
		return
	}
	drop := sort.Search(len(lines), func(drop int) bool { return fitsLines(result, lines[drop:]) })
	if drop < len(lines) {
		result.Truncated = drop > 0
		result.Lines = lines[drop:]
		return
	}
	last := lines[len(lines)-1]
	cut := sort.Search(len(last), func(i int) bool { return fitsLines(result, []string{last[i:]}) })
	for cut < len(last) && !utf8.RuneStart(last[cut]) {
		cut++
	}
	result.Truncated = true
	result.Lines = []string{last[cut:]}
}

// fitsLines reports whether result with lines as its lines is within MaxLogBytes.
func fitsLines(result *envproto.AgentLogsResult, lines []string) bool {
	trial := *result
	trial.Lines = lines
	return encodedLen(trial) <= envproto.MaxLogBytes
}

// encodedLen is the length of result's JSON. A result of strings always
// encodes, so an error is treated as too large.
func encodedLen(result envproto.AgentLogsResult) int {
	payload, err := json.Marshal(result)
	if err != nil {
		return math.MaxInt
	}
	return len(payload)
}

// agentSend types the message into the workspace's Claude Code session.
// A session Claude Code doesn't list, or a message it doesn't record, is claude_failed.
func (o *agentOperations) agentSend(ctx context.Context, args *envproto.SendArgs) (json.RawMessage, error) {
	ws, s, err := o.claudeAgentSession(ctx, args.Workspace)
	if err != nil {
		return nil, err
	}
	unlock := o.lockSession(s.ID)
	defer unlock()
	sessions, err := o.agentSessions(ctx)
	if err != nil {
		return nil, err
	}
	found := findAgent(sessions, s.ID)
	if found == nil {
		return nil, &envproto.ReplyError{Code: string(codeClaudeFailed), Message: "Claude Code doesn't list session " + s.ID}
	}
	err = o.app.claude.Send(ctx, found, args.Message)
	if errors.Is(err, claude.ErrNotDelivered) {
		return nil, &envproto.ReplyError{Code: string(codeClaudeFailed), Message: err.Error()}
	}
	if errors.Is(err, errors.ErrUnsupported) {
		return nil, &envproto.ReplyError{Code: string(codePlatformUnsupported), Message: "sending to an agent isn't supported on " + runtime.GOOS + " environments yet (ADR 0081)"}
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(envproto.AgentSendResult{Workspace: ws.ID, Session: s.ID, Sent: true})
}

// lockSession takes the send lock of the Claude Code session id, so messages
// to one session are typed one at a time. It returns the unlock.
func (o *agentOperations) lockSession(id string) func() {
	mu := o.sessionLock(id)
	mu.Lock()
	return mu.Unlock
}

// sessionLock is the send lock of the Claude Code session id, made on first use.
func (o *agentOperations) sessionLock(id string) *sync.Mutex {
	o.locksMu.Lock()
	defer o.locksMu.Unlock()
	if o.sendLocks == nil {
		o.sendLocks = map[string]*sync.Mutex{}
	}
	if o.sendLocks[id] == nil {
		o.sendLocks[id] = &sync.Mutex{}
	}
	return o.sendLocks[id]
}

// agentStop stops the workspace's Claude Code session. One that has already
// ended is stopped false, not an error.
func (o *agentOperations) agentStop(ctx context.Context, args *envproto.WorkspaceArgs) (json.RawMessage, error) {
	ws, s, err := o.claudeAgentSession(ctx, args.Workspace)
	if err != nil {
		return nil, err
	}
	stopped := true
	if err := o.app.claude.Stop(ctx, s.ID); errors.Is(err, claude.ErrNoSession) {
		stopped = false
	} else if err != nil {
		return nil, err
	}
	return json.Marshal(envproto.AgentStopResult{Workspace: ws.ID, Session: s.ID, Stopped: stopped})
}

// snapshotAgent converts a claude.Session; PID 0 and a zero StartedAt are null.
func snapshotAgent(s *claude.Session) envproto.SnapshotAgent {
	snap := envproto.SnapshotAgent{
		ID:        s.ID,
		SessionID: s.SessionID,
		Name:      s.Name,
		State:     string(s.State),
		Status:    s.Status,
	}
	if s.PID != 0 {
		snap.PID = new(s.PID)
	}
	if !s.StartedAt.IsZero() {
		snap.StartedAt = new(s.StartedAt.UTC())
	}
	return snap
}
