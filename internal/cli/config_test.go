package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
)

// commentedConfig is a config.toml with comments that set and unset must keep.
const commentedConfig = "# ws settings\n[defaults]\nhost = \"github.com\"  # the default host\nowners = [\"bobcob7\"]\n"

// newConfigApp is a test App whose config.toml holds text, in a new
// directory. It returns the App and the file's path.
func newConfigApp(t *testing.T, text string) (app *App, path string, stdout, stderr *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(text), 0o600))
	app, stdout, stderr = newTestApp()
	app.configs = &configLoaderMock{
		LoadFunc: func(string) (config.Paths, config.Config, error) {
			return config.NewPaths(dir), testConfig(), nil
		},
	}
	return app, path, stdout, stderr
}

// runConfigCmd runs ws with args and returns its exit code.
func runConfigCmd(t *testing.T, app *App, args ...string) int {
	t.Helper()
	return app.execute(t.Context(), app.newRootCmd(), args)
}

// errorCodeOf is the code in the --json error output on stderr.
func errorCodeOf(t *testing.T, stderr *bytes.Buffer) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(stderr.Bytes(), &body), stderr.String())
	return body.Error.Code
}

func readFileText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

func TestApp_newConfigCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newConfigCmd()
	t.Run("has every operation", func(t *testing.T) {
		var names []string
		for _, sub := range cmd.Commands() {
			names = append(names, sub.Name())
		}
		assert.ElementsMatch(t, []string{"list", "get", "set", "unset"}, names)
	})
	t.Run("refuses an unknown operation", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, []string{"frobnicate"}))
	})
}

func TestApp_configList(t *testing.T) {
	t.Parallel()
	t.Run("as text, on an empty file", func(t *testing.T) {
		t.Parallel()
		app, _, stdout, _ := newConfigApp(t, "")
		require.Equal(t, exitOK, runConfigCmd(t, app, "config", "list"))
		assert.Equal(t, "defaults.host = \"github.com\"  # default\n"+
			"defaults.owners = []  # default\n"+
			"review.timeout = \"1h\"  # default\n"+
			"review.remove_finished = true  # default\n", stdout.String())
	})
	t.Run("as text, with a profile table", func(t *testing.T) {
		t.Parallel()
		app, _, stdout, _ := newConfigApp(t, "[review.profiles.security]\nmodel = \"opus\"\n")
		require.Equal(t, exitOK, runConfigCmd(t, app, "config", "list"))
		assert.Contains(t, stdout.String(), "review.profiles.security.when = \"\"  # default\n"+
			"review.profiles.security.model = \"opus\"\n")
	})
	t.Run("--json", func(t *testing.T) {
		t.Parallel()
		app, path, stdout, _ := newConfigApp(t, "[defaults]\nowners = [\"bobcob7\", \"acme\"]\n")
		require.Equal(t, exitOK, runConfigCmd(t, app, "--json", "config", "list"))
		var out configListJSON
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &out))
		assert.Equal(t, path, out.Path)
		assert.Equal(t, []string{}, out.Warnings)
		require.Len(t, out.Settings, 4)
		assert.Equal(t, config.Setting{Key: "defaults.owners", Type: config.TypeStrings, Value: []any{"bobcob7", "acme"}, Default: []any{}, Set: true}, out.Settings[1])
	})
	t.Run("refuses arguments", func(t *testing.T) {
		app, _, _, _ := newConfigApp(t, "")
		cmd := app.newConfigListCmd()
		requireUsageError(t, cmd.Args(cmd, []string{"x"}))
	})
}

func TestApp_configGet(t *testing.T) {
	t.Parallel()
	t.Run("prints a list one item per line", func(t *testing.T) {
		t.Parallel()
		app, _, stdout, _ := newConfigApp(t, "[defaults]\nowners = [\"bobcob7\", \"acme\"]\n")
		require.Equal(t, exitOK, runConfigCmd(t, app, "config", "get", "defaults.owners"))
		assert.Equal(t, "bobcob7\nacme\n", stdout.String())
	})
	t.Run("prints a bool bare", func(t *testing.T) {
		t.Parallel()
		app, _, stdout, _ := newConfigApp(t, "")
		require.Equal(t, exitOK, runConfigCmd(t, app, "config", "get", "review.remove_finished"))
		assert.Equal(t, "true\n", stdout.String())
	})
	t.Run("prints an empty string as an empty line", func(t *testing.T) {
		t.Parallel()
		app, _, stdout, _ := newConfigApp(t, "")
		require.Equal(t, exitOK, runConfigCmd(t, app, "config", "get", "review.profiles.security.when"))
		assert.Equal(t, "\n", stdout.String())
	})
	t.Run("--json", func(t *testing.T) {
		t.Parallel()
		app, _, stdout, _ := newConfigApp(t, "")
		require.Equal(t, exitOK, runConfigCmd(t, app, "--json", "config", "get", "review.timeout"))
		var out configSettingJSON
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &out))
		assert.Equal(t, config.Setting{Key: "review.timeout", Type: config.TypeDuration, Value: "1h", Default: "1h", Set: false}, out.Setting)
	})
	t.Run("an unknown key is config_key_unknown, exit 2", func(t *testing.T) {
		t.Parallel()
		app, _, _, stderr := newConfigApp(t, "")
		assert.Equal(t, exitUsage, runConfigCmd(t, app, "--json", "config", "get", "nope"))
		assert.Equal(t, "config_key_unknown", errorCodeOf(t, stderr))
	})
}

func TestApp_configSet(t *testing.T) {
	t.Parallel()
	t.Run("rewrites the key and keeps the comments", func(t *testing.T) {
		t.Parallel()
		app, path, _, _ := newConfigApp(t, commentedConfig)
		require.Equal(t, exitOK, runConfigCmd(t, app, "config", "set", "defaults.host", "gitlab.example.com"))
		assert.Equal(t, "# ws settings\n[defaults]\nhost = \"gitlab.example.com\" # the default host\nowners = [\"bobcob7\"]\n", readFileText(t, path))
	})
	t.Run("prints the key's line, with values normalized", func(t *testing.T) {
		t.Parallel()
		app, _, stdout, _ := newConfigApp(t, "")
		require.Equal(t, exitOK, runConfigCmd(t, app, "config", "set", "defaults.owners", "Me", "acme"))
		assert.Equal(t, "defaults.owners = [\"me\", \"acme\"]\n", stdout.String())
	})
	t.Run("--json reports the change, and none when the file already says it", func(t *testing.T) {
		t.Parallel()
		app, path, stdout, _ := newConfigApp(t, commentedConfig)
		old := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
		require.NoError(t, os.Chtimes(path, old, old))
		require.Equal(t, exitOK, runConfigCmd(t, app, "--json", "config", "set", "defaults.host", "gitlab.example.com"))
		var first configChangeJSON
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &first))
		assert.True(t, first.Changed)

		stdout.Reset()
		mtime := mustModTime(t, path)
		require.Equal(t, exitOK, runConfigCmd(t, app, "--json", "config", "set", "defaults.host", "gitlab.example.com"))
		var second configChangeJSON
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &second))
		assert.False(t, second.Changed)
		assert.Equal(t, mtime, mustModTime(t, path))
	})
	t.Run("empties defaults.owners with no values", func(t *testing.T) {
		t.Parallel()
		app, path, stdout, _ := newConfigApp(t, commentedConfig)
		require.Equal(t, exitOK, runConfigCmd(t, app, "config", "set", "defaults.owners"))
		assert.Equal(t, "defaults.owners = []\n", stdout.String())
		assert.Contains(t, readFileText(t, path), "owners = []\n")
	})
	t.Run("a single-valued key with two values is usage", func(t *testing.T) {
		t.Parallel()
		app, path, _, stderr := newConfigApp(t, commentedConfig)
		assert.Equal(t, exitUsage, runConfigCmd(t, app, "--json", "config", "set", "review.timeout", "1h", "2h"))
		assert.Equal(t, "usage", errorCodeOf(t, stderr))
		assert.Equal(t, commentedConfig, readFileText(t, path))
	})
	t.Run("a single-valued key with no value is usage", func(t *testing.T) {
		t.Parallel()
		app, _, _, _ := newConfigApp(t, commentedConfig)
		assert.Equal(t, exitUsage, runConfigCmd(t, app, "config", "set", "review.timeout"))
	})
	t.Run("a value that breaks the rule is config_value_invalid, exit 2", func(t *testing.T) {
		t.Parallel()
		app, path, _, stderr := newConfigApp(t, commentedConfig)
		assert.Equal(t, exitUsage, runConfigCmd(t, app, "--json", "config", "set", "review.timeout", "nope"))
		assert.Equal(t, "config_value_invalid", errorCodeOf(t, stderr))
		assert.Equal(t, commentedConfig, readFileText(t, path))
	})
	t.Run("an unknown key is config_key_unknown, exit 2", func(t *testing.T) {
		t.Parallel()
		app, _, _, stderr := newConfigApp(t, commentedConfig)
		assert.Equal(t, exitUsage, runConfigCmd(t, app, "--json", "config", "set", "nope", "x"))
		assert.Equal(t, "config_key_unknown", errorCodeOf(t, stderr))
	})
	t.Run("a profile that doesn't exist is profile_unknown, exit 2", func(t *testing.T) {
		t.Parallel()
		app, path, _, stderr := newConfigApp(t, commentedConfig)
		assert.Equal(t, exitUsage, runConfigCmd(t, app, "--json", "config", "set", "review.profiles.nosuch.model", "opus"))
		assert.Equal(t, "profile_unknown", errorCodeOf(t, stderr))
		assert.Equal(t, commentedConfig, readFileText(t, path))
	})
	t.Run("a built-in profile is set", func(t *testing.T) {
		t.Parallel()
		app, path, _, _ := newConfigApp(t, "")
		require.Equal(t, exitOK, runConfigCmd(t, app, "config", "set", "review.profiles.security.model", "opus"))
		assert.Contains(t, readFileText(t, path), "[review.profiles.security]\nmodel = \"opus\"\n")
	})
	t.Run("a file that sets the key as a dotted key is config_uneditable, exit 1", func(t *testing.T) {
		t.Parallel()
		dotted := "[review]\nprofiles.security.model = \"sonnet\"\n"
		app, path, _, stderr := newConfigApp(t, dotted)
		assert.Equal(t, exitFailed, runConfigCmd(t, app, "--json", "config", "set", "review.profiles.security.model", "opus"))
		assert.Equal(t, "config_uneditable", errorCodeOf(t, stderr))
		assert.Equal(t, dotted, readFileText(t, path))
	})
}

func TestApp_configUnset(t *testing.T) {
	t.Parallel()
	t.Run("removes a set key, so its default applies", func(t *testing.T) {
		t.Parallel()
		app, path, stdout, _ := newConfigApp(t, "[review]\ntimeout = \"2h\"\nremove_finished = false\n")
		require.Equal(t, exitOK, runConfigCmd(t, app, "config", "unset", "review.timeout"))
		assert.Equal(t, "[review]\nremove_finished = false\n", readFileText(t, path))
		assert.Equal(t, "review.timeout = \"1h\"  # default\n", stdout.String())
	})
	t.Run("--json of a key that isn't set reports no change", func(t *testing.T) {
		t.Parallel()
		app, _, stdout, _ := newConfigApp(t, commentedConfig)
		require.Equal(t, exitOK, runConfigCmd(t, app, "--json", "config", "unset", "review.timeout"))
		var out configChangeJSON
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &out))
		assert.False(t, out.Changed)
		assert.Equal(t, "review.timeout", out.Setting.Key)
	})
	t.Run("an unknown key is config_key_unknown, exit 2", func(t *testing.T) {
		t.Parallel()
		app, _, _, stderr := newConfigApp(t, commentedConfig)
		assert.Equal(t, exitUsage, runConfigCmd(t, app, "--json", "config", "unset", "nope"))
		assert.Equal(t, "config_key_unknown", errorCodeOf(t, stderr))
	})
}

func TestApp_printConfigValue(t *testing.T) {
	t.Run("prints each list item on its own line", func(t *testing.T) {
		app, _, stdout, _ := newConfigApp(t, "")
		app.printConfigValue([]string{"a", "b"})
		assert.Equal(t, "a\nb\n", stdout.String())
	})
	t.Run("prints a bool or string as it is", func(t *testing.T) {
		app, _, stdout, _ := newConfigApp(t, "")
		app.printConfigValue(false)
		assert.Equal(t, "false\n", stdout.String())
	})
}

func TestApp_checkProfileKey(t *testing.T) {
	t.Parallel()
	newApp := func(t *testing.T) *App {
		t.Helper()
		app, path, _, _ := newConfigApp(t, "")
		app.paths = config.NewPaths(filepath.Dir(path))
		return app
	}
	t.Run("another key needs no profile", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, newApp(t).checkProfileKey("defaults.host"))
	})
	t.Run("a built-in profile exists", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, newApp(t).checkProfileKey("review.profiles.security.model"))
	})
	t.Run("a missing profile is profile_unknown", func(t *testing.T) {
		t.Parallel()
		requireCode(t, codeProfileUnknown, newApp(t).checkProfileKey("review.profiles.nosuch.when"))
	})
}

func TestProfileKeyName(t *testing.T) {
	tests := []struct {
		key  string
		name string
		ok   bool
	}{
		{key: "review.profiles.security.model", name: "security", ok: true},
		{key: "review.profiles.go-style.when", name: "go-style", ok: true},
		{key: "review.timeout", name: "", ok: false},
		{key: "defaults.host", name: "", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			name, ok := profileKeyName(tt.key)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.name, name)
		})
	}
}

func TestConfigLine(t *testing.T) {
	tests := []struct {
		name    string
		setting config.Setting
		want    string
	}{
		{name: "a set string", setting: config.Setting{Key: "defaults.host", Value: "gitlab.example.com", Set: true}, want: "defaults.host = \"gitlab.example.com\""},
		{name: "a default string", setting: config.Setting{Key: "review.profiles.security.when", Value: ""}, want: "review.profiles.security.when = \"\"  # default"},
		{name: "a set bool", setting: config.Setting{Key: "review.remove_finished", Value: false, Set: true}, want: "review.remove_finished = false"},
		{name: "a set list", setting: config.Setting{Key: "defaults.owners", Value: []string{"a", "b"}, Set: true}, want: "defaults.owners = [\"a\", \"b\"]"},
		{name: "an empty default list", setting: config.Setting{Key: "defaults.owners", Value: []string{}}, want: "defaults.owners = []  # default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, configLine(tt.setting))
		})
	}
}

func TestConfigError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want errorCode
	}{
		{name: "an unknown key", err: config.ErrKeyUnknown, want: codeConfigKeyUnknown},
		{name: "a value that breaks its rule", err: config.ErrValueInvalid, want: codeConfigValueInvalid},
		{name: "a value error also wraps ErrInvalid", err: errors.Join(config.ErrValueInvalid, config.ErrInvalid), want: codeConfigValueInvalid},
		{name: "an uneditable key", err: config.ErrUneditable, want: codeConfigUneditable},
		{name: "an invalid file", err: config.ErrInvalid, want: codeConfigInvalid},
		{name: "anything else", err: errTest, want: codeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireCode(t, tt.want, configError(tt.err))
		})
	}
}

// mustModTime is path's modification time.
func mustModTime(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	return info.ModTime()
}
