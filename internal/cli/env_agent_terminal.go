package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bobcob7/workspaces/internal/envagent"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// TerminalCommand implements envagent's terminalCommands (D6, D8): the
// program a terminal runs, as an argv with no shell, a scrubbed environment
// and a session id that comes from the workspace's own record.
func (o *agentOperations) TerminalCommand(ctx context.Context, args *envproto.TerminalOpenArgs) (envagent.TerminalCommand, error) {
	cmd, err := o.terminalCommand(ctx, args)
	return cmd, replyError(err)
}

func (o *agentOperations) terminalCommand(ctx context.Context, args *envproto.TerminalOpenArgs) (envagent.TerminalCommand, error) {
	switch args.Kind {
	case envproto.TermAttach:
		return o.attachCommand(ctx, args)
	case envproto.TermShell:
		return o.shellCommand(args)
	}
	return envagent.TerminalCommand{}, &envproto.ReplyError{Code: string(codeUsage), Message: "unknown terminal kind"}
}

// attachCommand is claude attach for the Claude session the workspace's
// record names.
func (o *agentOperations) attachCommand(ctx context.Context, args *envproto.TerminalOpenArgs) (envagent.TerminalCommand, error) {
	if args.Workspace == nil {
		return envagent.TerminalCommand{}, &envproto.ReplyError{Code: string(codeUsage), Message: "attach needs a workspace"}
	}
	ws, err := o.app.workspaces.Find(*args.Workspace)
	if errors.Is(err, workspace.ErrNotFound) {
		return envagent.TerminalCommand{}, &envproto.ReplyError{Code: string(codeWorkspaceNotFound), Message: err.Error()}
	}
	if err != nil {
		return envagent.TerminalCommand{}, err
	}
	session, err := o.claudeSessionOf(ctx, ws.ID)
	if err != nil {
		return envagent.TerminalCommand{}, err
	}
	dir := ws.SessionDir()
	if !within(o.app.paths.Workspaces, dir) {
		return envagent.TerminalCommand{}, &envproto.ReplyError{Code: string(codeUsage), Message: "the workspace is outside workspaces/"}
	}
	env := terminalEnv(o.app.environ)
	path, err := lookPathIn(env)
	if err != nil {
		return envagent.TerminalCommand{}, &envproto.ReplyError{Code: string(codeClaudeMissing), Message: "claude isn't installed: " + err.Error()}
	}
	return envagent.TerminalCommand{
		Path:      path,
		Args:      []string{"attach", session.ID},
		Dir:       dir,
		Env:       env,
		Workspace: &ws.ID,
		Session:   &session.ID,
	}, nil
}

// claudeSessionOf is the workspace's session when it is a Claude agent.
func (o *agentOperations) claudeSessionOf(ctx context.Context, id string) (*lifetime.Session, error) {
	listing, err := o.app.lifetime.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range listing.Workspaces {
		entry := &listing.Workspaces[i]
		if entry.Workspace.ID != id {
			continue
		}
		if s := entry.Session; s != nil && s.Kind == lifetime.KindAgent && s.Runner == lifetime.RunnerClaude && s.ID != "" {
			return s, nil
		}
		break
	}
	return nil, &envproto.ReplyError{Code: string(codeUsage), Message: "only a Claude agent can be attached"}
}

// shellCommand is ws shell, whose workspace is made for this environment.
// The specs follow "--", so none can be taken for a flag.
func (o *agentOperations) shellCommand(args *envproto.TerminalOpenArgs) (envagent.TerminalCommand, error) {
	exe, err := os.Executable()
	if err != nil {
		return envagent.TerminalCommand{}, err
	}
	argv := []string{"--config-dir", o.app.paths.Root, "shell"}
	if args.Name != nil {
		argv = append(argv, "--name", *args.Name)
	}
	if args.Offline {
		argv = append(argv, "--offline")
	}
	argv = append(argv, "--")
	argv = append(argv, args.Specs...)
	env := terminalEnv(o.app.environ)
	home := homeOf(env)
	if home == "" {
		return envagent.TerminalCommand{}, &envproto.ReplyError{Code: string(codeUsage), Message: "HOME is not set in the agent's environment"}
	}
	return envagent.TerminalCommand{
		Path: exe,
		Args: argv,
		Dir:  home,
		Env:  append(env, "WS_ENVIRONMENT="+o.name),
	}, nil
}

// terminalEnv is the allow-listed part of environ plus TERM.
func terminalEnv(environ []string) []string {
	env := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		key, _, _ := strings.Cut(kv, "=")
		if terminalEnvAllowed(key) {
			env = append(env, kv)
		}
	}
	return append(env, "TERM=xterm-256color")
}

// homeOf is HOME in env, or "" when it is unset or empty.
func homeOf(env []string) string {
	for _, kv := range env {
		if home, ok := strings.CutPrefix(kv, "HOME="); ok {
			return home
		}
	}
	return ""
}

// lookPathIn finds the claude executable in the absolute directories of the
// search path in env, the path the child itself will search. It tries the
// file names the platform gives claude (executableNames).
func lookPathIn(env []string) (string, error) {
	var pathVar string
	for _, kv := range env {
		if key, value, ok := strings.Cut(kv, "="); ok && isPathKey(key) {
			pathVar = value
		}
	}
	for _, dir := range filepath.SplitList(pathVar) {
		if !filepath.IsAbs(dir) {
			continue
		}
		for _, name := range executableNames("claude", env) {
			if info, err := os.Stat(filepath.Join(dir, name)); err == nil && isExecutable(info) {
				return filepath.Join(dir, name), nil
			}
		}
	}
	return "", exec.ErrNotFound
}

// within reports whether path is root or below it, after cleaning both.
func within(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
