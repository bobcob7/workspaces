package cli

import (
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/gate"
	"github.com/bobcob7/workspaces/internal/loop"
	"github.com/bobcob7/workspaces/internal/redact"
)

// loopEnv is the agent's operations over a real loop store in temp dirs.
type loopEnv struct {
	*tickEnv
	ops *agentOperations
}

func newLoopEnv(t *testing.T) *loopEnv {
	t.Helper()
	e := &loopEnv{tickEnv: newTickEnv(t)}
	e.ops = reviewOps(e.app)
	e.ops.redactor = redact.New("hunter2-secret")
	return e
}

// forbidScripts makes the machine's config.toml say agent_loop_scripts = false.
func (e *loopEnv) forbidScripts() {
	off := false
	loader, ok := e.app.configs.(*configLoaderMock)
	if !ok {
		panic("the test App's config loader is not a mock")
	}
	paths := e.app.paths
	loader.LoadFunc = func(string) (config.Paths, config.Config, error) {
		cfg := testConfig()
		cfg.Environments.AgentLoopScripts = &off
		return paths, cfg, nil
	}
}

// call decodes raw as op's arguments, as the agent does, and runs it.
func (e *loopEnv) call(t *testing.T, op, raw string) (map[string]any, error) {
	t.Helper()
	return callReview(t, e.ops, op, raw)
}

func (e *loopEnv) get(t *testing.T, name string) loop.Loop { //nolint:unparam // Every test reads loop deps today.
	t.Helper()
	l, err := e.loops.Get(name)
	require.NoError(t, err)
	return l
}

// seed stores the memory values of loop deps.
func (e *loopEnv) seed(t *testing.T, values map[string]string) {
	t.Helper()
	for key, value := range values {
		_, err := e.data.SetMemory(t.Context(), "deps", key, value, "test", loop.AnyRev, testNow())
		require.NoError(t, err)
	}
}

func (e *loopEnv) addPlain(t *testing.T, name string) {
	t.Helper()
	l := loop.Loop{Version: loop.FormatVersion, Name: name, Every: "1h", Prompt: "Do it", Keep: 5, CreatedAt: testNow(), NextRunAt: testNow().Add(time.Hour)}
	require.NoError(t, e.loops.Create(&l))
}

func argvAfter(argv []string, marker string) []string {
	for i, a := range argv {
		if a == marker {
			return argv[i+1:]
		}
	}
	return nil
}

func TestAgentOperations_loop(t *testing.T) {
	t.Run("add runs ws loop add with the prompt as one argument", func(t *testing.T) {
		e := newLoopEnv(t)
		got, err := e.call(t, envproto.OpLoopAdd, `{"name":"deps","every":"1h","prompt":"Bump the  modules, please","specs":["buff-bear"],"title":"Nightly"}`)
		require.NoError(t, err)
		assert.Equal(t, "deps", got["name"])
		l := e.get(t, "deps")
		assert.Equal(t, "Bump the  modules, please", l.Prompt)
		assert.Equal(t, "Nightly", l.Title)
		assert.Equal(t, []string{"buff-bear"}, l.Specs)
		assert.EqualValues(t, 0, got["redacted"])
	})
	t.Run("a prompt starting with -- is not read as a flag", func(t *testing.T) {
		e := newLoopEnv(t)
		_, err := e.call(t, envproto.OpLoopAdd, `{"name":"deps","every":"1h","prompt":"--every 5m --script x"}`)
		require.NoError(t, err)
		l := e.get(t, "deps")
		assert.Equal(t, "--every 5m --script x", l.Prompt)
		assert.Equal(t, "1h", l.Every)
		assert.Empty(t, l.Script)
	})
	t.Run("a var value with = and spaces arrives intact", func(t *testing.T) {
		e := newLoopEnv(t)
		_, err := e.call(t, envproto.OpLoopAdd, `{"name":"deps","every":"1h","prompt":"p","vars":{"QUERY":"a=b c  d"}}`)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"QUERY": "a=b c  d"}, e.get(t, "deps").Vars)
	})
	t.Run("edit with specs [] passes --no-specs", func(t *testing.T) {
		e := newLoopEnv(t)
		_, err := e.call(t, envproto.OpLoopAdd, `{"name":"deps","every":"1h","prompt":"p","specs":["buff-bear"]}`)
		require.NoError(t, err)
		_, err = e.call(t, envproto.OpLoopEdit, `{"name":"deps","specs":[]}`)
		require.NoError(t, err)
		assert.Empty(t, e.get(t, "deps").Specs)
	})
	t.Run("edit with script \"\" passes --no-script", func(t *testing.T) {
		e := newLoopEnv(t)
		_, err := e.call(t, envproto.OpLoopAdd, `{"name":"deps","every":"1h","prompt":"p","script":"true","confirm":true}`)
		require.NoError(t, err)
		require.Equal(t, "true", e.get(t, "deps").Script)
		_, err = e.call(t, envproto.OpLoopEdit, `{"name":"deps","script":""}`)
		require.NoError(t, err)
		assert.Empty(t, e.get(t, "deps").Script)
	})
	t.Run("a script is refused when agent_loop_scripts is false", func(t *testing.T) {
		e := newLoopEnv(t)
		e.forbidScripts()
		_, err := e.call(t, envproto.OpLoopAdd, `{"name":"deps","every":"1h","prompt":"p","script":"true","confirm":true}`)
		require.Error(t, err)
		re := replyOf(t, err)
		assert.Equal(t, "usage", re.Code)
		assert.Equal(t, "scripts from the controller are turned off on linux-server", re.Message)
		_, getErr := e.loops.Get("deps")
		require.Error(t, getErr, "the loop must not be created")
	})
	t.Run("a script is refused (internal) when the config can't be read", func(t *testing.T) {
		e := newLoopEnv(t)
		loader, ok := e.app.configs.(*configLoaderMock)
		require.True(t, ok)
		loader.LoadFunc = func(string) (config.Paths, config.Config, error) {
			return config.Paths{}, config.Config{}, errors.New("config.toml is unreadable")
		}
		wsRuns := 0
		newApp, newAppWithStdin := e.ops.newApp, e.ops.newAppWithStdin
		e.ops.newApp = func(stdout, stderr io.Writer) *App {
			wsRuns++
			return newApp(stdout, stderr)
		}
		e.ops.newAppWithStdin = func(stdin io.Reader, stdout, stderr io.Writer) *App {
			wsRuns++
			return newAppWithStdin(stdin, stdout, stderr)
		}
		_, err := e.call(t, envproto.OpLoopAdd, `{"name":"deps","every":"1h","prompt":"p","script":"true","confirm":true}`)
		requireReplyCode(t, err, "internal")
		assert.Zero(t, wsRuns, "ws must not run")
		_, getErr := e.loops.Get("deps")
		require.Error(t, getErr, "the loop must not be created")
	})
	t.Run("an edit that sets a script is refused when agent_loop_scripts is false", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addPlain(t, "deps")
		e.forbidScripts()
		_, err := e.call(t, envproto.OpLoopEdit, `{"name":"deps","script":"rm -rf x","confirm":true}`)
		requireReplyCode(t, err, "usage")
		assert.Empty(t, e.get(t, "deps").Script)
	})
	t.Run("a loop without a script is added when agent_loop_scripts is false", func(t *testing.T) {
		e := newLoopEnv(t)
		e.forbidScripts()
		_, err := e.call(t, envproto.OpLoopAdd, `{"name":"deps","every":"1h","prompt":"p"}`)
		require.NoError(t, err)
	})
	t.Run("removing a script is allowed when agent_loop_scripts is false", func(t *testing.T) {
		e := newLoopEnv(t)
		l := loop.Loop{Version: loop.FormatVersion, Name: "deps", Every: "1h", Prompt: "p", Keep: 5, Script: "true", CreatedAt: testNow(), NextRunAt: testNow().Add(time.Hour)}
		require.NoError(t, e.loops.Create(&l))
		e.forbidScripts()
		_, err := e.call(t, envproto.OpLoopEdit, `{"name":"deps","script":""}`)
		require.NoError(t, err)
		assert.Empty(t, e.get(t, "deps").Script)
	})
	t.Run("rm without confirm never reaches ws", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addPlain(t, "deps")
		_, err := envproto.DecodeArgs(envproto.OpLoopRm, json.RawMessage(`{"name":"deps"}`))
		require.ErrorIs(t, err, envproto.ErrNeedsConfirm)
		_, err = e.ops.Run(t.Context(), envproto.OpLoopRm, &envproto.LoopName{Name: "deps"})
		re := replyOf(t, err)
		assert.Equal(t, "usage", re.Code)
		assert.Equal(t, map[string]any{"needs": "confirm"}, re.Details)
		e.get(t, "deps")
	})
	t.Run("destructive operations without confirm never reach ws", func(t *testing.T) {
		for _, op := range []string{envproto.OpLoopHistoryClear, envproto.OpLoopMemoryClear} {
			e := newLoopEnv(t)
			e.addPlain(t, "deps")
			e.seed(t, map[string]string{"k": "v"})
			_, err := e.ops.Run(t.Context(), op, &envproto.LoopName{Name: "deps"})
			assert.Equal(t, map[string]any{"needs": "confirm"}, replyOf(t, err).Details, op)
			mem, readErr := e.data.ReadMemory("deps")
			require.NoError(t, readErr)
			assert.Contains(t, mem.Entries, "k", op)
		}
	})
	t.Run("a script or bypassPermissions without confirm never reaches ws", func(t *testing.T) {
		e := newLoopEnv(t)
		script, mode := "true", envproto.PermissionBypass
		every, prompt := "1h", "p"
		for _, spec := range []*envproto.LoopSpec{
			{Name: "deps", Every: &every, Prompt: &prompt, Script: &script},
			{Name: "deps", Every: &every, Prompt: &prompt, PermissionMode: &mode},
		} {
			_, err := e.ops.Run(t.Context(), envproto.OpLoopAdd, spec)
			assert.Equal(t, map[string]any{"needs": "confirm"}, replyOf(t, err).Details)
		}
		_, err := e.loops.Get("deps")
		require.Error(t, err)
	})
	t.Run("a [redacted] value is refused before ws", func(t *testing.T) {
		e := newLoopEnv(t)
		every, prompt := "1h", "token is [redacted]"
		_, err := e.ops.Run(t.Context(), envproto.OpLoopAdd, &envproto.LoopSpec{Name: "deps", Every: &every, Prompt: &prompt})
		re := replyOf(t, err)
		assert.Equal(t, "usage", re.Code)
		assert.Contains(t, re.Message, "[redacted]")
		_, err = e.loops.Get("deps")
		require.Error(t, err)
		e.addPlain(t, "other")
		_, err = e.ops.Run(t.Context(), envproto.OpLoopMemorySet, &envproto.LoopMemorySetArgs{Name: "other", Key: "k", Value: "[redacted]"})
		requireReplyCode(t, err, "usage")
	})
	t.Run("memory_set passes the value on stdin", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addPlain(t, "deps")
		got, err := e.call(t, envproto.OpLoopMemorySet, `{"name":"deps","key":"notes","value":"line one\nline two"}`)
		require.NoError(t, err)
		assert.Equal(t, "deps", got["loop"])
		mem, err := e.data.ReadMemory("deps")
		require.NoError(t, err)
		assert.Equal(t, "line one\nline two", mem.Entries["notes"].Value)
	})
	t.Run("memory_list keeps a value of 1 KiB whole and cuts one byte more", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addPlain(t, "deps")
		exact := strings.Repeat("a", envproto.MaxMemoryPreview)
		over := strings.Repeat("a", envproto.MaxMemoryPreview+1)
		multibyte := strings.Repeat("a", envproto.MaxMemoryPreview-2) + "é"
		e.seed(t, map[string]string{"exact": exact, "over": over, "multibyte": multibyte})
		got, err := e.call(t, envproto.OpLoopMemoryList, `{"name":"deps"}`)
		require.NoError(t, err)
		entries, ok := got["entries"].([]any)
		require.True(t, ok)
		byKey := map[string]map[string]any{}
		for _, entry := range entries {
			m, isMap := entry.(map[string]any)
			require.True(t, isMap)
			key, _ := m["key"].(string)
			byKey[key] = m
		}
		assert.Equal(t, exact, byKey["exact"]["value"])
		assert.NotContains(t, byKey["exact"], "value_truncated")
		assert.Equal(t, multibyte, byKey["multibyte"]["value"])
		assert.NotContains(t, byKey["multibyte"], "value_truncated")
		overValue, _ := byKey["over"]["value"].(string)
		assert.LessOrEqual(t, len(overValue), envproto.MaxMemoryPreview)
		assert.True(t, utf8.ValidString(overValue))
		assert.Equal(t, true, byKey["over"]["value_truncated"])
	})
	t.Run("an empty memory value is stored", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addPlain(t, "deps")
		_, err := e.call(t, envproto.OpLoopMemorySet, `{"name":"deps","key":"notes","value":""}`)
		require.NoError(t, err)
		mem, err := e.data.ReadMemory("deps")
		require.NoError(t, err)
		assert.Contains(t, mem.Entries, "notes")
	})
	t.Run("a memory value starting with a dash is stored as is", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addPlain(t, "deps")
		_, err := e.call(t, envproto.OpLoopMemorySet, `{"name":"deps","key":"notes","value":"--expect-rev 9 -f"}`)
		require.NoError(t, err)
		mem, err := e.data.ReadMemory("deps")
		require.NoError(t, err)
		assert.Equal(t, "--expect-rev 9 -f", mem.Entries["notes"].Value)
	})
	t.Run("memory_set honours expect_rev", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addPlain(t, "deps")
		_, err := e.call(t, envproto.OpLoopMemorySet, `{"name":"deps","key":"k","value":"a","expect_rev":3}`)
		requireReplyCode(t, err, "memory_conflict")
	})
	t.Run("memory_list previews values to 1 KiB", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addPlain(t, "deps")
		e.seed(t, map[string]string{
			"big":   strings.Repeat("é", 3000),
			"small": "tiny",
		})
		got, err := e.call(t, envproto.OpLoopMemoryList, `{"name":"deps"}`)
		require.NoError(t, err)
		entries, ok := got["entries"].([]any)
		require.True(t, ok)
		byKey := map[string]map[string]any{}
		for _, entry := range entries {
			m, isMap := entry.(map[string]any)
			require.True(t, isMap)
			key, _ := m["key"].(string)
			byKey[key] = m
		}
		big, _ := byKey["big"]["value"].(string)
		assert.LessOrEqual(t, len(big), envproto.MaxMemoryPreview)
		assert.Greater(t, len(big), envproto.MaxMemoryPreview-4)
		assert.Equal(t, true, byKey["big"]["value_truncated"])
		assert.Equal(t, "tiny", byKey["small"]["value"])
		assert.NotContains(t, byKey["small"], "value_truncated")
	})
	t.Run("memory_get returns the whole value", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addPlain(t, "deps")
		long := strings.Repeat("x", 5000)
		e.seed(t, map[string]string{"k": long})
		got, err := e.call(t, envproto.OpLoopMemoryGet, `{"name":"deps","key":"k"}`)
		require.NoError(t, err)
		entry, _ := got["entry"].(map[string]any)
		assert.Equal(t, long, entry["value"])
	})
	t.Run("memory_rm removes the key", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addPlain(t, "deps")
		e.seed(t, map[string]string{"k": "v"})
		got, err := e.call(t, envproto.OpLoopMemoryRm, `{"name":"deps","key":"k","expect_rev":1}`)
		require.NoError(t, err)
		assert.Equal(t, true, got["removed"])
	})
	t.Run("memory_clear with confirm empties the memory", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addPlain(t, "deps")
		e.seed(t, map[string]string{"k": "v"})
		_, err := e.call(t, envproto.OpLoopMemoryClear, `{"name":"deps","confirm":true}`)
		require.NoError(t, err)
		mem, err := e.data.ReadMemory("deps")
		require.NoError(t, err)
		assert.Empty(t, mem.Entries)
	})
	t.Run("rm with confirm deletes the loop", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addPlain(t, "deps")
		_, err := e.call(t, envproto.OpLoopRm, `{"name":"deps","confirm":true}`)
		require.NoError(t, err)
		_, err = e.loops.Get("deps")
		require.Error(t, err)
	})
	t.Run("pause and resume change the loop", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addPlain(t, "deps")
		_, err := e.call(t, envproto.OpLoopPause, `{"name":"deps"}`)
		require.NoError(t, err)
		assert.True(t, e.get(t, "deps").Paused)
		_, err = e.call(t, envproto.OpLoopResume, `{"name":"deps"}`)
		require.NoError(t, err)
		assert.False(t, e.get(t, "deps").Paused)
	})
	t.Run("history lists runs and history_clear with confirm empties them", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addPlain(t, "deps")
		writeHistory(t, e, []byte("# done\n"))
		got, err := e.call(t, envproto.OpLoopHistory, `{"name":"deps","limit":5}`)
		require.NoError(t, err)
		assert.EqualValues(t, 1, got["total"])
		got, err = e.call(t, envproto.OpLoopHistoryClear, `{"name":"deps","confirm":true}`)
		require.NoError(t, err)
		assert.EqualValues(t, 1, got["removed"])
	})
	t.Run("history_run cuts a 1 MiB report to 160 KiB", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addPlain(t, "deps")
		writeHistory(t, e, []byte(strings.Repeat("r", 1<<20)))
		got, err := e.call(t, envproto.OpLoopHistoryRun, `{"name":"deps","seq":1}`)
		require.NoError(t, err)
		text, _ := got["report_text"].(string)
		assert.Len(t, text, envproto.MaxReportText)
		assert.Equal(t, true, got["report_truncated"])
	})
	t.Run("history_run keeps a small report whole", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addPlain(t, "deps")
		writeHistory(t, e, []byte("# done\n"))
		got, err := e.call(t, envproto.OpLoopHistoryRun, `{"name":"deps","seq":1}`)
		require.NoError(t, err)
		assert.Equal(t, "# done\n", got["report_text"])
		assert.NotContains(t, got, "report_truncated")
	})
	t.Run("a token in a memory value is redacted in memory_get", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addPlain(t, "deps")
		token := "ghp_" + strings.Repeat("A", 36)
		e.seed(t, map[string]string{"k": "use " + token + " and hunter2-secret"})
		got, err := e.call(t, envproto.OpLoopMemoryGet, `{"name":"deps","key":"k"}`)
		require.NoError(t, err)
		raw, _ := json.Marshal(got)
		assert.NotContains(t, string(raw), "ghp_")
		assert.NotContains(t, string(raw), "hunter2-secret")
		assert.EqualValues(t, 2, got["redacted"])
	})
	t.Run("a token in a loop prompt is redacted in show and list", func(t *testing.T) {
		e := newLoopEnv(t)
		l := loop.Loop{Version: loop.FormatVersion, Name: "deps", Every: "1h", Prompt: "use ghp_" + strings.Repeat("B", 36), Keep: 5, CreatedAt: testNow(), NextRunAt: testNow().Add(time.Hour)}
		require.NoError(t, e.loops.Create(&l))
		for _, op := range []string{envproto.OpLoopShow, envproto.OpLoopList} {
			raw := `{"name":"deps"}`
			if op == envproto.OpLoopList {
				raw = `{}`
			}
			got, err := e.call(t, op, raw)
			require.NoError(t, err)
			out, _ := json.Marshal(got)
			assert.NotContains(t, string(out), "ghp_", op)
			assert.Contains(t, string(out), envproto.RedactedMarker, op)
			assert.EqualValues(t, 1, got["redacted"], op)
		}
	})
	t.Run("a token in an error message is redacted", func(t *testing.T) {
		e := newLoopEnv(t)
		_, err := e.call(t, envproto.OpLoopHistory, `{"name":"hunter2-secret"}`)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "hunter2-secret")
	})
	t.Run("a loop list over 192 KiB is paged", func(t *testing.T) {
		e := newLoopEnv(t)
		for i := range 8 {
			l := loop.Loop{Version: loop.FormatVersion, Name: "loop-" + string(rune('a'+i)), Every: "1h", Prompt: strings.Repeat("p", 60<<10), Keep: 5, CreatedAt: testNow(), NextRunAt: testNow().Add(time.Hour)}
			require.NoError(t, e.loops.Create(&l))
		}
		seen := 0
		offset := 0
		pages := 0
		for {
			got, err := e.call(t, envproto.OpLoopList, `{"offset":`+itoa(offset)+`}`)
			require.NoError(t, err)
			raw, _ := json.Marshal(got)
			assert.LessOrEqual(t, len(raw), envproto.MaxRequestBytes-resultSlack)
			loops, _ := got["loops"].([]any)
			require.NotEmpty(t, loops)
			seen += len(loops)
			pages++
			assert.EqualValues(t, 8, got["total"])
			next, more := got["next_offset"].(float64)
			if !more {
				break
			}
			require.Greater(t, int(next), offset)
			offset = int(next)
		}
		assert.Equal(t, 8-0, seen)
		assert.GreaterOrEqual(t, pages, 3)
	})
	t.Run("an offset past the end is an empty last page", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addPlain(t, "deps")
		got, err := e.call(t, envproto.OpLoopList, `{"offset":5}`)
		require.NoError(t, err)
		assert.Empty(t, got["loops"])
		assert.Nil(t, got["next_offset"])
	})
	t.Run("show of an unknown loop is loop_not_found", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addPlain(t, "deps")
		_, err := e.call(t, envproto.OpLoopShow, `{"name":"nope"}`)
		requireReplyCode(t, err, "loop_not_found")
	})
	t.Run("show gives the loop, its warnings and redacted", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addPlain(t, "deps")
		got, err := e.call(t, envproto.OpLoopShow, `{"name":"deps"}`)
		require.NoError(t, err)
		l, _ := got["loop"].(map[string]any)
		assert.Equal(t, "deps", l["name"])
		assert.Equal(t, []any{}, got["warnings"])
		assert.EqualValues(t, 0, got["redacted"])
	})
	t.Run("run --force passes --force", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addGatedLoop(t, "Do it", nil)
		g := &fakeGate{result: gate.Result{ExitCode: 1}}
		e.app.gate = g
		_, err := e.call(t, envproto.OpLoopRun, `{"name":"deps","force":true}`)
		require.NoError(t, err)
		assert.Empty(t, g.requests, "the gate must not run with --force")
		assert.Len(t, e.claude.StartCalls(), 1)
	})
	t.Run("loop_skipped from the gate keeps its code", func(t *testing.T) {
		e := newLoopEnv(t)
		e.addGatedLoop(t, "Do it", nil)
		e.app.gate = &fakeGate{result: gate.Result{ExitCode: 1}}
		_, err := e.call(t, envproto.OpLoopRun, `{"name":"deps"}`)
		requireReplyCode(t, err, "loop_skipped")
		assert.Empty(t, e.claude.StartCalls())
	})
	t.Run("an unknown loop op is usage", func(t *testing.T) {
		e := newLoopEnv(t)
		_, err := e.ops.Run(t.Context(), "loop.nope", &envproto.LoopName{Name: "deps"})
		requireReplyCode(t, err, "usage")
	})
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

// writeHistory gives loop deps one succeeded run, run 1, with report.
func writeHistory(t *testing.T, e *loopEnv, report []byte) {
	t.Helper()
	started := testNow().Add(-2 * time.Hour)
	finished := started.Add(time.Hour)
	h := loop.History{Version: loop.HistoryVersion, NextSeq: 2, Entries: []loop.Entry{{
		Seq: 1, Status: loop.StatusSucceeded, Trigger: loop.TriggerTick, StartedAt: started, FinishedAt: &finished,
		Workspace: "loop-deps-aaaa1111", Session: "e8cdf5d8", Repeats: 1,
		Verdict: &loop.Verdict{Verdict: "PASS", Outcome: "pass", RecordedAt: finished, Report: true},
	}}}
	require.NoError(t, e.data.WriteHistory("deps", &h))
	require.NoError(t, e.data.WriteReport("deps", 1, report))
}

func TestLoopAddArgv(t *testing.T) {
	str := func(s string) *string { return &s }
	num := func(n int) *int { return &n }
	flag := func(b bool) *bool { return &b }
	prefix := []string{"--json", "--config-dir", "/cfg", "loop"}
	t.Run("add puts text in one element each and specs after --", func(t *testing.T) {
		spec := &envproto.LoopSpec{
			Name: "deps", Every: str("1h"), Prompt: str("--do this"), Title: str("T"), Keep: num(3),
			Specs: &[]string{"a", "b"}, Paused: flag(true), Hidden: flag(false),
			Script: str("echo -n hi"), ScriptTimeout: str("30s"), Vars: &map[string]string{"B": "2 =", "A": "1"},
			Runner: str("ws"), Endpoint: str("e"), Model: str("m"), PermissionMode: str("plan"),
		}
		want := append(append([]string{}, prefix...), "add",
			"--endpoint=e", "--every=1h", "--model=m", "--permission-mode=plan", "--prompt=--do this", "--runner=ws",
			"--script-timeout=30s", "--title=T", "--keep=3", "--hidden=false", "--paused", "--script=echo -n hi",
			"--var=A=1", "--var=B=2 =", "--", "deps", "a", "b")
		assert.Equal(t, want, loopAddArgv("/cfg", spec))
	})
	t.Run("add leaves out a field that is not given and an empty script", func(t *testing.T) {
		spec := &envproto.LoopSpec{Name: "deps", Every: str("1h"), Prompt: str("p"), Script: str("")}
		assert.Equal(t, append(append([]string{}, prefix...), "add", "--every=1h", "--prompt=p", "--", "deps"), loopAddArgv("/cfg", spec))
	})
	t.Run("edit maps empty lists and script to the no- flags", func(t *testing.T) {
		spec := &envproto.LoopSpec{Name: "deps", Script: str(""), Vars: &map[string]string{}, Specs: &[]string{}}
		assert.Equal(t, append(append([]string{}, prefix...), "edit", "--no-script", "--no-vars", "--no-specs", "--", "deps"), loopEditArgv("/cfg", spec))
	})
	t.Run("edit with values passes them and the specs after --", func(t *testing.T) {
		spec := &envproto.LoopSpec{Name: "deps", Title: str(""), Script: str("x"), Vars: &map[string]string{"K": "v"}, Specs: &[]string{"s"}}
		assert.Equal(t, append(append([]string{}, prefix...), "edit", "--title=", "--script=x", "--var=K=v", "--", "deps", "s"), loopEditArgv("/cfg", spec))
	})
	t.Run("the name is always after --", func(t *testing.T) {
		spec := &envproto.LoopSpec{Name: "deps", Every: str("1h"), Prompt: str("p")}
		assert.Equal(t, []string{"deps"}, argvAfter(loopAddArgv("/cfg", spec), "--"))
		assert.Equal(t, []string{"deps"}, argvAfter(loopEditArgv("/cfg", spec), "--"))
	})
	t.Run("run, history and memory argv", func(t *testing.T) {
		assert.Equal(t, append(append([]string{}, prefix...), "run", "--force", "--", "deps"), loopRunArgv("/cfg", &envproto.LoopRunArgs{Name: "deps", Force: true}))
		assert.Equal(t, append(append([]string{}, prefix...), "run", "--", "deps"), loopRunArgv("/cfg", &envproto.LoopRunArgs{Name: "deps"}))
		assert.Equal(t, append(append([]string{}, prefix...), "history", "--limit=20", "--", "deps"), loopHistoryArgv("/cfg", &envproto.LoopHistoryArgs{Name: "deps"}))
		assert.Equal(t, append(append([]string{}, prefix...), "history", "--limit=7", "--", "deps"), loopHistoryArgv("/cfg", &envproto.LoopHistoryArgs{Name: "deps", Limit: 7}))
		assert.Equal(t, append(append([]string{}, prefix...), "memory", "get", "--", "deps", "k"), loopMemoryKeyArgv("/cfg", envproto.OpLoopMemoryGet, &envproto.LoopMemoryKey{Name: "deps", Key: "k"}))
		assert.Equal(t, append(append([]string{}, prefix...), "memory", "rm", "--expect-rev=2", "--", "deps", "k"), loopMemoryKeyArgv("/cfg", envproto.OpLoopMemoryRm, &envproto.LoopMemoryKey{Name: "deps", Key: "k", ExpectRev: num(2)}))
	})
	t.Run("memory set reads the value from stdin and never has it on the command line", func(t *testing.T) {
		argv := loopMemorySetArgv("/cfg", &envproto.LoopMemorySetArgs{Name: "deps", Key: "k", Value: "--secret-looking", ExpectRev: num(0)})
		assert.Equal(t, append(append([]string{}, prefix...), "memory", "set", "--file=-", "--expect-rev=0", "--", "deps", "k"), argv)
		assert.NotContains(t, argv, "--secret-looking")
	})
}

func TestLoopPage(t *testing.T) {
	item := func(name string, promptBytes int) string {
		return `{"name":"` + name + `","title":"t","every":"1h","paused":false,"hidden":true,"runner":"claude","prompt":"` + strings.Repeat("p", promptBytes) + `","script":"s","vars":{"K":"v"},"next_run_at":"2026-10-19T13:00:00Z","last_run":null,"last_gate":null}`
	}
	page := func(t *testing.T, list string, offset int) map[string]any {
		t.Helper()
		out, err := loopPage(json.RawMessage(list), offset)
		require.NoError(t, err)
		var got map[string]any
		require.NoError(t, json.Unmarshal(out, &got))
		return got
	}
	t.Run("a small list is one page", func(t *testing.T) {
		got := page(t, `{"loops":[`+item("a", 10)+`,`+item("b", 10)+`],"warnings":[]}`, 0)
		assert.Len(t, got["loops"], 2)
		assert.Nil(t, got["next_offset"])
		assert.EqualValues(t, 2, got["total"])
		assert.EqualValues(t, 0, got["offset"])
		assert.Equal(t, []any{}, got["warnings"])
	})
	t.Run("a page stops at the budget and next_offset continues", func(t *testing.T) {
		got := page(t, `{"loops":[`+item("a", 100<<10)+`,`+item("b", 100<<10)+`,`+item("c", 10)+`],"warnings":[]}`, 0)
		assert.Len(t, got["loops"], 1)
		assert.EqualValues(t, 1, got["next_offset"])
		got = page(t, `{"loops":[`+item("a", 100<<10)+`,`+item("b", 100<<10)+`,`+item("c", 10)+`],"warnings":[]}`, 1)
		assert.Len(t, got["loops"], 2)
		assert.Nil(t, got["next_offset"])
	})
	t.Run("a loop over the budget alone is cut to its snapshot fields", func(t *testing.T) {
		got := page(t, `{"loops":[`+item("a", 300<<10)+`,`+item("b", 10)+`],"warnings":[]}`, 0)
		loops, _ := got["loops"].([]any)
		require.Len(t, loops, 2)
		first, _ := loops[0].(map[string]any)
		assert.Equal(t, true, first["truncated"])
		assert.Equal(t, "a", first["name"])
		assert.Equal(t, "claude", first["runner"])
		assert.Equal(t, "2026-10-19T13:00:00Z", first["next_run_at"])
		assert.NotContains(t, first, "prompt")
		assert.NotContains(t, first, "script")
		assert.NotContains(t, first, "vars")
		second, _ := loops[1].(map[string]any)
		assert.NotContains(t, second, "truncated")
	})
	t.Run("an offset past the end is empty", func(t *testing.T) {
		got := page(t, `{"loops":[`+item("a", 10)+`],"warnings":[]}`, 4)
		assert.Empty(t, got["loops"])
		assert.Nil(t, got["next_offset"])
	})
	t.Run("a first loop still over the budget after its snapshot is one page", func(t *testing.T) {
		huge := `{"name":"a","title":"` + strings.Repeat("T", 200<<10) + `","every":"1h","paused":false,"hidden":false,"runner":"claude","prompt":"p","next_run_at":null,"last_run":null,"last_gate":null}`
		got := page(t, `{"loops":[`+huge+`,`+item("b", 10)+`],"warnings":[]}`, 0)
		loops, _ := got["loops"].([]any)
		require.Len(t, loops, 1)
		first, _ := loops[0].(map[string]any)
		assert.Equal(t, "a", first["name"])
		assert.Equal(t, true, first["truncated"])
		assert.EqualValues(t, 1, got["next_offset"])
	})
	t.Run("an object that is not a list is internal", func(t *testing.T) {
		_, err := loopPage(json.RawMessage(`{"loops":3}`), 0)
		requireReplyCode(t, err, "internal")
	})
}
