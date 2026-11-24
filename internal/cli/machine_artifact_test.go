package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/workspace"
)

// newMachineArtifactApp is a test App run inside workspace plan, which
// lives in a temp dir under boss, so get can write into it. It returns the
// app and the workspace's directory.
func newMachineArtifactApp(t *testing.T) (*mockedApp, string) {
	t.Helper()
	e := newArtifactApp(t)
	dir := t.TempDir()
	tree := []workspace.Workspace{
		{ID: "boss", Path: filepath.Join(dir, "boss")},
		{ID: "plan", Name: "planner", Path: dir, Parent: "boss"},
	}
	e.store.ListFunc = func() ([]workspace.Workspace, error) { return tree, nil }
	e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return tree[1], nil }
	e.app.getwd = func() (string, error) { return dir, nil }
	return e, dir
}

// machineCall runs ws machine artifact args --json and returns its exit code.
func machineCall(t *testing.T, e *mockedApp, args ...string) int {
	t.Helper()
	e.stdout.Reset()
	e.stderr.Reset()
	return runMachine(t, e, append(append([]string{"machine", "artifact"}, args...), "--json")...)
}

// machineAddedID adds the file at path as the caller and returns the id.
func machineAddedID(t *testing.T, e *mockedApp, path string) string {
	t.Helper()
	require.Equal(t, exitOK, machineCall(t, e, "add", path), e.stderr.String())
	return jsonString(t, jsonObject(t, jsonObjectOf(t, e.stdout.String())["artifact"])["id"])
}

func TestApp_machineArtifactAdd(t *testing.T) {
	t.Run("adds a file as the caller, in its tree", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		path := writeTestFile(t, "pitch.md", []byte("# Pitch\n"))
		require.Equal(t, exitOK, machineCall(t, e, "add", path, "--title", "Pitch: cache sharing"), e.stderr.String())
		art := jsonObject(t, jsonObjectOf(t, e.stdout.String())["artifact"])
		assert.Equal(t, "markdown", art["kind"])
		assert.Equal(t, "Pitch: cache sharing", art["title"])
		assert.Equal(t, map[string]any{"kind": "agent", "workspace": "plan", "name": "planner"}, jsonObject(t, art["created_by"]))
	})
	t.Run("reads stdin under --name", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		e.app.stdin = strings.NewReader("hi")
		require.Equal(t, exitOK, machineCall(t, e, "add", "-", "--name", "a.txt"), e.stderr.String())
		assert.Equal(t, "a.txt", jsonObject(t, jsonObjectOf(t, e.stdout.String())["artifact"])["name"])
	})
	t.Run("stdin without --name is usage", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		e.app.stdin = strings.NewReader("hi")
		assert.Equal(t, exitUsage, machineCall(t, e, "add", "-"))
		assert.Equal(t, string(codeUsage), errorCodeOf(t, e.stderr))
	})
	t.Run("a binary file is artifact_invalid, exit 2", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		path := writeTestFile(t, "tool.exe", []byte{0x7f, 'E', 'L', 'F', 0, 1, 2})
		assert.Equal(t, exitUsage, machineCall(t, e, "add", path))
		assert.Equal(t, string(codeArtifactInvalid), errorCodeOf(t, e.stderr))
	})
}

func TestApp_machineArtifactList(t *testing.T) {
	t.Run("lists every artifact", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := machineAddedID(t, e, writeTestFile(t, "a.txt", []byte("hi")))
		require.Equal(t, exitOK, machineCall(t, e, "list"), e.stderr.String())
		list := artifactsOf(t, e.stdout.String())
		require.Len(t, list, 1)
		assert.Equal(t, id, jsonObject(t, list[0])["id"])
	})
	t.Run("--mine hides the artifacts people added", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		person := writeTestFile(t, "person.txt", []byte("hi"))
		require.Equal(t, exitOK, runMachine(t, e, "artifact", "add", person, "--json"), e.stderr.String())
		own := machineAddedID(t, e, writeTestFile(t, "own.txt", []byte("hi")))
		require.Equal(t, exitOK, machineCall(t, e, "list", "--mine"), e.stderr.String())
		list := artifactsOf(t, e.stdout.String())
		require.Len(t, list, 1)
		assert.Equal(t, own, jsonObject(t, list[0])["id"])
	})
}

func TestApp_machineArtifactShow(t *testing.T) {
	t.Run("accepts a reference with a revision", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := machineAddedID(t, e, writeTestFile(t, "a.txt", []byte("hi")))
		assert.Equal(t, exitOK, machineCall(t, e, "show", "@artifact:"+id+"/r1"), e.stderr.String())
	})
	t.Run("a reference's revision and a different --rev is usage", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := machineAddedID(t, e, writeTestFile(t, "a.txt", []byte("hi")))
		assert.Equal(t, exitUsage, machineCall(t, e, "get", "@artifact:"+id+"/r1", "--rev", "2"))
	})
	t.Run("an unknown id is artifact_unknown, exit 3", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		assert.Equal(t, exitNotFound, machineCall(t, e, "show", "art-1a2b3c4d"))
		assert.Equal(t, string(codeArtifactUnknown), errorCodeOf(t, e.stderr))
	})
}

func TestApp_machineArtifactGet(t *testing.T) {
	t.Run("writes the revision into the workspace and prints its path", func(t *testing.T) {
		e, dir := newMachineArtifactApp(t)
		id := machineAddedID(t, e, writeTestFile(t, "a.txt", []byte("hi")))
		require.Equal(t, exitOK, machineCall(t, e, "get", id), e.stderr.String())
		want := filepath.Join(dir, ".ws-artifacts", id, "r1", "a.txt")
		assert.Equal(t, want, jsonObjectOf(t, e.stdout.String())["path"])
		got, err := os.ReadFile(want)
		require.NoError(t, err)
		assert.Equal(t, "hi", string(got))
	})
	t.Run("--out writes into that directory", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := machineAddedID(t, e, writeTestFile(t, "a.txt", []byte("hi")))
		out := t.TempDir()
		require.Equal(t, exitOK, machineCall(t, e, "get", id, "--out", out), e.stderr.String())
		assert.Equal(t, filepath.Join(out, "a.txt"), jsonObjectOf(t, e.stdout.String())["path"])
		_, err := os.Stat(filepath.Join(out, "a.txt"))
		require.NoError(t, err)
	})
}

func TestApp_machineArtifactPut(t *testing.T) {
	t.Run("a stale --base-rev is artifact_conflict with the latest revision", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := machineAddedID(t, e, writeTestFile(t, "a.txt", []byte("one")))
		next := writeTestFile(t, "a.txt", []byte("two"))
		require.Equal(t, exitOK, machineCall(t, e, "put", id, next, "--base-rev", "1"), e.stderr.String())
		assert.Equal(t, exitRefused, machineCall(t, e, "put", id, next, "--base-rev", "1"))
		body := jsonObject(t, jsonObjectOf(t, e.stderr.String())["error"])
		assert.Equal(t, string(codeArtifactConflict), body["code"])
		assert.Equal(t, map[string]any{"latest": 2.0}, body["details"])
	})
	t.Run("a put without --base-rev appends a revision", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := machineAddedID(t, e, writeTestFile(t, "a.txt", []byte("one")))
		next := writeTestFile(t, "a.txt", []byte("two"))
		require.Equal(t, exitOK, machineCall(t, e, "put", id, next), e.stderr.String())
		art := jsonObject(t, jsonObjectOf(t, e.stdout.String())["artifact"])
		assert.InDelta(t, 2, art["latest"], 0)
		assert.Equal(t, map[string]any{"anchored": 0.0, "detached": 0.0}, jsonObjectOf(t, e.stdout.String())["anchors"])
	})
}

func TestApp_machineArtifactRm(t *testing.T) {
	t.Run("removes an artifact the caller's tree created", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := machineAddedID(t, e, writeTestFile(t, "a.txt", []byte("hi")))
		require.Equal(t, exitOK, machineCall(t, e, "rm", id), e.stderr.String())
		assert.Equal(t, map[string]any{"id": id, "removed": true, "error": nil}, artifactsOf(t, e.stdout.String())[0])
	})
	t.Run("a person's artifact is artifact_forbidden, exit 4", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		person := writeTestFile(t, "person.txt", []byte("hi"))
		require.Equal(t, exitOK, runMachine(t, e, "artifact", "add", person, "--json"), e.stderr.String())
		id := jsonString(t, jsonObject(t, jsonObjectOf(t, e.stdout.String())["artifact"])["id"])
		assert.Equal(t, exitRefused, machineCall(t, e, "rm", id))
		item := jsonObject(t, artifactsOf(t, e.stdout.String())[0])
		assert.Equal(t, false, item["removed"])
		assert.Equal(t, string(codeArtifactForbidden), jsonObject(t, item["error"])["code"])
	})
}

func TestApp_machineArtifactOutside(t *testing.T) {
	t.Run("outside a workspace every command is outside_workspace", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{}, workspace.ErrNotFound }
		assert.Equal(t, exitUsage, machineCall(t, e, "list"))
		assert.Equal(t, string(codeOutsideWorkspace), errorCodeOf(t, e.stderr))
	})
	t.Run("on an enrolled environment's agent every command is artifact_unreachable", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		require.NoError(t, os.MkdirAll(e.app.paths.Environment, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(e.app.paths.Environment, "agent.toml"), []byte("x"), 0o600))
		path := writeTestFile(t, "a.txt", []byte("hi"))
		for _, args := range [][]string{{"add", path}, {"list"}, {"show", "art-1a2b3c4d"}, {"get", "art-1a2b3c4d"}, {"put", "art-1a2b3c4d", path}, {"rm", "art-1a2b3c4d"}} {
			assert.Equal(t, exitFailed, machineCall(t, e, args...), args)
			assert.Equal(t, string(codeArtifactUnreachable), errorCodeOf(t, e.stderr), args)
		}
	})
}
