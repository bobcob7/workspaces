package cli

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/artifact"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// personAdded adds a text artifact as a person and returns its id.
func personAdded(t *testing.T, e *mockedApp) string {
	t.Helper()
	rec, err := e.app.artifactStore().Add(t.Context(), &artifact.NewArtifact{Name: "a.md", Data: []byte("# A\n"), By: artifact.Person()})
	require.NoError(t, err)
	return rec.ID
}

// conversationsOf is the artifact's conversations, from ws artifact show.
func conversationsOf(t *testing.T, e *mockedApp, id string) []any {
	t.Helper()
	e.stdout.Reset()
	require.Equal(t, exitOK, runMachine(t, e, "artifact", "show", id, "--json"), e.stderr.String())
	rows, ok := jsonObject(t, jsonObjectOf(t, e.stdout.String())["artifact"])["conversations"].([]any)
	require.True(t, ok, e.stdout.String())
	return rows
}

// breakLinks makes every link write to id fail, with a links.json from a
// newer ws.
func breakLinks(t *testing.T, e *mockedApp, id string) {
	t.Helper()
	path := filepath.Join(e.app.paths.Artifacts, id, "links.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"version":99,"links":[]}`), 0o600))
}

// warningsOf is the warnings array of the JSON object s.
func warningsOf(t *testing.T, s string) []any {
	t.Helper()
	w, ok := jsonObjectOf(t, s)["warnings"].([]any)
	require.True(t, ok, s)
	return w
}

func TestApp_machineArtifactGet_recordsRead(t *testing.T) {
	t.Run("records read with the revision, and show lists the conversation", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := personAdded(t, e)
		require.Equal(t, exitOK, machineCall(t, e, "get", id), e.stderr.String())
		require.Equal(t, exitOK, machineCall(t, e, "get", id), e.stderr.String())
		rows := conversationsOf(t, e, id)
		require.Len(t, rows, 1)
		row := jsonObject(t, rows[0])
		assert.Equal(t, "plan", row["workspace"])
		assert.Equal(t, "planner", row["name"])
		assert.Equal(t, []any{"read"}, row["actions"])
		assert.Equal(t, []any{float64(1)}, row["revs"])
		assert.Equal(t, false, row["removed"])
		assert.Nil(t, row["environment"])
		links, err := os.ReadFile(filepath.Join(e.app.paths.Artifacts, id, "links.json"))
		require.NoError(t, err)
		assert.Contains(t, string(links), `"count":2`)
	})
	t.Run("summaries count conversations", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := personAdded(t, e)
		require.Equal(t, exitOK, machineCall(t, e, "get", id), e.stderr.String())
		require.Equal(t, exitOK, machineCall(t, e, "list"), e.stderr.String())
		summary := jsonObject(t, artifactsOf(t, e.stdout.String())[0])
		assert.InDelta(t, 1, summary["conversation_count"], 0)
		assert.Nil(t, summary["conversation"])
	})
	t.Run("a workspace ws no longer lists is removed", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := personAdded(t, e)
		require.Equal(t, exitOK, machineCall(t, e, "get", id), e.stderr.String())
		e.store.ListFunc = func() ([]workspace.Workspace, error) { return nil, nil }
		assert.Equal(t, true, jsonObject(t, conversationsOf(t, e, id)[0])["removed"])
	})
}

func TestApp_linkFailureIsAWarning(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, e *mockedApp, id string) int
	}{
		{name: "machine artifact get", run: func(t *testing.T, e *mockedApp, id string) int {
			t.Helper()
			return machineCall(t, e, "get", id)
		}},
		{name: "artifact cite", run: func(t *testing.T, e *mockedApp, id string) int {
			t.Helper()
			return runMachine(t, e, "artifact", "cite", "@artifact:"+id, "--workspace", "plan", "--json")
		}},
		{name: "artifact scan record", run: func(t *testing.T, e *mockedApp, id string) int {
			t.Helper()
			e.app.stdin = strings.NewReader(`{"version":1,"workspace":"plan","transcript":"/t","offset":10,"citations":[{"key":"m1","refs":["@artifact:` + id + `"]}]}`)
			return runMachine(t, e, "artifact", "scan", "record", "-", "--json")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _ := newMachineArtifactApp(t)
			writeWorkspaceRecord(t, e)
			id := personAdded(t, e)
			breakLinks(t, e, id)
			e.stdout.Reset()
			require.Equal(t, exitOK, tt.run(t, e, id), e.stderr.String())
			assert.NotEmpty(t, warningsOf(t, e.stdout.String()))
		})
	}
}

// writeWorkspaceRecord makes state/plan/workspace.toml, which scan record
// requires.
func writeWorkspaceRecord(t *testing.T, e *mockedApp) {
	t.Helper()
	dir := filepath.Join(e.app.paths.State, "plan")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "workspace.toml"), nil, 0o600))
}

func TestApp_claude_recordsCited(t *testing.T) {
	t.Run("ws claude records cited for the new workspace", func(t *testing.T) {
		e, id := hintEnv(t)
		require.NoError(t, e.run(t, "fix it @artifact:"+id+"/r1 and @artifact:"+id))
		entry, err := e.app.artifactStore().Show(id)
		require.NoError(t, err)
		require.Len(t, entry.Conversations, 1)
		assert.Equal(t, "x", entry.Conversations[0].Workspace)
		assert.Equal(t, []string{artifact.ActionCited}, entry.Conversations[0].Actions)
		assert.Equal(t, []int{1}, entry.Conversations[0].Revs)
	})
	t.Run("a link failure is a warning and the agent still starts", func(t *testing.T) {
		e, id := hintEnv(t)
		path := filepath.Join(e.app.paths.Artifacts, id, "links.json")
		require.NoError(t, os.WriteFile(path, []byte(`{"version":99}`), 0o600))
		require.NoError(t, e.run(t, "fix it @artifact:"+id))
		require.Len(t, e.claude.StartCalls(), 1)
		require.Len(t, e.app.warned, 1)
		assert.Contains(t, e.app.warned[0], "link not recorded")
	})
	t.Run("a workspace on an environment is recorded with it", func(t *testing.T) {
		e, id := hintEnv(t)
		e.app.recordRemoteCited(t.Context(), []byte(`{"id":"far-1","name":"far"}`), "win-rig", []artifact.Ref{{ID: id}})
		entry, err := e.app.artifactStore().Show(id)
		require.NoError(t, err)
		require.Len(t, entry.Conversations, 1)
		require.NotNil(t, entry.Conversations[0].Environment)
		assert.Equal(t, "win-rig", *entry.Conversations[0].Environment)
	})
}

func TestApp_runMachineSpawn_recordsCited(t *testing.T) {
	e := roleSpawnEnv(t)
	e.app.now = testNow
	rec, err := e.app.artifactStore().Add(t.Context(), &artifact.NewArtifact{Name: "login.txt", Data: []byte("hi"), By: artifact.Person()})
	require.NoError(t, err)
	opts := roleSpawnOptions()
	opts.vars = []string{"TASK=fix @artifact:" + rec.ID, "TASKS=t.md", "BASE=feat/x", "BRANCH=feat/x-t1"}
	require.NoError(t, e.app.runMachineSpawn(t.Context(), opts))
	entry, err := e.app.artifactStore().Show(rec.ID)
	require.NoError(t, err)
	require.Len(t, entry.Conversations, 1)
	assert.Equal(t, []string{artifact.ActionCited}, entry.Conversations[0].Actions)
}

func TestApp_artifactCite(t *testing.T) {
	t.Run("records each reference once, with its revision", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := personAdded(t, e)
		require.Equal(t, exitOK, runMachine(t, e, "artifact", "cite", "@artifact:"+id+"/r1", id, id, "--workspace", "plan", "--json"), e.stderr.String())
		out := jsonObjectOf(t, e.stdout.String())
		assert.Equal(t, "plan", out["workspace"])
		assert.Equal(t, []any{map[string]any{"id": id, "rev": float64(1)}, map[string]any{"id": id, "rev": nil}}, out["recorded"])
		row := jsonObject(t, conversationsOf(t, e, id)[0])
		assert.Equal(t, []any{"cited"}, row["actions"])
	})
	t.Run("an unknown artifact is artifact_unknown and records nothing", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := personAdded(t, e)
		assert.Equal(t, exitNotFound, runMachine(t, e, "artifact", "cite", id, "art-00000000", "--workspace", "plan", "--json"))
		assert.Equal(t, string(codeArtifactUnknown), errorCodeOf(t, e.stderr))
		assert.Empty(t, conversationsOf(t, e, id))
	})
	t.Run("a bad workspace id is usage", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := personAdded(t, e)
		assert.Equal(t, exitUsage, runMachine(t, e, "artifact", "cite", id, "--workspace", "../x", "--json"))
		assert.Equal(t, string(codeUsage), errorCodeOf(t, e.stderr))
	})
	t.Run("a workspace ws doesn't list is workspace_not_found", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := personAdded(t, e)
		assert.Equal(t, exitNotFound, runMachine(t, e, "artifact", "cite", id, "--workspace", "gone", "--json"))
		assert.Equal(t, string(codeWorkspaceNotFound), errorCodeOf(t, e.stderr))
	})
}

// scanInput is a scan record input for workspace plan.
func scanInput(t *testing.T, citations ...map[string]any) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"version": 1, "workspace": "plan", "transcript": "/t.jsonl", "offset": 42, "citations": citations})
	require.NoError(t, err)
	return string(data)
}

func scanRecord(t *testing.T, e *mockedApp, input string) int {
	t.Helper()
	e.stdout.Reset()
	e.stderr.Reset()
	e.app.stdin = strings.NewReader(input)
	return runMachine(t, e, "artifact", "scan", "record", "-", "--json")
}

func TestApp_artifactScanRecord(t *testing.T) {
	t.Run("records once however often the same scan comes", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		writeWorkspaceRecord(t, e)
		id := personAdded(t, e)
		input := scanInput(t, map[string]any{"key": "m1", "at": "2026-10-01T00:00:00Z", "refs": []string{"@artifact:" + id, "@artifact:art-00000000"}})
		require.Equal(t, exitOK, scanRecord(t, e, input), e.stderr.String())
		out := jsonObjectOf(t, e.stdout.String())
		assert.InDelta(t, 1, out["recorded"], 0)
		assert.InDelta(t, 1, out["skipped"], 0)
		assert.InDelta(t, 42, out["offset"], 0)
		require.Equal(t, exitOK, scanRecord(t, e, input), e.stderr.String())
		assert.InDelta(t, 0, jsonObjectOf(t, e.stdout.String())["recorded"], 0)
		row := jsonObject(t, conversationsOf(t, e, id)[0])
		assert.Equal(t, "2026-10-01T00:00:00Z", row["first_at"])
	})
	t.Run("scan cursors lists where it stopped", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		writeWorkspaceRecord(t, e)
		require.Equal(t, exitOK, scanRecord(t, e, scanInput(t)), e.stderr.String())
		e.stdout.Reset()
		require.Equal(t, exitOK, runMachine(t, e, "artifact", "scan", "cursors", "--json"), e.stderr.String())
		cursors, ok := jsonObjectOf(t, e.stdout.String())["cursors"].([]any)
		require.True(t, ok)
		require.Len(t, cursors, 1)
		assert.Equal(t, "/t.jsonl", jsonObject(t, cursors[0])["transcript"])
	})
	t.Run("a workspace with no record is workspace_not_found", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		assert.Equal(t, exitNotFound, scanRecord(t, e, scanInput(t)))
		assert.Equal(t, string(codeWorkspaceNotFound), errorCodeOf(t, e.stderr))
	})
	usage := []struct {
		name  string
		input string
	}{
		{name: "not JSON", input: "{"},
		{name: "version 2", input: `{"version":2,"workspace":"plan"}`},
		{name: "no version", input: `{"workspace":"plan"}`},
		{name: "a parent path", input: `{"version":1,"workspace":"../plan"}`},
		{name: "a 129-byte key", input: `{"version":1,"workspace":"plan","citations":[{"key":"` + strings.Repeat("k", 129) + `","refs":[]}]}`},
		{name: "a reference that doesn't parse", input: `{"version":1,"workspace":"plan","citations":[{"key":"m1","refs":["art-zz"]}]}`},
		{name: "over 1 MiB", input: `{"version":1,"workspace":"plan","transcript":"` + strings.Repeat("x", maxScanInput) + `"}`},
	}
	for _, tt := range usage {
		t.Run(tt.name+" is usage", func(t *testing.T) {
			e, _ := newMachineArtifactApp(t)
			writeWorkspaceRecord(t, e)
			assert.Equal(t, exitUsage, scanRecord(t, e, tt.input))
			assert.Equal(t, string(codeUsage), errorCodeOf(t, e.stderr))
		})
	}
	t.Run("a 128-byte key and 1000 citations pass", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		writeWorkspaceRecord(t, e)
		citations := make([]map[string]any, artifact.MaxScanCitations)
		for i := range citations {
			citations[i] = map[string]any{"key": strings.Repeat("k", 120) + strings.Repeat("0", 8-len(strconv.Itoa(i))) + strconv.Itoa(i), "refs": []string{}}
		}
		require.Equal(t, exitOK, scanRecord(t, e, scanInput(t, citations...)), e.stderr.String())
		over := slices.Concat(citations, []map[string]any{{"key": "one-more", "refs": []string{}}})
		assert.Equal(t, exitUsage, scanRecord(t, e, scanInput(t, over...)))
	})
}

func TestApp_artifactScanRecord_fixture(t *testing.T) {
	e, _ := newMachineArtifactApp(t)
	writeWorkspaceRecord(t, e)
	data, err := os.ReadFile(filepath.Join("testdata", "scan-record.json"))
	require.NoError(t, err)
	require.Equal(t, exitOK, scanRecord(t, e, strings.ReplaceAll(string(data), "fix-login-d8efb24f", "plan")), e.stderr.String())
	assert.InDelta(t, 2, jsonObjectOf(t, e.stdout.String())["skipped"], 0)
}

func TestApp_artifactList_conversation(t *testing.T) {
	t.Run("keeps the workspace's artifacts, latest first, with its row", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		first := personAdded(t, e)
		second := personAdded(t, e)
		personAdded(t, e)
		require.Equal(t, exitOK, runMachine(t, e, "artifact", "cite", second, "--workspace", "plan", "--json"), e.stderr.String())
		e.app.now = func() time.Time { return testNow().Add(time.Minute) }
		require.Equal(t, exitOK, machineCall(t, e, "get", first), e.stderr.String())
		e.stdout.Reset()
		require.Equal(t, exitOK, runMachine(t, e, "artifact", "list", "--conversation", "plan", "--json"), e.stderr.String())
		list := artifactsOf(t, e.stdout.String())
		require.Len(t, list, 2)
		assert.Equal(t, first, jsonObject(t, list[0])["id"])
		assert.Equal(t, second, jsonObject(t, list[1])["id"])
		row := jsonObject(t, jsonObject(t, list[0])["conversation"])
		assert.Equal(t, []any{"read"}, row["actions"])
	})
	t.Run("a bad id is usage", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		assert.Equal(t, exitUsage, runMachine(t, e, "artifact", "list", "--conversation", "", "--json"))
	})
}
