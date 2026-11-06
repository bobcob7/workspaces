package loop

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "a", ok: true},
		{name: "deps-bump", ok: true},
		{name: "0-x", ok: true},
		{name: strings.Repeat("a", 40), ok: true},
		{name: "", ok: false},
		{name: "-x", ok: false},
		{name: "A", ok: false},
		{name: "a_b", ok: false},
		{name: "a/b", ok: false},
		{name: strings.Repeat("a", 41), ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateName(tt.name)
			if tt.ok {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, ErrInvalid)
		})
	}
}

func TestParseEvery(t *testing.T) {
	t.Parallel()
	valid := map[string]time.Duration{
		"5m":    5 * time.Minute,
		"15m":   15 * time.Minute,
		"1h30m": 90 * time.Minute,
		"24h":   24 * time.Hour,
		"168h":  168 * time.Hour,
	}
	for s, want := range valid {
		t.Run(s, func(t *testing.T) {
			t.Parallel()
			got, err := ParseEvery(s)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
	for _, s := range []string{"4m", "169h", "90s", "1h0m30s", "x", ""} {
		t.Run("rejects "+s, func(t *testing.T) {
			t.Parallel()
			_, err := ParseEvery(s)
			assert.ErrorIs(t, err, ErrInvalid)
		})
	}
}

func TestFormatEvery(t *testing.T) {
	t.Parallel()
	tests := map[string]time.Duration{
		"5m":    5 * time.Minute,
		"15m":   15 * time.Minute,
		"30m":   30 * time.Minute,
		"1h":    time.Hour,
		"1h30m": 90 * time.Minute,
		"24h":   24 * time.Hour,
		"168h":  168 * time.Hour,
	}
	for want, d := range tests {
		t.Run(want, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, want, FormatEvery(d))
		})
	}
}

func TestFormatEveryRoundTrips(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"5m", "15m", "1h30m", "24h", "168h"} {
		t.Run(s, func(t *testing.T) {
			t.Parallel()
			d, err := ParseEvery(s)
			require.NoError(t, err)
			assert.Equal(t, s, FormatEvery(d))
		})
	}
}

func TestLoopInterval(t *testing.T) {
	t.Parallel()
	l := Loop{Every: "1h30m"}
	got, err := l.Interval()
	require.NoError(t, err)
	assert.Equal(t, 90*time.Minute, got)
}

func validLoop() Loop {
	return Loop{Name: "deps-bump", Every: "24h", Prompt: "bump deps", Keep: DefaultKeep}
}

func TestLoopValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Loop)
		want   error
	}{
		{name: "valid", mutate: func(*Loop) {}},
		{name: "bad name", mutate: func(l *Loop) { l.Name = "A" }, want: ErrInvalid},
		{name: "bad period", mutate: func(l *Loop) { l.Every = "4m" }, want: ErrInvalid},
		{name: "empty prompt", mutate: func(l *Loop) { l.Prompt = " \n\t" }, want: ErrInvalid},
		{name: "keep zero", mutate: func(l *Loop) { l.Keep = 0 }, want: ErrInvalid},
		{name: "keep too big", mutate: func(l *Loop) { l.Keep = MaxKeep + 1 }, want: ErrInvalid},
		{name: "keep max", mutate: func(l *Loop) { l.Keep = MaxKeep }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := validLoop()
			tt.mutate(&l)
			err := l.Validate()
			if tt.want == nil {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, tt.want)
		})
	}
}

func TestLoopLastRun(t *testing.T) {
	t.Parallel()
	t.Run("nil without runs", func(t *testing.T) {
		t.Parallel()
		l := validLoop()
		assert.Nil(t, l.LastRun())
	})
	t.Run("newest run", func(t *testing.T) {
		t.Parallel()
		l := validLoop()
		l.AddRun(Run{Workspace: "loop-deps-bump-00000001"})
		l.AddRun(Run{Workspace: "loop-deps-bump-00000002"})
		require.NotNil(t, l.LastRun())
		assert.Equal(t, "loop-deps-bump-00000002", l.LastRun().Workspace)
	})
}
