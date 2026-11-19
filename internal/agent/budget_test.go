package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRetryWait(t *testing.T) {
	assert.Equal(t, 2*time.Second, retryWait(1, 0))
	assert.Equal(t, 8*time.Second, retryWait(2, 0))
	assert.Equal(t, 30*time.Second, retryWait(3, 0))
	assert.Equal(t, 45*time.Second, retryWait(1, 45*time.Second))
	assert.Equal(t, 60*time.Second, retryWait(1, 90*time.Second))
	assert.Equal(t, 30*time.Second, retryWait(3, 10*time.Second))
}
