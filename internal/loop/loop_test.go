package loop

import (
	"fmt"
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

func TestValidateVarKey(t *testing.T) {
	t.Parallel()
	valid := []string{"A", "REPO", "_x", "a1", strings.Repeat("a", 64)}
	for _, key := range valid {
		t.Run("accepts "+key, func(t *testing.T) {
			t.Parallel()
			assert.NoError(t, ValidateVarKey(key))
		})
	}
	invalid := []string{"", "1a", "my-key", "a.b", strings.Repeat("a", 65)}
	for _, key := range invalid {
		t.Run("rejects "+key, func(t *testing.T) {
			t.Parallel()
			assert.ErrorIs(t, ValidateVarKey(key), ErrInvalid)
		})
	}
}

func TestParseVars(t *testing.T) {
	t.Parallel()
	t.Run("parses KEY=VALUE", func(t *testing.T) {
		t.Parallel()
		got, err := ParseVars([]string{"A=1", "B=", "U=a=b"})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"A": "1", "B": "", "U": "a=b"}, got)
	})
	t.Run("no arguments is nil", func(t *testing.T) {
		t.Parallel()
		got, err := ParseVars(nil)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
	invalid := map[string][]string{
		"missing equals": {"A"},
		"empty key":      {"=x"},
		"duplicate key":  {"A=1", "A=2"},
		"value too long": {"A=" + strings.Repeat("x", MaxVarValueLen+1)},
		"too many vars":  manyVars(MaxVars + 1),
	}
	for name, args := range invalid {
		t.Run("rejects "+name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseVars(args)
			assert.ErrorIs(t, err, ErrInvalid)
		})
	}
}

func manyVars(n int) []string {
	args := make([]string, 0, n)
	for i := range n {
		args = append(args, fmt.Sprintf("K%d=v", i))
	}
	return args
}

func TestParseScriptTimeout(t *testing.T) {
	t.Parallel()
	valid := map[string]time.Duration{
		"1s":    time.Second,
		"45s":   45 * time.Second,
		"1m":    time.Minute,
		"1m30s": 90 * time.Second,
		"10m":   10 * time.Minute,
	}
	for s, want := range valid {
		t.Run(s, func(t *testing.T) {
			t.Parallel()
			got, err := ParseScriptTimeout(s)
			require.NoError(t, err)
			assert.Equal(t, want, got)
			assert.Equal(t, s, FormatScriptTimeout(got))
		})
	}
	for _, s := range []string{"0s", "500ms", "1.5s", "11m", "x", ""} {
		t.Run("rejects "+s, func(t *testing.T) {
			t.Parallel()
			_, err := ParseScriptTimeout(s)
			assert.ErrorIs(t, err, ErrInvalid)
		})
	}
}

func TestLoopTimeout(t *testing.T) {
	t.Parallel()
	t.Run("default when empty", func(t *testing.T) {
		t.Parallel()
		l := validLoop()
		got, err := l.Timeout()
		require.NoError(t, err)
		assert.Equal(t, DefaultScriptTimeout, got)
	})
	t.Run("parsed when set", func(t *testing.T) {
		t.Parallel()
		l := validLoop()
		l.ScriptTimeout = "2m"
		got, err := l.Timeout()
		require.NoError(t, err)
		assert.Equal(t, 2*time.Minute, got)
	})
}

func TestLoopTemplated(t *testing.T) {
	t.Parallel()
	tests := map[int]bool{0: true, 1: false, FormatVersion: true}
	for version, want := range tests {
		t.Run(fmt.Sprintf("version %d", version), func(t *testing.T) {
			t.Parallel()
			l := Loop{Version: version}
			assert.Equal(t, want, l.Templated())
		})
	}
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
		{name: "timeout without script", mutate: func(l *Loop) { l.ScriptTimeout = "1m" }, want: ErrInvalid},
		{name: "bad var key", mutate: func(l *Loop) { l.Vars = map[string]string{"a-b": "x"} }, want: ErrInvalid},
		{name: "var value too long", mutate: func(l *Loop) {
			l.Vars = map[string]string{"A": strings.Repeat("x", MaxVarValueLen+1)}
		}, want: ErrInvalid},
		{name: "stdout without script", mutate: func(l *Loop) { l.Prompt = "{{.Stdout}}" }, want: ErrInvalid},
		{name: "stdout with script", mutate: func(l *Loop) {
			l.Script = "true"
			l.Prompt = "{{.Stdout}}"
		}},
		{name: "unknown var", mutate: func(l *Loop) { l.Prompt = "{{.Vars.NOPE}}" }, want: ErrInvalid},
		{name: "unparsable prompt", mutate: func(l *Loop) { l.Prompt = "{{" }, want: ErrInvalid},
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
