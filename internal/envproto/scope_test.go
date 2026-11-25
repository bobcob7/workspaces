package envproto

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const scopeTestCreate = `{"kind":"claude","specs":[],"name":null,"parent":null,"hidden":false,"offline":false,"prompt":"hi","model":null,"endpoint":null}`

func scopeTestList(n int, item func(i int) string) string {
	items := make([]string, n)
	for i := range items {
		items[i] = item(i)
	}
	return "[" + strings.Join(items, ",") + "]"
}

func scopeTestEnvID(i int) string {
	return fmt.Sprintf("%q", fmt.Sprintf("env-%08x", i))
}

func TestProtocol_scopes(t *testing.T) {
	t.Run("protocol 4 carries scopes", func(t *testing.T) {
		assert.Equal(t, 4, Protocol)
		assert.Equal(t, 4, ProtocolScopes)
	})
}

func TestDecodeArgs_scopes(t *testing.T) {
	t.Run("decodes machine.record as a workspace reference", func(t *testing.T) {
		got, err := DecodeArgs(OpMachineRecord, json.RawMessage(`{"workspace":"buff-bear"}`))
		require.NoError(t, err)
		assert.Equal(t, &WorkspaceArgs{Workspace: "buff-bear"}, got)
	})
	t.Run("decodes a valid scope.envs", func(t *testing.T) {
		raw := json.RawMessage(`{"workspace":"w1","ancestors":["p1"],"scope":["local","env-0000000a"]}`)
		got, err := DecodeArgs(OpScopeEnvs, raw)
		require.NoError(t, err)
		assert.IsType(t, &ScopeEnvsArgs{}, got)
	})
	t.Run("decodes a valid scope.spawn", func(t *testing.T) {
		raw := json.RawMessage(`{"workspace":"w1","ancestors":[],"scope":["local","env-0000000a"],"target":"win-rig","narrow":null,"create":` + scopeTestCreate + `,"spawn":null}`)
		got, err := DecodeArgs(OpScopeSpawn, raw)
		require.NoError(t, err)
		assert.IsType(t, &ScopeSpawnArgs{}, got)
	})
	t.Run("decodes a valid scope.children", func(t *testing.T) {
		raw := json.RawMessage(`{"workspace":"w1","ancestors":[],"scope":["local"],"descendants":["c1","c2"]}`)
		got, err := DecodeArgs(OpScopeChildren, raw)
		require.NoError(t, err)
		assert.IsType(t, &ScopeChildrenArgs{}, got)
	})
	t.Run("decodes a valid scope.child", func(t *testing.T) {
		raw := json.RawMessage(`{"workspace":"w1","ancestors":[],"scope":["local"],"environment":"win-rig","child":"c1","parent":"w1","op":"logs","tail":20,"keep_children":false}`)
		got, err := DecodeArgs(OpScopeChild, raw)
		require.NoError(t, err)
		assert.IsType(t, &ScopeChildArgs{}, got)
	})
	t.Run("refuses an unknown key in each new op with usage", func(t *testing.T) {
		for _, op := range []string{OpMachineRecord, OpScopeEnvs, OpScopeSpawn, OpScopeChildren, OpScopeChild} {
			_, err := DecodeArgs(op, json.RawMessage(`{"workspace":"w1","bogus":1}`))
			assertArgsError(t, err, "usage")
		}
	})
	t.Run("refuses a scope.spawn whose create carries a scope with usage", func(t *testing.T) {
		create := `{"kind":"claude","specs":[],"name":null,"parent":null,"hidden":false,"offline":false,"prompt":"hi","model":null,"endpoint":null,"scope":{"environments":[{"id":"local","name":"local"}],"parent":null}}`
		raw := json.RawMessage(`{"workspace":"w1","ancestors":[],"scope":["local"],"target":"b","narrow":null,"create":` + create + `,"spawn":null}`)
		_, err := DecodeArgs(OpScopeSpawn, raw)
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses a scope.spawn whose create carries a parent with usage", func(t *testing.T) {
		create := `{"kind":"claude","specs":[],"name":null,"parent":"w1","hidden":false,"offline":false,"prompt":"hi","model":null,"endpoint":null}`
		raw := json.RawMessage(`{"workspace":"w1","ancestors":[],"scope":["local"],"target":"b","narrow":null,"create":` + create + `,"spawn":null}`)
		_, err := DecodeArgs(OpScopeSpawn, raw)
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses a scope.spawn with an empty target with usage", func(t *testing.T) {
		raw := json.RawMessage(`{"workspace":"w1","ancestors":[],"scope":["local"],"target":"","narrow":null,"create":` + scopeTestCreate + `,"spawn":null}`)
		_, err := DecodeArgs(OpScopeSpawn, raw)
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses a scope.spawn whose target has a control character with usage", func(t *testing.T) {
		raw := json.RawMessage(`{"workspace":"w1","ancestors":[],"scope":["local"],"target":"b\nc","narrow":null,"create":` + scopeTestCreate + `,"spawn":null}`)
		_, err := DecodeArgs(OpScopeSpawn, raw)
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses a scope with a bad id with usage", func(t *testing.T) {
		raw := json.RawMessage(`{"workspace":"w1","ancestors":[],"scope":["Env-1"]}`)
		_, err := DecodeArgs(OpScopeEnvs, raw)
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses more than 64 scope ids with usage", func(t *testing.T) {
		raw := json.RawMessage(`{"workspace":"w1","ancestors":[],"scope":` + scopeTestList(65, scopeTestEnvID) + `}`)
		_, err := DecodeArgs(OpScopeEnvs, raw)
		assertArgsError(t, err, "usage")
	})
	t.Run("accepts exactly 64 scope ids", func(t *testing.T) {
		raw := json.RawMessage(`{"workspace":"w1","ancestors":[],"scope":` + scopeTestList(64, scopeTestEnvID) + `}`)
		_, err := DecodeArgs(OpScopeEnvs, raw)
		require.NoError(t, err)
	})
	t.Run("refuses more than 64 ancestors with usage", func(t *testing.T) {
		raw := json.RawMessage(`{"workspace":"w1","ancestors":` + scopeTestList(65, func(i int) string { return fmt.Sprintf("%q", "a") }) + `,"scope":["local"]}`)
		_, err := DecodeArgs(OpScopeEnvs, raw)
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses an ancestor that is not a single path element with usage", func(t *testing.T) {
		for _, ancestor := range []string{"a/b", `a\b`, ".hidden"} {
			raw := json.RawMessage(fmt.Sprintf(`{"workspace":"w1","ancestors":[%q],"scope":["local"]}`, ancestor))
			_, err := DecodeArgs(OpScopeEnvs, raw)
			assertArgsError(t, err, "usage")
		}
	})
	t.Run("refuses more than 1024 descendants with usage", func(t *testing.T) {
		raw := json.RawMessage(`{"workspace":"w1","ancestors":[],"scope":["local"],"descendants":` + scopeTestList(1025, func(i int) string { return fmt.Sprintf("%q", "c") }) + `}`)
		_, err := DecodeArgs(OpScopeChildren, raw)
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses more than 64 narrow names with usage", func(t *testing.T) {
		raw := json.RawMessage(`{"workspace":"w1","ancestors":[],"scope":["local"],"target":"b","narrow":` + scopeTestList(65, func(i int) string { return `"local"` }) + `,"create":` + scopeTestCreate + `,"spawn":null}`)
		_, err := DecodeArgs(OpScopeSpawn, raw)
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses an op outside the four child operations with usage", func(t *testing.T) {
		for _, op := range []string{"files", "rm ", "", "spawn"} {
			raw := json.RawMessage(fmt.Sprintf(`{"workspace":"w1","ancestors":[],"scope":["local"],"environment":"b","child":"c1","parent":"w1","op":%q,"tail":0,"keep_children":false}`, op))
			_, err := DecodeArgs(OpScopeChild, raw)
			assertArgsError(t, err, "usage")
		}
	})
	t.Run("refuses a tail over MaxTail with usage", func(t *testing.T) {
		raw := json.RawMessage(fmt.Sprintf(`{"workspace":"w1","ancestors":[],"scope":["local"],"environment":"b","child":"c1","parent":"w1","op":"logs","tail":%d,"keep_children":false}`, MaxTail+1))
		_, err := DecodeArgs(OpScopeChild, raw)
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses a negative tail with usage", func(t *testing.T) {
		raw := json.RawMessage(`{"workspace":"w1","ancestors":[],"scope":["local"],"environment":"b","child":"c1","parent":"w1","op":"logs","tail":-1,"keep_children":false}`)
		_, err := DecodeArgs(OpScopeChild, raw)
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses a child name that is not a single path element with usage", func(t *testing.T) {
		for _, child := range []string{"a/b", `a\b`, ".x"} {
			raw := json.RawMessage(fmt.Sprintf(`{"workspace":"w1","ancestors":[],"scope":["local"],"environment":"b","child":%q,"parent":"w1","op":"status","tail":0,"keep_children":false}`, child))
			_, err := DecodeArgs(OpScopeChild, raw)
			assertArgsError(t, err, "usage")
		}
	})
}

func TestValidate_scopeGrant(t *testing.T) {
	t.Run("accepts a grant of one to 64 valid refs", func(t *testing.T) {
		raw := json.RawMessage(`{"kind":"claude","specs":[],"scope":{"environments":[{"id":"local","name":"local"},{"id":"env-0000000a","name":"win-rig"}],"parent":null}}`)
		_, err := DecodeArgs(OpWorkspaceCreate, raw)
		require.NoError(t, err)
	})
	t.Run("refuses an empty grant with usage", func(t *testing.T) {
		raw := json.RawMessage(`{"kind":"claude","specs":[],"scope":{"environments":[],"parent":null}}`)
		_, err := DecodeArgs(OpWorkspaceCreate, raw)
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses more than 64 refs with usage", func(t *testing.T) {
		envs := scopeTestList(65, func(i int) string {
			return fmt.Sprintf(`{"id":%s,"name":"n"}`, scopeTestEnvID(i))
		})
		raw := json.RawMessage(`{"kind":"claude","specs":[],"scope":{"environments":` + envs + `,"parent":null}}`)
		_, err := DecodeArgs(OpWorkspaceCreate, raw)
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses a ref id that is neither local nor a valid id with usage", func(t *testing.T) {
		raw := json.RawMessage(`{"kind":"claude","specs":[],"scope":{"environments":[{"id":"../etc","name":"x"}],"parent":null}}`)
		_, err := DecodeArgs(OpWorkspaceCreate, raw)
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses a ref with an empty name with usage", func(t *testing.T) {
		raw := json.RawMessage(`{"kind":"claude","specs":[],"scope":{"environments":[{"id":"local","name":""}],"parent":null}}`)
		_, err := DecodeArgs(OpWorkspaceCreate, raw)
		assertArgsError(t, err, "usage")
	})
}

func TestValidate_spawnInfo(t *testing.T) {
	withSpawn := func(spawn string) json.RawMessage {
		return json.RawMessage(`{"kind":"claude","specs":[],"prompt":"hi","scope":null,"spawn":` + spawn + `}`)
	}
	t.Run("accepts a role of one to 64 bytes", func(t *testing.T) {
		_, err := DecodeArgs(OpWorkspaceCreate, withSpawn(`{"role":"helper","model":"","pass":[],"fail":[],"vars":{}}`))
		require.NoError(t, err)
	})
	t.Run("refuses an empty role with usage", func(t *testing.T) {
		_, err := DecodeArgs(OpWorkspaceCreate, withSpawn(`{"role":"","model":"","pass":[],"fail":[],"vars":{}}`))
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses a role over 64 bytes with usage", func(t *testing.T) {
		role := strings.Repeat("r", 65)
		_, err := DecodeArgs(OpWorkspaceCreate, withSpawn(fmt.Sprintf(`{"role":%q,"model":"","pass":[],"fail":[],"vars":{}}`, role)))
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses more than 16 pass entries with usage", func(t *testing.T) {
		pass := scopeTestList(17, func(i int) string { return `"go test"` })
		_, err := DecodeArgs(OpWorkspaceCreate, withSpawn(`{"role":"r","model":"","pass":`+pass+`,"fail":[],"vars":{}}`))
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses a fail entry over 32 bytes with usage", func(t *testing.T) {
		fail := fmt.Sprintf("[%q]", strings.Repeat("f", 33))
		_, err := DecodeArgs(OpWorkspaceCreate, withSpawn(`{"role":"r","model":"","pass":[],"fail":`+fail+`,"vars":{}}`))
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses more than 64 vars with usage", func(t *testing.T) {
		vars := make([]string, 65)
		for i := range vars {
			vars[i] = fmt.Sprintf("%q:\"v\"", fmt.Sprintf("K%d", i))
		}
		_, err := DecodeArgs(OpWorkspaceCreate, withSpawn(`{"role":"r","model":"","pass":[],"fail":[],"vars":{`+strings.Join(vars, ",")+`}}`))
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses a model that starts with a dash with usage", func(t *testing.T) {
		_, err := DecodeArgs(OpWorkspaceCreate, withSpawn(`{"role":"r","model":"--dangerously-skip-permissions","pass":[],"fail":[],"vars":{}}`))
		assertArgsError(t, err, "usage")
	})
}

func TestCreateArgs_scope(t *testing.T) {
	t.Run("nil scope and spawn marshal to the protocol 3 bytes", func(t *testing.T) {
		body, err := json.Marshal(CreateArgs{Kind: KindClaude, Specs: []string{}})
		require.NoError(t, err)
		assert.JSONEq(t, `{"kind":"claude","specs":[],"name":null,"parent":null,"hidden":false,"offline":false,"prompt":null,"model":null,"endpoint":null}`, string(body))
		assert.NotContains(t, string(body), "scope")
		assert.NotContains(t, string(body), "spawn")
	})
}

func TestScopeSpawnArgs_create(t *testing.T) {
	t.Run("a create with Scope set is refused with usage", func(t *testing.T) {
		args := &ScopeSpawnArgs{
			ScopeCallerArgs: ScopeCallerArgs{Ancestors: []string{}, Scope: []string{"local"}},
			Target:          "b",
			Create: CreateArgs{Kind: KindClaude, Specs: []string{}, Scope: &ScopeGrant{
				Environments: []EnvRef{{ID: Local, Name: Local}},
			}},
		}
		assertArgsError(t, Validate(args), "usage")
	})
	t.Run("a create with Parent set is refused with usage", func(t *testing.T) {
		parent := "w1"
		args := &ScopeSpawnArgs{
			ScopeCallerArgs: ScopeCallerArgs{Ancestors: []string{}, Scope: []string{"local"}},
			Target:          "b",
			Create:          CreateArgs{Kind: KindClaude, Specs: []string{}, Parent: &parent},
		}
		assertArgsError(t, Validate(args), "usage")
	})
}
