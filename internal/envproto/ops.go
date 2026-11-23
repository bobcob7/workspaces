package envproto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Operation names (ADR 0073's table, the rows this protocol serves).
const (
	OpWorkspaceCreate = "workspace.create"
	OpWorkspaceList   = "workspace.list"
	OpWorkspaceRm     = "workspace.rm"
	OpWorkspaceRename = "workspace.rename"
	OpWorkspaceHide   = "workspace.hide"
	OpAgentStatus     = "agent.status"
	OpAgentLogs       = "agent.logs"
	OpAgentSend       = "agent.send"
	OpAgentStop       = "agent.stop"
	OpTerminalOpen    = "terminal.open"
	OpTerminalClose   = "terminal.close"
)

// Workspace kinds an operation can create.
const (
	KindClaude = "claude"
	KindRunner = "runner"
)

// Kinds of terminal.open.
const (
	TermAttach = "attach"
	TermShell  = "shell"
)

// Grace is how much longer than an operation's deadline the controller waits.
const Grace = 15 * time.Second

// Size limits on operation arguments and results.
const (
	MaxPromptBytes  = 64 << 10
	MaxMessageBytes = 16 << 10
	MaxLogBytes     = 192 << 10
	MaxTail         = 2000
	DefaultTail     = 200
	// MaxSnapshotAgents caps the Claude Code sessions in a snapshot, newest first.
	MaxSnapshotAgents = 200
)

// Caller is who may send an operation.
type Caller int

const (
	// CallerController sends on env/<id>/req, through the daemon's call endpoint.
	CallerController Caller = iota + 1
	// CallerAgent sends on env/<id>/up.
	CallerAgent
	// CallerInternal sends on env/<id>/req, and only the controller itself does.
	CallerInternal
)

// CallerTerminal sends terminal.open and terminal.close on env/<id>/req, and only the controller's terminal path does.
const CallerTerminal Caller = iota + 4

// OpSpec is one operation's rules.
type OpSpec struct {
	Name    string
	Caller  Caller
	Since   int           // the protocol it needs (ADR 0073)
	Timeout time.Duration // the message expiry and the deadline's distance
	Changes bool          // the agent republishes its snapshot after it
}

// LookupOp returns name's spec; false for an operation that isn't allowed.
// A switch, not a map, so the table has no package variable.
func LookupOp(name string) (OpSpec, bool) {
	switch name {
	case OpCertRenew:
		return OpSpec{Name: name, Caller: CallerAgent, Timeout: 30 * time.Second}, true
	case OpCAUpdate:
		return OpSpec{Name: name, Caller: CallerInternal, Timeout: 30 * time.Second}, true
	case OpWorkspaceCreate:
		return OpSpec{Name: name, Caller: CallerController, Since: 1, Timeout: 10 * time.Minute, Changes: true}, true
	case OpWorkspaceList:
		return OpSpec{Name: name, Caller: CallerController, Since: 1, Timeout: 30 * time.Second}, true
	case OpWorkspaceRm:
		return OpSpec{Name: name, Caller: CallerController, Since: 1, Timeout: 2 * time.Minute, Changes: true}, true
	case OpWorkspaceRename, OpWorkspaceHide:
		return OpSpec{Name: name, Caller: CallerController, Since: 1, Timeout: 30 * time.Second, Changes: true}, true
	case OpAgentStatus, OpAgentLogs:
		return OpSpec{Name: name, Caller: CallerController, Since: 1, Timeout: 30 * time.Second}, true
	case OpAgentSend:
		return OpSpec{Name: name, Caller: CallerController, Since: 1, Timeout: 90 * time.Second}, true
	case OpAgentStop:
		return OpSpec{Name: name, Caller: CallerController, Since: 1, Timeout: 30 * time.Second, Changes: true}, true
	case OpTerminalOpen:
		return OpSpec{Name: name, Caller: CallerTerminal, Since: 2, Timeout: 30 * time.Second, Changes: true}, true
	case OpTerminalClose:
		return OpSpec{Name: name, Caller: CallerTerminal, Since: 2, Timeout: 30 * time.Second}, true
	}
	return OpSpec{}, false
}

// CreateArgs are the arguments of workspace.create.
type CreateArgs struct {
	Kind     string   `json:"kind"`  // KindClaude or KindRunner
	Specs    []string `json:"specs"` // [] for an empty workspace
	Name     *string  `json:"name"`
	Parent   *string  `json:"parent"` // a WorkspaceRef on the environment, or null for none
	Hidden   bool     `json:"hidden"`
	Offline  bool     `json:"offline"`
	Prompt   *string  `json:"prompt"` // required for KindRunner
	Model    *string  `json:"model"`
	Endpoint *string  `json:"endpoint"` // KindRunner only
}

// ListArgs are the arguments of workspace.list.
type ListArgs struct{}

// WorkspaceArgs are the arguments of agent.status and agent.stop.
type WorkspaceArgs struct {
	Workspace string `json:"workspace"`
}

// RmArgs are the arguments of workspace.rm.
type RmArgs struct {
	Workspace    string `json:"workspace"`
	KeepChildren bool   `json:"keep_children"`
}

// RenameArgs are the arguments of workspace.rename.
type RenameArgs struct {
	Workspace string `json:"workspace"`
	Name      string `json:"name"`
}

// HideArgs are the arguments of workspace.hide.
type HideArgs struct {
	Workspace string `json:"workspace"`
	Hidden    bool   `json:"hidden"` // false: unhide
}

// LogsArgs are the arguments of agent.logs.
type LogsArgs struct {
	Workspace string `json:"workspace"`
	Tail      int    `json:"tail"` // 0: DefaultTail lines; at most MaxTail
}

// SendArgs are the arguments of agent.send.
type SendArgs struct {
	Workspace string `json:"workspace"`
	Message   string `json:"message"`
}

// AgentStatusResult is the result of agent.status.
type AgentStatusResult struct {
	Workspace string         `json:"workspace"`
	Session   string         `json:"session"`
	Agent     *SnapshotAgent `json:"agent"` // null when Claude Code doesn't list it
}

// AgentLogsResult is the result of agent.logs.
type AgentLogsResult struct {
	Workspace string   `json:"workspace"`
	Session   string   `json:"session"`
	Lines     []string `json:"lines"`
	Truncated bool     `json:"truncated"` // lines were dropped to fit MaxLogBytes
}

// AgentSendResult is the result of agent.send.
type AgentSendResult struct {
	Workspace string `json:"workspace"`
	Session   string `json:"session"`
	Sent      bool   `json:"sent"`
}

// AgentStopResult is the result of agent.stop.
type AgentStopResult struct {
	Workspace string `json:"workspace"`
	Session   string `json:"session"`
	Stopped   bool   `json:"stopped"` // false when the session had already ended
}

// ErrArgs is wrapped by every ArgsError.
var ErrArgs = errors.New("invalid operation arguments")

// ArgsError is a refused argument; Code is the error code the reply uses.
type ArgsError struct {
	Code    string // "usage", "spec_invalid", "name_invalid" or "outside_workspace"
	Message string
}

// Error returns the message the reply carries.
func (e *ArgsError) Error() string {
	return e.Message
}

// Unwrap returns ErrArgs.
func (*ArgsError) Unwrap() error {
	return ErrArgs
}

var (
	modelPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
	requestIDRegex = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

// DecodeArgs decodes raw into op's argument struct with unknown keys
// refused and returns a pointer to it (*CreateArgs, *ListArgs, …), after
// Validate. A missing or null raw is {}. Unknown op: ArgsError{usage}.
func DecodeArgs(op string, raw json.RawMessage) (any, error) {
	target, ok := newArgs(op)
	if !ok {
		return nil, &ArgsError{Code: "usage", Message: fmt.Sprintf("unknown operation %q", op)}
	}
	if err := decodeStrict(raw, target); err != nil {
		return nil, err
	}
	if err := Validate(target); err != nil {
		return nil, err
	}
	return target, nil
}

// newArgs returns an empty argument struct for op.
func newArgs(op string) (any, bool) {
	switch op {
	case OpWorkspaceCreate:
		return &CreateArgs{}, true
	case OpWorkspaceList:
		return &ListArgs{}, true
	case OpWorkspaceRm:
		return &RmArgs{}, true
	case OpWorkspaceRename:
		return &RenameArgs{}, true
	case OpWorkspaceHide:
		return &HideArgs{}, true
	case OpAgentStatus, OpAgentStop:
		return &WorkspaceArgs{}, true
	case OpAgentLogs:
		return &LogsArgs{}, true
	case OpAgentSend:
		return &SendArgs{}, true
	case OpTerminalOpen:
		return &TerminalOpenArgs{}, true
	case OpTerminalClose:
		return &TerminalCloseArgs{}, true
	}
	return nil, false
}

// decodeStrict fills target from raw, refusing unknown keys and trailing data.
func decodeStrict(raw json.RawMessage, target any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return &ArgsError{Code: "usage", Message: err.Error()}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return &ArgsError{Code: "usage", Message: "unexpected data after the arguments"}
	}
	return nil
}

// Validate checks the fields of a decoded argument struct (ADR 0073) that
// need no file system: Kind; Specs not starting with "-" and without
// surrounding whitespace or control characters (spec_invalid); Name and
// RenameArgs.Name 1-64 bytes without control characters (name_invalid);
// Model against modelPattern; Endpoint 1-128 bytes, not starting with "-",
// without control characters; Prompt 1..MaxPromptBytes, required for
// KindRunner, refused with Endpoint for KindClaude, and for KindClaude
// not starting with "-" after leading whitespace; Message
// 1..MaxMessageBytes; Tail 0..MaxTail; every workspace reference and Parent
// non-empty and without a NUL byte. A nil pointer is a usage error. Paths
// are checked by the agent.
func Validate(args any) error {
	if isNilPointer(args) {
		return usageError("missing operation arguments")
	}
	switch a := args.(type) {
	case *CreateArgs:
		return validateCreate(a)
	case *ListArgs:
		return nil
	case *WorkspaceArgs:
		return validateWorkspace(a.Workspace)
	case *RmArgs:
		return validateWorkspace(a.Workspace)
	case *RenameArgs:
		return validateRename(a)
	case *HideArgs:
		return validateWorkspace(a.Workspace)
	case *LogsArgs:
		return validateLogs(a)
	case *SendArgs:
		return validateSend(a)
	case *TerminalOpenArgs:
		return validateTerminalOpen(a)
	case *TerminalCloseArgs:
		return validateTerminalClose(a)
	}
	return usageError("unknown operation arguments")
}

// ValidRequestID reports whether id is 16 lowercase hex characters.
func ValidRequestID(id string) bool {
	return requestIDRegex.MatchString(id)
}

func isNilPointer(args any) bool {
	v := reflect.ValueOf(args)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

func validateCreate(a *CreateArgs) error {
	if err := validateKind(a); err != nil {
		return err
	}
	for _, spec := range a.Specs {
		if err := validateSpec(spec); err != nil {
			return err
		}
	}
	if a.Name != nil {
		if err := validateName(*a.Name); err != nil {
			return err
		}
	}
	if a.Parent != nil {
		if err := validateWorkspace(*a.Parent); err != nil {
			return err
		}
	}
	if a.Model != nil && !modelPattern.MatchString(*a.Model) {
		return usageError("model " + strconv.Quote(*a.Model) + " is not a valid model name")
	}
	if a.Endpoint != nil {
		if err := validateEndpoint(*a.Endpoint); err != nil {
			return err
		}
	}
	return validatePrompt(a)
}

func validateEndpoint(endpoint string) error {
	if endpoint == "" || len(endpoint) > 128 || strings.HasPrefix(endpoint, "-") || strings.ContainsFunc(endpoint, unicode.IsControl) {
		return usageError("an endpoint is 1 to 128 bytes, does not start with a dash and has no control characters")
	}
	return nil
}

func validateKind(a *CreateArgs) error {
	switch a.Kind {
	case KindClaude:
		if a.Endpoint != nil {
			return usageError("an endpoint needs a runner")
		}
		return nil
	case KindRunner:
		return nil
	case "shell":
		return usageError("a shell on an environment needs terminal streams")
	}
	return usageError("unknown kind " + strconv.Quote(a.Kind))
}

func validatePrompt(a *CreateArgs) error {
	if a.Prompt == nil {
		if a.Kind == KindRunner {
			return usageError("a runner needs a prompt")
		}
		return nil
	}
	if *a.Prompt == "" || len(*a.Prompt) > MaxPromptBytes {
		return usageError(fmt.Sprintf("prompt must be 1 to %d bytes", MaxPromptBytes))
	}
	if a.Kind == KindClaude && strings.HasPrefix(strings.TrimSpace(*a.Prompt), "-") {
		return usageError("a claude prompt cannot start with a dash, claude would read it as a flag")
	}
	return nil
}

func validateSpec(spec string) error {
	if spec == "" || strings.HasPrefix(spec, "-") || strings.TrimSpace(spec) != spec || strings.ContainsFunc(spec, unicode.IsControl) {
		return &ArgsError{Code: "spec_invalid", Message: "spec " + strconv.Quote(spec) + " is not a workspace spec"}
	}
	return nil
}

func validateRename(a *RenameArgs) error {
	if err := validateWorkspace(a.Workspace); err != nil {
		return err
	}
	return validateName(a.Name)
}

func validateLogs(a *LogsArgs) error {
	if err := validateWorkspace(a.Workspace); err != nil {
		return err
	}
	if a.Tail < 0 || a.Tail > MaxTail {
		return usageError(fmt.Sprintf("tail must be 0 to %d", MaxTail))
	}
	return nil
}

func validateSend(a *SendArgs) error {
	if err := validateWorkspace(a.Workspace); err != nil {
		return err
	}
	if a.Message == "" || len(a.Message) > MaxMessageBytes {
		return usageError(fmt.Sprintf("message must be 1 to %d bytes", MaxMessageBytes))
	}
	return nil
}

func validateWorkspace(ref string) error {
	if ref == "" || strings.ContainsRune(ref, 0) {
		return usageError("a workspace reference is required and cannot contain a NUL byte")
	}
	return nil
}

func validateName(name string) error {
	if name == "" || len(name) > 64 || strings.ContainsFunc(name, unicode.IsControl) {
		return &ArgsError{Code: "name_invalid", Message: "a name is 1 to 64 bytes without control characters"}
	}
	return nil
}

func usageError(message string) error {
	return &ArgsError{Code: "usage", Message: message}
}
