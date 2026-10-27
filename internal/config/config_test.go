package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefault(t *testing.T) {
	t.Run("host is github.com", func(t *testing.T) {
		assert.Equal(t, "github.com", Default().Defaults.Host)
	})
	t.Run("owners is empty, not nil", func(t *testing.T) {
		assert.NotNil(t, Default().Defaults.Owners)
		assert.Empty(t, Default().Defaults.Owners)
	})
	t.Run("matches the generated file", func(t *testing.T) {
		cfg, err := parse([]byte(defaultFile))
		require.NoError(t, err)
		assert.Equal(t, Default(), cfg)
	})
}

func TestParse(t *testing.T) {
	t.Run("empty file gives the defaults", func(t *testing.T) {
		cfg, err := parse(nil)
		require.NoError(t, err)
		assert.Equal(t, Default(), cfg)
	})
	t.Run("reads defaults", func(t *testing.T) {
		cfg, err := parse([]byte("[defaults]\nhost = \"gitlab.com\"\nowners = [\"team\", \"me\"]\n"))
		require.NoError(t, err)
		assert.Equal(t, "gitlab.com", cfg.Defaults.Host)
		assert.Equal(t, []string{"team", "me"}, cfg.Defaults.Owners)
	})
	t.Run("keeps defaults for missing keys", func(t *testing.T) {
		cfg, err := parse([]byte("[defaults]\nowners = [\"me\"]\n"))
		require.NoError(t, err)
		assert.Equal(t, "github.com", cfg.Defaults.Host)
	})
	t.Run("normalizes values", func(t *testing.T) {
		cfg, err := parse([]byte("[defaults]\nhost = \"GitHub.com\"\n"))
		require.NoError(t, err)
		assert.Equal(t, "github.com", cfg.Defaults.Host)
	})
	t.Run("rejects bad TOML", func(t *testing.T) {
		_, err := parse([]byte("[defaults\n"))
		assert.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("rejects the wrong type", func(t *testing.T) {
		_, err := parse([]byte("[defaults]\nowners = \"me\"\n"))
		assert.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("rejects unknown keys and names them", func(t *testing.T) {
		_, err := parse([]byte("[defaults]\nprotocol = \"ssh\"\n[extra]\nx = 1\n"))
		require.ErrorIs(t, err, ErrInvalid)
		require.ErrorContains(t, err, "defaults.protocol")
		assert.ErrorContains(t, err, "extra.x")
	})
	t.Run("rejects invalid values", func(t *testing.T) {
		_, err := parse([]byte("[defaults]\nhost = \"\"\n"))
		assert.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("reads review", func(t *testing.T) {
		cfg, err := parse([]byte("[review]\ntimeout = \"90m\"\n[review.profiles.go]\nwhen = \"always\"\nmodel = \"opus\"\n"))
		require.NoError(t, err)
		assert.Equal(t, 90*time.Minute, cfg.Review.Timeout)
		assert.Equal(t, map[string]ReviewProfile{"go": {When: "always", Model: "opus"}}, cfg.Review.Profiles)
	})
	t.Run("rejects a bad review when", func(t *testing.T) {
		_, err := parse([]byte("[review.profiles.go]\nwhen = \"sometimes\"\n"))
		require.ErrorIs(t, err, ErrInvalid)
		assert.ErrorContains(t, err, "review.profiles.go.when")
	})
	t.Run("rejects a negative review timeout", func(t *testing.T) {
		_, err := parse([]byte("[review]\ntimeout = \"-1m\"\n"))
		assert.ErrorIs(t, err, ErrInvalid)
	})
}

func TestReview_WaitTimeout(t *testing.T) {
	t.Run("defaults to 60 minutes", func(t *testing.T) {
		assert.Equal(t, 60*time.Minute, (&Review{}).WaitTimeout())
	})
	t.Run("is the configured timeout", func(t *testing.T) {
		assert.Equal(t, 2*time.Hour, (&Review{Timeout: 2 * time.Hour}).WaitTimeout())
	})
}

func TestConfig_normalize(t *testing.T) {
	newConfig := func(host string, owners ...string) Config {
		return Config{Defaults: Defaults{Host: host, Owners: owners}}
	}
	t.Run("lowercases host and owners", func(t *testing.T) {
		cfg, err := newConfig("GitLab.COM", "MyOrg").normalize()
		require.NoError(t, err)
		assert.Equal(t, newConfig("gitlab.com", "myorg"), cfg)
	})
	t.Run("drops duplicate owners, keeping order", func(t *testing.T) {
		cfg, err := newConfig("github.com", "b", "A", "a", "b").normalize()
		require.NoError(t, err)
		assert.Equal(t, []string{"b", "a"}, cfg.Defaults.Owners)
	})
	t.Run("nil owners become empty", func(t *testing.T) {
		cfg, err := newConfig("github.com").normalize()
		require.NoError(t, err)
		assert.NotNil(t, cfg.Defaults.Owners)
	})
	t.Run("rejects a bad host", func(t *testing.T) {
		_, err := newConfig("").normalize()
		assert.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("rejects a bad owner", func(t *testing.T) {
		_, err := newConfig("github.com", "ok", "").normalize()
		assert.ErrorIs(t, err, ErrInvalid)
	})
}

func TestValidateHost(t *testing.T) {
	t.Run("accepts a host name", func(t *testing.T) {
		assert.NoError(t, validateHost("gitlab.example.com"))
	})
	t.Run("rejects empty", func(t *testing.T) {
		assert.ErrorIs(t, validateHost(""), ErrInvalid)
	})
	t.Run("rejects a URL", func(t *testing.T) {
		assert.ErrorIs(t, validateHost("https://github.com"), ErrInvalid)
	})
	t.Run("rejects a path", func(t *testing.T) {
		assert.ErrorIs(t, validateHost("github.com/bobcob7"), ErrInvalid)
	})
	t.Run("rejects a user", func(t *testing.T) {
		assert.ErrorIs(t, validateHost("git@github.com"), ErrInvalid)
	})
	t.Run("rejects spaces", func(t *testing.T) {
		assert.ErrorIs(t, validateHost("git hub.com"), ErrInvalid)
	})
}

func TestValidateOwner(t *testing.T) {
	t.Run("accepts a name", func(t *testing.T) {
		assert.NoError(t, validateOwner("bobcob7"))
	})
	t.Run("accepts nested groups", func(t *testing.T) {
		assert.NoError(t, validateOwner("gitlab-org/api"))
	})
	t.Run("rejects empty", func(t *testing.T) {
		assert.ErrorIs(t, validateOwner(""), ErrInvalid)
	})
	t.Run("rejects @", func(t *testing.T) {
		assert.ErrorIs(t, validateOwner("me@main"), ErrInvalid)
	})
	t.Run("rejects colons", func(t *testing.T) {
		assert.ErrorIs(t, validateOwner("git:me"), ErrInvalid)
	})
	t.Run("rejects spaces", func(t *testing.T) {
		assert.ErrorIs(t, validateOwner("my org"), ErrInvalid)
	})
	t.Run("rejects a leading slash", func(t *testing.T) {
		assert.ErrorIs(t, validateOwner("/me"), ErrInvalid)
	})
	t.Run("rejects a trailing slash", func(t *testing.T) {
		assert.ErrorIs(t, validateOwner("me/"), ErrInvalid)
	})
	t.Run("rejects an empty segment", func(t *testing.T) {
		assert.ErrorIs(t, validateOwner("a//b"), ErrInvalid)
	})
}
