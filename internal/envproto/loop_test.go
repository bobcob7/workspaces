package envproto

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidate_loop(t *testing.T) {
	t.Run("accepts a loop name in the ADR 0048 form", func(t *testing.T) {
		assert.NoError(t, Validate(&LoopName{Name: "daily-report-1"}))
	})
	t.Run("refuses a name with a slash with name_invalid", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopName{Name: "a/b"}), "name_invalid")
	})
	t.Run("refuses a name that starts with a dash with name_invalid", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopRunArgs{Name: "-a"}), "name_invalid")
	})
	t.Run("refuses a script over 16 KiB with usage", func(t *testing.T) {
		script := strings.Repeat("s", MaxScriptBytes+1)
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Script: &script}), "usage")
	})
	t.Run("accepts a script of exactly 16 KiB", func(t *testing.T) {
		script := strings.Repeat("s", MaxScriptBytes)
		assert.NoError(t, Validate(&LoopSpec{Name: "a", Script: &script}))
	})
	t.Run("accepts an edit that clears the script with an empty string", func(t *testing.T) {
		assert.NoError(t, Validate(&LoopSpec{Name: "a", Script: new("")}))
	})
	t.Run("refuses 33 vars with usage", func(t *testing.T) {
		vars := map[string]string{}
		for i := range MaxVars + 1 {
			vars[fmt.Sprintf("V%d", i)] = "x"
		}
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Vars: &vars}), "usage")
	})
	t.Run("refuses vars over 32 KiB in all with usage", func(t *testing.T) {
		vars := map[string]string{}
		for i := range 9 {
			vars[fmt.Sprintf("V%d", i)] = strings.Repeat("v", MaxVarValueBytes)
		}
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Vars: &vars}), "usage")
	})
	t.Run("refuses a var key starting with a digit with usage", func(t *testing.T) {
		vars := map[string]string{"1A": "x"}
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Vars: &vars}), "usage")
	})
	t.Run("refuses a var value over 4 KiB with usage", func(t *testing.T) {
		vars := map[string]string{"A": strings.Repeat("v", MaxVarValueBytes+1)}
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Vars: &vars}), "usage")
	})
	t.Run("refuses a spec starting with a dash with spec_invalid", func(t *testing.T) {
		specs := []string{"-x"}
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Specs: &specs}), "spec_invalid")
	})
	t.Run("refuses a title over 80 bytes with usage", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Title: new(strings.Repeat("t", 81))}), "usage")
	})
	t.Run("refuses a keep outside 1 to 50 with usage", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Keep: new(0)}), "usage")
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Keep: new(51)}), "usage")
	})
	t.Run("refuses a runner other than claude or ws with usage", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Runner: new("shell")}), "usage")
	})
	t.Run("refuses a permission mode that is not one of PermissionModes with usage", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopSpec{Name: "a", PermissionMode: new("root")}), "usage")
	})
	t.Run("refuses a prompt over MaxPromptBytes with usage", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Prompt: new(strings.Repeat("p", MaxPromptBytes+1))}), "usage")
	})
	t.Run("refuses a memory key starting with a digit with usage", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopMemoryKey{Name: "a", Key: "9x"}), "usage")
	})
	t.Run("refuses a memory value over 64 KiB with usage", func(t *testing.T) {
		err := Validate(&LoopMemorySetArgs{Name: "a", Key: "K", Value: strings.Repeat("v", MaxMemoryValue+1)})
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses a negative expect_rev with usage", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopMemoryKey{Name: "a", Key: "K", ExpectRev: new(-1)}), "usage")
	})
	t.Run("refuses a history limit over MaxHistoryLimit with usage", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopHistoryArgs{Name: "a", Limit: MaxHistoryLimit + 1}), "usage")
	})
	t.Run("refuses a history sequence below 1 with usage", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopHistoryRunArgs{Name: "a", Seq: 0}), "usage")
	})
}

func TestValidateOp_loop(t *testing.T) {
	t.Run("refuses loop.edit with nothing to change with usage", func(t *testing.T) {
		assertArgsError(t, validateOp(OpLoopEdit, &LoopSpec{Name: "a"}), "usage")
	})
	t.Run("refuses loop.add without a prompt with usage", func(t *testing.T) {
		assertArgsError(t, validateOp(OpLoopAdd, &LoopSpec{Name: "a", Every: new("15m")}), "usage")
	})
	t.Run("refuses loop.add without an every with usage", func(t *testing.T) {
		assertArgsError(t, validateOp(OpLoopAdd, &LoopSpec{Name: "a", Prompt: new("p")}), "usage")
	})
	t.Run("refuses paused on loop.edit with usage", func(t *testing.T) {
		assertArgsError(t, validateOp(OpLoopEdit, &LoopSpec{Name: "a", Paused: new(true)}), "usage")
	})
	t.Run("accepts a loop.edit that changes one field", func(t *testing.T) {
		assert.NoError(t, validateOp(OpLoopEdit, &LoopSpec{Name: "a", Keep: new(3)}))
	})
	t.Run("ignores the operation for a type without op rules", func(t *testing.T) {
		assert.NoError(t, validateOp(OpLoopShow, &LoopName{Name: "a"}))
	})
}

func TestDecodeArgs_confirm(t *testing.T) {
	t.Run("loop.rm without confirm is refused with usage", func(t *testing.T) {
		_, err := DecodeArgs(OpLoopRm, json.RawMessage(`{"name":"a"}`))
		assertArgsError(t, err, "usage")
	})
	t.Run("a confirm refusal wraps ErrNeedsConfirm", func(t *testing.T) {
		for op, raw := range map[string]string{
			OpLoopRm:           `{"name":"a"}`,
			OpLoopHistoryClear: `{"name":"a"}`,
			OpLoopMemoryClear:  `{"name":"a"}`,
			OpLoopAdd:          `{"name":"a","every":"15m","prompt":"p","script":"echo hi"}`,
		} {
			_, err := DecodeArgs(op, json.RawMessage(raw))
			assert.ErrorIs(t, err, ErrNeedsConfirm, op)
		}
	})
	t.Run("another refusal does not wrap ErrNeedsConfirm", func(t *testing.T) {
		_, err := DecodeArgs(OpLoopRm, json.RawMessage(`{"name":"a/b","confirm":true}`))
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrNeedsConfirm)
	})
	t.Run("loop.rm with confirm is accepted", func(t *testing.T) {
		_, err := DecodeArgs(OpLoopRm, json.RawMessage(`{"name":"a","confirm":true}`))
		assert.NoError(t, err)
	})
	t.Run("loop.add with a script and no confirm is refused with usage", func(t *testing.T) {
		raw := json.RawMessage(`{"name":"a","every":"15m","prompt":"p","script":"echo hi"}`)
		_, err := DecodeArgs(OpLoopAdd, raw)
		assertArgsError(t, err, "usage")
	})
	t.Run("loop.add with bypassPermissions and no confirm is refused with usage", func(t *testing.T) {
		raw := json.RawMessage(`{"name":"a","every":"15m","prompt":"p","permission_mode":"bypassPermissions"}`)
		_, err := DecodeArgs(OpLoopAdd, raw)
		assertArgsError(t, err, "usage")
	})
	t.Run("loop.add without a script needs no confirm", func(t *testing.T) {
		_, err := DecodeArgs(OpLoopAdd, json.RawMessage(`{"name":"a","every":"15m","prompt":"p"}`))
		assert.NoError(t, err)
	})
	t.Run("loop.edit that removes the script needs no confirm", func(t *testing.T) {
		_, err := DecodeArgs(OpLoopEdit, json.RawMessage(`{"name":"a","script":""}`))
		assert.NoError(t, err)
	})
	t.Run("loop.history_clear and loop.memory_clear without confirm are refused", func(t *testing.T) {
		_, err := DecodeArgs(OpLoopHistoryClear, json.RawMessage(`{"name":"a"}`))
		assertArgsError(t, err, "usage")
		_, err = DecodeArgs(OpLoopMemoryClear, json.RawMessage(`{"name":"a"}`))
		assertArgsError(t, err, "usage")
	})
}

func TestDecodeArgs_redacted(t *testing.T) {
	t.Run("a prompt containing [redacted] is refused with usage", func(t *testing.T) {
		_, err := DecodeArgs(OpLoopAdd, json.RawMessage(`{"name":"a","every":"15m","prompt":"token [redacted]"}`))
		assertArgsError(t, err, "usage")
	})
	t.Run("a memory value containing [redacted] is refused with usage", func(t *testing.T) {
		_, err := DecodeArgs(OpLoopMemorySet, json.RawMessage(`{"name":"a","key":"K","value":"[redacted]"}`))
		assertArgsError(t, err, "usage")
	})
	t.Run("loop.show ignores the marker", func(t *testing.T) {
		assert.True(t, HasRedacted(&LoopSpec{Name: "a", Prompt: new("[redacted]")}))
		assert.NoError(t, validateOp(OpLoopShow, &LoopSpec{Name: "a", Prompt: new("[redacted]")}))
	})
}

func TestSnapshot_loops(t *testing.T) {
	at := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	t.Run("a snapshot without loops decodes to nil", func(t *testing.T) {
		var snap Snapshot
		require.NoError(t, json.Unmarshal([]byte(`{"at":"2026-11-23T00:00:00Z","protocol":2,"workspaces":[],"agents":[]}`), &snap))
		assert.Nil(t, snap.Loops)
	})
	t.Run("an agent's snapshot always has loops, even an empty list", func(t *testing.T) {
		body, err := json.Marshal(Snapshot{At: at, Protocol: Protocol, Workspaces: json.RawMessage(`[]`), Agents: []SnapshotAgent{}, Loops: []SnapshotLoop{}})
		require.NoError(t, err)
		assert.Contains(t, string(body), `"loops":[]`)
	})
	t.Run("a nil loops list marshals as null, not missing", func(t *testing.T) {
		body, err := json.Marshal(Snapshot{At: at, Protocol: 2, Workspaces: json.RawMessage(`[]`), Agents: []SnapshotAgent{}})
		require.NoError(t, err)
		assert.Contains(t, string(body), `"loops":null`)
	})
	t.Run("a snapshot with loops round-trips", func(t *testing.T) {
		next := at.Add(time.Hour)
		in := Snapshot{At: at, Protocol: Protocol, Workspaces: json.RawMessage(`[]`), Agents: []SnapshotAgent{}, Loops: []SnapshotLoop{{
			Name: "daily", Title: "Daily", Every: "24h", Runner: "claude", NextRunAt: &next,
			LastRun: json.RawMessage(`null`), LastGate: json.RawMessage(`null`),
		}}}
		body, err := json.Marshal(in)
		require.NoError(t, err)
		var out Snapshot
		require.NoError(t, json.Unmarshal(body, &out))
		assert.Equal(t, in.Loops[0].Name, out.Loops[0].Name)
		assert.Equal(t, in.Loops[0].NextRunAt.UTC(), out.Loops[0].NextRunAt.UTC())
	})
}

func TestValidate_loopDurations(t *testing.T) {
	t.Run("every that is not a duration is refused", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Every: new("soon")}), "usage")
	})
	t.Run("every starting with a dash is refused", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Every: new("--force")}), "usage")
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Every: new("-5m")}), "usage")
	})
	t.Run("an empty every is refused", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Every: new("")}), "usage")
	})
	t.Run("a zero every is refused", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Every: new("0s")}), "usage")
	})
	t.Run("every with a control character is refused", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Every: new("5m\n")}), "usage")
	})
	t.Run("a valid every is accepted and ws keeps the range check", func(t *testing.T) {
		assert.NoError(t, Validate(&LoopSpec{Name: "a", Every: new("15m")}))
		assert.NoError(t, Validate(&LoopSpec{Name: "a", Every: new("1s")}))
	})
	t.Run("a script_timeout that is not a duration is refused", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopSpec{Name: "a", ScriptTimeout: new("--x")}), "usage")
		assertArgsError(t, Validate(&LoopSpec{Name: "a", ScriptTimeout: new("")}), "usage")
		assertArgsError(t, Validate(&LoopSpec{Name: "a", ScriptTimeout: new("-1s")}), "usage")
		assertArgsError(t, Validate(&LoopSpec{Name: "a", ScriptTimeout: new("0s")}), "usage")
	})
	t.Run("a valid script_timeout is accepted", func(t *testing.T) {
		assert.NoError(t, Validate(&LoopSpec{Name: "a", ScriptTimeout: new("30s")}))
	})
}

func TestValidate_loopText(t *testing.T) {
	t.Run("a script with a NUL is refused", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Script: new("a\x00b")}), "usage")
	})
	t.Run("a prompt with a NUL is refused", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Prompt: new("\x00p")}), "usage")
	})
	t.Run("a var value with a NUL is refused", func(t *testing.T) {
		vars := map[string]string{"A": "x\x00"}
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Vars: &vars}), "usage")
	})
	t.Run("a script, prompt or var value that is not UTF-8 is refused", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Script: new("\xff")}), "usage")
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Prompt: new("\xff")}), "usage")
		vars := map[string]string{"A": "\xff"}
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Vars: &vars}), "usage")
	})
	t.Run("newlines, tabs and carriage returns are allowed in a script and a prompt", func(t *testing.T) {
		assert.NoError(t, Validate(&LoopSpec{Name: "a", Script: new("a\n\tb\r\n"), Prompt: new("p\nq")}))
	})
}

func TestValidate_loopBounds(t *testing.T) {
	t.Run("refuses a negative list offset and accepts 0", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopListArgs{Offset: -1}), "usage")
		assert.NoError(t, Validate(&LoopListArgs{Offset: 0}))
	})
	t.Run("refuses a negative history limit and accepts 0 and the maximum", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopHistoryArgs{Name: "a", Limit: -1}), "usage")
		assert.NoError(t, Validate(&LoopHistoryArgs{Name: "a", Limit: 0}))
		assert.NoError(t, Validate(&LoopHistoryArgs{Name: "a", Limit: MaxHistoryLimit}))
	})
	t.Run("vars of exactly 32 KiB counting keys and values are accepted", func(t *testing.T) {
		vars := map[string]string{}
		for i := range 8 {
			vars[fmt.Sprintf("V%d", i)] = strings.Repeat("v", MaxVarValueBytes-2)
		}
		assert.NoError(t, Validate(&LoopSpec{Name: "a", Vars: &vars}))
	})
	t.Run("vars one byte over 32 KiB are refused because keys count", func(t *testing.T) {
		vars := map[string]string{}
		for i := range 8 {
			vars[fmt.Sprintf("V%d", i)] = strings.Repeat("v", MaxVarValueBytes-2)
		}
		vars["V0"] += "v"
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Vars: &vars}), "usage")
	})
	t.Run("32 vars are accepted", func(t *testing.T) {
		vars := map[string]string{}
		for i := range MaxVars {
			vars[fmt.Sprintf("V%d", i)] = "x"
		}
		assert.NoError(t, Validate(&LoopSpec{Name: "a", Vars: &vars}))
	})
	t.Run("an empty prompt is refused", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Prompt: new("")}), "usage")
	})
	t.Run("a title with a control character is refused", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Title: new("a\x07b")}), "usage")
	})
	t.Run("a title of exactly 80 bytes is accepted", func(t *testing.T) {
		assert.NoError(t, Validate(&LoopSpec{Name: "a", Title: new(strings.Repeat("t", 80))}))
	})
	t.Run("accepts both runners", func(t *testing.T) {
		assert.NoError(t, Validate(&LoopSpec{Name: "a", Runner: new("ws")}))
		assert.NoError(t, Validate(&LoopSpec{Name: "a", Runner: new("claude")}))
	})
	t.Run("refuses an invalid model with usage", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Model: new("-x")}), "usage")
		assert.NoError(t, Validate(&LoopSpec{Name: "a", Model: new("claude-sonnet-5-5")}))
	})
	t.Run("refuses an invalid endpoint with usage", func(t *testing.T) {
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Endpoint: new("-x")}), "usage")
		assertArgsError(t, Validate(&LoopSpec{Name: "a", Endpoint: new("")}), "usage")
		assert.NoError(t, Validate(&LoopSpec{Name: "a", Endpoint: new("local")}))
	})
	t.Run("accepts a 40 character name and refuses 41", func(t *testing.T) {
		require.NoError(t, Validate(&LoopName{Name: strings.Repeat("a", 40)}))
		assertArgsError(t, Validate(&LoopName{Name: strings.Repeat("a", 41)}), "name_invalid")
	})
	t.Run("a snapshot carries at most 100 loops", func(t *testing.T) {
		assert.Equal(t, 100, MaxSnapshotLoops)
	})
}

func TestValidateOp_loopEdit(t *testing.T) {
	t.Run("refuses loop.edit with a redacted value", func(t *testing.T) {
		err := validateOp(OpLoopEdit, &LoopSpec{Name: "a", Prompt: new("token [redacted]")})
		assertArgsError(t, err, "usage")
		assert.ErrorContains(t, err, "redacted")
	})
	t.Run("refuses paused on loop.edit even with another field", func(t *testing.T) {
		err := validateOp(OpLoopEdit, &LoopSpec{Name: "a", Paused: new(true), Keep: new(3)})
		assertArgsError(t, err, "usage")
		assert.ErrorContains(t, err, "paused")
	})
	for name, raw := range map[string]string{
		"vars":           `{"name":"a","vars":{}}`,
		"hidden":         `{"name":"a","hidden":true}`,
		"script_timeout": `{"name":"a","script_timeout":"30s"}`,
		"specs":          `{"name":"a","specs":[]}`,
		"title":          `{"name":"a","title":"t"}`,
		"every":          `{"name":"a","every":"15m"}`,
		"prompt":         `{"name":"a","prompt":"p"}`,
		"runner":         `{"name":"a","runner":"ws"}`,
		"endpoint":       `{"name":"a","endpoint":"local"}`,
		"model":          `{"name":"a","model":"m"}`,
		"permission":     `{"name":"a","permission_mode":"plan"}`,
		"script":         `{"name":"a","script":""}`,
	} {
		t.Run("a loop.edit that sets only "+name+" is a change", func(t *testing.T) {
			_, err := DecodeArgs(OpLoopEdit, json.RawMessage(raw))
			assert.NoError(t, err)
		})
	}
}
