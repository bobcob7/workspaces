package proc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statLine is a /proc/<pid>/stat line whose command name holds a space and
// a parenthesis, with starttime 4242.
const statLine = "123 (a b) c) S 1 123 123 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 4242 1000 10 18446744073709551615\n"

func TestParseStartTicks(t *testing.T) {
	t.Run("counts fields from the last parenthesis", func(t *testing.T) {
		ticks, err := parseStartTicks([]byte(statLine))
		require.NoError(t, err)
		assert.Equal(t, int64(4242), ticks)
	})
	t.Run("no command name", func(t *testing.T) {
		_, err := parseStartTicks([]byte("123 S 1"))
		assert.ErrorIs(t, err, errBadStat)
	})
	t.Run("too few fields", func(t *testing.T) {
		_, err := parseStartTicks([]byte("123 (x) S 1 2"))
		assert.ErrorIs(t, err, errBadStat)
	})
	t.Run("starttime isn't a number", func(t *testing.T) {
		_, err := parseStartTicks([]byte("123 (x) S 1 123 123 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 x 1000"))
		assert.ErrorIs(t, err, errBadStat)
	})
}

func TestParseBootTime(t *testing.T) {
	t.Run("reads btime", func(t *testing.T) {
		got, err := parseBootTime([]byte("cpu  1 2 3\nbtime 1792400000\nprocesses 5\n"))
		require.NoError(t, err)
		assert.Equal(t, time.Unix(1792400000, 0), got)
	})
	t.Run("no btime", func(t *testing.T) {
		_, err := parseBootTime([]byte("cpu  1 2 3\n"))
		assert.ErrorIs(t, err, errBadStat)
	})
	t.Run("btime isn't a number", func(t *testing.T) {
		_, err := parseBootTime([]byte("btime soon\n"))
		assert.ErrorIs(t, err, errBadStat)
	})
}

func TestTicksToDuration(t *testing.T) {
	assert.Equal(t, 42420*time.Millisecond, ticksToDuration(4242))
}
