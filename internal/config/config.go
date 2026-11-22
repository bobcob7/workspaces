// Package config resolves CONFIG_DIR and reads config.toml (ADR 0011).
package config

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
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
#
# Run some agents on other models (ws llm machine spawn).
# [runner]
# model = "qwen9b"
#
# [endpoints.gpu]
# url = "http://localhost:1234"
# api = "openai"             # OpenAI-compatible chat completions (LM Studio, Ollama, llm-router, vLLM)
#
# [models.qwen9b]
# endpoint = "gpu"
# id = "qwen/qwen3.5-9b"     # as the endpoint lists it; on Ollama: qwen3.5:9b
# context_tokens = 65536     # must match the server's context length for this model
# reasoning = "none"         # thinking off; "" leaves the server's default
#
# [runner.roles.verifier]
# runner = "ws"
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
	Defaults  Defaults            `toml:"defaults"`
	Hosts     []Host              `toml:"hosts"`
	Review    Review              `toml:"review"`
	Runner    Runner              `toml:"runner"`
	Endpoints map[string]Endpoint `toml:"endpoints"`
	Models    map[string]Model    `toml:"models"`
	Daemon    Daemon              `toml:"daemon"`
	// Environments is [environments] (ADRs 0074, 0075).
	Environments Environments `toml:"environments"`
	// RunnerErr is why the runner's tables can't be used, or nil (ADR 0071).
	RunnerErr error `toml:"-"`
	// roleRunners is each runner.roles.<role>.runner as written; roleRunnersBad
	// is set when the roles can't be read at all. See RunnerFor.
	roleRunners    map[string]any
	roleRunnersBad bool
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

// Environments is [environments] (ADRs 0074, 0075).
type Environments struct {
	Listen          string `toml:"listen" json:"listen"`
	EnrollListen    string `toml:"enroll_listen" json:"enroll_listen"`
	PublicName      string `toml:"public_name" json:"public_name"`
	RenewBeforeDays *int   `toml:"renew_before_days" json:"renew_before_days"`
}

// Defaults for [environments]: the agents' port, first contact's port, and
// how long before expiry a certificate is renewed.
const (
	defaultEnvListen       = "0.0.0.0:8883"
	defaultEnvEnrollListen = "0.0.0.0:8884"
	defaultRenewBeforeDays = 30
)

// ErrNoPublicName marks [environments] that needs public_name to listen.
var ErrNoPublicName = errors.New("environments.public_name isn't set; set it with ws config set environments.public_name <dns name or IP>")

// ListenAddr is the address agents connect to over MQTT (ADR 0074).
func (e *Environments) ListenAddr() string {
	if e.Listen == "" {
		return defaultEnvListen
	}
	return e.Listen
}

// EnrollAddr is the address agents first contact to enroll (ADR 0075).
func (e *Environments) EnrollAddr() string {
	if e.EnrollListen == "" {
		return defaultEnvEnrollListen
	}
	return e.EnrollListen
}

// RenewDays is how many days before expiry an agent renews its certificate.
func (e *Environments) RenewDays() int {
	if e.RenewBeforeDays == nil {
		return defaultRenewBeforeDays
	}
	return *e.RenewBeforeDays
}

// EnrollHost is the public name with the enrollment listener's port.
func (e *Environments) EnrollHost() (string, error) {
	return e.hostOn(e.EnrollAddr())
}

// BrokerHost is the public name with the broker's listener port.
func (e *Environments) BrokerHost() (string, error) {
	return e.hostOn(e.ListenAddr())
}

// hostOn joins public_name with addr's port.
func (e *Environments) hostOn(addr string) (string, error) {
	if e.PublicName == "" {
		return "", ErrNoPublicName
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("splitting %s: %w", addr, err)
	}
	return net.JoinHostPort(e.PublicName, port), nil
}

// check rejects listen addresses without a port from 1 to 65535, a
// public_name that isn't an IP or a DNS name, and renew_before_days outside
// 1 to 60. Each failure wraps ErrInvalid and names its key.
func (e *Environments) check() error {
	for _, listen := range []struct{ key, addr string }{
		{"environments.listen", e.Listen},
		{"environments.enroll_listen", e.EnrollListen},
	} {
		if listen.addr == "" {
			continue
		}
		if err := checkListenAddr(listen.key, listen.addr); err != nil {
			return err
		}
	}
	if e.PublicName != "" && !validPublicName(e.PublicName) {
		return fmt.Errorf("%w: environments.public_name %q must be an IP address or a DNS name, without a port", ErrInvalid, e.PublicName)
	}
	if e.RenewBeforeDays != nil && (*e.RenewBeforeDays < 1 || *e.RenewBeforeDays > 60) {
		return fmt.Errorf("%w: environments.renew_before_days %d must be 1 to 60", ErrInvalid, *e.RenewBeforeDays)
	}
	return nil
}

// checkListenAddr is nil when addr is host:port with a port from 1 to 65535.
func checkListenAddr(key, addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%w: %s %q must be host:port: %w", ErrInvalid, key, addr, err)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("%w: %s %q needs a port from 1 to 65535", ErrInvalid, key, addr)
	}
	return nil
}

// validPublicName is whether name is an IP address or a DNS name whose
// labels are letters, digits and hyphens, 1 to 63 characters, neither
// starting nor ending with a hyphen.
func validPublicName(name string) bool {
	if net.ParseIP(name) != nil {
		return true
	}
	for label := range strings.SplitSeq(name, ".") {
		if !validLabel(label) {
			return false
		}
	}
	return true
}

// validLabel is whether label is a DNS label: 1 to 63 letters, digits or
// hyphens, not starting or ending with a hyphen.
func validLabel(label string) bool {
	if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	return !strings.ContainsFunc(label, func(r rune) bool {
		return r != '-' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
	})
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
// error, so a typo doesn't silently fall back to a default. The runner's
// tables are read apart from the rest, and a problem in them goes to
// RunnerErr.
func parse(data []byte) (Config, error) {
	cfg, err := parseCore(data)
	if err != nil {
		return Config{}, err
	}
	cfg.loadRunner(data)
	return cfg, nil
}

// coreFile is config.toml's tables that predate the runner's.
type coreFile struct {
	Defaults     Defaults     `toml:"defaults"`
	Hosts        []Host       `toml:"hosts"`
	Review       Review       `toml:"review"`
	Environments Environments `toml:"environments"`
}

// withoutRunnerTables is data with the runner's sections cut out, so a
// syntax error in them doesn't stop the rest of the file from loading.
func withoutRunnerTables(data []byte) []byte {
	var kept []string
	skip := false
	for _, line := range strings.SplitAfter(string(data), "\n") {
		text := lineText(line)
		if name, ok := headerName(text); ok {
			skip = isRunnerHeader(name, strings.Contains(text, "]"))
		}
		if !skip {
			kept = append(kept, line)
		}
	}
	return []byte(strings.Join(kept, ""))
}

// isRunnerHeader is whether the table header name belongs to the runner. An
// unclosed header counts when its name starts with a runner table's name, so
// a half-written "[runner" is cut out like a finished one.
func isRunnerHeader(name string, closed bool) bool {
	top, _, _ := strings.Cut(name, ".")
	if slices.Contains(runnerTableNames(), top) {
		return true
	}
	if closed {
		return false
	}
	return slices.ContainsFunc(runnerTableNames(), func(table string) bool {
		return strings.HasPrefix(name, table)
	})
}

func parseCore(data []byte) (Config, error) {
	core := coreFile{Defaults: Default().Defaults}
	meta, err := toml.Decode(string(withoutRunnerTables(data)), &core)
	if err != nil {
		return Config{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if keys := undecodedNames(meta.Undecoded(), false); len(keys) > 0 {
		return Config{}, fmt.Errorf("%w: unknown keys %s%s", ErrInvalid, strings.Join(keys, ", "), tokenHint(keys))
	}
	cfg := Config{Defaults: core.Defaults, Hosts: core.Hosts, Review: core.Review, Environments: core.Environments}
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
	if err := c.Environments.check(); err != nil {
		return Config{}, err
	}
	return Config{Defaults: Defaults{Host: host, Owners: owners}, Hosts: hosts, Review: c.Review, Environments: c.Environments}, nil
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
