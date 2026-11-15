package fakeollama

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/fakeollama/internal/yamlconfig"
)

func TestFault_validate(t *testing.T) {
	models := testConfig().Models
	tests := []struct {
		name  string
		fault Fault
		want  string
	}{
		{"negative count", Fault{Hang: true, Count: -1}, "invalid fault: values must not be negative"},
		{"negative chunks", Fault{DropAfterChunks: -1}, "invalid fault: values must not be negative"},
		{"success status", Fault{Status: 200}, "invalid fault: status 200 is not an error status"},
		{"status too high", Fault{Status: 600}, "invalid fault: status 600 is not an error status"},
		{"scoped down", Fault{Down: true, Model: "fake-chat"}, "invalid fault: down can't be scoped to a model or count"},
		{"counted down", Fault{Down: true, Count: 1}, "invalid fault: down can't be scoped to a model or count"},
		{"no effect", Fault{Model: "fake-chat"}, "invalid fault: no effect set"},
		{"unknown model", Fault{Hang: true, Model: "missing"}, `invalid fault: unknown model "missing"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.EqualError(t, tt.fault.validate(models), tt.want)
		})
	}
	t.Run("accepts valid faults", func(t *testing.T) {
		for _, f := range []Fault{{Down: true}, {Status: 503, Model: "fake-chat", Count: 2}, {SlowFirstToken: 1}, {StallAfterChunks: 1}} {
			assert.NoError(t, f.validate(models))
		}
	})
}

func TestValidateFaults(t *testing.T) {
	err := validateFaults([]Fault{{Hang: true}, {}}, testConfig().Models)
	require.ErrorIs(t, err, errInvalidFault)
	assert.ErrorContains(t, err, "fault 2")
}

func TestAnyDown(t *testing.T) {
	assert.False(t, anyDown([]Fault{{Hang: true}}))
	assert.True(t, anyDown([]Fault{{Hang: true}, {Down: true}}))
}

func TestFault_affects(t *testing.T) {
	tests := []struct {
		name   string
		fault  Fault
		target target
		want   bool
	}{
		{"down never", Fault{Down: true}, target{}, false},
		{"other model", Fault{Hang: true, Model: "a"}, target{model: "b"}, false},
		{"scoped fault and model listing", Fault{Status: 500, Model: "a"}, target{}, false},
		{"same model", Fault{Hang: true, Model: "a"}, target{model: "a"}, true},
		{"unscoped status", Fault{Status: 500}, target{model: "b"}, true},
		{"slow first token", Fault{SlowFirstToken: 1}, target{}, true},
		{"drop without stream", Fault{DropAfterChunks: 1}, target{model: "a"}, false},
		{"drop with stream", Fault{DropAfterChunks: 1}, target{model: "a", stream: true}, true},
		{"stall with stream", Fault{StallAfterChunks: 1}, target{stream: true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.fault.affects(tt.target))
		})
	}
}

func TestDecodeFaults(t *testing.T) {
	t.Run("decodes a single fault", func(t *testing.T) {
		faults, err := decodeFaults([]byte(` {"slow_first_token":"2s","model":"m"} `))
		require.NoError(t, err)
		assert.Equal(t, []Fault{{SlowFirstToken: yamlconfig.Duration(2e9), Model: "m"}}, faults)
	})
	t.Run("decodes a list", func(t *testing.T) {
		faults, err := decodeFaults([]byte(`[{"down":true},{"status":500,"count":1}]`))
		require.NoError(t, err)
		assert.Equal(t, []Fault{{Down: true}, {Status: 500, Count: 1}}, faults)
	})
	t.Run("rejects empty bodies", func(t *testing.T) {
		_, err := decodeFaults([]byte("  "))
		assert.ErrorIs(t, err, errInvalidFault)
	})
	t.Run("rejects unknown fields", func(t *testing.T) {
		_, err := decodeFaults([]byte(`{"dwon":true}`))
		assert.ErrorIs(t, err, errInvalidFault)
	})
	t.Run("rejects invalid lists", func(t *testing.T) {
		_, err := decodeFaults([]byte(`[{"status":"x"}]`))
		assert.ErrorIs(t, err, errInvalidFault)
	})
	t.Run("rejects invalid durations", func(t *testing.T) {
		_, err := decodeFaults([]byte(`{"slow_first_token":"soon"}`))
		assert.ErrorIs(t, err, errInvalidFault)
	})
}
