package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/artifact"
	"github.com/bobcob7/workspaces/internal/config"
)

// newArtifactApp is a test App whose config comes from a temp CONFIG_DIR, so
// the artifact store is real.
func newArtifactApp(t *testing.T) *mockedApp {
	t.Helper()
	e := newMockedApp()
	paths := config.NewPaths(t.TempDir())
	e.app.paths = paths
	e.app.configs = &configLoaderMock{
		LoadFunc: func(string) (config.Paths, config.Config, error) { return paths, config.Config{}, nil },
	}
	e.app.now = testNow
	return e
}

// writeTestFile writes data to a file called name in a new temp dir, and
// returns its path.
func writeTestFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

// addedID runs ws artifact add --json on args and returns the new artifact's id.
func addedID(t *testing.T, e *mockedApp, args ...string) string {
	t.Helper()
	require.Equal(t, exitOK, runMachine(t, e, append([]string{"artifact", "add"}, append(args, "--json")...)...), e.stderr.String())
	return jsonObjectOf(t, e.stdout.String())["artifact"].(map[string]any)["id"].(string)
}

func TestApp_artifactAdd(t *testing.T) {
	t.Run("stores a file as a new artifact by a person, and prints it", func(t *testing.T) {
		e := newArtifactApp(t)
		path := writeTestFile(t, "notes.txt", []byte("hi\n"))
		require.Equal(t, exitOK, runMachine(t, e, "artifact", "add", path, "--title", "Notes", "--json"), e.stderr.String())
		art := jsonObject(t, jsonObjectOf(t, e.stdout.String())["artifact"])
		assert.Regexp(t, `^art-[0-9a-f]{8}$`, art["id"])
		assert.Equal(t, "notes.txt", art["name"])
		assert.Equal(t, "Notes", art["title"])
		assert.Equal(t, "text", art["kind"])
		assert.Equal(t, "person", jsonObject(t, art["created_by"])["kind"])
		assert.Nil(t, jsonObject(t, art["created_by"])["workspace"])
	})
	t.Run("prints the id without --json", func(t *testing.T) {
		e := newArtifactApp(t)
		path := writeTestFile(t, "a.txt", []byte("hi"))
		require.Equal(t, exitOK, runMachine(t, e, "artifact", "add", path), e.stderr.String())
		assert.Regexp(t, `^art-[0-9a-f]{8}\n$`, e.stdout.String())
	})
	t.Run("reads stdin under --name", func(t *testing.T) {
		e := newArtifactApp(t)
		e.app.stdin = strings.NewReader("hi")
		require.Equal(t, exitOK, runMachine(t, e, "artifact", "add", "-", "--name", "a.txt", "--json"), e.stderr.String())
		art := jsonObject(t, jsonObjectOf(t, e.stdout.String())["artifact"])
		assert.Equal(t, "a.txt", art["name"])
	})
	t.Run("refuses stdin without --name as usage", func(t *testing.T) {
		e := newArtifactApp(t)
		e.app.stdin = strings.NewReader("hi")
		assert.Equal(t, exitUsage, runMachine(t, e, "artifact", "add", "-", "--json"))
		assert.Equal(t, string(codeUsage), errorCodeOf(t, e.stderr))
	})
	t.Run("refuses a binary file as artifact_invalid", func(t *testing.T) {
		e := newArtifactApp(t)
		path := writeTestFile(t, "tool.exe", []byte{0x7f, 'E', 'L', 'F', 0, 1, 2})
		assert.Equal(t, exitUsage, runMachine(t, e, "artifact", "add", path, "--json"))
		assert.Equal(t, string(codeArtifactInvalid), errorCodeOf(t, e.stderr))
	})
}

func TestApp_artifactList(t *testing.T) {
	t.Run("lists artifacts as lines, and as JSON with --json", func(t *testing.T) {
		e := newArtifactApp(t)
		id := addedID(t, e, writePNGFile(t))
		e.stdout.Reset()
		require.Equal(t, exitOK, runMachine(t, e, "artifact", "list"), e.stderr.String())
		assert.Contains(t, e.stdout.String(), id+"  image ")
		e.stdout.Reset()
		require.Equal(t, exitOK, runMachine(t, e, "artifact", "list", "--json"), e.stderr.String())
		list := jsonObjectOf(t, e.stdout.String())["artifacts"].([]any)
		require.Len(t, list, 1)
		assert.Equal(t, id, jsonObject(t, list[0])["id"])
	})
	t.Run("--workspace keeps those a workspace created or is in the tree of", func(t *testing.T) {
		e := newArtifactApp(t)
		addedID(t, e, writeTestFile(t, "a.txt", []byte("hi")))
		e.stdout.Reset()
		require.Equal(t, exitOK, runMachine(t, e, "artifact", "list", "--workspace", "plan", "--json"), e.stderr.String())
		assert.Empty(t, jsonObjectOf(t, e.stdout.String())["artifacts"])
	})
	t.Run("refuses an unknown kind and a limit out of range as usage", func(t *testing.T) {
		e := newArtifactApp(t)
		assert.Equal(t, exitUsage, runMachine(t, e, "artifact", "list", "--kind", "pdf", "--json"))
		assert.Equal(t, exitUsage, runMachine(t, e, "artifact", "list", "--limit", "0", "--json"))
	})
}

func TestApp_artifactShowAndCat(t *testing.T) {
	t.Run("show prints the artifact with its revisions", func(t *testing.T) {
		e := newArtifactApp(t)
		id := addedID(t, e, writeTestFile(t, "a.txt", []byte("hi")))
		e.stdout.Reset()
		require.Equal(t, exitOK, runMachine(t, e, "artifact", "show", "@artifact:"+id+"/r1", "--json"), e.stderr.String())
		art := jsonObject(t, jsonObjectOf(t, e.stdout.String())["artifact"])
		assert.Equal(t, "@artifact:"+id, art["reference"])
		assert.Len(t, art["revisions"], 1)
	})
	t.Run("cat writes the raw bytes, even with --json", func(t *testing.T) {
		e := newArtifactApp(t)
		id := addedID(t, e, writeTestFile(t, "a.txt", []byte("hi")))
		e.stdout.Reset()
		require.Equal(t, exitOK, runMachine(t, e, "artifact", "cat", id, "--json"), e.stderr.String())
		assert.Equal(t, "hi", e.stdout.String())
	})
	t.Run("an unknown id is artifact_unknown, exit 3", func(t *testing.T) {
		e := newArtifactApp(t)
		assert.Equal(t, exitNotFound, runMachine(t, e, "artifact", "show", "art-1a2b3c4d", "--json"))
		assert.Equal(t, string(codeArtifactUnknown), errorCodeOf(t, e.stderr))
	})
	t.Run("--rev with a reference to another revision is usage", func(t *testing.T) {
		e := newArtifactApp(t)
		id := addedID(t, e, writeTestFile(t, "a.txt", []byte("hi")))
		assert.Equal(t, exitUsage, runMachine(t, e, "artifact", "cat", "@artifact:"+id+"/r1", "--rev", "2", "--json"))
	})
}

func TestApp_artifactRm(t *testing.T) {
	t.Run("removes an artifact, and reports each argument", func(t *testing.T) {
		e := newArtifactApp(t)
		id := addedID(t, e, writeTestFile(t, "a.txt", []byte("hi")))
		e.stdout.Reset()
		require.Equal(t, exitOK, runMachine(t, e, "artifact", "rm", id, "--json"), e.stderr.String())
		items := jsonObjectOf(t, e.stdout.String())["artifacts"].([]any)
		assert.Equal(t, map[string]any{"id": id, "removed": true, "error": nil}, items[0])
		assert.Equal(t, exitNotFound, runMachine(t, e, "artifact", "show", id, "--json"))
	})
	t.Run("an unknown id fails its item and the exit code", func(t *testing.T) {
		e := newArtifactApp(t)
		assert.Equal(t, exitNotFound, runMachine(t, e, "artifact", "rm", "art-1a2b3c4d", "--json"))
		items := jsonObjectOf(t, e.stdout.String())["artifacts"].([]any)
		assert.Equal(t, false, jsonObject(t, items[0])["removed"])
	})
}

func TestArtifactRef(t *testing.T) {
	t.Run("a bare id, or a reference, has no revision unless it names one", func(t *testing.T) {
		id, r, err := artifactRef("art-1a2b3c4d", 0, false)
		require.NoError(t, err)
		assert.Equal(t, "art-1a2b3c4d", id)
		assert.Equal(t, 0, r)
		id, r, err = artifactRef("@artifact:art-1a2b3c4d/r3", 0, false)
		require.NoError(t, err)
		assert.Equal(t, "art-1a2b3c4d", id)
		assert.Equal(t, 3, r)
	})
	t.Run("--rev applies to a bare id", func(t *testing.T) {
		_, r, err := artifactRef("art-1a2b3c4d", 2, true)
		require.NoError(t, err)
		assert.Equal(t, 2, r)
	})
	t.Run("a reference's revision and a different --rev is usage", func(t *testing.T) {
		_, _, err := artifactRef("@artifact:art-1a2b3c4d/r1", 2, true)
		requireUsageError(t, err)
	})
	t.Run("a revision of 0 or less is usage", func(t *testing.T) {
		_, _, err := artifactRef("art-1a2b3c4d", 0, true)
		requireUsageError(t, err)
	})
	t.Run("something that is not an id is artifact_unknown", func(t *testing.T) {
		_, _, err := artifactRef("pitch.md", 0, false)
		requireCode(t, codeArtifactUnknown, err)
	})
}

func TestArtifactError(t *testing.T) {
	t.Run("maps the store's errors to ws codes", func(t *testing.T) {
		assert.NoError(t, artifactError(nil))
		requireCode(t, codeArtifactInvalid, artifactError(artifact.ErrInvalid))
		requireCode(t, codeArtifactInvalid, artifactError(artifact.ErrNewer))
		requireCode(t, codeArtifactUnknown, artifactError(artifact.ErrNotFound))
		requireCode(t, codeArtifactFull, artifactError(artifact.ErrFull))
	})
	t.Run("a conflict carries the latest revision as details", func(t *testing.T) {
		err := artifactError(&artifact.ConflictError{Base: 1, Latest: 2})
		ce, ok := errors.AsType[*cliError](err)
		require.True(t, ok)
		assert.Equal(t, codeArtifactConflict, ce.code)
		assert.Equal(t, 2, ce.details["latest"])
	})
}

func TestApp_readArtifactInput(t *testing.T) {
	t.Run("names a file by its base name, unless --name", func(t *testing.T) {
		e := newArtifactApp(t)
		path := writeTestFile(t, "login.log", []byte("hi"))
		name, data, err := e.app.readArtifactInput(path, "")
		require.NoError(t, err)
		assert.Equal(t, "login.log", name)
		assert.Equal(t, []byte("hi"), data)
		name, _, err = e.app.readArtifactInput(path, "other.log")
		require.NoError(t, err)
		assert.Equal(t, "other.log", name)
	})
	t.Run("a missing file is artifact_invalid", func(t *testing.T) {
		e := newArtifactApp(t)
		_, _, err := e.app.readArtifactInput(filepath.Join(t.TempDir(), "gone.txt"), "")
		requireCode(t, codeArtifactInvalid, err)
	})
	t.Run("a file over the image limit is artifact_invalid", func(t *testing.T) {
		e := newArtifactApp(t)
		path := writeTestFile(t, "big.bin", make([]byte, artifact.MaxImageSize+1))
		_, _, err := e.app.readArtifactInput(path, "")
		requireCode(t, codeArtifactInvalid, err)
	})
}

// writePNGFile writes a 1x1 PNG and returns its path.
func writePNGFile(t *testing.T) string {
	t.Helper()
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")
	return writeTestFile(t, "shot.png", png)
}
