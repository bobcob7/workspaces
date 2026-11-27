package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/BurntSushi/toml"
)

// ErrKeyUnknown marks a key that isn't one of ws config's keys.
var ErrKeyUnknown = errors.New("unknown config key")

// ErrValueInvalid marks a value that breaks its key's rule.
var ErrValueInvalid = errors.New("invalid config value")

// ErrUneditable marks a file that sets the key in a form Set and Unset
// don't edit, such as a dotted key or an inline table.
var ErrUneditable = errors.New("config key can't be edited in place")

// Type is a setting's value type, as ws config --json prints it.
type Type string

// The value types ws config --json prints.
const (
	TypeString   Type = "string"
	TypeStrings  Type = "strings"
	TypeDuration Type = "duration"
	TypeBool     Type = "bool"
	TypeHosts    Type = "hosts"
	TypeInt      Type = "int"
	TypeFloat    Type = "float"
)

// Setting is one key of config.toml, as ws config lists it. Value is the
// effective value: the file's, or Default. Value and Default are string,
// []string, bool, or []Host; a duration is its short string ("1h30m").
type Setting struct {
	Key     string `json:"key"`
	Type    Type   `json:"type"`
	Value   any    `json:"value"`
	Default any    `json:"default"`
	Set     bool   `json:"set"`
}

// keyRule is one of ws config's keys: the table that holds it and its leaf.
type keyRule struct {
	key   string
	typ   Type
	table string
	leaf  string
}

// Settings parses data as config.toml, with the same checks as Load, and
// returns every key in docs/tools/ws-config.md order: defaults.host,
// defaults.owners, hosts, review.timeout, review.remove_finished, the
// runner's fixed keys, then .when and .model for each [review.profiles.<name>]
// table, then each endpoint, model and runner role's keys, sorted by name.
func Settings(data []byte) ([]Setting, error) {
	cfg, meta, err := load(data)
	if err != nil {
		return nil, err
	}
	rules := fixedRules()
	if meta.IsDefined("runner") || meta.IsDefined("daemon") {
		rules = append(rules, runnerFixedRules()...)
	}
	rules = append(rules, profileRules(&cfg)...)
	rules = append(rules, entryRules(&cfg)...)
	if meta.IsDefined("environments") {
		rules = append(rules, environmentRules()...)
	}
	if meta.IsDefined("claude") {
		rules = append(rules, claudeRules()...)
	}
	settings := make([]Setting, 0, len(rules))
	for _, rule := range rules {
		settings = append(settings, settingFor(rule, &cfg, meta))
	}
	return settings, nil
}

// profileRules are the .when and .model keys of each review profile table.
func profileRules(cfg *Config) []keyRule {
	var rules []keyRule
	for _, name := range slices.Sorted(maps.Keys(cfg.Review.Profiles)) {
		for _, leaf := range []string{"when", "model"} {
			rules = append(rules, profileRule(name, leaf))
		}
	}
	return rules
}

// entryRules are the keys of each endpoint, model and runner role, sorted
// by name within each kind.
func entryRules(cfg *Config) []keyRule {
	var rules []keyRule
	for _, name := range slices.Sorted(maps.Keys(cfg.Endpoints)) {
		rules = append(rules, leafRules("endpoints", name)...)
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Models)) {
		rules = append(rules, leafRules("models", name)...)
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Runner.Roles)) {
		rules = append(rules, leafRules("runner.roles", name)...)
	}
	return rules
}

// leafRules are the keys of the entry kind.name, in leafSpecs order.
func leafRules(kind, name string) []keyRule {
	specs := leafSpecs(kind)
	rules := make([]keyRule, 0, len(specs))
	for _, spec := range specs {
		table := kind + "." + name
		rules = append(rules, keyRule{key: table + "." + spec.leaf, typ: spec.typ, table: table, leaf: spec.leaf})
	}
	return rules
}

// leafSpec is one key of an entry table, such as models.<alias>.id.
type leafSpec struct {
	leaf string
	typ  Type
}

// leafSpecs are the keys of an endpoints, models or runner.roles entry, in
// the order ws config lists them. Nil for any other kind.
func leafSpecs(kind string) []leafSpec {
	switch kind {
	case "endpoints":
		return []leafSpec{
			{"url", TypeString},
			{"api", TypeString},
			{"probe", TypeString},
			{"request_timeout", TypeDuration},
			{"idle_timeout", TypeDuration},
		}
	case "models":
		return []leafSpec{
			{"endpoint", TypeString},
			{"id", TypeString},
			{"context_tokens", TypeInt},
			{"max_output", TypeInt},
			{"temperature", TypeFloat},
			{"reasoning", TypeString},
		}
	case "runner.roles":
		return []leafSpec{
			{"runner", TypeString},
			{"model", TypeString},
			{"max_steps", TypeInt},
			{"timeout", TypeDuration},
			{"max_tokens", TypeInt},
			{"mcp_servers", TypeStrings},
			{"mcp_tools", TypeStrings},
			{"mcp_deny", TypeStrings},
			{"mcp_upfront", TypeInt},
		}
	}
	return nil
}

// leafType is the type of leaf under an entry kind, and whether it is one.
func leafType(kind, leaf string) (Type, bool) {
	for _, spec := range leafSpecs(kind) {
		if spec.leaf == leaf {
			return spec.typ, true
		}
	}
	return "", false
}

// Lookup returns key's Setting in data. A well-formed profile key whose
// table isn't in data has Set false and the default. An unknown key
// wraps ErrKeyUnknown.
func Lookup(data []byte, key string) (Setting, error) {
	rule, err := parseKey(key)
	if err != nil {
		return Setting{}, err
	}
	cfg, meta, err := load(data)
	if err != nil {
		return Setting{}, err
	}
	return settingFor(rule, &cfg, meta), nil
}

// Set returns data with key set to values, plus whether the text changed.
// Values are checked and normalized by the key's rule (ErrValueInvalid).
// Only key's lines change (see docs/tools/ws-config.md), except that hosts
// rewrites its whole list (rewriteHosts). The result is parsed again and
// must decode to the normalized value, or Set returns ErrUneditable.
func Set(data []byte, key string, values []string) ([]byte, bool, error) {
	out, err := setKey(data, key, values)
	if err != nil {
		return nil, false, err
	}
	if err := refuseNewRunnerError(data, out); err != nil {
		return nil, false, err
	}
	return out, !bytes.Equal(data, out), nil
}

// SetEntry creates or updates the entry table (endpoints.<name>,
// models.<alias> or runner.roles.<role>) from leaf=value pairs, in one edit.
// A model needs its endpoint, id and context_tokens together, so setting
// them one key at a time can't create it: each step alone leaves the runner's
// tables unusable. The runner's tables are checked once, after every pair.
func SetEntry(data []byte, table string, pairs []string) ([]byte, bool, error) {
	if _, ok := entryTable(table); !ok {
		return nil, false, fmt.Errorf("%w: %s", ErrKeyUnknown, table)
	}
	out := data
	for _, pair := range pairs {
		leaf, value, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, false, fmt.Errorf("%w: %q isn't leaf=value", ErrValueInvalid, pair)
		}
		var err error
		if out, err = setKey(out, table+"."+leaf, []string{value}); err != nil {
			return nil, false, err
		}
	}
	if err := refuseNewRunnerError(data, out); err != nil {
		return nil, false, err
	}
	return out, !bytes.Equal(data, out), nil
}

// setKey is Set without the check of the runner's tables.
func setKey(data []byte, key string, values []string) ([]byte, error) {
	rule, err := parseKey(key)
	if err != nil {
		return nil, err
	}
	if rule.typ == TypeHosts {
		out, _, hostsErr := replaceHosts(data, values)
		return out, hostsErr
	}
	want, err := normalize(rule, values)
	if err != nil {
		return nil, err
	}
	if rule.key == "defaults.host" && hostsListSet(data) {
		return nil, fmt.Errorf("%w: defaults.host can't be set while [[hosts]] is; use ws config set hosts", ErrValueInvalid)
	}
	lines, err := setLines(splitLines(data), rule, rule.leaf+" = "+encode(want))
	if err != nil {
		return nil, err
	}
	out := []byte(strings.Join(lines, ""))
	if err := verifySet(out, rule, want); err != nil {
		return nil, err
	}
	return out, nil
}

// Unset returns data without key, plus whether the text changed. A key
// that isn't set leaves data as it is. A [review.profiles.<name>] table
// left with no keys loses its header line too. Unset of hosts removes
// every [[hosts]] table.
func Unset(data []byte, key string) ([]byte, bool, error) {
	if table, ok := entryTable(key); ok {
		return unsetTable(data, table)
	}
	rule, err := parseUnsetKey(key)
	if err != nil {
		return nil, false, err
	}
	if rule.typ == TypeHosts {
		return replaceHosts(data, nil)
	}
	lines := splitLines(data)
	sp, found, err := findSpan(lines, rule)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return data, false, nil
	}
	lines = dropEmptyTable(slices.Delete(lines, sp.first, sp.last+1), rule.table)
	out := []byte(strings.Join(lines, ""))
	cfg, meta, err := load(out)
	if err != nil || settingFor(rule, &cfg, meta).Set {
		return nil, false, fmt.Errorf("%w: %s", ErrUneditable, rule.key)
	}
	if err := refuseNewRunnerError(data, out); err != nil {
		return nil, false, err
	}
	return out, !bytes.Equal(data, out), nil
}

// unsetTable removes the whole table, such as [models.<alias>] with its
// keys, from data. A table that isn't there leaves data as it is.
func unsetTable(data []byte, table string) ([]byte, bool, error) {
	lines := splitLines(data)
	kept := removeTable(lines, table)
	if len(kept) == len(lines) {
		return data, false, nil
	}
	out := []byte(strings.Join(kept, ""))
	_, meta, err := load(out)
	if err != nil || meta.IsDefined(strings.Split(table, ".")...) {
		return nil, false, fmt.Errorf("%w: %s", ErrUneditable, table)
	}
	if err := refuseNewRunnerError(data, out); err != nil {
		return nil, false, err
	}
	return out, !bytes.Equal(data, out), nil
}

// IsEntryTable reports whether key names a table that Unset removes whole:
// endpoints.<name>, models.<alias>, or runner.roles.<role>.
func IsEntryTable(key string) bool {
	_, ok := entryTable(key)
	return ok
}

// entryTable is the table of an entry key that unset removes whole:
// endpoints.<name>, models.<alias>, or runner.roles.<role>.
func entryTable(key string) (string, bool) {
	for _, kind := range []string{"endpoints", "models", "runner.roles"} {
		rest, ok := strings.CutPrefix(key, kind+".")
		if ok && runnerNamePattern.MatchString(rest) {
			return kind + "." + rest, true
		}
	}
	return "", false
}

// refuseNewRunnerError refuses a write that leaves the runner's tables
// unusable when they were usable before (ADR 0071).
func refuseNewRunnerError(before, after []byte) error {
	prior, err := parse(before)
	if err != nil {
		return err
	}
	next, err := parse(after)
	if err != nil {
		return err
	}
	if prior.RunnerErr == nil && next.RunnerErr != nil {
		return fmt.Errorf("%w: %w", ErrValueInvalid, next.RunnerErr)
	}
	return nil
}

// WriteFile replaces path with data atomically. It follows a symlink at
// path, writes a temp file in the target's directory, syncs it, gives it
// the old file's mode (0o644 if there is none), and renames it into place.
func WriteFile(path string, data []byte) error {
	target, err := resolveTarget(path)
	if err != nil {
		return err
	}
	mode, err := fileMode(target)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+"-*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := writeSynced(tmp, data, mode); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// FormatDuration is d in short form: "1h30m", "1h", "45s", "0s".
func FormatDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// fixedRules are the keys that always exist in ws config.
func fixedRules() []keyRule {
	return []keyRule{
		{key: "defaults.host", typ: TypeString, table: "defaults", leaf: "host"},
		{key: "defaults.owners", typ: TypeStrings, table: "defaults", leaf: "owners"},
		{key: "hosts", typ: TypeHosts, table: "", leaf: "hosts"},
		{key: "review.timeout", typ: TypeDuration, table: "review", leaf: "timeout"},
		{key: "review.remove_finished", typ: TypeBool, table: "review", leaf: "remove_finished"},
	}
}

// runnerFixedRules are the runner's and daemon's fixed keys. Settings lists
// them only when config.toml has their tables, as it does for profiles.
func runnerFixedRules() []keyRule {
	return []keyRule{
		{key: "runner.model", typ: TypeString, table: "runner", leaf: "model"},
		{key: "runner.keep_git_tokens", typ: TypeBool, table: "runner", leaf: "keep_git_tokens"},
		{key: "runner.user_instructions", typ: TypeBool, table: "runner", leaf: "user_instructions"},
		{key: "daemon.resume_on_start", typ: TypeBool, table: "daemon", leaf: "resume_on_start"},
	}
}

// environmentRules are [environments]'s keys, in ws config's order.
func environmentRules() []keyRule {
	return []keyRule{
		{key: "environments.listen", typ: TypeString, table: "environments", leaf: "listen"},
		{key: "environments.enroll_listen", typ: TypeString, table: "environments", leaf: "enroll_listen"},
		{key: "environments.public_name", typ: TypeString, table: "environments", leaf: "public_name"},
		{key: "environments.renew_before_days", typ: TypeInt, table: "environments", leaf: "renew_before_days"},
		{key: "environments.agent_loop_scripts", typ: TypeBool, table: "environments", leaf: "agent_loop_scripts"},
		{key: "environments.default_scope", typ: TypeStrings, table: "environments", leaf: "default_scope"},
	}
}

// claudeRules is [claude]'s key, listed only when config.toml has the table (ADR 0085).
func claudeRules() []keyRule {
	return []keyRule{
		{key: "claude.config_dir", typ: TypeString, table: "claude", leaf: "config_dir"},
	}
}

// profileRule is review.profiles.<name>.<leaf>.
func profileRule(name, leaf string) keyRule {
	table := "review.profiles." + name
	return keyRule{key: table + "." + leaf, typ: TypeString, table: table, leaf: leaf}
}

// parseKey returns the rule for key, or an error wrapping ErrKeyUnknown.
func parseKey(key string) (keyRule, error) {
	return parseKeyWith(key, profileNameValid)
}

// parseUnsetKey is parseKey, but accepts any profile name, so unset can
// clean up a hand-written profile whose name Set refuses.
func parseUnsetKey(key string) (keyRule, error) {
	return parseKeyWith(key, func(name string) bool { return name != "" })
}

// parseKeyWith is parseKey with the rule for profile names passed in.
func parseKeyWith(key string, nameOK func(string) bool) (keyRule, error) {
	for _, rule := range slices.Concat(fixedRules(), runnerFixedRules(), environmentRules(), claudeRules()) {
		if rule.key == key {
			return rule, nil
		}
	}
	if rest, ok := strings.CutPrefix(key, "review.profiles."); ok {
		name, leaf, found := strings.Cut(rest, ".")
		if found && nameOK(name) && (leaf == "when" || leaf == "model") {
			return profileRule(name, leaf), nil
		}
	}
	if rule, ok := parseEntryKey(key); ok {
		return rule, nil
	}
	return keyRule{}, fmt.Errorf("%w: %s", ErrKeyUnknown, key)
}

// parseEntryKey is the rule for a runner key under an endpoint, model, role
// or env entry: endpoints.<name>.<leaf>, models.<alias>.<leaf>,
// runner.roles.<role>.<leaf> or runner.env.<NAME>.
func parseEntryKey(key string) (keyRule, bool) {
	if name, ok := strings.CutPrefix(key, "runner.env."); ok {
		if !runnerEnvPattern.MatchString(name) {
			return keyRule{}, false
		}
		return keyRule{key: key, typ: TypeString, table: "runner.env", leaf: name}, true
	}
	for _, kind := range []string{"endpoints", "models", "runner.roles"} {
		rest, ok := strings.CutPrefix(key, kind+".")
		if !ok {
			continue
		}
		name, leaf, found := strings.Cut(rest, ".")
		typ, known := leafType(kind, leaf)
		if !found || !known || !runnerNamePattern.MatchString(name) {
			return keyRule{}, false
		}
		return keyRule{key: key, typ: typ, table: kind + "." + name, leaf: leaf}, true
	}
	return keyRule{}, false
}

// profileNameValid reports whether name matches ^[a-z0-9-]+$.
func profileNameValid(name string) bool {
	return name != "" && !strings.ContainsFunc(name, func(r rune) bool {
		return r != '-' && (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
}

// load parses data the way Load does and also returns its MetaData, which
// tells which keys the file sets.
func load(data []byte) (Config, *toml.MetaData, error) {
	cfg, err := parse(data)
	if err != nil {
		return Config{}, nil, err
	}
	meta, err := toml.Decode(string(data), &map[string]any{})
	if err != nil {
		meta, err = toml.Decode(string(withoutRunnerTables(data)), &map[string]any{})
	}
	if err != nil {
		return Config{}, nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return cfg, &meta, nil
}

// settingFor is rule's Setting for a parsed file.
func settingFor(rule keyRule, cfg *Config, meta *toml.MetaData) Setting {
	set := meta.IsDefined(strings.Split(rule.key, ".")...)
	if rule.typ == TypeHosts {
		set = set && len(cfg.Hosts) > 0
	}
	return Setting{
		Key:     rule.key,
		Type:    rule.typ,
		Value:   valueOf(rule, cfg),
		Default: defaultOf(rule),
		Set:     set,
	}
}

// valueOf is rule's effective value in cfg.
func valueOf(rule keyRule, cfg *Config) any {
	switch rule.key {
	case "claude.config_dir":
		return cfg.Claude.Mode()
	case "defaults.host":
		return cfg.EffectiveHosts()[0].Name
	case "defaults.owners":
		return cfg.Defaults.Owners
	case "hosts":
		return cfg.EffectiveHosts()
	case "review.timeout":
		return FormatDuration(cfg.Review.WaitTimeout())
	case "review.remove_finished":
		return cfg.Review.RemovesFinished()
	case "runner.model":
		return cfg.Runner.Model
	case "runner.keep_git_tokens":
		return cfg.Runner.KeepsGitTokens()
	case "runner.user_instructions":
		return cfg.Runner.UserInstructions
	case "daemon.resume_on_start":
		return cfg.Daemon.ResumesOnStart()
	}
	if value, ok := environmentValue(rule, cfg); ok {
		return value
	}
	if value, ok := entryValue(rule, cfg); ok {
		return value
	}
	profile := cfg.Review.Profiles[strings.TrimPrefix(rule.table, "review.profiles.")]
	if rule.leaf == "when" {
		return profile.When
	}
	return profile.Model
}

// environmentValue is the effective value of an [environments] key, and
// whether rule is one of those keys.
func environmentValue(rule keyRule, cfg *Config) (any, bool) {
	switch rule.key {
	case "environments.listen":
		return cfg.Environments.ListenAddr(), true
	case "environments.enroll_listen":
		return cfg.Environments.EnrollAddr(), true
	case "environments.public_name":
		return cfg.Environments.PublicName, true
	case "environments.renew_before_days":
		return cfg.Environments.RenewDays(), true
	case "environments.agent_loop_scripts":
		return cfg.Environments.LoopScriptsAllowed(), true
	case "environments.default_scope":
		return append([]string{}, cfg.Environments.DefaultScope...), true
	}
	return nil, false
}

// entryValue is the effective value of a key under an endpoint, model,
// role or env entry, and whether rule is one of those keys.
func entryValue(rule keyRule, cfg *Config) (any, bool) {
	if rule.table == "runner.env" {
		return cfg.Runner.Env[rule.leaf], true
	}
	if name, ok := strings.CutPrefix(rule.table, "endpoints."); ok {
		return endpointValue(endpointWithDefaults(cfg.Endpoints[name]), rule.leaf), true
	}
	if name, ok := strings.CutPrefix(rule.table, "models."); ok {
		return modelValue(cfg.Models[name], rule.leaf), true
	}
	if name, ok := strings.CutPrefix(rule.table, "runner.roles."); ok {
		return roleValue(name, cfg.Runner.Roles[name], rule.leaf), true
	}
	return nil, false
}

func endpointValue(e Endpoint, leaf string) any {
	switch leaf {
	case "url":
		return e.URL
	case "api":
		return e.API
	case "probe":
		return e.Probe
	case "request_timeout":
		return FormatDuration(e.RequestTimeout)
	}
	return FormatDuration(e.IdleTimeout)
}

func modelValue(m Model, leaf string) any { //nolint:gocritic // hugeParam: a map value, read once per key.
	switch leaf {
	case "endpoint":
		return m.Endpoint
	case "id":
		return m.ID
	case "context_tokens":
		return m.ContextTokens
	case "max_output":
		return maxOutputOf(m.MaxOutput)
	case "temperature":
		if m.Temperature == "" {
			return defaultTemperature
		}
		return m.Temperature
	}
	return m.Reasoning
}

func roleValue(name string, r RunnerRole, leaf string) any { //nolint:gocritic // hugeParam: a map value, read once per key.
	switch leaf {
	case "runner":
		return runnerOrClaude(r.Runner)
	case "model":
		return r.Model
	case "max_steps":
		steps, _, _ := DefaultBudget(name)
		return withDefault(r.MaxSteps, steps)
	case "timeout":
		_, timeout, _ := DefaultBudget(name)
		return FormatDuration(withDefault(r.Timeout, timeout))
	case "max_tokens":
		_, _, tokens := DefaultBudget(name)
		return withDefault(r.MaxTokens, tokens)
	case "mcp_servers":
		return nonNilStrings(r.MCPServers)
	case "mcp_tools":
		if r.MCPTools == nil {
			return []string{"*"}
		}
		return r.MCPTools
	case "mcp_deny":
		return nonNilStrings(r.MCPDeny)
	}
	if r.MCPUpfront == nil {
		return defaultMCPUpfront
	}
	return *r.MCPUpfront
}

func runnerOrClaude(runner string) string {
	if runner == "" {
		return RunnerClaude
	}
	return runner
}

// withDefault is value, or fallback when value is zero.
func withDefault[T comparable](value, fallback T) T {
	var zero T
	if value == zero {
		return fallback
	}
	return value
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// defaultOf is rule's value when the file doesn't set it.
func defaultOf(rule keyRule) any {
	switch rule.key {
	case "claude.config_dir":
		return ClaudeConfigAuto
	case "defaults.host":
		return defaultHost
	case "defaults.owners", "environments.default_scope":
		return []string{}
	case "hosts":
		return []Host{{Name: defaultHost, Kind: KindAuto}}
	case "review.timeout":
		return FormatDuration(defaultReviewTimeout)
	case "review.remove_finished", "runner.keep_git_tokens", "daemon.resume_on_start", "environments.agent_loop_scripts":
		return true
	case "runner.user_instructions":
		return false
	case "environments.listen":
		return defaultEnvListen
	case "environments.enroll_listen":
		return defaultEnvEnrollListen
	case "environments.public_name":
		return ""
	case "environments.renew_before_days":
		return defaultRenewBeforeDays
	}
	if value, ok := entryDefault(rule); ok {
		return value
	}
	return ""
}

// entryDefault is the value of an entry key the file doesn't set, and
// whether rule is one of those keys.
func entryDefault(rule keyRule) (any, bool) {
	switch {
	case rule.table == "runner.env":
		return "", true
	case strings.HasPrefix(rule.table, "endpoints."):
		return endpointValue(endpointWithDefaults(Endpoint{}), rule.leaf), true
	case strings.HasPrefix(rule.table, "models."):
		return modelValue(Model{}, rule.leaf), true
	case strings.HasPrefix(rule.table, "runner.roles."):
		name := strings.TrimPrefix(rule.table, "runner.roles.")
		return roleValue(name, RunnerRole{}, rule.leaf), true
	}
	return nil, false
}

// verifySet checks that out sets rule's key to want.
func verifySet(out []byte, rule keyRule, want any) error {
	s, err := Lookup(out, rule.key)
	if err != nil || !s.Set || !reflect.DeepEqual(s.Value, want) {
		return fmt.Errorf("%w: %s", ErrUneditable, rule.key)
	}
	return nil
}

// normalize checks values against rule and returns the value Settings
// reads back for them.
func normalize(rule keyRule, values []string) (any, error) {
	for _, value := range values {
		if err := checkText(value); err != nil {
			return nil, err
		}
	}
	if rule.typ == TypeStrings {
		return normalizeList(rule, values)
	}
	if len(values) != 1 {
		return nil, fmt.Errorf("%w: %s takes one value, not %d", ErrValueInvalid, rule.key, len(values))
	}
	return normalizeOne(rule, values[0])
}

// normalizeList checks a list key's values: owners are lower-cased, and
// the runner's lists are kept as given.
func normalizeList(rule keyRule, values []string) (any, error) {
	if rule.key == "defaults.owners" {
		return normalizeOwners(values)
	}
	return nonNilStrings(slices.Clone(values)), nil
}

// checkText rejects text that can't be written inside a TOML basic string.
func checkText(value string) error {
	if strings.ContainsAny(value, `"\`) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: %q has a quote, backslash, or control character", ErrValueInvalid, value)
	}
	return nil
}

// normalizeOwners lower-cases owners and drops repeats, keeping the first.
func normalizeOwners(values []string) ([]string, error) {
	owners := []string{}
	for _, value := range values {
		owner := strings.ToLower(value)
		if err := validateOwner(owner); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrValueInvalid, err)
		}
		if !slices.Contains(owners, owner) {
			owners = append(owners, owner)
		}
	}
	return owners, nil
}

// normalizeOne checks the single value of a key other than a list.
func normalizeOne(rule keyRule, value string) (any, error) {
	if rule.table == "environments" {
		return normalizeEnvironment(rule, value)
	}
	if rule.table == "claude" {
		return normalizeClaudeConfigDir(value)
	}
	switch rule.typ {
	case TypeBool:
		return normalizeBool(rule.key, value)
	case TypeDuration:
		return normalizeTimeout(rule.key, value)
	case TypeInt:
		return normalizeInt(rule, value)
	case TypeFloat:
		return normalizeTemperature(value)
	case TypeString, TypeStrings, TypeHosts:
	}
	if rule.key == "defaults.host" {
		return normalizeHost(value)
	}
	if strings.HasPrefix(rule.table, "review.profiles.") {
		return normalizeProfileValue(rule, value)
	}
	return value, nil
}

// normalizeEnvironment checks an [environments] value with the rules load
// applies to it, so ws config set refuses what config.toml wouldn't load.
func normalizeEnvironment(rule keyRule, value string) (any, error) {
	var env Environments
	var out any = value
	switch rule.key {
	case "environments.listen":
		env.Listen = value
	case "environments.enroll_listen":
		env.EnrollListen = value
	case "environments.public_name":
		env.PublicName = value
	case "environments.renew_before_days":
		n, err := normalizeInt(rule, value)
		if err != nil {
			return nil, err
		}
		env.RenewBeforeDays, out = &n, n
	case "environments.agent_loop_scripts":
		b, err := normalizeBool(rule.key, value)
		if err != nil {
			return nil, err
		}
		return b, nil
	}
	if err := env.check(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrValueInvalid, err)
	}
	return out, nil
}

// normalizeClaudeConfigDir checks claude.config_dir is a mode (ADR 0085).
func normalizeClaudeConfigDir(value string) (any, error) {
	if !ValidClaudeConfigMode(value) {
		return nil, fmt.Errorf("%w: claude.config_dir must be auto, workspace or shared, not %q", ErrValueInvalid, value)
	}
	return value, nil
}

func normalizeProfileValue(rule keyRule, value string) (any, error) {
	if rule.leaf == "when" {
		return normalizeWhen(value)
	}
	return normalizeModel(value)
}

// normalizeInt reads a whole number. A context size has a floor, and no
// count is negative.
func normalizeInt(rule keyRule, value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%w: %s %q must be a whole number: %w", ErrValueInvalid, rule.key, value, err)
	}
	if n < 0 || (rule.leaf == "context_tokens" && n < MinContextTokens) {
		return 0, fmt.Errorf("%w: %s %d is out of range", ErrValueInvalid, rule.key, n)
	}
	return n, nil
}

// normalizeTemperature keeps a temperature as the string the file stores,
// after checking it's a number in [0, 2].
func normalizeTemperature(value string) (string, error) {
	if _, err := parseTemperature(value); err != nil {
		return "", fmt.Errorf("%w: %w", ErrValueInvalid, err)
	}
	return value, nil
}

func normalizeHost(value string) (string, error) {
	host := strings.ToLower(value)
	if err := validateHost(host); err != nil {
		return "", fmt.Errorf("%w: %w", ErrValueInvalid, err)
	}
	return host, nil
}

func normalizeTimeout(key, value string) (string, error) {
	d, err := time.ParseDuration(value)
	if err != nil {
		return "", fmt.Errorf("%w: %s %q: %w", ErrValueInvalid, key, value, err)
	}
	if d <= 0 {
		return "", fmt.Errorf("%w: %s %q must be greater than zero", ErrValueInvalid, key, value)
	}
	return FormatDuration(d), nil
}

func normalizeBool(key, value string) (bool, error) {
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("%w: %s must be true or false, not %q", ErrValueInvalid, key, value)
}

func normalizeWhen(value string) (string, error) {
	switch value {
	case "always", "auto", "off":
		return value, nil
	}
	return "", fmt.Errorf("%w: when must be always, auto, or off, not %q", ErrValueInvalid, value)
}

func normalizeModel(value string) (string, error) {
	if strings.ContainsFunc(value, unicode.IsSpace) {
		return "", fmt.Errorf("%w: model %q must not contain spaces", ErrValueInvalid, value)
	}
	return value, nil
}

// parseHostValues reads the NAME or NAME=KIND values of ws config set hosts.
func parseHostValues(values []string) ([]Host, error) {
	hosts := make([]Host, 0, len(values))
	for _, value := range values {
		if err := checkText(value); err != nil {
			return nil, err
		}
		name, kind, found := strings.Cut(value, "=")
		if !found {
			kind = KindAuto
		}
		hosts = append(hosts, Host{Name: name, Kind: kind})
	}
	normalized, err := normalizeHosts(hosts)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrValueInvalid, err)
	}
	return normalized, nil
}

// replaceHosts is Set and Unset for hosts: values become the whole list, and
// none removes it. A file that doesn't set hosts stays as it is for none.
func replaceHosts(data []byte, values []string) ([]byte, bool, error) {
	want, err := parseHostValues(values)
	if err != nil {
		return nil, false, err
	}
	_, meta, err := load(data)
	if err != nil {
		return nil, false, err
	}
	if len(want) == 0 && !meta.IsDefined("hosts") {
		return data, false, nil
	}
	lines, err := rewriteHosts(splitLines(data), want)
	if err != nil {
		return nil, false, err
	}
	out := []byte(strings.Join(lines, ""))
	if err := verifyHosts(out, want); err != nil {
		return nil, false, err
	}
	return out, !bytes.Equal(data, out), nil
}

// hostsListSet reports whether data sets [[hosts]] with at least one entry.
func hostsListSet(data []byte) bool {
	cfg, _, err := load(data)
	return err == nil && len(cfg.Hosts) > 0
}

// rewriteHosts returns lines with every [[hosts]] table replaced by hosts,
// written as one block where the first table was. defaults.host is removed
// too, and comment lines are kept (reference-hosts.md).
func rewriteHosts(lines []string, hosts []Host) ([]string, error) {
	if err := checkHostsForm(lines); err != nil {
		return nil, err
	}
	drop, err := dropMarks(lines)
	if err != nil {
		return nil, err
	}
	kept := make([]string, 0, len(lines))
	at := -1
	for i, line := range lines {
		if !drop[i] {
			kept = append(kept, line)
			continue
		}
		if at < 0 && isHostsHeader(lineText(line)) {
			at = len(kept)
		}
	}
	if len(hosts) == 0 {
		return kept, nil
	}
	eol := newline(lines)
	block := hostsBlock(hosts, eol)
	if at < 0 {
		return appendBlock(kept, block, eol), nil
	}
	return insertBlock(kept, at, block, eol), nil
}

// checkHostsForm rejects hosts set in a form rewriteHosts can't edit, such
// as an inline hosts = [...].
func checkHostsForm(lines []string) error {
	meta, err := toml.Decode(strings.Join(lines, ""), &Config{})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if !meta.IsDefined("hosts") || slices.ContainsFunc(lines, func(line string) bool { return isHostsHeader(lineText(line)) }) {
		return nil
	}
	return fmt.Errorf("%w: hosts", ErrUneditable)
}

// dropMarks marks the lines rewriteHosts removes: each [[hosts]] header,
// each key of a hosts table with all the lines of its value, and the
// defaults.host line.
func dropMarks(lines []string) ([]bool, error) {
	names := sectionNames(lines)
	drop := make([]bool, len(lines))
	for i := 0; i < len(lines); i++ {
		text := lineText(lines[i])
		if isHostsHeader(text) {
			drop[i] = true
			continue
		}
		if names[i] != "[hosts" || keyName(text) == "" {
			continue
		}
		sp, ok := spanAt(lines, i)
		if !ok {
			return nil, fmt.Errorf("%w: hosts", ErrUneditable)
		}
		markRange(drop, sp.first, sp.last)
		i = sp.last
	}
	sp, found, err := findSpan(lines, keyRule{key: "defaults.host", table: "defaults", leaf: "host"})
	if err != nil {
		return nil, err
	}
	if found {
		markRange(drop, sp.first, sp.last)
	}
	return drop, nil
}

// markRange sets drop from first through last.
func markRange(drop []bool, first, last int) {
	for i := first; i <= last; i++ {
		drop[i] = true
	}
}

// isHostsHeader reports whether text is a [[hosts]] header, spaces and tabs
// aside.
func isHostsHeader(text string) bool {
	return strings.HasPrefix(withoutBlanks(text), "[[hosts]]")
}

// hostsBlock is hosts as [[hosts]] tables, with a blank line between them.
func hostsBlock(hosts []Host, eol string) []string {
	var block []string
	for i, host := range hosts {
		if i > 0 {
			block = append(block, eol)
		}
		block = append(block, "[[hosts]]"+eol, "name = "+quote(host.Name)+eol, "kind = "+quote(host.Kind)+eol)
	}
	return block
}

// insertBlock puts block at index at of kept, with a blank line after it
// when a non-blank line follows.
func insertBlock(kept []string, at int, block []string, eol string) []string {
	if at > 0 && lineEnding(kept[at-1]) == "" {
		kept[at-1] += eol
	}
	out := slices.Insert(kept, at, block...)
	after := at + len(block)
	if after < len(out) && strings.TrimSpace(lineText(out[after])) != "" {
		out = slices.Insert(out, after, eol)
	}
	return out
}

// appendBlock adds block at the end of kept, after one blank line, the way
// appendTable adds a table.
func appendBlock(kept, block []string, eol string) []string {
	if len(kept) == 0 {
		return block
	}
	if lineEnding(kept[len(kept)-1]) == "" {
		kept[len(kept)-1] += eol
	}
	kept = append(kept, eol)
	return append(kept, block...)
}

// verifyHosts checks that out sets hosts to want and doesn't set defaults.host.
func verifyHosts(out []byte, want []Host) error {
	hosts, err := Lookup(out, "hosts")
	if err != nil || hosts.Set != (len(want) > 0) || (hosts.Set && !reflect.DeepEqual(hosts.Value, want)) {
		return fmt.Errorf("%w: hosts", ErrUneditable)
	}
	host, err := Lookup(out, "defaults.host")
	if err != nil || host.Set {
		return fmt.Errorf("%w: hosts", ErrUneditable)
	}
	return nil
}

// encode is value as TOML text.
func encode(value any) string {
	switch v := value.(type) {
	case bool:
		return strconv.FormatBool(v)
	case int:
		return strconv.Itoa(v)
	case []string:
		quoted := make([]string, 0, len(v))
		for _, s := range v {
			quoted = append(quoted, quote(s))
		}
		return "[" + strings.Join(quoted, ", ") + "]"
	}
	return quote(fmt.Sprint(value))
}

func quote(s string) string {
	return `"` + s + `"`
}

// splitLines splits data after each newline, keeping the line endings.
func splitLines(data []byte) []string {
	lines := strings.SplitAfter(string(data), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// lineText is line without its line ending.
func lineText(line string) string {
	return strings.TrimRight(line, "\r\n")
}

// lineEnding is line's line ending, or "" for a last line without one.
func lineEnding(line string) string {
	switch {
	case strings.HasSuffix(line, "\r\n"):
		return "\r\n"
	case strings.HasSuffix(line, "\n"):
		return "\n"
	}
	return ""
}

// newline is "\r\n" when lines use CRLF, and "\n" otherwise.
func newline(lines []string) string {
	if slices.ContainsFunc(lines, func(line string) bool { return strings.HasSuffix(line, "\r\n") }) {
		return "\r\n"
	}
	return "\n"
}

// headerName returns the table a header line names, with spaces and tabs
// removed, and whether text is a header at all.
func headerName(text string) (string, bool) {
	trimmed := strings.TrimLeft(text, " \t")
	if !strings.HasPrefix(trimmed, "[") {
		return "", false
	}
	inner, _, _ := strings.Cut(trimmed[1:], "]")
	return withoutBlanks(inner), true
}

// withoutBlanks is text with spaces and tabs removed.
func withoutBlanks(text string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, text)
}

// keyName is the key a "key = value" line sets, dotted names included, or ""
// when text isn't such a line.
func keyName(text string) string {
	name, _, found := strings.Cut(strings.TrimLeft(text, " \t"), "=")
	name = strings.TrimRight(name, " \t")
	if !found || name == "" || strings.ContainsFunc(name, notBareKeyRune) {
		return ""
	}
	return name
}

func notBareKeyRune(r rune) bool {
	return r != '_' && r != '-' && r != '.' && !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

// sectionNames is the table each line belongs to. A header line belongs to
// the table it names, and lines before the first header to "".
func sectionNames(lines []string) []string {
	names := make([]string, len(lines))
	current := ""
	for i, line := range lines {
		if name, ok := headerName(lineText(line)); ok {
			current = name
		}
		names[i] = current
	}
	return names
}

// lastHeader is the index of the last header line for table, or -1.
func lastHeader(lines []string, table string) int {
	found := -1
	for i, line := range lines {
		if name, ok := headerName(lineText(line)); ok && name == table {
			found = i
		}
	}
	return found
}

// span is the lines of one key, and the comment after its value.
type span struct {
	first   int
	last    int
	comment string
}

// findSpan returns the lines of rule's key, and whether the file sets it.
func findSpan(lines []string, rule keyRule) (span, bool, error) {
	names := sectionNames(lines)
	for i, line := range lines {
		if names[i] != rule.table || keyName(lineText(line)) != rule.leaf {
			continue
		}
		sp, ok := spanAt(lines, i)
		if !ok {
			return span{}, false, fmt.Errorf("%w: %s", ErrUneditable, rule.key)
		}
		return sp, true, nil
	}
	return span{}, false, nil
}

// spanAt returns the key on line i through the end of its value. It
// reports false for a multi-line string or an array that never closes.
func spanAt(lines []string, i int) (span, bool) {
	value := lineText(lines[i])
	value = value[strings.IndexByte(value, '=')+1:]
	trimmed := strings.TrimLeft(value, " \t")
	if strings.HasPrefix(trimmed, `"""`) || strings.HasPrefix(trimmed, "'''") {
		return span{}, false
	}
	isArray := strings.HasPrefix(trimmed, "[")
	depth := 0
	for last := i; last < len(lines); last++ {
		text := lineText(lines[last])
		if last != i {
			value = text
		}
		var comment string
		depth, comment = scanValueLine(value, depth)
		if !isArray || depth == 0 {
			return span{first: i, last: last, comment: comment}, true
		}
	}
	return span{}, false
}

// scanValueLine returns the bracket depth after text, skipping strings, and
// the comment that ends text, if any.
func scanValueLine(text string, depth int) (int, string) {
	for j := 0; j < len(text); j++ {
		switch text[j] {
		case '"', '\'':
			j = closingQuote(text, j)
		case '#':
			return depth, text[j:]
		case '[':
			depth++
		case ']':
			depth--
		}
	}
	return depth, ""
}

// closingQuote is the index of the quote that closes the string opened at
// open. Only basic strings, with double quotes, have escapes.
func closingQuote(text string, open int) int {
	for j := open + 1; j < len(text); j++ {
		switch {
		case text[open] == '"' && text[j] == '\\':
			j++
		case text[j] == text[open]:
			return j
		}
	}
	return len(text)
}

// setLines returns lines with rule's key set to assignment, which is
// "leaf = value".
func setLines(lines []string, rule keyRule, assignment string) ([]string, error) {
	sp, found, err := findSpan(lines, rule)
	if err != nil {
		return nil, err
	}
	if found {
		return replaceSpan(lines, sp, assignment), nil
	}
	if lastHeader(lines, rule.table) >= 0 {
		return insertInTable(lines, rule, assignment)
	}
	return appendTable(lines, rule, assignment), nil
}

// replaceSpan replaces sp's lines with one line of assignment, keeping the
// indent and the trailing comment.
func replaceSpan(lines []string, sp span, assignment string) []string {
	first := lineText(lines[sp.first])
	indent := first[:len(first)-len(strings.TrimLeft(first, " \t"))]
	text := indent + assignment
	if sp.comment != "" {
		text += " " + sp.comment
	}
	return slices.Replace(lines, sp.first, sp.last+1, text+lineEnding(lines[sp.last]))
}

// insertInTable adds assignment after the last key of rule's table, or
// after its header when the table has no keys.
func insertInTable(lines []string, rule keyRule, assignment string) ([]string, error) {
	names := sectionNames(lines)
	header := lastHeader(lines, rule.table)
	at := header + 1
	for i := header + 1; i < len(lines) && names[i] == rule.table; i++ {
		if keyName(lineText(lines[i])) == "" {
			continue
		}
		sp, ok := spanAt(lines, i)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUneditable, rule.key)
		}
		at = sp.last + 1
		i = sp.last
	}
	return insertLine(lines, at, assignment), nil
}

// insertLine inserts text as a line before index at.
func insertLine(lines []string, at int, text string) []string {
	eol := newline(lines)
	if lineEnding(lines[at-1]) == "" {
		lines[at-1] += eol
	}
	return slices.Insert(lines, at, text+eol)
}

// appendTable adds rule's table at the end of the file, with assignment.
func appendTable(lines []string, rule keyRule, assignment string) []string {
	eol := newline(lines)
	if n := len(lines); n > 0 {
		if lineEnding(lines[n-1]) == "" {
			lines[n-1] += eol
		}
		lines = append(lines, eol)
	}
	return append(lines, "["+rule.table+"]"+eol, assignment+eol)
}

// dynamicTableNames are the tables whose names carry a review profile,
// endpoint, model, role or env name. Unset drops their headers once they're
// empty.
func dynamicTableNames() []string {
	return []string{"review.profiles.", "endpoints.", "models.", "runner.roles.", "runner.env"}
}

// dropEmptyTable removes the headers of a dynamic table that has no keys
// left, with one blank line before each. Comments are kept.
func dropEmptyTable(lines []string, table string) []string {
	if !isDynamicTable(table) || tableHasKeys(lines, table) {
		return lines
	}
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if name, ok := headerName(lineText(line)); ok && name == table {
			if n := len(kept); n > 0 && strings.TrimSpace(lineText(kept[n-1])) == "" {
				kept = kept[:n-1]
			}
			continue
		}
		kept = append(kept, line)
	}
	return kept
}

func isDynamicTable(table string) bool {
	for _, prefix := range dynamicTableNames() {
		if strings.HasPrefix(table, prefix) {
			return true
		}
	}
	return false
}

// removeTable returns lines without table's header and keys, and the blank
// lines that follow the header or a removed key. Comment lines stay.
func removeTable(lines []string, table string) []string {
	names := sectionNames(lines)
	kept := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if names[i] != table {
			kept = append(kept, lines[i])
			continue
		}
		if header, ok := headerName(lineText(lines[i])); ok && header == table {
			i = skipBlank(lines, i)
			continue
		}
		sp, ok := spanAt(lines, i)
		if keyName(lineText(lines[i])) == "" || !ok {
			kept = append(kept, lines[i])
			continue
		}
		i = skipBlank(lines, sp.last)
	}
	return kept
}

// skipBlank is the index of the last blank line right after lines[i], or i.
func skipBlank(lines []string, i int) int {
	for i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "" {
		i++
	}
	return i
}

// tableHasKeys reports whether any key line belongs to table.
func tableHasKeys(lines []string, table string) bool {
	names := sectionNames(lines)
	for i, line := range lines {
		if names[i] == table && keyName(lineText(line)) != "" {
			return true
		}
	}
	return false
}

// resolveTarget is path with any symlinks followed. A path that doesn't
// exist is its own target.
func resolveTarget(path string) (string, error) {
	target, err := filepath.EvalSymlinks(path)
	if errors.Is(err, fs.ErrNotExist) {
		return path, nil
	}
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", path, err)
	}
	return target, nil
}

// fileMode is the permission bits of the file at path, or 0o644 when there
// is no file.
func fileMode(path string) (fs.FileMode, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0o644, nil
	}
	if err != nil {
		return 0, err
	}
	return info.Mode().Perm(), nil
}

// writeSynced writes data to f with mode, syncs it, and closes it.
func writeSynced(f *os.File, data []byte, mode fs.FileMode) error {
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
