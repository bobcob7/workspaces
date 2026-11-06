package loop

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// Edit is a change to a loop, for ws loop edit (ADR 0052). A nil field is
// left as it is.
type Edit struct {
	Every          *string
	Prompt         *string
	Specs          *[]string // replaces the specs; an empty slice clears them
	Keep           *int
	Model          *string // "" clears it
	PermissionMode *string // "" clears it
	Script         *string // blank after strings.TrimSpace removes the script
	ScriptTimeout  *string
	Vars           *map[string]string // replaces the vars; an empty map clears them
}

// Empty reports whether e changes nothing.
func (e *Edit) Empty() bool {
	return e.Every == nil && e.Prompt == nil && e.Specs == nil && e.Keep == nil &&
		e.Model == nil && e.PermissionMode == nil && e.Script == nil &&
		e.ScriptTimeout == nil && e.Vars == nil
}

// Apply changes l by e at now, and validates the result with Validate.
// On any error l is left unchanged. See ADR 0052 for each rule.
func (l *Loop) Apply(e *Edit, now time.Time) error {
	c := *l
	c.Specs = slices.Clone(l.Specs)
	c.Vars = maps.Clone(l.Vars)
	if err := e.applyTo(&c, l, now); err != nil {
		return err
	}
	if err := c.Validate(); err != nil {
		return err
	}
	*l = c
	return nil
}

// applyTo sets the fields of c that e changes. old is the loop before the edit.
func (e *Edit) applyTo(c, old *Loop, now time.Time) error {
	if e.Every != nil {
		if err := applyEvery(c, old, *e.Every, now); err != nil {
			return err
		}
	}
	e.applyFields(c)
	if e.Script != nil {
		applyScript(c, old, *e.Script, e.ScriptTimeout != nil)
	}
	if e.ScriptTimeout != nil {
		if err := applyScriptTimeout(c, *e.ScriptTimeout); err != nil {
			return err
		}
	}
	if c.Version == Version1 && (e.Prompt != nil || e.Script != nil || e.Vars != nil) {
		c.Version = FormatVersion
	}
	return nil
}

// applyFields sets the plain fields of c that e changes: prompt, specs,
// keep, model, permission mode, and vars.
func (e *Edit) applyFields(c *Loop) {
	if e.Prompt != nil {
		c.Prompt = *e.Prompt
	}
	if e.Specs != nil {
		c.Specs = slices.Clone(*e.Specs)
		if len(c.Specs) == 0 {
			c.Specs = nil
		}
	}
	if e.Keep != nil {
		c.Keep = *e.Keep
	}
	if e.Model != nil {
		c.Model = strings.TrimSpace(*e.Model)
	}
	if e.PermissionMode != nil {
		c.PermissionMode = strings.TrimSpace(*e.PermissionMode)
	}
	if e.Vars != nil {
		c.Vars = maps.Clone(*e.Vars)
		if len(c.Vars) == 0 {
			c.Vars = nil
		}
	}
}

// applyEvery sets the period to every. When its length changes, the next
// run counts from the last run's start (or the creation), and is now plus
// the period if that is not after now.
func applyEvery(c, old *Loop, every string, now time.Time) error {
	d, err := ParseEvery(every)
	if err != nil {
		return err
	}
	oldD, oldErr := old.Interval()
	c.Every = FormatEvery(d)
	if oldErr == nil && d == oldD {
		return nil
	}
	base := old.CreatedAt
	if last := old.LastRun(); last != nil {
		base = last.StartedAt
	}
	next := base.Add(d)
	if !next.After(now) {
		next = now.Add(d)
	}
	c.NextRunAt = next
	return nil
}

// applyScript sets the script to text. A blank text removes the script, its
// timeout, and its gate. Changing the text clears the gate.
func applyScript(c, old *Loop, text string, timeoutGiven bool) {
	if strings.TrimSpace(text) == "" {
		c.Script = ""
		c.ScriptTimeout = ""
		c.LastGate = nil
		return
	}
	if text != old.Script {
		c.LastGate = nil
	}
	c.Script = text
	if c.ScriptTimeout == "" && !timeoutGiven {
		c.ScriptTimeout = FormatScriptTimeout(DefaultScriptTimeout)
	}
}

// applyScriptTimeout sets the script timeout to s, which needs a script.
func applyScriptTimeout(c *Loop, s string) error {
	if c.Script == "" {
		return fmt.Errorf("%w: script timeout without a script", ErrInvalid)
	}
	d, err := ParseScriptTimeout(s)
	if err != nil {
		return err
	}
	c.ScriptTimeout = FormatScriptTimeout(d)
	return nil
}
