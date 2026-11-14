// Package config resolves CONFIG_DIR and reads config.toml (ADR 0011).
package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/bobcob7/workspaces/internal/repo"
)

// ErrInvalid marks a config.toml that can't be parsed or holds bad values.
var ErrInvalid = errors.New("invalid config")

// defaultHost is the host for specs without one (ADR 0007).
const defaultHost = "github.com"

// defaultFile is written when config.toml is missing. It must parse to
// Default().
const defaultFile = `# ws configuration. Edit it by hand, or with ws config set; ws keeps your comments.

[defaults]
# Owners to try, in order, for a bare repo name that isn't cached,
# for example ["your-name", "your-org"].
owners = []

# Hosts for repo specs without one, such as "owner/name", tried in order;
# the first is the primary host. With none, github.com is used.
# kind is auto, github, or forgejo (read by the desktop app's Reviews).
# Edit with ws config set hosts github.com git.example.com=forgejo
# [[hosts]]
# name = "github.com"
# kind = "auto"

# [review]
# How long ws review wait waits for a review by default.
# timeout = "60m"
#
# Remove a review's agent and workspace once every reviewer has reported;
# its findings are kept.
# remove_finished = true
#
# Override a review profile's when (always, auto, or off), and pin its model.
# [review.profiles.security]
# when = "always"
# model = "opus"
`

// defaultReviewTimeout is how long ws review wait waits when [review]
// timeout isn't set (ADR 0039).
const defaultReviewTimeout = 60 * time.Minute

// Host kinds (ADR 0061). Only the desktop acts on them.
const (
	KindAuto    = "auto"
	KindGithub  = "github"
	KindForgejo = "forgejo"
)

// Host is one [[hosts]] table (ADR 0061).
type Host struct {
	Name string `toml:"name" json:"name"`
	Kind string `toml:"kind" json:"kind"`
}

// Config is the contents of config.toml.
type Config struct {
	Defaults Defaults `toml:"defaults"`
	Hosts    []Host   `toml:"hosts"`
	Review   Review   `toml:"review"`
}

// Defaults drive short-name resolution (ADR 0007).
type Defaults struct {
	Host   string   `toml:"host"`
	Owners []string `toml:"owners"`
}

// SpecDefaults is what short specs resolve against (ADRs 0007, 0061).
type SpecDefaults struct {
	Hosts  []string // effective host names, in order; never empty
	Owners []string // defaults.owners
}

// EffectiveHosts is [[hosts]], or defaults.host, or github.com; never empty.
func (c Config) EffectiveHosts() []Host { //nolint:gocritic // hugeParam: the contract takes Config by value, like the rest of this file.
	if len(c.Hosts) > 0 {
		return slices.Clone(c.Hosts)
	}
	host := c.Defaults.Host
	if host == "" {
		host = defaultHost
	}
	return []Host{{Name: host, Kind: KindAuto}}
}

// SpecDefaults returns the effective hosts' names and defaults.owners.
func (c Config) SpecDefaults() SpecDefaults { //nolint:gocritic // hugeParam: the contract takes Config by value, like the rest of this file.
	hosts := c.EffectiveHosts()
	names := make([]string, 0, len(hosts))
	for _, host := range hosts {
		names = append(names, host.Name)
	}
	return SpecDefaults{Hosts: names, Owners: c.Defaults.Owners}
}

// Review configures ws review (ADRs 0038, 0039).
type Review struct {
	// Timeout is how long ws review wait waits by default; zero for
	// defaultReviewTimeout.
	Timeout time.Duration `toml:"timeout"`
	// Profiles override profiles' when and set their models, by name.
	Profiles map[string]ReviewProfile `toml:"profiles"`
	// RemoveFinished is whether the removal pass removes reviews whose
	// every profile has reported; nil for true (ADR 0045).
	RemoveFinished *bool `toml:"remove_finished"`
}

// ReviewProfile is [review.profiles.<name>]. Empty fields leave the
// profile's own when, and the session's model.
type ReviewProfile struct {
	When  string `toml:"when"`
	Model string `toml:"model"`
}

// RemovesFinished is [review] remove_finished, true when it isn't set.
func (r *Review) RemovesFinished() bool {
	return r.RemoveFinished == nil || *r.RemoveFinished
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
		return Config{}, fmt.Errorf("%w: unknown keys %s%s", ErrInvalid, strings.Join(keys, ", "), tokenHint(keys))
	}
	return cfg.normalize(meta.IsDefined("defaults", "host"))
}

// tokenHint says where a token goes when keys includes hosts.token.
func tokenHint(keys []string) string {
	if !slices.Contains(keys, "hosts.token") {
		return ""
	}
	return "; tokens don't go in config.toml: use WS_FORGEJO_TOKEN_<HOST> or Settings › Forges"
}

// normalize lowercases names, since canonical ids are lowercase (ADR 0007),
// and rejects values that can't appear in a repo id. hostSet is whether the
// file sets defaults.host, which can't be used together with [[hosts]].
func (c Config) normalize(hostSet bool) (Config, error) { //nolint:gocritic // hugeParam: the contract takes Config by value, like the rest of this file.
	hosts, err := normalizeHosts(c.Hosts)
	if err != nil {
		return Config{}, err
	}
	host := strings.ToLower(c.Defaults.Host)
	if err := validateHost(host); err != nil {
		return Config{}, err
	}
	if hostSet && len(hosts) > 0 {
		return Config{}, fmt.Errorf("%w: set defaults.host or [[hosts]], not both; move %q into [[hosts]] with ws config set hosts", ErrInvalid, host)
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
	return Config{Defaults: Defaults{Host: host, Owners: owners}, Hosts: hosts, Review: c.Review}, nil
}

// normalizeHosts lowercases names, fills an empty kind with "auto", and
// checks the rules in docs/config.md. Shared by parse and by ws config set
// hosts. It returns nil for no hosts.
func normalizeHosts(hosts []Host) ([]Host, error) {
	if len(hosts) == 0 {
		return nil, nil
	}
	normalized := make([]Host, 0, len(hosts))
	for i, host := range hosts {
		entry, err := normalizeHostEntry(i+1, host)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(normalized, func(h Host) bool { return h.Name == entry.Name }) {
			return nil, fmt.Errorf("%w: hosts lists %q twice", ErrInvalid, entry.Name)
		}
		normalized = append(normalized, entry)
	}
	return normalized, nil
}

// normalizeHostEntry checks the n-th [[hosts]] table, counting from 1.
func normalizeHostEntry(n int, host Host) (Host, error) {
	name := strings.ToLower(host.Name)
	if name == "" {
		return Host{}, fmt.Errorf("%w: hosts entry %d has no name", ErrInvalid, n)
	}
	if err := repo.ValidateHost(name); err != nil {
		return Host{}, fmt.Errorf("%w: hosts entry %d: name %q: %w", ErrInvalid, n, name, err)
	}
	kind := host.Kind
	if kind == "" {
		kind = KindAuto
	}
	switch kind {
	case KindAuto, KindGithub, KindForgejo:
		return Host{Name: name, Kind: kind}, nil
	}
	return Host{}, fmt.Errorf("%w: hosts entry %d (%s): kind must be auto, github, or forgejo, not %q", ErrInvalid, n, name, host.Kind)
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
