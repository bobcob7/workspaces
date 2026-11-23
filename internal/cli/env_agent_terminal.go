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

// terminalEnvAllowed reports whether a terminal's program may see the
// variable key of the agent's environment. Nothing else is passed on: the
// agent may hold CLAUDE_CODE_OAUTH_TOKEN, forge tokens and other secrets.
func terminalEnvAllowed(key string) bool {
	switch key {
	case "PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LANGUAGE", "TMPDIR",
		"XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME":
		return true
	}
	return strings.HasPrefix(key, "LC_")
}

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
	path, err := exec.LookPath("claude")
	if err != nil {
		return envagent.TerminalCommand{}, &envproto.ReplyError{Code: string(codeClaudeMissing), Message: "claude isn't installed: " + err.Error()}
	}
	return envagent.TerminalCommand{
		Path:      path,
		Args:      []string{"attach", session.ID},
		Dir:       dir,
		Env:       terminalEnv(o.app.environ),
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
	return envagent.TerminalCommand{
		Path: exe,
		Args: argv,
		Dir:  homeOf(env),
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

// homeOf is HOME in env, else the process's home directory.
func homeOf(env []string) string {
	for _, kv := range env {
		if home, ok := strings.CutPrefix(kv, "HOME="); ok && home != "" {
			return home
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "/"
	}
	return home
}

// within reports whether path is root or below it, after cleaning both.
func within(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
