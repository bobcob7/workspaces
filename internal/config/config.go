// Package config resolves CONFIG_DIR and reads config.toml (ADR 0011).
package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"

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
`

// Config is the contents of config.toml.
type Config struct {
	Defaults Defaults `toml:"defaults"`
}

// Defaults drive short-name resolution (ADR 0007).
type Defaults struct {
	Host   string   `toml:"host"`
	Owners []string `toml:"owners"`
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
	return Config{Defaults: Defaults{Host: host, Owners: owners}}, nil
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
