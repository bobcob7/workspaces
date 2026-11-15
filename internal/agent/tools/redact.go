package tools

import (
	"slices"
	"strings"
	"sync"
)

const redactMinLength = 4

// Redactor replaces registered values with "[redacted]". Safe for
// concurrent use; values shorter than 4 bytes are ignored.
type Redactor struct {
	mu     sync.Mutex
	values []string // longest first
}

// NewRedactor returns a Redactor for values.
func NewRedactor(values ...string) *Redactor {
	r := &Redactor{}
	r.Add(values...)
	return r
}

// Add registers values to be redacted from now on.
func (r *Redactor) Add(values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range values {
		if len(v) >= redactMinLength && !slices.Contains(r.values, v) {
			r.values = append(r.values, v)
		}
	}
	slices.SortStableFunc(r.values, func(a, b string) int { return len(b) - len(a) })
}

// Redact returns s with every registered value replaced.
func (r *Redactor) Redact(s string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.values {
		s = strings.ReplaceAll(s, v, "[redacted]")
	}
	return s
}
