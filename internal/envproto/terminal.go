package envproto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Limits of a terminal stream (ADR 0074, D2, D6, D10).
const (
	MaxChunkBytes    = 16 << 10
	MaxInputBytes    = 4 << 10
	MaxQueuedOutput  = 1 << 20
	InputPaceBytes   = 64 << 10
	InputLimitBytes  = 128 << 10
	InputLimitFrames = 500
	MaxTerminals     = 8
	MaxTermSize      = 1000
	PingInterval     = 15 * time.Second
	IdleClose        = 60 * time.Second
	RedrawEvery      = time.Second
	frameHeader      = 10
	frameVersion     = 1
)

// FrameKind is a stream frame's kind.
type FrameKind byte

// Output kinds (agent to controller, on stream/<sid>/out, QoS 0).
const (
	FrameData    FrameKind = 'd'
	FrameDropped FrameKind = 'g'
	FrameExit    FrameKind = 'x'
)

// Input kinds (controller to agent, on stream/<sid>/in, QoS 1).
const (
	FrameInput  FrameKind = 'i'
	FrameResize FrameKind = 'r'
	FrameRedraw FrameKind = 'w'
	FramePing   FrameKind = 'p'
	FrameClose  FrameKind = 'c'
)

// Frame is one stream message: version, kind, a big-endian uint64 sequence and the body.
type Frame struct {
	Kind FrameKind
	Seq  uint64
	Body []byte
}

// ErrFrame is wrapped by every frame encoding and decoding error.
var ErrFrame = errors.New("invalid stream frame")

var sidPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// MarshalFrame encodes f; it refuses an unknown kind or a body outside its kind's limits.
func MarshalFrame(f Frame) ([]byte, error) {
	if err := checkBody(f.Kind, len(f.Body)); err != nil {
		return nil, err
	}
	b := make([]byte, frameHeader+len(f.Body))
	b[0] = frameVersion
	b[1] = byte(f.Kind)
	binary.BigEndian.PutUint64(b[2:frameHeader], f.Seq)
	copy(b[frameHeader:], f.Body)
	return b, nil
}

// UnmarshalFrame decodes b, refusing a wrong version, a short header, an unknown kind or a body outside its kind's limits.
func UnmarshalFrame(b []byte) (Frame, error) {
	if len(b) < frameHeader {
		return Frame{}, fmt.Errorf("%w: a frame is at least %d bytes, got %d", ErrFrame, frameHeader, len(b))
	}
	if b[0] != frameVersion {
		return Frame{}, fmt.Errorf("%w: version %d", ErrFrame, b[0])
	}
	f := Frame{Kind: FrameKind(b[1]), Seq: binary.BigEndian.Uint64(b[2:frameHeader]), Body: b[frameHeader:]}
	if err := checkBody(f.Kind, len(f.Body)); err != nil {
		return Frame{}, err
	}
	return f, nil
}

// checkBody refuses an unknown kind and a body whose length is outside the kind's limits.
func checkBody(kind FrameKind, n int) error {
	lo, hi, ok := bodyLimits(kind)
	if !ok {
		return fmt.Errorf("%w: unknown kind %q", ErrFrame, byte(kind))
	}
	if n < lo || n > hi {
		return fmt.Errorf("%w: a %q body is %d to %d bytes, got %d", ErrFrame, byte(kind), lo, hi, n)
	}
	return nil
}

// bodyLimits returns the smallest and largest body length of kind.
func bodyLimits(kind FrameKind) (lo, hi int, ok bool) {
	switch kind {
	case FrameData, FrameExit:
		return 1, MaxChunkBytes, true
	case FrameInput:
		return 1, MaxInputBytes, true
	case FrameDropped:
		return 8, 8, true
	case FrameResize:
		return 4, 4, true
	case FrameRedraw, FramePing, FrameClose:
		return 0, 0, true
	}
	return 0, 0, false
}

// ResizeBody encodes a resize frame's body: columns, then rows, as big-endian uint16.
func ResizeBody(cols, rows int) []byte {
	body := make([]byte, 4)
	binary.BigEndian.PutUint16(body[0:2], uint16(cols)) //nolint:gosec // callers pass sizes checked by validTermSize
	binary.BigEndian.PutUint16(body[2:4], uint16(rows)) //nolint:gosec // callers pass sizes checked by validTermSize
	return body
}

// ParseResize decodes a resize frame's body; each size is 1 to MaxTermSize.
func ParseResize(body []byte) (cols, rows int, err error) {
	if len(body) != 4 {
		return 0, 0, fmt.Errorf("%w: a resize body is 4 bytes, got %d", ErrFrame, len(body))
	}
	cols = int(binary.BigEndian.Uint16(body[0:2]))
	rows = int(binary.BigEndian.Uint16(body[2:4]))
	if !validTermSize(cols) || !validTermSize(rows) {
		return 0, 0, fmt.Errorf("%w: a terminal size is 1 to %d", ErrFrame, MaxTermSize)
	}
	return cols, rows, nil
}

// DroppedMarker is the text a receiver writes for a dropped count.
func DroppedMarker(n uint64) []byte {
	return []byte(fmt.Sprintf("\r\n\x1b[7m[ws: %d bytes of output dropped]\x1b[0m\r\n", n))
}

// LostMarker is the text a receiver writes for a gap in the sequence.
func LostMarker() []byte {
	return []byte("\r\n\x1b[7m[ws: output lost; redrawing]\x1b[0m\r\n")
}

// ValidSID reports whether sid is 16 lowercase hex characters.
func ValidSID(sid string) bool {
	return sidPattern.MatchString(sid)
}

// TerminalOpenArgs are the arguments of terminal.open.
type TerminalOpenArgs struct {
	Kind      string   `json:"kind"`      // TermAttach or TermShell
	Workspace *string  `json:"workspace"` // attach: a workspace reference; null for shell
	Name      *string  `json:"name"`      // shell only
	Specs     []string `json:"specs"`     // shell only; [] for none
	Offline   bool     `json:"offline"`   // shell only
	Cols      int      `json:"cols"`      // 1 to MaxTermSize
	Rows      int      `json:"rows"`      // 1 to MaxTermSize
}

// TerminalOpenResult is the result of terminal.open.
type TerminalOpenResult struct {
	SID       string  `json:"sid"`
	Workspace *string `json:"workspace"` // attach: the workspace id; shell: null
	Session   *string `json:"session"`   // attach: the Claude session id; shell: null
}

// TerminalCloseArgs are the arguments of terminal.close.
type TerminalCloseArgs struct {
	SID string `json:"sid"`
}

// TerminalCloseResult is the result of terminal.close.
type TerminalCloseResult struct {
	SID    string `json:"sid"`
	Closed bool   `json:"closed"` // false when it had already ended
}

// TerminalExit is the body of a FrameExit.
type TerminalExit struct {
	Code   *int   `json:"code"`
	Reason string `json:"reason"` // "exited", "closed", "idle", "input_gap", "input_limit", "disconnected"
}

func validateTerminalOpen(a *TerminalOpenArgs) error {
	if !validTermSize(a.Cols) || !validTermSize(a.Rows) {
		return usageError(fmt.Sprintf("a terminal size is 1 to %d", MaxTermSize))
	}
	switch a.Kind {
	case TermAttach:
		return validateAttach(a)
	case TermShell:
		return validateShell(a)
	}
	return usageError("unknown terminal kind " + fmt.Sprintf("%q", a.Kind))
}

func validateAttach(a *TerminalOpenArgs) error {
	if a.Workspace == nil {
		return usageError("attach needs a workspace")
	}
	if a.Name != nil || len(a.Specs) > 0 || a.Offline {
		return usageError("attach takes no name, specs or offline")
	}
	return validateWorkspace(*a.Workspace)
}

func validateShell(a *TerminalOpenArgs) error {
	if a.Workspace != nil {
		return usageError("a shell takes no workspace")
	}
	if a.Name != nil {
		if err := validateName(*a.Name); err != nil {
			return err
		}
	}
	for _, spec := range a.Specs {
		if err := validateSpec(spec); err != nil {
			return err
		}
	}
	return nil
}

func validateTerminalClose(a *TerminalCloseArgs) error {
	if !ValidSID(a.SID) {
		return usageError("a terminal id is 16 lowercase hex characters")
	}
	return nil
}

func validTermSize(n int) bool {
	return n >= 1 && n <= MaxTermSize
}
