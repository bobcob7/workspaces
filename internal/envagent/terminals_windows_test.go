//go:build windows

package envagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// conptyHarness is a termHarness whose terminals run cmd.exe with args on a
// real pseudoconsole (ptyproc on Windows).
func conptyHarness(t *testing.T, args ...string) *termHarness {
	t.Helper()
	h := newTermHarness(t)
	h.m.starter = ptyprocStarter{}
	cmdExe := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	h.m.commands = &terminalCommandsMock{TerminalCommandFunc: func(context.Context, *envproto.TerminalOpenArgs) (TerminalCommand, error) {
		return TerminalCommand{Path: cmdExe, Args: args, Dir: t.TempDir(), Env: os.Environ()}, nil
	}}
	return h
}

// openReal opens a terminal on h and returns its sid.
func openReal(t *testing.T, h *termHarness) string {
	t.Helper()
	raw, err := h.m.open(t.Context(), attachArgs())
	require.NoError(t, err)
	var result envproto.TerminalOpenResult
	require.NoError(t, json.Unmarshal(raw, &result))
	return result.SID
}

// output is the data frames' bodies for sid so far, joined.
func output(h *termHarness, sid string) string {
	var out strings.Builder
	for _, f := range h.frames(sid) {
		if f.Kind == envproto.FrameData {
			out.Write(f.Body)
		}
	}
	return out.String()
}

func TestTerminals_conpty(t *testing.T) {
	t.Run("a program that exits sends its output and an exited frame", func(t *testing.T) {
		h := conptyHarness(t, "/c", "echo hello& exit 4")
		sid := openReal(t, h)
		exit := h.exit(sid)
		assert.Equal(t, reasonExited, exit.Reason)
		require.NotNil(t, exit.Code)
		assert.Equal(t, 4, *exit.Code)
		assert.Contains(t, output(h, sid), "hello")
	})
	t.Run("input reaches the program and close ends it", func(t *testing.T) {
		h := conptyHarness(t)
		sid := openReal(t, h)
		h.send(sid, envproto.FrameInput, 1, []byte("echo mark^er\r\n"))
		require.Eventually(t, func() bool { return strings.Contains(output(h, sid), "marker") }, 15*time.Second, 50*time.Millisecond)
		started := time.Now()
		h.send(sid, envproto.FrameClose, 2, nil)
		exit := h.exit(sid)
		assert.Equal(t, reasonClosed, exit.Reason)
		assert.Less(t, time.Since(started), 15*time.Second)
	})
}
