package envproto

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarshalFrame(t *testing.T) {
	t.Run("round-trips every kind", func(t *testing.T) {
		frames := []Frame{
			{Kind: FrameData, Seq: 1, Body: []byte("ls\r\n")},
			{Kind: FrameDropped, Seq: 2, Body: []byte{0, 0, 0, 0, 0, 0, 1, 0}},
			{Kind: FrameExit, Seq: 3, Body: []byte(`{"code":0,"reason":"exited"}`)},
			{Kind: FrameInput, Seq: 4, Body: []byte("q")},
			{Kind: FrameResize, Seq: 5, Body: ResizeBody(80, 24)},
			{Kind: FrameRedraw, Seq: 6},
			{Kind: FramePing, Seq: 7},
			{Kind: FrameClose, Seq: 8},
		}
		for _, want := range frames {
			b, err := MarshalFrame(want)
			require.NoError(t, err)
			got, err := UnmarshalFrame(b)
			require.NoError(t, err)
			assert.Equal(t, want.Kind, got.Kind)
			assert.Equal(t, want.Seq, got.Seq)
			assert.Equal(t, string(want.Body), string(got.Body))
		}
	})
	t.Run("writes the version, the kind and the big-endian sequence", func(t *testing.T) {
		b, err := MarshalFrame(Frame{Kind: FrameInput, Seq: 258, Body: []byte("a")})
		require.NoError(t, err)
		assert.Equal(t, []byte{frameVersion, byte(FrameInput), 0, 0, 0, 0, 0, 0, 1, 2, 'a'}, b)
	})
	t.Run("refuses an unknown kind", func(t *testing.T) {
		_, err := MarshalFrame(Frame{Kind: 'z', Body: []byte("a")})
		require.ErrorIs(t, err, ErrFrame)
	})
	t.Run("refuses a body over its kind's limit", func(t *testing.T) {
		_, err := MarshalFrame(Frame{Kind: FrameInput, Body: make([]byte, MaxInputBytes+1)})
		require.ErrorIs(t, err, ErrFrame)
	})
	t.Run("refuses an empty data body", func(t *testing.T) {
		_, err := MarshalFrame(Frame{Kind: FrameData})
		require.ErrorIs(t, err, ErrFrame)
	})
	t.Run("refuses a body on a redraw", func(t *testing.T) {
		_, err := MarshalFrame(Frame{Kind: FrameRedraw, Body: []byte("x")})
		require.ErrorIs(t, err, ErrFrame)
	})
}

func TestUnmarshalFrame(t *testing.T) {
	t.Run("refuses a wrong version", func(t *testing.T) {
		_, err := UnmarshalFrame([]byte{2, byte(FrameData), 0, 0, 0, 0, 0, 0, 0, 1, 'x'})
		require.ErrorIs(t, err, ErrFrame)
	})
	t.Run("refuses a short header", func(t *testing.T) {
		_, err := UnmarshalFrame([]byte{frameVersion, byte(FrameData)})
		require.ErrorIs(t, err, ErrFrame)
	})
	t.Run("refuses an unknown kind", func(t *testing.T) {
		_, err := UnmarshalFrame([]byte{frameVersion, 'z', 0, 0, 0, 0, 0, 0, 0, 1, 'x'})
		require.ErrorIs(t, err, ErrFrame)
	})
	t.Run("refuses an oversized data body", func(t *testing.T) {
		b := append([]byte{frameVersion, byte(FrameData), 0, 0, 0, 0, 0, 0, 0, 1}, make([]byte, MaxChunkBytes+1)...)
		_, err := UnmarshalFrame(b)
		require.ErrorIs(t, err, ErrFrame)
	})
	t.Run("refuses a dropped body that is not 8 bytes", func(t *testing.T) {
		_, err := UnmarshalFrame([]byte{frameVersion, byte(FrameDropped), 0, 0, 0, 0, 0, 0, 0, 1, 1})
		require.ErrorIs(t, err, ErrFrame)
	})
}

func TestParseResize(t *testing.T) {
	t.Run("reads the size that ResizeBody wrote", func(t *testing.T) {
		cols, rows, err := ParseResize(ResizeBody(120, 40))
		require.NoError(t, err)
		assert.Equal(t, 120, cols)
		assert.Equal(t, 40, rows)
	})
	t.Run("refuses a size of 0 or over 1000", func(t *testing.T) {
		_, _, err := ParseResize(ResizeBody(0, 24))
		require.ErrorIs(t, err, ErrFrame)
		_, _, err = ParseResize(ResizeBody(80, 1001))
		require.ErrorIs(t, err, ErrFrame)
	})
	t.Run("refuses a body that is not 4 bytes", func(t *testing.T) {
		_, _, err := ParseResize([]byte{0, 80, 0})
		require.ErrorIs(t, err, ErrFrame)
	})
}

func TestDroppedMarker(t *testing.T) {
	t.Run("names the byte count in reverse video", func(t *testing.T) {
		assert.Equal(t, "\r\n\x1b[7m[ws: 12 bytes of output dropped]\x1b[0m\r\n", string(DroppedMarker(12)))
	})
	t.Run("the lost marker asks for a redraw", func(t *testing.T) {
		assert.Equal(t, "\r\n\x1b[7m[ws: output lost; redrawing]\x1b[0m\r\n", string(LostMarker()))
	})
}

func TestValidSID(t *testing.T) {
	t.Run("accepts 16 lowercase hex characters", func(t *testing.T) {
		assert.True(t, ValidSID("0123456789abcdef"))
	})
	t.Run("refuses any other text", func(t *testing.T) {
		for _, sid := range []string{"", "0123456789ABCDEF", "0123456789abcdeg", "0123456789abcde", "0123456789abcdef0", "0123456789abcdef\n"} {
			assert.False(t, ValidSID(sid), sid)
		}
	})
}

func TestValidate_terminalOpen(t *testing.T) {
	cases := []struct {
		name string
		args TerminalOpenArgs
		code string
	}{
		{name: "an attach to a workspace", args: TerminalOpenArgs{Kind: TermAttach, Workspace: new("ab"), Specs: []string{}, Cols: 80, Rows: 24}},
		{name: "a shell with specs and a name", args: TerminalOpenArgs{Kind: TermShell, Name: new("n"), Specs: []string{"a@main"}, Offline: true, Cols: 1, Rows: 1000}},
		{name: "an attach without a workspace", args: TerminalOpenArgs{Kind: TermAttach, Cols: 80, Rows: 24}, code: "usage"},
		{name: "an attach with a name", args: TerminalOpenArgs{Kind: TermAttach, Workspace: new("ab"), Name: new("n"), Cols: 80, Rows: 24}, code: "usage"},
		{name: "an attach with specs", args: TerminalOpenArgs{Kind: TermAttach, Workspace: new("ab"), Specs: []string{"a"}, Cols: 80, Rows: 24}, code: "usage"},
		{name: "an attach offline", args: TerminalOpenArgs{Kind: TermAttach, Workspace: new("ab"), Offline: true, Cols: 80, Rows: 24}, code: "usage"},
		{name: "a shell with a workspace", args: TerminalOpenArgs{Kind: TermShell, Workspace: new("ab"), Cols: 80, Rows: 24}, code: "usage"},
		{name: "a columns size of 0", args: TerminalOpenArgs{Kind: TermShell, Cols: 0, Rows: 24}, code: "usage"},
		{name: "a rows size of 1001", args: TerminalOpenArgs{Kind: TermShell, Cols: 80, Rows: 1001}, code: "usage"},
		{name: "an unknown kind", args: TerminalOpenArgs{Kind: "run", Cols: 80, Rows: 24}, code: "usage"},
		{name: "a spec that starts with a dash", args: TerminalOpenArgs{Kind: TermShell, Specs: []string{"-x"}, Cols: 80, Rows: 24}, code: "spec_invalid"},
		{name: "a name with a control character", args: TerminalOpenArgs{Kind: TermShell, Name: new("a\x01b"), Cols: 80, Rows: 24}, code: "name_invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(&tc.args)
			if tc.code == "" {
				assert.NoError(t, err)
				return
			}
			assertArgsError(t, err, tc.code)
		})
	}
}

func TestValidate_terminalClose(t *testing.T) {
	t.Run("accepts a terminal id", func(t *testing.T) {
		assert.NoError(t, Validate(&TerminalCloseArgs{SID: "0123456789abcdef"}))
	})
	t.Run("refuses a malformed terminal id with usage", func(t *testing.T) {
		assertArgsError(t, Validate(&TerminalCloseArgs{SID: "nope"}), "usage")
	})
}
