package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/review/record"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// listedWorkspace is workspace id, created two hours before testNow, with
// buff-bear at main.
func listedWorkspace(id string) workspace.Workspace {
	return workspace.Workspace{
		ID:        id,
		Path:      "/cfg/workspaces/" + id,
		CreatedAt: testNow().Add(-2 * time.Hour),
		Repos:     []workspace.Repo{{ID: "github.com/bobcob7/buff-bear", Path: "buff-bear", Ref: "main", RefKind: "branch", Commit: "3f2a9c1e"}},
	}
}

// newListApp is a test App whose lifetime manager lists entries and
// unknown.
func newListApp(entries []lifetime.Entry, unknown []string) *mockedApp {
	e := newMockedApp()
	e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) {
		return lifetime.Listing{Workspaces: entries, Unknown: unknown}, nil
	}
	return e
}

func TestApp_newListCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newListCmd()
	t.Run("has -q and --repo", func(t *testing.T) {
		assert.NotNil(t, cmd.Flags().ShorthandLookup("q"))
		assertFlag(t, cmd, "repo", "")
	})
	t.Run("takes no arguments", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, []string{"extra"}))
	})
	t.Run("lists without running the removal pass first", func(t *testing.T) {
		e := newMockedApp()
		cmd := e.app.newListCmd()
		cmd.SetContext(t.Context())
		require.NoError(t, cmd.RunE(cmd, nil))
		assert.Len(t, e.lifetime.ListCalls(), 1)
		assert.Empty(t, e.lifetime.SweepCalls())
	})
}

func TestApp_runList(t *testing.T) {
	named := listedWorkspace("fix-login")
	named.Name = "Fix login"
	agent := lifetime.Entry{Workspace: named, Session: &lifetime.Session{Kind: lifetime.KindAgent, ID: "e8cdf5d8", State: lifetime.StateStopped, Stale: new(false)}}
	shell := lifetime.Entry{Workspace: listedWorkspace("api-bump"), Session: &lifetime.Session{Kind: lifetime.KindShell, State: lifetime.StateRunning}}
	t.Run("a table, unknown directories last", func(t *testing.T) {
		e := newListApp([]lifetime.Entry{agent, shell}, []string{"/cfg/workspaces/stray"})
		require.NoError(t, e.app.runList(t.Context(), &listOptions{}))
		assert.Equal(t, ""+
			"ID          NAME        CREATED   SESSION                   REPOS\n"+
			"fix-login   Fix login   2h ago    agent e8cdf5d8: stopped   buff-bear@main\n"+
			"api-bump    -           2h ago    shell                     buff-bear@main\n"+
			"unknown     -           -         -                         /cfg/workspaces/stray\n", e.stdout.String())
	})
	t.Run("-q prints only ids", func(t *testing.T) {
		e := newListApp([]lifetime.Entry{agent, shell}, []string{"/cfg/workspaces/stray"})
		require.NoError(t, e.app.runList(t.Context(), &listOptions{idsOnly: true}))
		assert.Equal(t, "fix-login\napi-bump\n", e.stdout.String())
	})
	t.Run("--json", func(t *testing.T) {
		e := newListApp([]lifetime.Entry{agent, shell}, []string{"/cfg/workspaces/stray"})
		e.app.global.json = true
		require.NoError(t, e.app.runList(t.Context(), &listOptions{}))
		assert.JSONEq(t, `{"workspaces":[
			{"id":"fix-login","name":"Fix login","path":"/cfg/workspaces/fix-login","created_at":"2026-10-19T10:00:00Z",
			 "session":{"kind":"agent","id":"e8cdf5d8","state":"stopped","pid":null,"stale":false},
			 "repos":[{"id":"github.com/bobcob7/buff-bear","path":"buff-bear","ref":"main","ref_kind":"branch"}],"review":null},
			{"id":"api-bump","name":null,"path":"/cfg/workspaces/api-bump","created_at":"2026-10-19T10:00:00Z",
			 "session":{"kind":"shell","id":null,"state":"running","pid":null,"stale":null},
			 "repos":[{"id":"github.com/bobcob7/buff-bear","path":"buff-bear","ref":"main","ref_kind":"branch"}],"review":null},
			{"id":null,"name":null,"path":"/cfg/workspaces/stray","created_at":null,"session":null,"repos":[],"review":null}
		],"warnings":[]}`, e.stdout.String())
	})
	t.Run("--json with nothing to list", func(t *testing.T) {
		e := newListApp(nil, nil)
		e.app.global.json = true
		require.NoError(t, e.app.runList(t.Context(), &listOptions{}))
		assert.JSONEq(t, `{"workspaces":[],"warnings":[]}`, e.stdout.String())
	})
	t.Run("--repo keeps matching workspaces and drops unknown directories", func(t *testing.T) {
		other := lifetime.Entry{Workspace: workspace.Workspace{ID: "other", Repos: []workspace.Repo{{ID: "github.com/bobcob7/api", Path: "api", Ref: "v2"}}}}
		e := newListApp([]lifetime.Entry{agent, other}, []string{"/cfg/workspaces/stray"})
		require.NoError(t, e.app.runList(t.Context(), &listOptions{idsOnly: true, repo: "api"}))
		assert.Equal(t, "other\n", e.stdout.String())
	})
	t.Run("an invalid --repo", func(t *testing.T) {
		e := newListApp(nil, nil)
		requireCode(t, codeSpecInvalid, e.app.runList(t.Context(), &listOptions{repo: "a b"}))
		assert.Empty(t, e.lifetime.ListCalls())
	})
	t.Run("-q with --json is a usage error", func(t *testing.T) {
		e := newListApp(nil, nil)
		e.app.global.json = true
		requireUsageError(t, e.app.runList(t.Context(), &listOptions{idsOnly: true}))
		assert.Empty(t, e.lifetime.ListCalls())
	})
	t.Run("warnings go to stderr, or into the JSON", func(t *testing.T) {
		e := newListApp(nil, nil)
		e.lifetime.WarningsFunc = func() []string { return []string{"couldn't list Claude Code sessions"} }
		require.NoError(t, e.app.runList(t.Context(), &listOptions{}))
		assert.Equal(t, "ws: warning: couldn't list Claude Code sessions\n", e.stderr.String())
		e.stdout.Reset()
		e.app.global.json = true
		require.NoError(t, e.app.runList(t.Context(), &listOptions{}))
		assert.JSONEq(t, `{"workspaces":[],"warnings":["couldn't list Claude Code sessions"]}`, e.stdout.String())
	})
	t.Run("a failed listing", func(t *testing.T) {
		e := newListApp(nil, nil)
		e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) { return lifetime.Listing{}, errTest }
		assert.ErrorIs(t, e.app.runList(t.Context(), &listOptions{}), errTest)
	})
	t.Run("end to end: a workspace goes once its session is gone", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		path := e.start(t, "fix-login", "e8cdf5d8")
		id := filepath.Base(path)
		claudeMD := filepath.Join(e.paths.Workspaces, "CLAUDE.md")
		require.FileExists(t, claudeMD, "written with workspaces/")
		e.setAgents(t, "["+agentJSON("e8cdf5d8", path)+"]")
		require.Equal(t, exitOK, e.run(t, "--json", "list"), "stderr: %s", e.stderr.String())
		var got listJSON
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &got))
		require.Len(t, got.Workspaces, 1, "CLAUDE.md isn't listed")
		assert.Equal(t, "agent", string(got.Workspaces[0].Session.Kind))
		assert.Equal(t, "idle", string(got.Workspaces[0].Session.State))
		e.setAgents(t, "[]")
		require.NoError(t, os.MkdirAll(filepath.Join(e.jobs, "e8cdf5d8"), 0o750))
		require.Equal(t, exitOK, e.run(t, "list", "-q"))
		assert.Equal(t, id+"\n", e.stdout.String(), "kept while its jobs directory exists")
		require.NoError(t, os.RemoveAll(filepath.Join(e.jobs, "e8cdf5d8")))
		require.Equal(t, exitOK, e.run(t, "list", "-q"))
		assert.Empty(t, e.stdout.String())
		assert.NoDirExists(t, path)
		assert.NoDirExists(t, filepath.Join(e.paths.State, id))
		assert.FileExists(t, claudeMD, "the removal pass leaves CLAUDE.md")
		assert.Empty(t, e.calls(t), "a gone session needs no claude rm")
	})
	t.Run("end to end: an abandoned workspace goes with only the sessions started in it or its one repo", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		path := e.start(t, "killed", "e8cdf5d8")
		state := filepath.Join(e.paths.State, filepath.Base(path))
		require.NoError(t, os.Remove(filepath.Join(state, "session")))
		e.setAgents(t, "["+agentJSON("e8cdf5d8", path)+","+agentJSON("5d8d652c", path+"-2")+","+agentJSON("9a9a9a9a", filepath.Join(path, "n"))+","+agentJSON("7b7b7b7b", filepath.Join(path, "n", "sub"))+"]")
		require.Equal(t, exitOK, e.run(t, "list", "-q"))
		assert.Equal(t, filepath.Base(path)+"\n", e.stdout.String(), "kept for its first minute")
		old := time.Now().Add(-2 * time.Minute)
		require.NoError(t, os.Chtimes(state, old, old))
		require.Equal(t, exitOK, e.run(t, "list", "-q"), "stderr: %s", e.stderr.String())
		assert.Empty(t, e.stdout.String())
		assert.Equal(t, []string{"rm_e8cdf5d8", "rm_9a9a9a9a"}, e.calls(t))
		assert.NoDirExists(t, path)
		assert.NoDirExists(t, state)
	})
	t.Run("end to end: a gone agent's trust goes with its workspace", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		writeClaudeJSON(t, e.claudeJSON, e.paths.Workspaces)
		path := e.start(t, "fix-login", "e8cdf5d8")
		require.Equal(t, exitOK, e.run(t, "list", "-q"), "stderr: %s", e.stderr.String())
		assert.NoDirExists(t, path)
		assert.Equal(t, []string{e.paths.Workspaces}, trustedDirsIn(t, e.claudeJSON))
	})
	t.Run("end to end: an abandoned agent's trust goes with its workspace", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		writeClaudeJSON(t, e.claudeJSON, e.paths.Workspaces)
		path := e.start(t, "killed", "e8cdf5d8")
		state := filepath.Join(e.paths.State, filepath.Base(path))
		require.NoError(t, os.Remove(filepath.Join(state, "session")))
		old := time.Now().Add(-2 * time.Minute)
		require.NoError(t, os.Chtimes(state, old, old))
		e.setAgents(t, "["+agentJSON("e8cdf5d8", filepath.Join(path, "n"))+"]")
		require.Equal(t, exitOK, e.run(t, "list", "-q"), "stderr: %s", e.stderr.String())
		assert.NoDirExists(t, path)
		assert.Equal(t, []string{"rm_e8cdf5d8"}, e.calls(t))
		assert.Equal(t, []string{e.paths.Workspaces}, trustedDirsIn(t, e.claudeJSON))
	})
	t.Run("end to end: a shell workspace stays while a process started from it holds its lock", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		path := e.start(t, "sh", "e8cdf5d8")
		state := filepath.Join(e.paths.State, filepath.Base(path))
		require.NoError(t, os.WriteFile(filepath.Join(state, "session"), []byte("kind = \"shell\"\nclaude_session_id = \"\"\n"), 0o600))
		lock := takeLock(t, filepath.Join(state, "lock"))
		child := exec.CommandContext(t.Context(), "/bin/cat")
		child.ExtraFiles = []*os.File{lock.File()}
		stdin, err := child.StdinPipe()
		require.NoError(t, err)
		require.NoError(t, child.Start())
		lock.Unlock()
		require.Equal(t, exitOK, e.run(t, "list"), "stderr: %s", e.stderr.String())
		assert.Contains(t, e.stdout.String(), filepath.Base(path)+" ")
		assert.DirExists(t, path)
		require.NoError(t, stdin.Close())
		require.NoError(t, child.Wait())
		require.Equal(t, exitOK, e.run(t, "list", "-q"))
		assert.Empty(t, e.stdout.String())
		assert.NoDirExists(t, path)
	})
	t.Run("end to end: an agent listed without a process yet stays", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		path := e.start(t, "fix-login", "e8cdf5d8")
		e.setAgents(t, fmt.Sprintf(`[{"id":"e8cdf5d8","cwd":%q,"kind":"background","startedAt":%d,"state":"blocked"}]`, path, time.Now().UnixMilli()))
		require.Equal(t, exitOK, e.run(t, "list"), "stderr: %s", e.stderr.String())
		assert.Contains(t, e.stdout.String(), "agent e8cdf5d8: ")
		assert.DirExists(t, path)
	})
	t.Run("end to end: a corrupt session record keeps the workspace", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		path := e.start(t, "fix-login", "e8cdf5d8")
		require.NoError(t, os.WriteFile(filepath.Join(e.paths.State, filepath.Base(path), "session"), []byte("kind = "), 0o600))
		require.Equal(t, exitOK, e.run(t, "--json", "list"))
		var got listJSON
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &got))
		require.Len(t, got.Workspaces, 1)
		assert.Nil(t, got.Workspaces[0].Session)
		require.Len(t, got.Warnings, 1)
		assert.Contains(t, got.Warnings[0], "kept workspace "+filepath.Base(path))
		assert.DirExists(t, path)
		assert.Empty(t, e.calls(t))
	})
	t.Run("end to end: a failing claude keeps agent workspaces", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		e.start(t, "fix-login", "aaaa1111")
		e.setAgents(t, "not json")
		require.Equal(t, exitOK, e.run(t, "--json", "list"))
		var got listJSON
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &got))
		require.Len(t, got.Workspaces, 1)
		assert.Equal(t, "unknown", string(got.Workspaces[0].Session.State))
		assert.Len(t, got.Warnings, 1)
	})
}

func TestFilterEntries(t *testing.T) {
	a := lifetime.Entry{Workspace: listedWorkspace("a")}
	b := lifetime.Entry{Workspace: workspace.Workspace{ID: "b"}}
	t.Run("no filter keeps everything", func(t *testing.T) {
		assert.Equal(t, []lifetime.Entry{a, b}, filterEntries([]lifetime.Entry{a, b}, nil))
	})
	t.Run("keeps workspaces with the repo", func(t *testing.T) {
		sp, err := repo.ParseSpec("buff-bear")
		require.NoError(t, err)
		assert.Equal(t, []lifetime.Entry{a}, filterEntries([]lifetime.Entry{a, b}, &sp))
	})
}

func TestHasRepo(t *testing.T) {
	ws := listedWorkspace("a")
	has := func(t *testing.T, spec string) bool {
		t.Helper()
		sp, err := repo.ParseSpec(spec)
		require.NoError(t, err)
		return hasRepo(&ws, &sp)
	}
	t.Run("by name", func(t *testing.T) {
		assert.True(t, has(t, "buff-bear"))
	})
	t.Run("at the ref checked out", func(t *testing.T) {
		assert.True(t, has(t, "buff-bear@main"))
	})
	t.Run("at another ref", func(t *testing.T) {
		assert.False(t, has(t, "buff-bear@v1.2.0"))
	})
	t.Run("another repo", func(t *testing.T) {
		assert.False(t, has(t, "api"))
	})
}

func TestRepoMatches(t *testing.T) {
	const id = "github.com/bobcob7/buff-bear"
	matches := func(t *testing.T, spec string) bool {
		t.Helper()
		sp, err := repo.ParseSpec(spec)
		require.NoError(t, err)
		return repoMatches(id, &sp)
	}
	t.Run("a name matches any owner", func(t *testing.T) {
		assert.True(t, matches(t, "buff-bear"))
	})
	t.Run("a name doesn't match a longer one", func(t *testing.T) {
		assert.False(t, matches(t, "bear"))
	})
	t.Run("owner/name", func(t *testing.T) {
		assert.True(t, matches(t, "bobcob7/buff-bear"))
	})
	t.Run("another owner", func(t *testing.T) {
		assert.False(t, matches(t, "someone/buff-bear"))
	})
	t.Run("a full id", func(t *testing.T) {
		assert.True(t, matches(t, "github.com/bobcob7/buff-bear"))
	})
	t.Run("another host", func(t *testing.T) {
		assert.False(t, matches(t, "gitlab.com/bobcob7/buff-bear"))
	})
	t.Run("a URL", func(t *testing.T) {
		assert.True(t, matches(t, "git@github.com:bobcob7/buff-bear.git"))
	})
}

func TestApp_listWorkspaces(t *testing.T) {
	t.Run("never null", func(t *testing.T) {
		app, _, _ := newTestApp()
		assert.Equal(t, []listWorkspaceJSON{}, app.listWorkspaces(nil, nil))
	})
	t.Run("workspaces, then unknown directories", func(t *testing.T) {
		app, _, _ := newTestApp()
		got := app.listWorkspaces([]lifetime.Entry{{Workspace: listedWorkspace("a")}}, []string{"/cfg/workspaces/stray"})
		require.Len(t, got, 2)
		assert.Equal(t, "a", *got[0].ID)
		assert.Nil(t, got[0].Review)
		assert.Equal(t, listWorkspaceJSON{Path: "/cfg/workspaces/stray", Repos: []listRepoJSON{}}, got[1])
	})
	t.Run("a review workspace has its record", func(t *testing.T) {
		app, _, _ := newTestApp()
		app.paths = config.NewPaths(t.TempDir())
		rec := testRecord()
		require.NoError(t, os.MkdirAll(filepath.Join(app.paths.State, "a"), 0o750))
		require.NoError(t, record.Write(record.Path(app.paths.State, "a"), &rec))
		got := app.listWorkspaces([]lifetime.Entry{{Workspace: listedWorkspace("a")}}, nil)
		require.NotNil(t, got[0].Review)
		assert.Equal(t, rec.Repo, got[0].Review.Repo)
		assert.Equal(t, 1, got[0].Review.Version)
	})
	t.Run("an unreadable record is a warning", func(t *testing.T) {
		app, _, _ := newTestApp()
		app.paths = config.NewPaths(t.TempDir())
		require.NoError(t, os.MkdirAll(filepath.Join(app.paths.State, "a"), 0o750))
		require.NoError(t, os.WriteFile(record.Path(app.paths.State, "a"), []byte("version = 99\n"), 0o600))
		got := app.listWorkspaces([]lifetime.Entry{{Workspace: listedWorkspace("a")}}, nil)
		assert.Nil(t, got[0].Review)
		require.Len(t, app.warned, 1)
		assert.Contains(t, app.warned[0], "review record")
	})
}

func TestListWorkspace(t *testing.T) {
	t.Run("a workspace still being handed over has no session", func(t *testing.T) {
		got := listWorkspace(&lifetime.Entry{Workspace: listedWorkspace("a")})
		assert.Nil(t, got.Session)
		assert.Equal(t, "2026-10-19T10:00:00Z", *got.CreatedAt)
	})
	t.Run("no repos is an empty list", func(t *testing.T) {
		got := listWorkspace(&lifetime.Entry{Workspace: workspace.Workspace{ID: "a"}})
		assert.Equal(t, []listRepoJSON{}, got.Repos)
	})
}

func TestListSession(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		assert.Nil(t, listSession(nil))
	})
	t.Run("an agent with a process", func(t *testing.T) {
		got := listSession(&lifetime.Session{Kind: lifetime.KindAgent, ID: "e8cdf5d8", State: lifetime.StateWorking, PID: 4242, Stale: new(true)})
		data, err := json.Marshal(got)
		require.NoError(t, err)
		assert.JSONEq(t, `{"kind":"agent","id":"e8cdf5d8","state":"working","pid":4242,"stale":true}`, string(data))
	})
	t.Run("a shell", func(t *testing.T) {
		got := listSession(&lifetime.Session{Kind: lifetime.KindShell, State: lifetime.StateRunning})
		data, err := json.Marshal(got)
		require.NoError(t, err)
		assert.JSONEq(t, `{"kind":"shell","id":null,"state":"running","pid":null,"stale":null}`, string(data))
	})
}

func TestApp_printList(t *testing.T) {
	t.Run("just the header with nothing to list", func(t *testing.T) {
		e := newListApp(nil, nil)
		e.app.printList(nil, nil)
		assert.Equal(t, "ID   NAME   CREATED   SESSION   REPOS\n", e.stdout.String())
	})
}

func TestSessionText(t *testing.T) {
	t.Run("not recorded yet", func(t *testing.T) {
		assert.Equal(t, "-", sessionText(nil))
	})
	t.Run("a running shell", func(t *testing.T) {
		assert.Equal(t, "shell", sessionText(&lifetime.Session{Kind: lifetime.KindShell, State: lifetime.StateRunning}))
	})
	t.Run("a shell that can't be read", func(t *testing.T) {
		assert.Equal(t, "shell: unknown", sessionText(&lifetime.Session{Kind: lifetime.KindShell, State: lifetime.StateUnknown}))
	})
	t.Run("an agent", func(t *testing.T) {
		assert.Equal(t, "agent e8cdf5d8: idle", sessionText(&lifetime.Session{Kind: lifetime.KindAgent, ID: "e8cdf5d8", State: lifetime.StateIdle, Stale: new(false)}))
	})
	t.Run("a stale agent", func(t *testing.T) {
		assert.Equal(t, "agent e8cdf5d8: working, stale", sessionText(&lifetime.Session{Kind: lifetime.KindAgent, ID: "e8cdf5d8", State: lifetime.StateWorking, Stale: new(true)}))
	})
}

func TestReposText(t *testing.T) {
	t.Run("each repo's directory and ref", func(t *testing.T) {
		assert.Equal(t, "buff-bear@main, api@v2.1.0", reposText([]workspace.Repo{{Path: "buff-bear", Ref: "main"}, {Path: "api", Ref: "v2.1.0"}}))
	})
	t.Run("none", func(t *testing.T) {
		assert.Empty(t, reposText(nil))
	})
}

func TestAgo(t *testing.T) {
	t.Run("seconds", func(t *testing.T) {
		assert.Equal(t, "45s ago", ago(45*time.Second))
	})
	t.Run("minutes", func(t *testing.T) {
		assert.Equal(t, "20m ago", ago(20*time.Minute+30*time.Second))
	})
	t.Run("hours", func(t *testing.T) {
		assert.Equal(t, "47h ago", ago(47*time.Hour))
	})
	t.Run("days", func(t *testing.T) {
		assert.Equal(t, "3d ago", ago(72*time.Hour))
	})
	t.Run("in the future, after the clock went back", func(t *testing.T) {
		assert.Equal(t, "0s ago", ago(-time.Hour))
	})
}
