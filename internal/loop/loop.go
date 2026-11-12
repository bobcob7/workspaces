// Package loop keeps agent loops: a named prompt that ws starts in a new
// workspace every period (ADR 0048).
package loop

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// FormatVersion is the newest loop record version this ws reads and writes.
const FormatVersion = 3

// Version1 is the record version of loops added before ADR 0049: no
// script, no vars, and a literal prompt.
const Version1 = 1

// Version2 is the record version of loops added before ADR 0057: no
// hidden.
const Version2 = 2

// Bounds of a loop's period and keep count, as ADR 0048 and ws loop add
// document them.
const (
	MinEvery    = 5 * time.Minute
	MaxEvery    = 168 * time.Hour
	DefaultKeep = 5
	MaxKeep     = 50
	// MaxRuns is how many runs a record keeps, oldest dropped first.
	MaxRuns     = 20
	maxNameLen  = 40
	maxTitleLen = 80
)

// Bounds of a loop's script timeout and vars (ADR 0049).
const (
	DefaultScriptTimeout = time.Minute
	MinScriptTimeout     = time.Second
	MaxScriptTimeout     = 10 * time.Minute
	MaxVars              = 32
	MaxVarValueLen       = 4096
	maxVarKeyLen         = 64
)

// Outcomes of a loop's gate script, as last_gate.outcome records them.
const (
	GatePassed   = "passed"
	GateSkipped  = "skipped"
	GateTimedOut = "timed_out"
	GateFailed   = "failed"
)

// Errors that the loop store and validation return, wrapped with context.
var (
	// ErrInvalid is a loop whose name, period, prompt, or keep is out of range.
	ErrInvalid = errors.New("invalid loop")
	// ErrNotFound is a loop that doesn't exist.
	ErrNotFound = errors.New("loop not found")
	// ErrExists is a loop that already exists.
	ErrExists = errors.New("loop already exists")
	// ErrSkipped is a run not started because the loop's script exited non-zero.
	ErrSkipped = errors.New("loop script exited non-zero")
	// ErrScriptFailed is a loop script that timed out or couldn't start.
	ErrScriptFailed = errors.New("loop script failed")
)

// Run is one run of a loop: the workspace and session it started, or why
// it couldn't start.
type Run struct {
	StartedAt    time.Time `toml:"started_at"`
	Workspace    string    `toml:"workspace"`
	Session      string    `toml:"session"`
	ErrorCode    string    `toml:"error_code"`
	ErrorMessage string    `toml:"error_message"`
}

// Gate is the outcome of the last time a loop's script ran.
type Gate struct {
	At       time.Time `toml:"at" json:"at"`
	Outcome  string    `toml:"outcome" json:"outcome"`
	ExitCode int       `toml:"exit_code" json:"exit_code"` // -1 when it timed out, was killed by a signal, or couldn't start
	Message  string    `toml:"message" json:"message"`
}

// Loop is CONFIG_DIR/loops/<Name>.toml.
type Loop struct {
	Version        int               `toml:"version"`
	Name           string            `toml:"name"`
	Title          string            `toml:"title,omitempty"`
	Every          string            `toml:"every"`
	Prompt         string            `toml:"prompt"`
	Specs          []string          `toml:"specs"`
	Model          string            `toml:"model"`
	PermissionMode string            `toml:"permission_mode"`
	Keep           int               `toml:"keep"`
	Paused         bool              `toml:"paused"`
	CreatedAt      time.Time         `toml:"created_at"`
	NextRunAt      time.Time         `toml:"next_run_at"`
	Script         string            `toml:"script,omitempty"`
	ScriptTimeout  string            `toml:"script_timeout,omitempty"`
	Vars           map[string]string `toml:"vars,omitempty"`
	LastGate       *Gate             `toml:"last_gate,omitempty"`
	// Hidden is whether runs start hidden (ADR 0057); nil (a version 1 or 2
	// record) is the default, hidden.
	Hidden *bool `toml:"hidden,omitempty"`
	Runs   []Run `toml:"runs"`
}

// RunsHidden is *l.Hidden, or true when it is nil.
func (l *Loop) RunsHidden() bool {
	return l.Hidden == nil || *l.Hidden
}

// ValidateName checks a loop name: 1–40 chars of a-z, 0-9, '-', and the
// first char a letter or digit.
func ValidateName(name string) error {
	if name == "" || len(name) > maxNameLen {
		return fmt.Errorf("%w: name %q is not 1-%d characters", ErrInvalid, name, maxNameLen)
	}
	for i := range len(name) {
		c := name[i]
		switch {
		case isLowerOrDigit(c):
		case c == '-' && i > 0:
		default:
			return fmt.Errorf("%w: name %q must be a-z, 0-9, or '-', starting with a letter or digit", ErrInvalid, name)
		}
	}
	return nil
}

// ValidateTitle checks a loop's display title: "" (no title), or up to
// 80 characters with no control characters, already trimmed of spaces.
func ValidateTitle(title string) error {
	if title == "" {
		return nil
	}
	if title != strings.TrimSpace(title) {
		return fmt.Errorf("%w: title %q has leading or trailing spaces", ErrInvalid, title)
	}
	if utf8.RuneCountInString(title) > maxTitleLen {
		return fmt.Errorf("%w: title %q is over %d characters", ErrInvalid, title, maxTitleLen)
	}
	for _, r := range title {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: title %q has a control character", ErrInvalid, title)
		}
	}
	return nil
}

func isLowerOrDigit(c byte) bool {
	return ('a' <= c && c <= 'z') || ('0' <= c && c <= '9')
}

// ParseEvery parses a Go duration of whole minutes in [MinEvery, MaxEvery].
func ParseEvery(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%w: period %q is not a duration like 15m or 24h: %w", ErrInvalid, s, err)
	}
	if d%time.Minute != 0 {
		return 0, fmt.Errorf("%w: period %q is not a whole number of minutes", ErrInvalid, s)
	}
	if d < MinEvery {
		return 0, fmt.Errorf("%w: period %q is under %s", ErrInvalid, s, FormatEvery(MinEvery))
	}
	if d > MaxEvery {
		return 0, fmt.Errorf("%w: period %q is over %s", ErrInvalid, s, FormatEvery(MaxEvery))
	}
	return d, nil
}

// FormatEvery prints d without zero units: 24h, 1h30m, 15m.
func FormatEvery(d time.Duration) string {
	s := d.String()
	// Whole minutes only, so the seconds are always "0s" and the minutes
	// are "0m" only when they are the hour's zero ("1h0m").
	s = strings.TrimSuffix(s, "0s")
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// ValidateVarKey checks a var key: 1–64 bytes of [A-Za-z0-9_], not
// starting with a digit.
func ValidateVarKey(key string) error {
	if key == "" || len(key) > maxVarKeyLen {
		return fmt.Errorf("%w: var key %q is not 1-%d characters", ErrInvalid, key, maxVarKeyLen)
	}
	for i := range len(key) {
		c := key[i]
		isDigit := '0' <= c && c <= '9'
		switch {
		case c == '_', 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z':
		case isDigit && i > 0:
		default:
			return fmt.Errorf("%w: var key %q must be letters, digits, or '_', not starting with a digit", ErrInvalid, key)
		}
	}
	return nil
}

// ParseVars parses --var arguments, KEY=VALUE each, split at the first
// '='. VALUE may be empty or contain '='. A missing '=', a bad key, a key
// given twice, a value over MaxVarValueLen bytes, or more than MaxVars
// is ErrInvalid. No arguments is a nil map.
func ParseVars(args []string) (map[string]string, error) {
	if len(args) > MaxVars {
		return nil, fmt.Errorf("%w: %d vars is more than %d", ErrInvalid, len(args), MaxVars)
	}
	var vars map[string]string
	if len(args) > 0 {
		vars = make(map[string]string, len(args))
	}
	for _, arg := range args {
		key, value, ok := strings.Cut(arg, "=")
		if !ok {
			return nil, fmt.Errorf("%w: var %q must be KEY=VALUE", ErrInvalid, arg)
		}
		if err := ValidateVarKey(key); err != nil {
			return nil, err
		}
		if _, dup := vars[key]; dup {
			return nil, fmt.Errorf("%w: var key %q is given twice", ErrInvalid, key)
		}
		if len(value) > MaxVarValueLen {
			return nil, fmt.Errorf("%w: var %q value is over %d bytes", ErrInvalid, key, MaxVarValueLen)
		}
		vars[key] = value
	}
	return vars, nil
}

// ParseScriptTimeout parses a Go duration of whole seconds in
// [MinScriptTimeout, MaxScriptTimeout].
func ParseScriptTimeout(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%w: script timeout %q is not a duration like 45s or 2m: %w", ErrInvalid, s, err)
	}
	if d%time.Second != 0 {
		return 0, fmt.Errorf("%w: script timeout %q is not a whole number of seconds", ErrInvalid, s)
	}
	if d < MinScriptTimeout || d > MaxScriptTimeout {
		return 0, fmt.Errorf("%w: script timeout %q is not %s-%s", ErrInvalid, s, FormatScriptTimeout(MinScriptTimeout), FormatScriptTimeout(MaxScriptTimeout))
	}
	return d, nil
}

// FormatScriptTimeout prints d without zero units: 1m, 1m30s, 45s, 10m.
func FormatScriptTimeout(d time.Duration) string {
	s := d.String()
	// Whole seconds only: strip the "0s" after a whole minute, then the
	// "0m" after a whole hour.
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// Interval is ParseEvery(l.Every).
func (l *Loop) Interval() (time.Duration, error) {
	return ParseEvery(l.Every)
}

// Timeout is the script's timeout: DefaultScriptTimeout when
// ScriptTimeout is empty, else ParseScriptTimeout(l.ScriptTimeout).
func (l *Loop) Timeout() (time.Duration, error) {
	if l.ScriptTimeout == "" {
		return DefaultScriptTimeout, nil
	}
	return ParseScriptTimeout(l.ScriptTimeout)
}

// Templated reports whether the prompt is a template: every version but
// Version1 (a new, unsaved Loop has Version 0 and is templated).
func (l *Loop) Templated() bool {
	return l.Version != Version1
}

// Validate checks Name, Title, Every, Prompt (not empty after strings.TrimSpace),
// Keep (1..MaxKeep), Vars, the script timeout, and the prompt template.
func (l *Loop) Validate() error {
	if err := ValidateName(l.Name); err != nil {
		return err
	}
	if err := ValidateTitle(l.Title); err != nil {
		return err
	}
	if _, err := l.Interval(); err != nil {
		return err
	}
	if strings.TrimSpace(l.Prompt) == "" {
		return fmt.Errorf("%w: prompt is empty", ErrInvalid)
	}
	if l.Keep < 1 || l.Keep > MaxKeep {
		return fmt.Errorf("%w: keep %d is not 1-%d", ErrInvalid, l.Keep, MaxKeep)
	}
	if len(l.Vars) > MaxVars {
		return fmt.Errorf("%w: %d vars is more than %d", ErrInvalid, len(l.Vars), MaxVars)
	}
	for key, value := range l.Vars {
		if err := ValidateVarKey(key); err != nil {
			return err
		}
		if len(value) > MaxVarValueLen {
			return fmt.Errorf("%w: var %q value is over %d bytes", ErrInvalid, key, MaxVarValueLen)
		}
	}
	if l.Script == "" && l.ScriptTimeout != "" {
		return fmt.Errorf("%w: script_timeout without a script", ErrInvalid)
	}
	if l.Script != "" {
		if _, err := l.Timeout(); err != nil {
			return err
		}
	}
	return l.CheckPrompt()
}

// LastRun is the newest run, or nil.
func (l *Loop) LastRun() *Run {
	if len(l.Runs) == 0 {
		return nil
	}
	return &l.Runs[len(l.Runs)-1]
}
