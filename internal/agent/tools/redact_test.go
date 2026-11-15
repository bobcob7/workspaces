package tools

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedactor_Add(t *testing.T) {
	r := NewRedactor()
	r.Add("secret-one", "abc")
	assert.Equal(t, "[redacted] abc", r.Redact("secret-one abc"))
	r.Add("token-value")
	assert.Equal(t, "[redacted]", r.Redact("token-value"))
}

func TestRedactor_Redact(t *testing.T) {
	t.Run("replaces the longest registered value first", func(t *testing.T) {
		r := NewRedactor("abc", "abcdef")
		assert.Equal(t, "x[redacted]x", r.Redact("xabcdefx"))
	})
	t.Run("ignores values under 4 bytes", func(t *testing.T) {
		r := NewRedactor("abc")
		assert.Equal(t, "abc", r.Redact("abc"))
	})
	t.Run("is safe for concurrent Add and Redact", func(t *testing.T) {
		r := NewRedactor()
		var wg sync.WaitGroup
		for i := range 20 {
			wg.Add(2)
			go func() {
				defer wg.Done()
				r.Add(fmt.Sprintf("secret-%d", i))
			}()
			go func() {
				defer wg.Done()
				_ = r.Redact("secret-1")
			}()
		}
		wg.Wait()
		assert.Equal(t, "[redacted]", r.Redact("secret-1"))
	})
}
