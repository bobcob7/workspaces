package role

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validRole is a role that passes Validate, for tests to change one field of.
func validRole() Role {
	return Role{
		Name:        "tester",
		Description: "Tests one thing",
		Model:       "haiku",
		Pass:        []string{"PASS"},
		Fail:        []string{"FAIL"},
		Vars:        []string{"TASK"},
		ClaudeArgs:  []string{"--permission-mode", "auto"},
		Prompt:      "Test {{.Vars.TASK}}.",
	}
}

func TestParse(t *testing.T) {
	t.Run("defaults for absent keys", func(t *testing.T) {
		ro, err := Parse("plain", []byte("---\ndescription: Plain\n---\n\nDo it.\n"))
		require.NoError(t, err)
		assert.Equal(t, []string{"PASS"}, ro.Pass)
		assert.Equal(t, []string{"FAIL"}, ro.Fail)
		assert.Equal(t, []string{}, ro.Vars)
		assert.Equal(t, []string{}, ro.ClaudeArgs)
		assert.Empty(t, ro.Model)
		assert.Equal(t, "Do it.", ro.Prompt)
	})
	t.Run("refuses a missing opening fence", func(t *testing.T) {
		_, err := Parse("plain", []byte("description: Plain\n---\nDo it.\n"))
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("refuses a missing closing fence", func(t *testing.T) {
		_, err := Parse("plain", []byte("---\ndescription: Plain\nDo it.\n"))
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("refuses an unknown key", func(t *testing.T) {
		_, err := Parse("plain", []byte("---\ndescription: Plain\ncolor: red\n---\nDo it.\n"))
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("refuses a repeated key", func(t *testing.T) {
		_, err := Parse("plain", []byte("---\ndescription: Plain\ndescription: Again\n---\nDo it.\n"))
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("refuses a list that is not JSON", func(t *testing.T) {
		_, err := Parse("plain", []byte("---\ndescription: Plain\npass: PASS\n---\nDo it.\n"))
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("reads CRLF line endings", func(t *testing.T) {
		ro, err := Parse("plain", []byte("---\r\ndescription: Plain\r\n---\r\n\r\nDo it.\r\n"))
		require.NoError(t, err)
		assert.Equal(t, "Plain", ro.Description)
		assert.Equal(t, "Do it.", ro.Prompt)
	})
}

func TestFormat(t *testing.T) {
	t.Run("round-trips a role with a model", func(t *testing.T) {
		want := validRole()
		raw, err := Format(&want)
		require.NoError(t, err)
		got, err := Parse(want.Name, raw)
		require.NoError(t, err)
		want.Source = ""
		assert.Equal(t, want, got)
	})
	t.Run("round-trips a role without a model", func(t *testing.T) {
		want := validRole()
		want.Model = ""
		want.Vars = []string{}
		want.ClaudeArgs = []string{}
		want.Prompt = "Test it."
		raw, err := Format(&want)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "model:")
		got, err := Parse(want.Name, raw)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})
	t.Run("writes nil lists as empty arrays", func(t *testing.T) {
		ro := validRole()
		ro.Vars = nil
		ro.ClaudeArgs = nil
		raw, err := Format(&ro)
		require.NoError(t, err)
		assert.Contains(t, string(raw), "vars: []\n")
		assert.Contains(t, string(raw), "claude_args: []\n")
	})
}

func TestValidate(t *testing.T) {
	t.Run("accepts a valid role", func(t *testing.T) {
		ro := validRole()
		assert.NoError(t, ro.Validate())
	})
	refusals := map[string]func(*Role){
		"upper case name":       func(r *Role) { r.Name = "Bad" },
		"name starting hyphen":  func(r *Role) { r.Name = "-bad" },
		"empty description":     func(r *Role) { r.Description = "" },
		"description too long":  func(r *Role) { r.Description = repeat('x', 121) },
		"description two lines": func(r *Role) { r.Description = "one\ntwo" },
		"empty pass":            func(r *Role) { r.Pass = []string{} },
		"lower-case verdict":    func(r *Role) { r.Pass = []string{"pass"} },
		"verdict in both":       func(r *Role) { r.Pass = []string{"X"}; r.Fail = []string{"X"} },
		"bad var key":           func(r *Role) { r.Vars = []string{"1A"} },
		"duplicate var":         func(r *Role) { r.Vars = []string{"TASK", "TASK"} },
		"settings argument":     func(r *Role) { r.ClaudeArgs = []string{"--settings", "x"} },
		"model argument":        func(r *Role) { r.ClaudeArgs = []string{"--model=opus"} },
		"model with space":      func(r *Role) { r.Model = "claude haiku" },
		"blank prompt":          func(r *Role) { r.Prompt = " \n" },
		"undeclared var":        func(r *Role) { r.Vars = []string{}; r.Prompt = "{{.Vars.NOPE}}" },
		"template parse error":  func(r *Role) { r.Prompt = "{{ .Vars.X" },
	}
	for name, change := range refusals {
		t.Run("refuses "+name, func(t *testing.T) {
			ro := validRole()
			change(&ro)
			require.ErrorIs(t, ro.Validate(), ErrInvalid)
		})
	}
}

func TestRole_Render(t *testing.T) {
	t.Run("renders with the given vars", func(t *testing.T) {
		ro := validRole()
		got, err := ro.Render(map[string]string{"TASK": "T1"}, "")
		require.NoError(t, err)
		assert.Equal(t, "Test T1.", got)
	})
	t.Run("refuses a var the role does not declare", func(t *testing.T) {
		ro := validRole()
		_, err := ro.Render(map[string]string{"OTHER": "x"}, "")
		require.ErrorIs(t, err, ErrRender)
		assert.ErrorContains(t, err, `"OTHER"`)
	})
	t.Run("refuses a declared var that is not set", func(t *testing.T) {
		ro := validRole()
		_, err := ro.Render(nil, "")
		require.ErrorIs(t, err, ErrRender)
	})
	t.Run("appends extra after one blank line", func(t *testing.T) {
		ro := validRole()
		got, err := ro.Render(map[string]string{"TASK": "T1"}, "fix 1.")
		require.NoError(t, err)
		assert.Equal(t, "Test T1.\n\nfix 1.", got)
	})
	t.Run("adds nothing for blank extra", func(t *testing.T) {
		ro := validRole()
		got, err := ro.Render(map[string]string{"TASK": "T1"}, "  \n")
		require.NoError(t, err)
		assert.Equal(t, "Test T1.", got)
	})
	t.Run("refuses a prompt that renders blank", func(t *testing.T) {
		ro := validRole()
		ro.Prompt = "{{if .Vars.TASK}}x{{end}}"
		ro.Vars = []string{"TASK"}
		_, err := ro.Render(nil, "")
		require.ErrorIs(t, err, ErrRender)
	})
}

func TestRole_Args(t *testing.T) {
	t.Run("orders the model, the role's arguments, then the caller's", func(t *testing.T) {
		ro := validRole()
		got := ro.Args("p", []string{"--model", "opus"})
		assert.Equal(t, []string{"p", "--model", "haiku", "--permission-mode", "auto", "--model", "opus"}, got)
	})
	t.Run("is just the prompt for a role with no model and no arguments", func(t *testing.T) {
		ro := validRole()
		ro.Model = ""
		ro.ClaudeArgs = nil
		assert.Equal(t, []string{"p"}, ro.Args("p", nil))
	})
}

func TestBuiltinRoles(t *testing.T) {
	t.Run("lists the three built-in roles, each valid", func(t *testing.T) {
		roles, broken, err := Builtin().List()
		require.NoError(t, err)
		assert.Empty(t, broken)
		require.Len(t, roles, 3)
		byName := map[string]Role{}
		for _, ro := range roles {
			require.NoError(t, ro.Validate(), ro.Name)
			assert.Equal(t, SourceBuiltin, ro.Source, ro.Name)
			assert.Equal(t, []string{"--permission-mode", "auto"}, ro.ClaudeArgs, ro.Name)
			byName[ro.Name] = ro
		}
		require.Contains(t, byName, "implementer")
		require.Contains(t, byName, "reviewer")
		require.Contains(t, byName, "verifier")
		assert.Equal(t, "haiku", byName["implementer"].Model)
		assert.Equal(t, []string{"PASS"}, byName["implementer"].Pass)
		assert.Equal(t, []string{"FAIL"}, byName["implementer"].Fail)
		assert.Equal(t, []string{"TASK", "TASKS", "BASE", "BRANCH"}, byName["implementer"].Vars)
		assert.Equal(t, "sonnet", byName["reviewer"].Model)
		assert.Equal(t, []string{"APPROVED"}, byName["reviewer"].Pass)
		assert.Equal(t, []string{"CHANGES_REQUESTED"}, byName["reviewer"].Fail)
		assert.Equal(t, []string{"TASK", "TASKS", "BASE", "KNOWN_ISSUES"}, byName["reviewer"].Vars)
		assert.Equal(t, "haiku", byName["verifier"].Model)
		assert.Equal(t, []string{"PASS", "FLAKY"}, byName["verifier"].Pass)
		assert.Equal(t, []string{"BRANCH", "CHECKS"}, byName["verifier"].Vars)
	})
	t.Run("implementer renders with its variables", func(t *testing.T) {
		ro := builtinRole(t, "implementer")
		got, err := ro.Render(map[string]string{"TASK": "T2", "TASKS": "tasks.md", "BASE": "main", "BRANCH": "feat/t2"}, "")
		require.NoError(t, err)
		assert.Contains(t, got, "ws machine result PASS --file ../report.md")
		assert.Contains(t, got, "feat/t2")
	})
	t.Run("implementer refuses to render without BRANCH", func(t *testing.T) {
		ro := builtinRole(t, "implementer")
		_, err := ro.Render(map[string]string{"TASK": "T2", "TASKS": "tasks.md", "BASE": "main"}, "")
		require.ErrorIs(t, err, ErrRender)
	})
	t.Run("reviewer omits known problems without KNOWN_ISSUES", func(t *testing.T) {
		ro := builtinRole(t, "reviewer")
		got, err := ro.Render(map[string]string{"TASK": "T2", "TASKS": "tasks.md", "BASE": "main"}, "")
		require.NoError(t, err)
		assert.NotContains(t, got, "known problems")
	})
	t.Run("reviewer names known issues when given", func(t *testing.T) {
		ro := builtinRole(t, "reviewer")
		got, err := ro.Render(map[string]string{"TASK": "T2", "TASKS": "tasks.md", "BASE": "main", "KNOWN_ISSUES": "/x/base.md"}, "")
		require.NoError(t, err)
		assert.Contains(t, got, "/x/base.md are known problems")
	})
}

func builtinRole(t *testing.T, name string) Role {
	t.Helper()
	ro, ok, err := Builtin().Load(name)
	require.NoError(t, err)
	require.True(t, ok)
	return ro
}

func repeat(c rune, n int) string {
	out := make([]rune, n)
	for i := range out {
		out[i] = c
	}
	return string(out)
}
