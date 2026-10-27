package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
)

func TestApp_runReviewProfiles(t *testing.T) {
	setup := func(t *testing.T) *mockedApp {
		t.Helper()
		e := newMockedApp()
		e.app.paths = config.NewPaths(t.TempDir())
		e.app.config = testConfig()
		e.app.config.Review.Profiles = map[string]config.ReviewProfile{"go": {When: "always", Model: "opus"}}
		return e
	}
	t.Run("lists every profile with config.toml applied", func(t *testing.T) {
		e := setup(t)
		e.app.global.json = true
		require.NoError(t, e.app.runReviewProfiles())
		var out struct {
			Profiles []reviewProfileInfoJSON `json:"profiles"`
			Warnings []string                `json:"warnings"`
		}
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &out))
		require.Len(t, out.Profiles, 8)
		byName := map[string]reviewProfileInfoJSON{}
		for _, p := range out.Profiles {
			byName[p.Name] = p
		}
		assert.Equal(t, reviewProfileInfoJSON{
			Name: "go", Description: byName["go"].Description, When: "always", Match: []string{"*.go", "go.mod"}, Model: "opus", Builtin: true,
		}, byName["go"])
		assert.Equal(t, "off", string(byName["efficiency"].When))
		assert.Equal(t, []string{}, byName["security"].Match)
		assert.Empty(t, out.Warnings)
	})
	t.Run("a user profile, and a broken one as a warning", func(t *testing.T) {
		e := setup(t)
		dir := e.app.paths.ReviewProfiles
		require.NoError(t, os.MkdirAll(dir, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "docs.md"), []byte("---\ndescription: Docs\n---\nRead.\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.md"), []byte("no front matter\n"), 0o600))
		require.NoError(t, e.app.runReviewProfiles())
		assert.Contains(t, e.stdout.String(), "\ndocs ")
		assert.Contains(t, e.stderr.String(), "skipped review profile")
	})
	t.Run("a table", func(t *testing.T) {
		e := setup(t)
		require.NoError(t, e.app.runReviewProfiles())
		lines := strings.Split(e.stdout.String(), "\n")
		assert.Regexp(t, `^NAME\s+WHEN\s+MATCH\s+MODEL\s+DESCRIPTION$`, lines[0])
		assert.Contains(t, e.stdout.String(), "*.go go.mod")
		assert.Regexp(t, `\nsecurity\s+always\s+-\s+-\s+`, e.stdout.String())
	})
}
