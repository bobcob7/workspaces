package runner

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewSessionID(t *testing.T) {
	t.Parallel()
	id := NewSessionID()
	assert.Regexp(t, `^rn-[0-9a-f]{8}$`, id)
	assert.NotEqual(t, id, NewSessionID())
}
