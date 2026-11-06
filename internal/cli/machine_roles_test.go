package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/machine/role"
)

// rolesApp runs ws machine roles against a CONFIG_DIR in a temp directory.
type rolesApp struct {
	app    *App
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	dir    string
}

func newRolesApp(t *testing.T) *rolesApp {
	t.Helper()
	app, stdout, stderr := newTestApp()
	paths := config.NewPaths(t.TempDir())
	app.configs = &configLoaderMock{
		LoadFunc: func(string) (config.Paths, config.Config, error) { return paths, testConfig(), nil },
	}
	return &rolesApp{app: app, stdout: stdout, stderr: stderr, dir: paths.MachineRoles}
}

// run runs ws with args and stdin, and returns its exit code.
func (e *rolesApp) run(t *testing.T, stdin string, args ...string) int {
	t.Helper()
	e.stdout.Reset()
	e.stderr.Reset()
	e.app.stdin = strings.NewReader(stdin)
	return e.app.Run(t.Context(), args)
}

// errorCode decodes a failure's error code, and checks nothing reached stdout.
func (e *rolesApp) errorCode(t *testing.T) string {
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

// detail decodes a show, add, or edit result.
func (e *rolesApp) detail(t *testing.T) machineRoleDetailJSON {
	t.Helper()
	var out machineRoleDetailJSON
	require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &out), e.stdout.String())
	return out
}

// list decodes a roles listing.
func (e *rolesApp) list(t *testing.T) machineRolesJSON {
	t.Helper()
	var out machineRolesJSON
	require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &out), e.stdout.String())
	return out
}

// writeUser writes a role file into the user's directory.
func (e *rolesApp) writeUser(t *testing.T, name, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(e.dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(e.dir, name+".md"), []byte(content), 0o600))
}

// promptFile writes a prompt template to a file and returns its path.
func promptFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "prompt.md")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// roleNamed is the listed role with name, if it is listed.
func roleNamed(roles []machineRoleInfoJSON, name string) (machineRoleInfoJSON, bool) {
	for i := range roles {
		if roles[i].Name == name {
			return roles[i], true
		}
	}
	return machineRoleInfoJSON{}, false
}

func TestApp_newMachineRoles(t *testing.T) {
	t.Parallel()
	t.Run("is a machine operation with show, add, edit, and rm", func(t *testing.T) {
		t.Parallel()
		app, _, _ := newTestApp()
		var names []string
		for _, sub := range app.newMachineCmd().Commands() {
			if sub.Name() == "roles" {
				for _, op := range sub.Commands() {
					names = append(names, op.Name())
				}
			}
		}
		assert.ElementsMatch(t, []string{"show", "add", "edit", "rm"}, names)
	})
	t.Run("refuses an argument to the list", func(t *testing.T) {
		t.Parallel()
		app, _, _ := newTestApp()
		roles := app.newMachineRolesCmd()
		err := roles.Args(roles, []string{"implementer"})
		assert.Equal(t, codeUsage, asCLIError(err).code)
	})
}

func TestApp_runMachineRoles(t *testing.T) {
	t.Parallel()
	t.Run("lists the built-in roles with their settings", func(t *testing.T) {
		t.Parallel()
		e := newRolesApp(t)
		require.Equal(t, exitOK, e.run(t, "", "machine", "roles"), e.stderr.String())
		out := e.list(t)
		require.Len(t, out.Roles, 3)
		implementer, ok := roleNamed(out.Roles, "implementer")
		require.True(t, ok)
		assert.Equal(t, "builtin", implementer.Source)
		assert.Equal(t, "haiku", implementer.Model)
		assert.Equal(t, []string{"TASK", "TASKS", "BASE", "BRANCH"}, implementer.Vars)
		assert.Equal(t, []string{"--permission-mode", "auto"}, implementer.ClaudeArgs)
		reviewer, ok := roleNamed(out.Roles, "reviewer")
		require.True(t, ok)
		assert.Equal(t, "sonnet", reviewer.Model)
		assert.Equal(t, []string{"APPROVED"}, reviewer.Pass)
		assert.Equal(t, []string{"CHANGES_REQUESTED"}, reviewer.Fail)
		verifier, ok := roleNamed(out.Roles, "verifier")
		require.True(t, ok)
		assert.Equal(t, "haiku", verifier.Model)
		assert.Equal(t, []string{"PASS", "FLAKY"}, verifier.Pass)
		assert.Empty(t, out.Warnings)
	})
	t.Run("shows a built-in with its prompt and no path", func(t *testing.T) {
		t.Parallel()
		e := newRolesApp(t)
		require.Equal(t, exitOK, e.run(t, "", "machine", "roles", "show", "implementer"), e.stderr.String())
		out := e.detail(t)
		assert.Equal(t, "builtin", out.Source)
		assert.Contains(t, out.Prompt, "ws machine result")
		assert.Nil(t, out.Path)
	})
	t.Run("shows an unknown role as role_unknown", func(t *testing.T) {
		t.Parallel()
		e := newRolesApp(t)
		require.Equal(t, exitNotFound, e.run(t, "", "machine", "roles", "show", "nope"))
		assert.Equal(t, string(codeRoleUnknown), e.errorCode(t))
	})
	t.Run("adds a role from a file, then refuses the same name", func(t *testing.T) {
		t.Parallel()
		e := newRolesApp(t)
		prompt := promptFile(t, "Do {{.Vars.X}}.\n")
		args := []string{"machine", "roles", "add", "tester", "--description", "Smoke", "--vars", "X", "--pass", "ok", "--fail", "bad", "--prompt-file", prompt}
		require.Equal(t, exitOK, e.run(t, "", args...), e.stderr.String())
		out := e.detail(t)
		assert.Equal(t, "user", out.Source)
		assert.Equal(t, []string{"OK"}, out.Pass)
		assert.Equal(t, []string{"BAD"}, out.Fail)
		require.NotNil(t, out.Path)
		assert.Equal(t, filepath.Join(e.dir, "tester.md"), *out.Path)

		require.Equal(t, exitRefused, e.run(t, "", args...))
		assert.Equal(t, string(codeRoleExists), e.errorCode(t))
	})
	t.Run("adds a role with the prompt on stdin", func(t *testing.T) {
		t.Parallel()
		e := newRolesApp(t)
		require.Equal(t, exitOK, e.run(t, "Do {{.Vars.X}}.\n", "machine", "roles", "add", "reader", "--description", "Stdin", "--vars", "X", "-"), e.stderr.String())
		assert.Equal(t, "Do {{.Vars.X}}.", e.detail(t).Prompt)
	})
	t.Run("refuses a prompt that uses a var it doesn't declare", func(t *testing.T) {
		t.Parallel()
		e := newRolesApp(t)
		prompt := promptFile(t, "Do {{.Vars.Y}}.\n")
		require.Equal(t, exitUsage, e.run(t, "", "machine", "roles", "add", "tester", "--description", "Smoke", "--vars", "X", "--prompt-file", prompt))
		assert.Equal(t, string(codeRoleInvalid), e.errorCode(t))
	})
	t.Run("add needs a description", func(t *testing.T) {
		t.Parallel()
		e := newRolesApp(t)
		prompt := promptFile(t, "Do it.\n")
		require.Equal(t, exitUsage, e.run(t, "", "machine", "roles", "add", "tester", "--prompt-file", prompt))
		assert.Equal(t, string(codeUsage), e.errorCode(t))
	})
	t.Run("edit overrides a built-in, and keeps its prompt", func(t *testing.T) {
		t.Parallel()
		e := newRolesApp(t)
		require.Equal(t, exitOK, e.run(t, "", "machine", "roles", "show", "reviewer"), e.stderr.String())
		before := e.detail(t).Prompt
		require.Equal(t, exitOK, e.run(t, "", "machine", "roles", "edit", "reviewer", "--model", "opus"), e.stderr.String())
		out := e.detail(t)
		assert.Equal(t, "override", out.Source)
		assert.Equal(t, "opus", out.Model)
		assert.Equal(t, before, out.Prompt)
		require.NotNil(t, out.Path)
	})
	t.Run("edit with --model empty clears the model", func(t *testing.T) {
		t.Parallel()
		e := newRolesApp(t)
		require.Equal(t, exitOK, e.run(t, "", "machine", "roles", "edit", "reviewer", "--model", ""), e.stderr.String())
		assert.Empty(t, e.detail(t).Model)
	})
	t.Run("edit with nothing to change is usage", func(t *testing.T) {
		t.Parallel()
		e := newRolesApp(t)
		require.Equal(t, exitUsage, e.run(t, "", "machine", "roles", "edit", "reviewer"))
		assert.Equal(t, string(codeUsage), e.errorCode(t))
	})
	t.Run("edit refuses a change that breaks the prompt, and leaves the file", func(t *testing.T) {
		t.Parallel()
		e := newRolesApp(t)
		prompt := promptFile(t, "Do {{.Vars.X}}.\n")
		require.Equal(t, exitOK, e.run(t, "", "machine", "roles", "add", "tester", "--description", "Smoke", "--vars", "X", "--prompt-file", prompt), e.stderr.String())
		path := filepath.Join(e.dir, "tester.md")
		before, err := os.ReadFile(path)
		require.NoError(t, err)

		require.Equal(t, exitUsage, e.run(t, "", "machine", "roles", "edit", "tester", "--no-vars"))
		assert.Equal(t, string(codeRoleInvalid), e.errorCode(t))
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, string(before), string(after))
	})
	t.Run("rm restores a built-in, and refuses a built-in with no override", func(t *testing.T) {
		t.Parallel()
		e := newRolesApp(t)
		require.Equal(t, exitOK, e.run(t, "", "machine", "roles", "edit", "reviewer", "--model", "opus"), e.stderr.String())
		require.Equal(t, exitOK, e.run(t, "", "machine", "roles", "rm", "reviewer"), e.stderr.String())
		var removed machineRoleRemovedJSON
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &removed))
		assert.True(t, removed.Restored)

		require.Equal(t, exitOK, e.run(t, "", "machine", "roles", "show", "reviewer"), e.stderr.String())
		assert.Equal(t, "builtin", e.detail(t).Source)

		require.Equal(t, exitRefused, e.run(t, "", "machine", "roles", "rm", "implementer"))
		assert.Equal(t, string(codeRoleBuiltin), e.errorCode(t))
	})
	t.Run("rm of an unknown role is role_unknown", func(t *testing.T) {
		t.Parallel()
		e := newRolesApp(t)
		require.Equal(t, exitNotFound, e.run(t, "", "machine", "roles", "rm", "nope"))
		assert.Equal(t, string(codeRoleUnknown), e.errorCode(t))
	})
	t.Run("an unparsable file is a warning, and hides the built-in it overrides", func(t *testing.T) {
		t.Parallel()
		e := newRolesApp(t)
		e.writeUser(t, "verifier", "no front matter\n")
		require.Equal(t, exitOK, e.run(t, "", "machine", "roles"), e.stderr.String())
		out := e.list(t)
		_, ok := roleNamed(out.Roles, "verifier")
		assert.False(t, ok)
		require.Len(t, out.Warnings, 1)
		assert.Contains(t, out.Warnings[0], "skipped role file")
	})
}

func TestRoleArgs(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		args    []string
		wantErr bool
	}{
		"no arguments":          {args: nil, wantErr: true},
		"name only":             {args: []string{"tester"}},
		"name and stdin":        {args: []string{"tester", "-"}},
		"name and another word": {args: []string{"tester", "x"}, wantErr: true},
		"too many arguments":    {args: []string{"tester", "-", "extra"}, wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cmd := &cobra.Command{Use: "roles"}
			err := roleArgs(cmd, tc.args)
			if !tc.wantErr {
				assert.NoError(t, err)
				return
			}
			assert.Equal(t, codeUsage, asCLIError(err).code)
		})
	}
}

func TestRoleWriteError(t *testing.T) {
	t.Parallel()
	cases := map[error]errorCode{
		role.ErrInvalid: codeRoleInvalid,
		role.ErrExists:  codeRoleExists,
		role.ErrBuiltin: codeRoleBuiltin,
		role.ErrUnknown: codeRoleUnknown,
		role.ErrBroken:  codeConfigInvalid,
	}
	for sentinel, want := range cases {
		t.Run(string(want), func(t *testing.T) {
			t.Parallel()
			err := roleWriteError(fmt.Errorf("wrapped: %w", sentinel))
			assert.Equal(t, want, asCLIError(err).code)
			assert.ErrorIs(t, err, sentinel)
		})
	}
	t.Run("another error is returned as it is", func(t *testing.T) {
		t.Parallel()
		assert.Same(t, errTest, roleWriteError(errTest))
		assert.NotErrorIs(t, roleWriteError(errTest), role.ErrInvalid)
	})
}
