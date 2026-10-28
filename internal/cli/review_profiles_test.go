package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/review/profile"
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
			Name: "go", Description: byName["go"].Description, When: "always", Match: []string{"*.go", "go.mod"}, Model: "opus", Builtin: true, Source: profile.SourceBuiltin,
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
		assert.Regexp(t, `\ndocs\s+user\s+always`, e.stdout.String())
		assert.Contains(t, e.stderr.String(), "skipped review profile")
	})
	t.Run("a table", func(t *testing.T) {
		e := setup(t)
		require.NoError(t, e.app.runReviewProfiles())
		lines := strings.Split(e.stdout.String(), "\n")
		assert.Regexp(t, `^NAME\s+SOURCE\s+WHEN\s+MATCH\s+MODEL\s+DESCRIPTION$`, lines[0])
		assert.Contains(t, e.stdout.String(), "*.go go.mod")
		assert.Regexp(t, `\nsecurity\s+builtin\s+always\s+-\s+-\s+`, e.stdout.String())
	})
}

// profilesApp runs ws commands against a CONFIG_DIR in a temp directory.
type profilesApp struct {
	app    *App
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	dir    string
}

func newProfilesApp(t *testing.T) *profilesApp {
	t.Helper()
	app, stdout, stderr := newTestApp()
	paths := config.NewPaths(t.TempDir())
	app.configs = &configLoaderMock{
		LoadFunc: func(string) (config.Paths, config.Config, error) { return paths, testConfig(), nil },
	}
	return &profilesApp{app: app, stdout: stdout, stderr: stderr, dir: paths.ReviewProfiles}
}

// run runs ws with args and stdin, and returns its exit code.
func (e *profilesApp) run(t *testing.T, stdin string, args ...string) int {
	t.Helper()
	e.stdout.Reset()
	e.stderr.Reset()
	e.app.stdin = strings.NewReader(stdin)
	return e.app.Run(t.Context(), args)
}

// profile decodes a show, add, or edit result.
func (e *profilesApp) profile(t *testing.T) reviewProfileDetailJSON {
	t.Helper()
	var out reviewProfileDetailJSON
	require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &out))
	return out
}

// errorCode decodes a --json failure's error code.
func (e *profilesApp) errorCode(t *testing.T) string {
	t.Helper()
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(e.stderr.Bytes(), &out), e.stderr.String())
	assert.Empty(t, e.stdout.String())
	return out.Error.Code
}

func (e *profilesApp) write(t *testing.T, name, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(e.dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(e.dir, name+".md"), []byte(content), 0o600))
}

func TestApp_runReviewProfilesShow(t *testing.T) {
	t.Parallel()
	t.Run("a built-in, with its guide and no path", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		require.Equal(t, exitOK, e.run(t, "", "review", "profiles", "show", "--json", "go"), e.stderr.String())
		out := e.profile(t)
		assert.Equal(t, profile.SourceBuiltin, out.Source)
		assert.True(t, out.Builtin)
		assert.Contains(t, out.Guide, "Anchor a concurrency finding")
		assert.Nil(t, out.Path)
		assert.Equal(t, []string{}, out.Warnings)
	})
	t.Run("an override, with its path", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		e.write(t, "go", "---\ndescription: Our Go\n---\nOurs.\n")
		require.Equal(t, exitOK, e.run(t, "", "review", "profiles", "show", "--json", "go"), e.stderr.String())
		out := e.profile(t)
		assert.Equal(t, profile.SourceOverride, out.Source)
		assert.False(t, out.Builtin)
		assert.Equal(t, "Ours.", out.Guide)
		assert.Equal(t, filepath.Join(e.dir, "go.md"), deref(out.Path))
	})
	t.Run("as text", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		require.Equal(t, exitOK, e.run(t, "", "review", "profiles", "show", "security"), e.stderr.String())
		assert.Regexp(t, `^name:\s+security\nsource:\s+builtin\nwhen:\s+always\n`, e.stdout.String())
		assert.Contains(t, e.stdout.String(), "\n\n")
	})
	t.Run("an unknown name is profile_unknown", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		require.Equal(t, exitUsage, e.run(t, "", "review", "profiles", "show", "--json", "cobol"))
		assert.Equal(t, "profile_unknown", e.errorCode(t))
	})
	t.Run("a broken file is config_invalid", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		e.write(t, "docs", "broken\n")
		require.Equal(t, exitFailed, e.run(t, "", "review", "profiles", "show", "--json", "docs"))
		assert.Equal(t, "config_invalid", e.errorCode(t))
	})
	t.Run("needs exactly one name", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		require.Equal(t, exitUsage, e.run(t, "", "review", "profiles", "show", "--json"))
		assert.Equal(t, "usage", e.errorCode(t))
	})
}

func TestApp_runReviewProfilesAdd(t *testing.T) {
	t.Parallel()
	add := func(args ...string) []string {
		return append([]string{"review", "profiles", "add", "--json"}, args...)
	}
	t.Run("adds a profile with its guide from stdin", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		require.Equal(t, exitOK, e.run(t, "Check links.\n", add("docs", "--description", "Docs", "--match", "*.md", "--match", "docs/*", "-")...), e.stderr.String())
		out := e.profile(t)
		assert.Equal(t, reviewProfileInfoJSON{
			Name: "docs", Description: "Docs", When: profile.WhenAuto, Match: []string{"*.md", "docs/*"}, Model: "", Source: profile.SourceUser,
		}, out.reviewProfileInfoJSON)
		assert.Equal(t, "Check links.", out.Guide)
		assert.Equal(t, filepath.Join(e.dir, "docs.md"), deref(out.Path))
		require.Equal(t, exitOK, e.run(t, "", "review", "profiles", "--json"))
		assert.Contains(t, e.stdout.String(), `"name":"docs"`)
	})
	t.Run("reads the guide from a file, with when", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		guide := filepath.Join(t.TempDir(), "guide.md")
		require.NoError(t, os.WriteFile(guide, []byte("Guide.\n"), 0o600))
		require.Equal(t, exitOK, e.run(t, "", add("docs", "--description", "Docs", "--when", "off", "--guide-file", guide)...), e.stderr.String())
		assert.Equal(t, profile.WhenOff, e.profile(t).When)
	})
	t.Run("as text", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		require.Equal(t, exitOK, e.run(t, "Guide.", "review", "profiles", "add", "docs", "--description", "Docs", "-"), e.stderr.String())
		assert.Equal(t, "added review profile docs at "+filepath.Join(e.dir, "docs.md")+"\n", e.stdout.String())
	})
	t.Run("an invalid name is profile_invalid", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		require.Equal(t, exitUsage, e.run(t, "Guide.", add("My_Docs", "--description", "Docs", "-")...))
		assert.Equal(t, "profile_invalid", e.errorCode(t))
	})
	t.Run("the reserved name is profile_invalid", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		require.Equal(t, exitUsage, e.run(t, "Guide.", add("decisions", "--description", "Docs", "-")...))
		assert.Equal(t, "profile_invalid", e.errorCode(t))
	})
	t.Run("an empty guide is profile_invalid", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		require.Equal(t, exitUsage, e.run(t, "\n  \n", add("docs", "--description", "Docs", "-")...))
		assert.Equal(t, "profile_invalid", e.errorCode(t))
	})
	t.Run("a profile that wouldn't parse is profile_invalid", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		require.Equal(t, exitUsage, e.run(t, "Guide.", add("docs", "--description", "Docs", "--when", "auto", "-")...))
		assert.Equal(t, "profile_invalid", e.errorCode(t))
		assert.NoFileExists(t, filepath.Join(e.dir, "docs.md"))
	})
	t.Run("a built-in's name is profile_exists", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		require.Equal(t, exitRefused, e.run(t, "Guide.", add("security", "--description", "Docs", "-")...))
		assert.Equal(t, "profile_exists", e.errorCode(t))
	})
	t.Run("a user profile's name is profile_exists", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		e.write(t, "docs", "---\ndescription: Docs\n---\nRead.\n")
		require.Equal(t, exitRefused, e.run(t, "Guide.", add("docs", "--description", "Docs", "-")...))
		assert.Equal(t, "profile_exists", e.errorCode(t))
	})
	t.Run("usage errors", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		guide := filepath.Join(t.TempDir(), "guide.md")
		require.NoError(t, os.WriteFile(guide, []byte("Guide.\n"), 0o600))
		for _, args := range [][]string{
			add("docs", "-"),
			add("docs", "--description", "Docs"),
			add("docs", "--description", "Docs", "--guide-file", guide, "-"),
			add("docs", "--description", "Docs", "--guide-file", filepath.Join(t.TempDir(), "missing")),
			add("docs", "--description", "Docs", "--when", "sometimes", "-"),
			add("docs", "--description", "Docs", "guide.md"),
			add("--description", "Docs", "-"),
		} {
			require.Equal(t, exitUsage, e.run(t, "Guide.", args...), "%v", args)
			assert.Equal(t, "usage", e.errorCode(t), "%v", args)
		}
	})
}

func TestApp_runReviewProfilesEdit(t *testing.T) {
	t.Parallel()
	edit := func(args ...string) []string {
		return append([]string{"review", "profiles", "edit", "--json"}, args...)
	}
	t.Run("editing a built-in makes a full override", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		builtin, _, err := profile.Builtin().Load("go")
		require.NoError(t, err)
		require.Equal(t, exitOK, e.run(t, "", edit("go", "--description", "Our Go")...), e.stderr.String())
		out := e.profile(t)
		assert.Equal(t, profile.SourceOverride, out.Source)
		assert.False(t, out.Builtin)
		assert.Equal(t, "Our Go", out.Description)
		assert.Equal(t, builtin.Match, out.Match)
		assert.Equal(t, builtin.Body, out.Guide)
		assert.FileExists(t, filepath.Join(e.dir, "go.md"))
	})
	t.Run("changes only the fields given", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		e.write(t, "docs", "---\ndescription: Docs\nmatch: [\"*.md\"]\n---\nRead.\n")
		require.Equal(t, exitOK, e.run(t, "Read closely.", edit("docs", "--when", "always", "--no-match", "-")...), e.stderr.String())
		out := e.profile(t)
		assert.Equal(t, reviewProfileInfoJSON{Name: "docs", Description: "Docs", When: profile.WhenAlways, Match: []string{}, Source: profile.SourceUser}, out.reviewProfileInfoJSON)
		assert.Equal(t, "Read closely.", out.Guide)
	})
	t.Run("as text, an override says how to restore the built-in", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		require.Equal(t, exitOK, e.run(t, "", "review", "profiles", "edit", "tests", "--when", "off"), e.stderr.String())
		assert.Contains(t, e.stdout.String(), "edited review profile tests at ")
		assert.Contains(t, e.stdout.String(), "ws review profiles rm tests restores it")
	})
	t.Run("an edit that wouldn't parse is profile_invalid and keeps the file", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		original := "---\ndescription: Docs\nmatch: [\"*.md\"]\n---\nRead.\n"
		e.write(t, "docs", original)
		require.Equal(t, exitUsage, e.run(t, "", edit("docs", "--no-match")...))
		assert.Equal(t, "profile_invalid", e.errorCode(t))
		raw, err := os.ReadFile(filepath.Join(e.dir, "docs.md"))
		require.NoError(t, err)
		assert.Equal(t, original, string(raw))
	})
	t.Run("an empty guide is profile_invalid", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		require.Equal(t, exitUsage, e.run(t, "", edit("go", "-")...))
		assert.Equal(t, "profile_invalid", e.errorCode(t))
		assert.NoFileExists(t, filepath.Join(e.dir, "go.md"))
	})
	t.Run("the reserved name is profile_invalid", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		require.Equal(t, exitUsage, e.run(t, "", edit("decisions", "--when", "off")...))
		assert.Equal(t, "profile_invalid", e.errorCode(t))
	})
	t.Run("an unknown name is profile_unknown", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		require.Equal(t, exitUsage, e.run(t, "", edit("cobol", "--when", "off")...))
		assert.Equal(t, "profile_unknown", e.errorCode(t))
	})
	t.Run("a broken file is config_invalid", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		e.write(t, "docs", "broken\n")
		require.Equal(t, exitFailed, e.run(t, "", edit("docs", "--when", "off")...))
		assert.Equal(t, "config_invalid", e.errorCode(t))
	})
	t.Run("usage errors", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		for _, args := range [][]string{
			edit("go"),
			edit("go", "--match", "*.go", "--no-match"),
			edit("go", "--when", "never"),
		} {
			require.Equal(t, exitUsage, e.run(t, "", args...), "%v", args)
			assert.Equal(t, "usage", e.errorCode(t), "%v", args)
		}
		assert.NoFileExists(t, filepath.Join(e.dir, "go.md"))
	})
}

func TestApp_runReviewProfilesRm(t *testing.T) {
	t.Parallel()
	rm := func(name string) []string {
		return []string{"review", "profiles", "rm", "--json", name}
	}
	removed := func(t *testing.T, e *profilesApp) reviewProfileRemovedJSON {
		t.Helper()
		var out reviewProfileRemovedJSON
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &out))
		return out
	}
	t.Run("removing an override restores the built-in", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		e.write(t, "go", "---\ndescription: Our Go\n---\nOurs.\n")
		require.Equal(t, exitOK, e.run(t, "", rm("go")...), e.stderr.String())
		assert.Equal(t, reviewProfileRemovedJSON{Name: "go", Path: filepath.Join(e.dir, "go.md"), Restored: true, Warnings: []string{}}, removed(t, e))
		require.Equal(t, exitOK, e.run(t, "", "review", "profiles", "show", "--json", "go"))
		assert.Equal(t, profile.SourceBuiltin, e.profile(t).Source)
	})
	t.Run("removes a user's profile", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		e.write(t, "docs", "---\ndescription: Docs\n---\nRead.\n")
		require.Equal(t, exitOK, e.run(t, "", rm("docs")...), e.stderr.String())
		assert.False(t, removed(t, e).Restored)
		require.Equal(t, exitUsage, e.run(t, "", "review", "profiles", "show", "--json", "docs"))
	})
	t.Run("as text", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		e.write(t, "go", "---\ndescription: Our Go\n---\nOurs.\n")
		e.write(t, "docs", "---\ndescription: Docs\n---\nRead.\n")
		require.Equal(t, exitOK, e.run(t, "", "review", "profiles", "rm", "go"))
		assert.Equal(t, "removed your edits to review profile go; the built-in is back\n", e.stdout.String())
		require.Equal(t, exitOK, e.run(t, "", "review", "profiles", "rm", "docs"))
		assert.Equal(t, "removed review profile docs\n", e.stdout.String())
	})
	t.Run("a built-in is profile_builtin", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		require.Equal(t, exitRefused, e.run(t, "", rm("security")...))
		assert.Equal(t, "profile_builtin", e.errorCode(t))
	})
	t.Run("an unknown name is profile_unknown", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		require.Equal(t, exitUsage, e.run(t, "", rm("cobol")...))
		assert.Equal(t, "profile_unknown", e.errorCode(t))
	})
	t.Run("an invalid name is profile_invalid", func(t *testing.T) {
		t.Parallel()
		e := newProfilesApp(t)
		require.Equal(t, exitUsage, e.run(t, "", rm("decisions")...))
		assert.Equal(t, "profile_invalid", e.errorCode(t))
	})
}

func TestApp_newReviewProfilesCmd(t *testing.T) {
	t.Run("refuses an unknown operation", func(t *testing.T) {
		app, _, _ := newTestApp()
		cmd := app.newReviewProfilesCmd()
		requireUsageError(t, cmd.Args(cmd, []string{"nope"}))
	})
}

func TestProfileWriteError(t *testing.T) {
	t.Run("passes other errors through", func(t *testing.T) {
		assert.Equal(t, errTest, profileWriteError(errTest))
	})
}
