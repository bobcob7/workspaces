package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// commentDoc is a Markdown artifact whose quote "The cache is shared." starts
// on lines 3 and 7.
const commentDoc = "# Pitch\n\nThe cache is shared.\n\nEach clone reads from it.\n\nThe cache is shared.\n"

// commentedArtifact adds commentDoc as the agent and returns its id.
func commentedArtifact(t *testing.T, e *mockedApp) string {
	t.Helper()
	return machineAddedID(t, e, writeTestFile(t, "pitch.md", []byte(commentDoc)))
}

// errorDetailsOf is the details object of the JSON error on stderr.
func errorDetailsOf(t *testing.T, stderr *bytes.Buffer) map[string]any {
	t.Helper()
	var body struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(stderr.Bytes(), &body), stderr.String())
	return body.Error.Details
}

// threadAnchorOf is the anchor of the thread in the JSON on stdout, or nil.
func threadAnchorOf(t *testing.T, e *mockedApp) map[string]any {
	t.Helper()
	thread := jsonObject(t, jsonObjectOf(t, e.stdout.String())["thread"])
	if thread["anchor"] == nil {
		return nil
	}
	return jsonObject(t, thread["anchor"])
}

func TestApp_machineArtifactComment(t *testing.T) {
	t.Run("a whole-artifact thread has no anchor, and the agent is its author", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := commentedArtifact(t, e)
		require.Equal(t, exitOK, machineCall(t, e, "comment", "add", id, "--body", "Looks good"), e.stderr.String())
		out := jsonObjectOf(t, e.stdout.String())
		assert.Equal(t, id, out["artifact_id"])
		assert.Nil(t, jsonObject(t, out["thread"])["anchor"])
		assert.Equal(t, "t1", jsonObject(t, out["thread"])["id"])
		assert.Equal(t, map[string]any{"kind": "agent", "workspace": "plan", "name": "planner"}, jsonObject(t, jsonObject(t, out["comment"])["author"]))
	})
	t.Run("a quote anchors a thread to its line", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := commentedArtifact(t, e)
		require.Equal(t, exitOK, machineCall(t, e, "comment", "add", id, "--quote", "Each clone reads", "--body", "Which clone?"), e.stderr.String())
		anchor := threadAnchorOf(t, e)
		require.NotNil(t, anchor)
		assert.Equal(t, "Each clone reads", anchor["quote"])
		assert.EqualValues(t, 5, anchor["line_start"])
		assert.Equal(t, "anchored", anchor["state"])
	})
	t.Run("a quote found twice is anchor_not_found, naming its lines; --line picks one", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := commentedArtifact(t, e)
		code := machineCall(t, e, "comment", "add", id, "--quote", "The cache is shared.", "--body", "Which one?")
		require.Equal(t, exitNotFound, code)
		assert.Equal(t, string(codeAnchorNotFound), errorCodeOf(t, e.stderr))
		details := errorDetailsOf(t, e.stderr)
		assert.EqualValues(t, 2, details["occurrences"])
		assert.Equal(t, []any{3.0, 7.0}, details["lines"])
		require.Equal(t, exitOK, machineCall(t, e, "comment", "add", id, "--quote", "The cache is shared.", "--line", "7", "--body", "This one"), e.stderr.String())
		assert.EqualValues(t, 7, threadAnchorOf(t, e)["line_start"])
	})
	t.Run("--reply adds comment c2 to thread t1", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := commentedArtifact(t, e)
		require.Equal(t, exitOK, machineCall(t, e, "comment", "add", id, "--body", "Looks good"), e.stderr.String())
		require.Equal(t, exitOK, machineCall(t, e, "comment", "add", id, "--reply", "t1", "--body", "Done in r2"), e.stderr.String())
		out := jsonObjectOf(t, e.stdout.String())
		assert.Equal(t, "c2", jsonObject(t, out["comment"])["id"])
		assert.Equal(t, "t1", jsonObject(t, out["thread"])["id"])
		assert.Len(t, jsonObject(t, out["thread"])["comments"], 2)
	})
	t.Run("--reply with --quote is usage", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := commentedArtifact(t, e)
		assert.Equal(t, exitUsage, machineCall(t, e, "comment", "add", id, "--reply", "t1", "--quote", "Each clone", "--body", "x"))
		assert.Equal(t, string(codeUsage), errorCodeOf(t, e.stderr))
	})
	t.Run("--line without --quote is usage", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := commentedArtifact(t, e)
		assert.Equal(t, exitUsage, machineCall(t, e, "comment", "add", id, "--line", "5", "--body", "x"))
		assert.Equal(t, string(codeUsage), errorCodeOf(t, e.stderr))
	})
	t.Run("resolve hides a thread from --open, and --reopen shows it again", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := commentedArtifact(t, e)
		require.Equal(t, exitOK, machineCall(t, e, "comment", "add", id, "--body", "Looks good"), e.stderr.String())
		require.Equal(t, exitOK, machineCall(t, e, "comment", "resolve", id, "t1"), e.stderr.String())
		require.Equal(t, exitOK, machineCall(t, e, "comment", "list", id, "--open"), e.stderr.String())
		assert.Empty(t, jsonObjectOf(t, e.stdout.String())["threads"])
		require.Equal(t, exitOK, machineCall(t, e, "comment", "resolve", id, "t1", "--reopen"), e.stderr.String())
		require.Equal(t, exitOK, machineCall(t, e, "comment", "list", id, "--open"), e.stderr.String())
		assert.Len(t, jsonObjectOf(t, e.stdout.String())["threads"], 1)
	})
	t.Run("a quote on an image is artifact_invalid", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := machineAddedID(t, e, writePNGFile(t))
		assert.Equal(t, exitUsage, machineCall(t, e, "comment", "add", id, "--quote", "x", "--body", "y"))
		assert.Equal(t, string(codeArtifactInvalid), errorCodeOf(t, e.stderr))
	})
	t.Run("put moves one anchor and detaches the one whose quote is gone", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := machineAddedID(t, e, writeTestFile(t, "pitch.md", []byte("# Pitch\n\nThe cache is shared.\n\nEach clone reads from it.\n")))
		require.Equal(t, exitOK, machineCall(t, e, "comment", "add", id, "--quote", "The cache is shared.", "--body", "Keep?"), e.stderr.String())
		require.Equal(t, exitOK, machineCall(t, e, "comment", "add", id, "--quote", "Each clone reads from it.", "--body", "Cut?"), e.stderr.String())
		next := writeTestFile(t, "pitch2.md", []byte("# Pitch\n\nThe cache is shared.\n\nClones read it.\n"))
		require.Equal(t, exitOK, machineCall(t, e, "put", id, next, "--base-rev", "1"), e.stderr.String())
		assert.Equal(t, map[string]any{"anchored": 1.0, "detached": 1.0}, jsonObjectOf(t, e.stdout.String())["anchors"])
	})
}

func TestApp_artifactComment(t *testing.T) {
	t.Run("a person's anchored thread is theirs, on the line of its quote", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := commentedArtifact(t, e)
		e.stdout.Reset()
		require.Equal(t, exitOK, runMachine(t, e, "artifact", "comment", "add", id, "--quote", "Each clone reads", "--prefix", "", "--suffix", "", "--body", "Which clone?", "--json"), e.stderr.String())
		out := jsonObjectOf(t, e.stdout.String())
		assert.Equal(t, map[string]any{"kind": "person", "workspace": nil, "name": nil}, jsonObject(t, jsonObject(t, out["comment"])["author"]))
		assert.EqualValues(t, 5, threadAnchorOf(t, e)["line_start"])
	})
	t.Run("the people's list shows the open threads", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := commentedArtifact(t, e)
		require.Equal(t, exitOK, machineCall(t, e, "comment", "add", id, "--body", "Looks good"), e.stderr.String())
		e.stdout.Reset()
		require.Equal(t, exitOK, runMachine(t, e, "artifact", "comment", "list", id, "--open", "--json"), e.stderr.String())
		assert.Len(t, jsonObjectOf(t, e.stdout.String())["threads"], 1)
	})
	t.Run("the agent's tree has no --prefix", func(t *testing.T) {
		e, _ := newMachineArtifactApp(t)
		id := commentedArtifact(t, e)
		assert.Equal(t, exitUsage, machineCall(t, e, "comment", "add", id, "--quote", "Each clone reads", "--prefix", "x", "--body", "y"))
	})
}
