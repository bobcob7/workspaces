package envproto

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Operation names of ADR 0078's loop rows.
const (
	OpLoopList         = "loop.list"
	OpLoopShow         = "loop.show"
	OpLoopAdd          = "loop.add"
	OpLoopEdit         = "loop.edit"
	OpLoopPause        = "loop.pause"
	OpLoopResume       = "loop.resume"
	OpLoopRm           = "loop.rm"
	OpLoopRun          = "loop.run"
	OpLoopHistory      = "loop.history"
	OpLoopHistoryRun   = "loop.history_run"
	OpLoopHistoryClear = "loop.history_clear"
	OpLoopMemoryList   = "loop.memory_list"
	OpLoopMemoryGet    = "loop.memory_get"
	OpLoopMemorySet    = "loop.memory_set"
	OpLoopMemoryRm     = "loop.memory_rm"
	OpLoopMemoryClear  = "loop.memory_clear"
)

// Bounds of loop arguments, results and snapshots (ADR 0078).
const (
	MaxScriptBytes   = 16 << 10
	MaxVars          = 32
	MaxVarValueBytes = 4 << 10
	MaxVarsBytes     = 32 << 10
	MaxMemoryValue   = 64 << 10 // ADR 0056
	MaxReportText    = 160 << 10
	MaxMemoryPreview = 1 << 10
	MaxSnapshotLoops = 100
	MaxHistoryLimit  = 100
	PermissionBypass = "bypassPermissions"
	RedactedMarker   = "[redacted]"
)

const (
	maxLoopTitleBytes = 80
	maxKeepPerLoop    = 50
	runnerClaude      = "claude"
	runnerWS          = "ws"
)

var (
	loopNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	varKeyPattern   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
)

// LoopName is the argument of loop.show, loop.pause, loop.resume, loop.history_clear,
// loop.memory_list, loop.memory_clear and loop.rm (the last three need Confirm).
type LoopName struct {
	Name    string `json:"name"`    // ^[a-z0-9][a-z0-9-]{0,39}$
	Confirm bool   `json:"confirm"` // required true for loop.rm, loop.history_clear, loop.memory_clear
}

// LoopListArgs are loop.list's: a page of loops by name.
type LoopListArgs struct {
	Offset int `json:"offset"`
}

// LoopSpec is loop.add's arguments, and loop.edit's with every field optional.
// Nil means "not given" (edit: unchanged; add: ws's default).
type LoopSpec struct {
	Name           string             `json:"name"`
	Title          *string            `json:"title"`           // <= 80 bytes, no control characters
	Every          *string            `json:"every"`           // a Go duration; add: required. ws checks 5m..168h
	Prompt         *string            `json:"prompt"`          // add: required; 1..MaxPromptBytes
	Specs          *[]string          `json:"specs"`           // each validateSpec; edit: [] clears (--no-specs)
	Keep           *int               `json:"keep"`            // 1..50
	Runner         *string            `json:"runner"`          // "claude" or "ws"
	Endpoint       *string            `json:"endpoint"`        // validateEndpoint
	Model          *string            `json:"model"`           // modelPattern
	PermissionMode *string            `json:"permission_mode"` // PermissionModes()
	Paused         *bool              `json:"paused"`          // add only
	Script         *string            `json:"script"`          // <= MaxScriptBytes; edit: "" removes (--no-script)
	ScriptTimeout  *string            `json:"script_timeout"`  // a Go duration; ws checks 1s..10m
	Vars           *map[string]string `json:"vars"`            // <= MaxVars, keys [A-Za-z_][A-Za-z0-9_]{0,63}, values <= MaxVarValueBytes, all <= MaxVarsBytes; edit: {} clears (--no-vars)
	Hidden         *bool              `json:"hidden"`
	Confirm        bool               `json:"confirm"` // NeedsConfirm(op, spec) must be satisfied
}

// LoopRunArgs are loop.run's.
type LoopRunArgs struct {
	Name  string `json:"name"`
	Force bool   `json:"force"`
}

// LoopHistoryArgs are loop.history's.
type LoopHistoryArgs struct {
	Name  string `json:"name"`
	Limit int    `json:"limit"` // 0: 20; 1..MaxHistoryLimit
}

// LoopHistoryRunArgs are loop.history_run's.
type LoopHistoryRunArgs struct {
	Name string `json:"name"`
	Seq  int    `json:"seq"` // >= 1
}

// LoopMemoryKey is loop.memory_get's and loop.memory_rm's.
type LoopMemoryKey struct {
	Name      string `json:"name"`
	Key       string `json:"key"`        // ADR 0056's key rule
	ExpectRev *int   `json:"expect_rev"` // rm only; >= 0
}

// LoopMemorySetArgs are loop.memory_set's.
type LoopMemorySetArgs struct {
	Name      string `json:"name"`
	Key       string `json:"key"`
	Value     string `json:"value"`      // <= MaxMemoryValue; may be ""
	ExpectRev *int   `json:"expect_rev"` // >= 0
}

// SnapshotLoop is one loop in the snapshot (ADR 0078): no prompt, script or vars.
type SnapshotLoop struct {
	Name      string          `json:"name"`
	Title     string          `json:"title"`
	Every     string          `json:"every"`
	Paused    bool            `json:"paused"`
	Hidden    bool            `json:"hidden"`
	Runner    string          `json:"runner"`
	NextRunAt *time.Time      `json:"next_run_at"`
	LastRun   json.RawMessage `json:"last_run"`  // ws loop list's last_run, or null
	LastGate  json.RawMessage `json:"last_gate"` // ws loop list's last_gate, or null
}

// loopOpSpec returns the rule of a loop operation: its timeout and whether it changes the snapshot.
// A false second result means op is not a loop operation.
func loopOpSpec(op string) (OpSpec, bool) {
	spec := OpSpec{Name: op, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second}
	switch op {
	case OpLoopRun:
		spec.Timeout = 15 * time.Minute
		spec.Changes = true
	case OpLoopAdd, OpLoopEdit, OpLoopPause, OpLoopResume, OpLoopRm:
		spec.Changes = true
	case OpLoopList, OpLoopShow, OpLoopHistory, OpLoopHistoryRun, OpLoopHistoryClear,
		OpLoopMemoryList, OpLoopMemoryGet, OpLoopMemorySet, OpLoopMemoryRm, OpLoopMemoryClear:
	default:
		return OpSpec{}, false
	}
	return spec, true
}

// newLoopArgs returns an empty argument struct for a loop operation.
func newLoopArgs(op string) (any, bool) {
	switch op {
	case OpLoopList:
		return &LoopListArgs{}, true
	case OpLoopAdd, OpLoopEdit:
		return &LoopSpec{}, true
	case OpLoopRun:
		return &LoopRunArgs{}, true
	case OpLoopHistory:
		return &LoopHistoryArgs{}, true
	case OpLoopHistoryRun:
		return &LoopHistoryRunArgs{}, true
	case OpLoopMemoryGet, OpLoopMemoryRm:
		return &LoopMemoryKey{}, true
	case OpLoopMemorySet:
		return &LoopMemorySetArgs{}, true
	case OpLoopShow, OpLoopPause, OpLoopResume, OpLoopRm, OpLoopHistoryClear, OpLoopMemoryList, OpLoopMemoryClear:
		return &LoopName{}, true
	}
	return nil, false
}

// validateLoopArgs checks the fields of a loop argument struct that need no operation name.
func validateLoopArgs(args any) error {
	switch a := args.(type) {
	case *LoopListArgs:
		if a.Offset < 0 {
			return usageError("offset must be 0 or more")
		}
		return nil
	case *LoopSpec:
		return validateLoopSpec(a)
	case *LoopName:
		return validateLoopName(a.Name)
	case *LoopRunArgs:
		return validateLoopName(a.Name)
	case *LoopHistoryArgs:
		return validateLoopHistory(a)
	case *LoopHistoryRunArgs:
		return validateLoopHistoryRun(a)
	case *LoopMemoryKey:
		return validateLoopMemoryKey(a)
	case *LoopMemorySetArgs:
		return validateLoopMemorySet(a)
	}
	return usageError("unknown operation arguments")
}

func validateLoopName(name string) error {
	if !loopNamePattern.MatchString(name) {
		return &ArgsError{Code: "name_invalid", Message: "a loop name is 1 to 40 characters of a-z, 0-9 and dashes, starting with a letter or digit"}
	}
	return nil
}

func validateLoopSpec(a *LoopSpec) error {
	if err := validateLoopName(a.Name); err != nil {
		return err
	}
	if err := validateLoopTitle(a.Title); err != nil {
		return err
	}
	if err := validateLoopPrompt(a.Prompt); err != nil {
		return err
	}
	if a.Specs != nil {
		for _, spec := range *a.Specs {
			if err := validateSpec(spec); err != nil {
				return err
			}
		}
	}
	if err := validateLoopOptions(a); err != nil {
		return err
	}
	if a.Script != nil && len(*a.Script) > MaxScriptBytes {
		return usageError(fmt.Sprintf("a script is at most %d bytes", MaxScriptBytes))
	}
	if a.Script != nil && !validLoopText(*a.Script) {
		return usageError("a script is UTF-8 text without NUL")
	}
	if a.Vars != nil {
		return validateLoopVars(*a.Vars)
	}
	return nil
}

// validateLoopOptions checks the optional fields whose values are fixed sets or patterns.
func validateLoopOptions(a *LoopSpec) error {
	if a.Keep != nil && (*a.Keep < 1 || *a.Keep > maxKeepPerLoop) {
		return usageError(fmt.Sprintf("keep must be 1 to %d", maxKeepPerLoop))
	}
	if a.Runner != nil && *a.Runner != runnerClaude && *a.Runner != runnerWS {
		return usageError("runner must be claude or ws")
	}
	if a.Endpoint != nil {
		if err := validateEndpoint(*a.Endpoint); err != nil {
			return err
		}
	}
	if a.Model != nil && !modelPattern.MatchString(*a.Model) {
		return usageError("model " + strconv.Quote(*a.Model) + " is not a valid model name")
	}
	if a.PermissionMode != nil && !slices.Contains(PermissionModes(), *a.PermissionMode) {
		return usageError("permission mode " + strconv.Quote(*a.PermissionMode) + " is not one of " + fmt.Sprint(PermissionModes()))
	}
	if err := validateLoopDuration("every", a.Every); err != nil {
		return err
	}
	return validateLoopDuration("script_timeout", a.ScriptTimeout)
}

// validateLoopDuration checks a given duration argument: a positive Go duration
// (so never a flag or text with control characters). ws checks the allowed range.
func validateLoopDuration(field string, value *string) error {
	if value == nil {
		return nil
	}
	if d, err := time.ParseDuration(*value); err != nil || d <= 0 {
		return usageError(field + " is a positive duration such as 15m")
	}
	return nil
}

// validLoopText reports whether s is valid UTF-8 without NUL.
func validLoopText(s string) bool {
	return utf8.ValidString(s) && !strings.Contains(s, "\x00")
}

func validateLoopTitle(title *string) error {
	if title == nil {
		return nil
	}
	if len(*title) > maxLoopTitleBytes || containsControl(*title) {
		return usageError(fmt.Sprintf("a title is at most %d bytes without control characters", maxLoopTitleBytes))
	}
	return nil
}

func validateLoopPrompt(prompt *string) error {
	if prompt == nil {
		return nil
	}
	if *prompt == "" || len(*prompt) > MaxPromptBytes {
		return usageError(fmt.Sprintf("a prompt is 1 to %d bytes", MaxPromptBytes))
	}
	if !validLoopText(*prompt) {
		return usageError("a prompt is UTF-8 text without NUL")
	}
	return nil
}

func validateLoopVars(vars map[string]string) error {
	if len(vars) > MaxVars {
		return usageError(fmt.Sprintf("at most %d vars", MaxVars))
	}
	total := 0
	for key, value := range vars {
		if !varKeyPattern.MatchString(key) {
			return usageError("var key " + strconv.Quote(key) + " is not a letter or underscore, then up to 63 letters, digits or underscores")
		}
		if len(value) > MaxVarValueBytes {
			return usageError(fmt.Sprintf("a var value is at most %d bytes", MaxVarValueBytes))
		}
		if !validLoopText(value) {
			return usageError("a var value is UTF-8 text without NUL")
		}
		total += len(key) + len(value)
	}
	if total > MaxVarsBytes {
		return usageError(fmt.Sprintf("vars are at most %d bytes in all", MaxVarsBytes))
	}
	return nil
}

func validateLoopHistory(a *LoopHistoryArgs) error {
	if err := validateLoopName(a.Name); err != nil {
		return err
	}
	if a.Limit < 0 || a.Limit > MaxHistoryLimit {
		return usageError(fmt.Sprintf("limit must be 0 to %d", MaxHistoryLimit))
	}
	return nil
}

func validateLoopHistoryRun(a *LoopHistoryRunArgs) error {
	if err := validateLoopName(a.Name); err != nil {
		return err
	}
	if a.Seq < 1 {
		return usageError("seq must be 1 or more")
	}
	return nil
}

func validateLoopMemoryKey(a *LoopMemoryKey) error {
	if err := validateLoopName(a.Name); err != nil {
		return err
	}
	if err := validateMemoryKey(a.Key); err != nil {
		return err
	}
	return validateExpectRev(a.ExpectRev)
}

func validateLoopMemorySet(a *LoopMemorySetArgs) error {
	if err := validateLoopName(a.Name); err != nil {
		return err
	}
	if err := validateMemoryKey(a.Key); err != nil {
		return err
	}
	if len(a.Value) > MaxMemoryValue {
		return usageError(fmt.Sprintf("a memory value is at most %d bytes", MaxMemoryValue))
	}
	return validateExpectRev(a.ExpectRev)
}

func validateMemoryKey(key string) error {
	if !varKeyPattern.MatchString(key) {
		return usageError("a memory key is a letter or underscore, then up to 63 letters, digits or underscores")
	}
	return nil
}

func validateExpectRev(rev *int) error {
	if rev != nil && *rev < 0 {
		return usageError("expect_rev must be 0 or more")
	}
	return nil
}

// validateOp checks the rules that depend on the operation: confirm, a
// redacted value, and the fields loop.add and loop.edit need. DecodeArgs
// calls it after Validate.
func validateOp(op string, args any) error {
	if NeedsConfirm(op, args) && !confirmed(args) {
		return usageError(op + " needs confirm")
	}
	if (op == OpLoopAdd || op == OpLoopEdit || op == OpLoopMemorySet) && HasRedacted(args) {
		return usageError("a value contains [redacted]; retype it or leave it unchanged")
	}
	spec, isSpec := args.(*LoopSpec)
	if !isSpec {
		return nil
	}
	switch op {
	case OpLoopAdd:
		return validateLoopAdd(spec)
	case OpLoopEdit:
		return validateLoopEdit(spec)
	}
	return nil
}

func validateLoopAdd(spec *LoopSpec) error {
	if spec.Every == nil {
		return usageError("loop.add needs every")
	}
	if spec.Prompt == nil {
		return usageError("loop.add needs a prompt")
	}
	return nil
}

func validateLoopEdit(spec *LoopSpec) error {
	if spec.Paused != nil {
		return usageError("paused is for loop.add; use loop.pause or loop.resume")
	}
	if !spec.changes() {
		return usageError("nothing to change")
	}
	return nil
}

// changes reports whether a loop.edit's spec sets any field besides Name and Confirm.
func (spec *LoopSpec) changes() bool {
	return spec.Title != nil || spec.Every != nil || spec.Prompt != nil || spec.Specs != nil ||
		spec.Keep != nil || spec.Runner != nil || spec.Endpoint != nil || spec.Model != nil ||
		spec.PermissionMode != nil || spec.Script != nil || spec.ScriptTimeout != nil ||
		spec.Vars != nil || spec.Hidden != nil
}

// NeedsConfirm reports whether op with args needs Confirm (ADR 0078):
// loop.rm, loop.history_clear and loop.memory_clear always; loop.add and
// loop.edit when Script is non-nil and non-empty, or PermissionMode is
// PermissionBypass.
func NeedsConfirm(op string, args any) bool {
	switch op {
	case OpLoopRm, OpLoopHistoryClear, OpLoopMemoryClear:
		return true
	case OpLoopAdd, OpLoopEdit:
		spec, ok := args.(*LoopSpec)
		if !ok {
			return false
		}
		hasScript := spec.Script != nil && *spec.Script != ""
		bypass := spec.PermissionMode != nil && *spec.PermissionMode == PermissionBypass
		return hasScript || bypass
	}
	return false
}

// confirmed reports whether args carries Confirm set to true.
func confirmed(args any) bool {
	switch a := args.(type) {
	case *LoopName:
		return a.Confirm
	case *LoopSpec:
		return a.Confirm
	}
	return false
}

// HasRedacted reports whether any string in args contains RedactedMarker.
// It searches the JSON encoding, where the marker's characters are never escaped.
func HasRedacted(args any) bool {
	body, err := json.Marshal(args)
	if err != nil {
		return false
	}
	return bytes.Contains(body, []byte(RedactedMarker))
}

func containsControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
