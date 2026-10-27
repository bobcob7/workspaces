// Package config resolves CONFIG_DIR and reads config.toml (ADR 0011).
package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// ErrInvalid marks a config.toml that can't be parsed or holds bad values.
var ErrInvalid = errors.New("invalid config")

// defaultHost is the host for specs without one (ADR 0007).
const defaultHost = "github.com"

// defaultFile is written when config.toml is missing. It must parse to
// Default().
const defaultFile = `# ws configuration. ws writes this file once and never overwrites it.

[defaults]
# Host for repo specs without one, such as "owner/name".
host = "github.com"

# Owners to try, in order, for a bare repo name that isn't cached,
# for example ["your-name", "your-org"].
owners = []

# [review]
# How long ws review wait waits for a review by default.
# timeout = "60m"
#
# Override a review profile's when (always, auto, or off), and pin its model.
# [review.profiles.security]
# when = "always"
# model = "opus"
`

// defaultReviewTimeout is how long ws review wait waits when [review]
// timeout isn't set (ADR 0039).
const defaultReviewTimeout = 60 * time.Minute

// Config is the contents of config.toml.
type Config struct {
	Defaults Defaults `toml:"defaults"`
	Review   Review   `toml:"review"`
}

// Defaults drive short-name resolution (ADR 0007).
type Defaults struct {
	Host   string   `toml:"host"`
	Owners []string `toml:"owners"`
}

// Review configures ws review (ADRs 0038, 0039).
type Review struct {
	// Timeout is how long ws review wait waits by default; zero for
	// defaultReviewTimeout.
	Timeout time.Duration `toml:"timeout"`
	// Profiles override profiles' when and set their models, by name.
	Profiles map[string]ReviewProfile `toml:"profiles"`
}

// ReviewProfile is [review.profiles.<name>]. Empty fields leave the
// profile's own when, and the session's model.
type ReviewProfile struct {
	When  string `toml:"when"`
	Model string `toml:"model"`
}

// WaitTimeout is [review] timeout, or 60 minutes when it isn't set.
func (r *Review) WaitTimeout() time.Duration {
	if r.Timeout == 0 {
		return defaultReviewTimeout
	}
	return r.Timeout
}

// Default is the configuration used for keys config.toml leaves out.
func Default() Config {
	return Config{Defaults: Defaults{Host: defaultHost, Owners: []string{}}}
}

// parse reads config.toml's contents over the defaults. Unknown keys are an
// error, so a typo doesn't silently fall back to a default.
func parse(data []byte) (Config, error) {
	cfg := Default()
	meta, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}
		return Config{}, fmt.Errorf("%w: unknown keys %s", ErrInvalid, strings.Join(keys, ", "))
	}
	return cfg.normalize()
}

// normalize lowercases names, since canonical ids are lowercase (ADR 0007),
// and rejects values that can't appear in a repo id.
func (c Config) normalize() (Config, error) {
	host := strings.ToLower(c.Defaults.Host)
	if err := validateHost(host); err != nil {
		return Config{}, err
	}
	owners := make([]string, 0, len(c.Defaults.Owners))
	for _, owner := range c.Defaults.Owners {
		owner = strings.ToLower(owner)
		if err := validateOwner(owner); err != nil {
			return Config{}, err
		}
		if !slices.Contains(owners, owner) {
			owners = append(owners, owner)
		}
	}
	if err := c.Review.check(); err != nil {
		return Config{}, err
	}
	return Config{Defaults: Defaults{Host: host, Owners: owners}, Review: c.Review}, nil
}

// check rejects a negative timeout and a when that isn't always, auto, or
// off.
func (r *Review) check() error {
	if r.Timeout < 0 {
		return fmt.Errorf("%w: review.timeout %s is negative", ErrInvalid, r.Timeout)
	}
	for name, p := range r.Profiles {
		switch p.When {
		case "", "always", "auto", "off":
		default:
			return fmt.Errorf("%w: review.profiles.%s.when must be always, auto, or off, not %q", ErrInvalid, name, p.When)
		}
	}
	return nil
}

func validateHost(host string) error {
	if host == "" {
		return fmt.Errorf("%w: defaults.host is empty", ErrInvalid)
	}
	if strings.ContainsAny(host, " \t/@:") {
		return fmt.Errorf("%w: defaults.host %q must be a bare host name, such as %q", ErrInvalid, host, defaultHost)
	}
	return nil
}

// validateOwner allows "/" inside an owner for nested groups, as on GitLab.
func validateOwner(owner string) error {
	switch {
	case owner == "":
		return fmt.Errorf("%w: defaults.owners has an empty entry", ErrInvalid)
	case strings.ContainsAny(owner, " \t@:"):
		return fmt.Errorf("%w: defaults.owners entry %q must not contain spaces, @, or colons", ErrInvalid, owner)
	case strings.HasPrefix(owner, "/"), strings.HasSuffix(owner, "/"), strings.Contains(owner, "//"):
		return fmt.Errorf("%w: defaults.owners entry %q has an empty path segment", ErrInvalid, owner)
	}
	return nil
}
