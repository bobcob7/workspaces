package cli

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/gate"
	"github.com/bobcob7/workspaces/internal/loop"
)

func TestGateEnv(t *testing.T) {
	t.Run("name then vars by key", func(t *testing.T) {
		l := loop.Loop{Name: "deps", Vars: map[string]string{"B": "2", "A": "1"}}
		assert.Equal(t, []string{"WS_LOOP_NAME=deps", "WS_LOOP_VAR_A=1", "WS_LOOP_VAR_B=2"}, gateEnv(&l))
	})
	t.Run("no vars", func(t *testing.T) {
		l := loop.Loop{Name: "deps"}
		assert.Equal(t, []string{"WS_LOOP_NAME=deps"}, gateEnv(&l))
	})
}

func TestSkipMessage(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   string
	}{
		{name: "last non-blank line", stderr: "x\nno new issues\n\n", want: "no new issues"},
		{name: "empty", stderr: "", want: ""},
		{name: "blank lines only", stderr: "\n \n", want: ""},
		{name: "trims carriage returns", stderr: "no new issues\r\n", want: "no new issues"},
		{name: "long line is cut", stderr: strings.Repeat("a", 300), want: strings.Repeat("a", 200)},
	}
	for i := range tests {
		t.Run(tests[i].name, func(t *testing.T) {
			assert.Equal(t, tests[i].want, skipMessage(tests[i].stderr))
		})
	}
	t.Run("long multi-byte line is cut at a rune boundary", func(t *testing.T) {
		got := skipMessage(strings.Repeat("é", 150))
		assert.LessOrEqual(t, len(got), 200)
		assert.True(t, utf8.ValidString(got))
	})
}

func TestApp_runGate(t *testing.T) {
	t.Run("no script makes no request and leaves last_gate alone", func(t *testing.T) {
		app, _, _ := newTestApp()
		g := &fakeGate{}
		app.gate = g
		l := loop.Loop{Name: "deps", Prompt: "Do it"}
		data, err := app.runGate(t.Context(), &l)
		require.NoError(t, err)
		assert.Equal(t, loop.PromptData{}, data)
		assert.Empty(t, g.requests)
		assert.Nil(t, l.LastGate)
	})
	t.Run("a passed script returns its output", func(t *testing.T) {
		app, _, _ := newTestApp()
		app.gate = &fakeGate{result: gate.Result{Stdout: "out", Stderr: "err"}}
		l := loop.Loop{Name: "deps", Script: "check"}
		data, err := app.runGate(t.Context(), &l)
		require.NoError(t, err)
		assert.Equal(t, loop.PromptData{Stdout: "out", Stderr: "err"}, data)
		require.NotNil(t, l.LastGate)
		assert.Equal(t, loop.GatePassed, l.LastGate.Outcome)
		assert.Equal(t, testNow(), l.LastGate.At)
	})
	t.Run("a skipped script wraps ErrSkipped with its exit code and message", func(t *testing.T) {
		app, _, _ := newTestApp()
		app.gate = &fakeGate{result: gate.Result{ExitCode: 3, Stderr: "stop"}}
		l := loop.Loop{Name: "deps", Script: "check"}
		_, err := app.runGate(t.Context(), &l)
		require.ErrorIs(t, err, loop.ErrSkipped)
		assert.EqualError(t, err, "loop script exited non-zero: exit 3: stop")
	})
	t.Run("a home directory error fails the script", func(t *testing.T) {
		app, _, _ := newTestApp()
		g := &fakeGate{}
		app.gate = g
		app.homeDir = func() (string, error) { return "", errors.New("no home") }
		l := loop.Loop{Name: "deps", Script: "check"}
		_, err := app.runGate(t.Context(), &l)
		require.ErrorIs(t, err, loop.ErrScriptFailed)
		assert.Empty(t, g.requests)
		require.NotNil(t, l.LastGate)
		assert.Equal(t, loop.GateFailed, l.LastGate.Outcome)
		assert.Equal(t, -1, l.LastGate.ExitCode)
	})
	t.Run("the request uses the loop's timeout", func(t *testing.T) {
		app, _, _ := newTestApp()
		g := &fakeGate{}
		app.gate = g
		l := loop.Loop{Name: "deps", Script: "check", ScriptTimeout: "45s"}
		_, err := app.runGate(t.Context(), &l)
		require.NoError(t, err)
		require.Len(t, g.requests, 1)
		assert.Equal(t, 45*time.Second, g.requests[0].Timeout)
	})
}
