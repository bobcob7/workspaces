// Package loop keeps agent loops: a named prompt that ws starts in a new
// workspace every period (ADR 0048).
package loop

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// FormatVersion is the newest loop record version this ws reads and writes.
const FormatVersion = 1

// Bounds of a loop's period and keep count, as ADR 0048 and ws loop add
// document them.
const (
	MinEvery    = 5 * time.Minute
	MaxEvery    = 168 * time.Hour
	DefaultKeep = 5
	MaxKeep     = 50
	// MaxRuns is how many runs a record keeps, oldest dropped first.
	MaxRuns    = 20
	maxNameLen = 40
)

// Errors that the loop store and validation return, wrapped with context.
var (
	// ErrInvalid is a loop whose name, period, prompt, or keep is out of range.
	ErrInvalid = errors.New("invalid loop")
	// ErrNotFound is a loop that doesn't exist.
	ErrNotFound = errors.New("loop not found")
	// ErrExists is a loop that already exists.
	ErrExists = errors.New("loop already exists")
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

// Loop is CONFIG_DIR/loops/<Name>.toml.
type Loop struct {
	Version        int       `toml:"version"`
	Name           string    `toml:"name"`
	Every          string    `toml:"every"`
	Prompt         string    `toml:"prompt"`
	Specs          []string  `toml:"specs"`
	Model          string    `toml:"model"`
	PermissionMode string    `toml:"permission_mode"`
	Keep           int       `toml:"keep"`
	Paused         bool      `toml:"paused"`
	CreatedAt      time.Time `toml:"created_at"`
	NextRunAt      time.Time `toml:"next_run_at"`
	Runs           []Run     `toml:"runs"`
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

// Interval is ParseEvery(l.Every).
func (l *Loop) Interval() (time.Duration, error) {
	return ParseEvery(l.Every)
}

// Validate checks Name, Every, Prompt (not empty after strings.TrimSpace),
// and Keep (1..MaxKeep).
func (l *Loop) Validate() error {
	if err := ValidateName(l.Name); err != nil {
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
	return nil
}

// LastRun is the newest run, or nil.
func (l *Loop) LastRun() *Run {
	if len(l.Runs) == 0 {
		return nil
	}
	return &l.Runs[len(l.Runs)-1]
}
