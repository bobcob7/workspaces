package envagent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHasPathPrefix(t *testing.T) {
	t.Run("ignores case", func(t *testing.T) {
		assert.True(t, hasPathPrefix(`C:\ws\a`, `c:\WS\`))
	})
	t.Run("matches whole path elements only", func(t *testing.T) {
		assert.False(t, hasPathPrefix(`C:\wsx`, `C:\ws\`))
	})
	t.Run("different drives don't match", func(t *testing.T) {
		assert.False(t, hasPathPrefix(`D:\ws\a`, `C:\ws\`))
	})
}
