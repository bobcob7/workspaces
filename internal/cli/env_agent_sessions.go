package cli

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// agentSession resolves a workspace reference to its workspace and Claude
// Code session: workspaces.Find(ref), then lifetime.List for its entry.
// No session, a shell, or a ws runner session: usage "<ref> has no Claude
// Code agent". Not found: workspace_not_found.
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
		if s := entry.Session; s != nil && s.Kind == lifetime.KindAgent && s.Runner != lifetime.RunnerWS {
			return ws, s, nil
		}
		break
	}
	return workspace.Workspace{}, nil, &envproto.ReplyError{Code: string(codeUsage), Message: ref + " has no Claude Code agent"}
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

// agentStatus is the workspace's session and its Claude Code agent, which is
// null when Claude Code doesn't list it.
func (o *agentOperations) agentStatus(ctx context.Context, args *envproto.WorkspaceArgs) (json.RawMessage, error) {
	ws, s, err := o.agentSession(ctx, args.Workspace)
	if err != nil {
		return nil, err
	}
	sessions, err := o.agentSessions(ctx)
	if err != nil {
		return nil, err
	}
	result := envproto.AgentStatusResult{Workspace: ws.ID, Session: s.ID}
	if agent := findAgent(sessions, s.ID); agent != nil {
		snap := snapshotAgent(agent)
		result.Agent = &snap
	}
	return json.Marshal(result)
}

// agentLogs keeps the last Tail lines (DefaultTail for 0) of claude.Logs,
// then drops lines from the front until the JSON fits MaxLogBytes, setting
// truncated.
func (o *agentOperations) agentLogs(ctx context.Context, args *envproto.LogsArgs) (json.RawMessage, error) {
	ws, s, err := o.agentSession(ctx, args.Workspace)
	if err != nil {
		return nil, err
	}
	text, err := o.app.claude.Logs(ctx, s.ID)
	if err != nil {
		return nil, err
	}
	result := envproto.AgentLogsResult{Workspace: ws.ID, Session: s.ID, Lines: tailLines(text, args.Tail)}
	fitLogs(&result)
	return json.Marshal(result)
}

// tailLines is the last tail lines of text, DefaultTail when tail is 0.
func tailLines(text string, tail int) []string {
	if text == "" {
		return []string{}
	}
	if tail == 0 {
		tail = envproto.DefaultTail
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	return lines[max(0, len(lines)-tail):]
}

// fitLogs drops the fewest lines from the front of result that bring its
// JSON within MaxLogBytes, and sets Truncated when it drops any.
func fitLogs(result *envproto.AgentLogsResult) {
	fits := func(drop int) bool {
		trial := *result
		trial.Lines = result.Lines[drop:]
		return encodedLen(trial) <= envproto.MaxLogBytes
	}
	drop := sort.Search(len(result.Lines), fits)
	result.Truncated = drop > 0
	result.Lines = result.Lines[drop:]
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
	ws, s, err := o.agentSession(ctx, args.Workspace)
	if err != nil {
		return nil, err
	}
	sessions, err := o.agentSessions(ctx)
	if err != nil {
		return nil, err
	}
	agent := findAgent(sessions, s.ID)
	if agent == nil {
		return nil, &envproto.ReplyError{Code: string(codeClaudeFailed), Message: "Claude Code doesn't list session " + s.ID}
	}
	err = o.app.claude.Send(ctx, agent, args.Message)
	if errors.Is(err, claude.ErrNotDelivered) {
		return nil, &envproto.ReplyError{Code: string(codeClaudeFailed), Message: err.Error()}
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(envproto.AgentSendResult{Workspace: ws.ID, Session: s.ID, Sent: true})
}

// agentStop stops the workspace's Claude Code session. One that has already
// ended is stopped false, not an error.
func (o *agentOperations) agentStop(ctx context.Context, args *envproto.WorkspaceArgs) (json.RawMessage, error) {
	ws, s, err := o.agentSession(ctx, args.Workspace)
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
	agent := envproto.SnapshotAgent{
		ID:        s.ID,
		SessionID: s.SessionID,
		Name:      s.Name,
		State:     string(s.State),
		Status:    s.Status,
	}
	if s.PID != 0 {
		agent.PID = new(s.PID)
	}
	if !s.StartedAt.IsZero() {
		agent.StartedAt = new(s.StartedAt.UTC())
	}
	return agent
}
