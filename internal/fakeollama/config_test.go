package fakeollama

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/fakeollama/internal/yamlconfig"
)

func TestParseConfig(t *testing.T) {
	t.Run("defaults listen addresses to Ollama's ports", func(t *testing.T) {
		cfg, err := ParseConfig(nil)
		require.NoError(t, err)
		assert.Equal(t, Config{Listen: ":11434", ControlListen: ":11435"}, cfg)
	})
	t.Run("parses every field", func(t *testing.T) {
		cfg, err := ParseConfig([]byte(`
listen: "127.0.0.1:1"
control_listen: "127.0.0.1:2"
models:
  - name: m
    capabilities: [tools, reasoning]
timing: {first_token: 50ms, chunk_interval: 10ms, words_per_chunk: 2}
rules:
  - {match: "a", reply: "b"}
faults:
  - {model: m, count: 2, status: 503, message: x, hang: true, slow_first_token: 1s, drop_after_chunks: 3, stall_after_chunks: 4}
  - {down: true}
`))
		require.NoError(t, err)
		want := Config{
			Listen:        "127.0.0.1:1",
			ControlListen: "127.0.0.1:2",
			Models:        []Model{{Name: "m", Capabilities: []Capability{CapabilityTools, CapabilityReasoning}}},
			Timing:        Timing{FirstToken: yamlconfig.Duration(50 * time.Millisecond), ChunkInterval: yamlconfig.Duration(10 * time.Millisecond), WordsPerChunk: 2},
			Rules:         []Rule{{Match: "a", Reply: "b"}},
			Faults: []Fault{
				{Model: "m", Count: 2, Status: 503, Message: "x", Hang: true, SlowFirstToken: yamlconfig.Duration(time.Second), DropAfterChunks: 3, StallAfterChunks: 4},
				{Down: true},
			},
		}
		assert.Equal(t, want, cfg)
	})
	t.Run("rejects unknown fields", func(t *testing.T) {
		_, err := ParseConfig([]byte("modles: []"))
		assert.ErrorContains(t, err, "parsing config")
	})
	t.Run("rejects invalid durations", func(t *testing.T) {
		_, err := ParseConfig([]byte("timing: {first_token: soon}"))
		assert.Error(t, err)
	})
}

func TestLoadConfig(t *testing.T) {
	t.Parallel()
	t.Run("reads a file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(path, []byte("listen: ':1'"), 0o600))
		cfg, err := LoadConfig(path)
		require.NoError(t, err)
		assert.Equal(t, ":1", cfg.Listen)
	})
	t.Run("fails for a missing file", func(t *testing.T) {
		t.Parallel()
		_, err := LoadConfig(filepath.Join(t.TempDir(), "missing.yaml"))
		assert.ErrorContains(t, err, "reading config")
	})
}

func TestValidateModels(t *testing.T) {
	tests := []struct {
		name   string
		models []Model
		want   string
	}{
		{"none", nil, "invalid config: at least one model is required"},
		{"unnamed", []Model{{}}, "invalid config: model name is required"},
		{"duplicate", []Model{{Name: "a"}, {Name: "a"}}, `invalid config: duplicate model "a"`},
		{"unknown capability", []Model{{Name: "a", Capabilities: []Capability{"vision"}}}, `invalid config: model "a": unknown capability "vision"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.EqualError(t, validateModels(tt.models), tt.want)
		})
	}
}

func TestValidateTiming(t *testing.T) {
	t.Run("accepts zero values", func(t *testing.T) {
		assert.NoError(t, validateTiming(Timing{}))
	})
	t.Run("rejects negative values", func(t *testing.T) {
		assert.ErrorIs(t, validateTiming(Timing{WordsPerChunk: -1}), errInvalidConfig)
	})
}
