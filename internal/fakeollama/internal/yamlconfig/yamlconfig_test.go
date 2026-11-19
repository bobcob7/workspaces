package yamlconfig

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testConfig struct {
	Name    string   `yaml:"name"`
	Timeout Duration `yaml:"timeout"`
}

func TestDuration_MarshalText(t *testing.T) {
	t.Run("writes Go duration syntax", func(t *testing.T) {
		b, err := Duration(1500 * time.Millisecond).MarshalText()
		require.NoError(t, err)
		assert.Equal(t, "1.5s", string(b))
	})
	t.Run("round-trips through JSON", func(t *testing.T) {
		b, err := json.Marshal(struct{ D Duration }{Duration(time.Minute)})
		require.NoError(t, err)
		assert.JSONEq(t, `{"D":"1m0s"}`, string(b))
	})
}

func TestDuration_UnmarshalText(t *testing.T) {
	t.Run("parses Go durations", func(t *testing.T) {
		var d Duration
		require.NoError(t, d.UnmarshalText([]byte("50ms")))
		assert.Equal(t, Duration(50*time.Millisecond), d)
	})
	t.Run("rejects invalid durations", func(t *testing.T) {
		var d Duration
		assert.ErrorContains(t, d.UnmarshalText([]byte("soon")), "parsing duration")
	})
}

func TestLoad(t *testing.T) {
	t.Parallel()
	t.Run("reads a file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(path, []byte("name: a\ntimeout: 2s\n"), 0o600))
		var cfg testConfig
		require.NoError(t, Load(path, &cfg))
		assert.Equal(t, testConfig{Name: "a", Timeout: Duration(2 * time.Second)}, cfg)
	})
	t.Run("fails for a missing file", func(t *testing.T) {
		t.Parallel()
		var cfg testConfig
		assert.ErrorContains(t, Load(filepath.Join(t.TempDir(), "missing.yaml"), &cfg), "reading config")
	})
}

func TestParse(t *testing.T) {
	t.Run("keeps defaults for absent fields", func(t *testing.T) {
		cfg := testConfig{Name: "default"}
		require.NoError(t, Parse([]byte("timeout: 1s"), &cfg))
		assert.Equal(t, testConfig{Name: "default", Timeout: Duration(time.Second)}, cfg)
	})
	t.Run("accepts empty input", func(t *testing.T) {
		cfg := testConfig{Name: "default"}
		require.NoError(t, Parse(nil, &cfg))
		assert.Equal(t, "default", cfg.Name)
	})
	t.Run("rejects unknown fields", func(t *testing.T) {
		var cfg testConfig
		assert.ErrorContains(t, Parse([]byte("color: blue"), &cfg), "field color not found")
	})
	t.Run("rejects invalid values", func(t *testing.T) {
		var cfg testConfig
		assert.ErrorContains(t, Parse([]byte("timeout: soon"), &cfg), "parsing config")
	})
}
