package fakeollama

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"

	"github.com/bobcob7/workspaces/internal/fakeollama/internal/yamlconfig"
)

var errInvalidFault = errors.New("invalid fault")

// Fault is an injected failure. A fault may combine several effects; they
// apply in the order hang, slow first token, status, then the stream effects.
type Fault struct {
	// Model limits the fault to chat completions for this model. Without it
	// the fault applies to every API request, including /v1/models.
	Model string `json:"model,omitzero" yaml:"model"`
	// Count limits the fault to the next Count requests it affects. Zero
	// means until cleared. Reading faults back reports the remaining count.
	Count int `json:"count,omitzero" yaml:"count"`
	// Down stops accepting connections. It can't be scoped or counted.
	Down bool `json:"down,omitzero" yaml:"down"`
	// Status responds with this HTTP status and an OpenAI error body.
	Status int `json:"status,omitzero" yaml:"status"`
	// Message overrides the error message sent with Status.
	Message string `json:"message,omitzero" yaml:"message"`
	// RetryAfter, with Status, sends a Retry-After header of this many seconds.
	RetryAfter int `json:"retry_after,omitzero" yaml:"retry_after"`
	// Hang accepts the request and never responds.
	Hang bool `json:"hang,omitzero" yaml:"hang"`
	// SlowFirstToken delays the first byte of the response.
	SlowFirstToken yamlconfig.Duration `json:"slow_first_token,omitzero" yaml:"slow_first_token"`
	// DropAfterChunks closes the connection after this many stream chunks.
	DropAfterChunks int `json:"drop_after_chunks,omitzero" yaml:"drop_after_chunks"`
	// StallAfterChunks stops sending, without closing, after this many
	// stream chunks.
	StallAfterChunks int `json:"stall_after_chunks,omitzero" yaml:"stall_after_chunks"`
}

// target describes a request for fault matching.
type target struct {
	model  string
	stream bool
}

func (f Fault) validate(models []Model) error {
	if err := f.validateValues(); err != nil {
		return err
	}
	if err := f.validateEffect(); err != nil {
		return err
	}
	if f.Model != "" && !slices.ContainsFunc(models, func(m Model) bool { return m.Name == f.Model }) {
		return fmt.Errorf("%w: unknown model %q", errInvalidFault, f.Model)
	}
	return nil
}

func (f Fault) validateValues() error {
	switch {
	case f.Count < 0 || f.SlowFirstToken < 0 || f.DropAfterChunks < 0 || f.StallAfterChunks < 0 || f.RetryAfter < 0:
		return fmt.Errorf("%w: values must not be negative", errInvalidFault)
	case f.Status != 0 && (f.Status < 400 || f.Status > 599):
		return fmt.Errorf("%w: status %d is not an error status", errInvalidFault, f.Status)
	}
	return nil
}

func (f Fault) validateEffect() error {
	switch {
	case f.RetryAfter != 0 && f.Status == 0:
		return fmt.Errorf("%w: retry_after needs status", errInvalidFault)
	case f.Down && (f.Model != "" || f.Count != 0):
		return fmt.Errorf("%w: down can't be scoped to a model or count", errInvalidFault)
	case !f.Down && !f.Hang && f.Status == 0 && f.SlowFirstToken == 0 && f.DropAfterChunks == 0 && f.StallAfterChunks == 0:
		return fmt.Errorf("%w: no effect set", errInvalidFault)
	}
	return nil
}

func validateFaults(faults []Fault, models []Model) error {
	for i, f := range faults {
		if err := f.validate(models); err != nil {
			return fmt.Errorf("fault %d: %w", i+1, err)
		}
	}
	return nil
}

func anyDown(faults []Fault) bool {
	return slices.ContainsFunc(faults, func(f Fault) bool { return f.Down })
}

// affects reports whether the fault changes the response to t. Requests a
// fault can't affect don't use up its count.
func (f Fault) affects(t target) bool {
	if f.Down || (f.Model != "" && f.Model != t.model) {
		return false
	}
	streamEffect := f.DropAfterChunks > 0 || f.StallAfterChunks > 0
	return f.Hang || f.Status != 0 || f.SlowFirstToken > 0 || (t.stream && streamEffect)
}

// decodeFaults accepts a single fault object or an array of them.
func decodeFaults(data []byte) ([]Fault, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("%w: empty body", errInvalidFault)
	}
	if trimmed[0] == '[' {
		var faults []Fault
		if err := json.Unmarshal(trimmed, &faults, json.RejectUnknownMembers(true)); err != nil {
			return nil, fmt.Errorf("%w: %w", errInvalidFault, err)
		}
		return faults, nil
	}
	var f Fault
	if err := json.Unmarshal(trimmed, &f, json.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("%w: %w", errInvalidFault, err)
	}
	return []Fault{f}, nil
}
