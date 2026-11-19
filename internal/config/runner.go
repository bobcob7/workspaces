package config

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/BurntSushi/toml"
)

// Runner is config.toml's [runner] (ADRs 0064, 0068, 0069).
type Runner struct {
	Model            string                `toml:"model" json:"model"`
	KeepGitTokens    *bool                 `toml:"keep_git_tokens" json:"keep_git_tokens"`
	UserInstructions bool                  `toml:"user_instructions" json:"user_instructions"`
	Env              map[string]string     `toml:"env" json:"env"`
	Roles            map[string]RunnerRole `toml:"roles" json:"roles"`
}

// Endpoint is one [endpoints.<name>].
type Endpoint struct {
	URL            string        `toml:"url" json:"url"`
	API            string        `toml:"api" json:"api"`     // default APIOpenAI
	Probe          string        `toml:"probe" json:"probe"` // auto | ollama | none; default auto
	RequestTimeout time.Duration `toml:"request_timeout" json:"request_timeout"`
	IdleTimeout    time.Duration `toml:"idle_timeout" json:"idle_timeout"`
}

// Model is one [models.<alias>].
type Model struct {
	Endpoint      string `toml:"endpoint" json:"endpoint"`
	ID            string `toml:"id" json:"id"`
	ContextTokens int    `toml:"context_tokens" json:"context_tokens"`
	MaxOutput     int    `toml:"max_output" json:"max_output"`
	Temperature   string `toml:"temperature" json:"temperature"` // a float as a string; default "0.2"
	Reasoning     string `toml:"reasoning" json:"reasoning"`     // "" | none | low | medium | high
}

// RunnerRole is one [runner.roles.<role>].
type RunnerRole struct {
	Runner     string        `toml:"runner" json:"runner"`
	Model      string        `toml:"model" json:"model"`
	MaxSteps   int           `toml:"max_steps" json:"max_steps"`
	Timeout    time.Duration `toml:"timeout" json:"timeout"`
	MaxTokens  int           `toml:"max_tokens" json:"max_tokens"`
	MCPServers []string      `toml:"mcp_servers" json:"mcp_servers"`
	MCPTools   []string      `toml:"mcp_tools" json:"mcp_tools"` // default ["*"]
	MCPDeny    []string      `toml:"mcp_deny" json:"mcp_deny"`
	MCPUpfront *int          `toml:"mcp_upfront" json:"mcp_upfront"` // default 8
}

// Daemon is [daemon].
type Daemon struct {
	ResumeOnStart *bool `toml:"resume_on_start" json:"resume_on_start"` // default true
}

// Runner names, the one API ws implements, and the context floor
// (ADRs 0062, 0064).
const (
	RunnerClaude     = "claude"
	RunnerWS         = "ws"
	APIOpenAI        = "openai"
	MinContextTokens = 16384

	defaultMaxOutput      = 4096
	defaultTemperature    = "0.2"
	defaultMCPUpfront     = 8
	defaultRequestTimeout = 10 * time.Minute
	defaultIdleTimeout    = 90 * time.Second
	defaultProbe          = "auto"
)

// ReservedAPIs are wire protocols the design names but ws doesn't
// implement yet (ADR 0062): "ollama" is planned next (T27, which removes
// it from this list), the others are reserved.
func ReservedAPIs() []string { return []string{"ollama", "openai-responses", "anthropic"} }

// ReasoningLevels are the values of a model's reasoning besides "".
func ReasoningLevels() []string { return []string{"none", "low", "medium", "high"} }

// Errors a resolution returns when the configuration can't serve it.
var (
	ErrEndpointUnknown   = errors.New("endpoint not configured")
	ErrNoModel           = errors.New("no runner model given or configured")
	ErrAPINotImplemented = errors.New("api not implemented yet")
	ErrModelGone         = errors.New("model alias no longer configured")
)

// Target is the part of a resolution a run may re-read mid-run (ADR 0064,
// re-resolution): the model on its endpoint, defaults filled in.
type Target struct {
	ModelAlias    string // "" when a raw id was given
	ModelID       string
	EndpointName  string
	Endpoint      Endpoint
	ContextTokens int
	MaxOutput     int
	Temperature   float64
	Reasoning     string
}

// RunKey is what a run's resolution started from; run.toml keeps it, and
// it stays fixed for the run.
type RunKey struct {
	ModelAlias    string // run.toml model_alias; "" for a raw id
	ModelID       string // the raw id, when ModelAlias is ""
	EndpointAlias string // for a raw id: the alias whose endpoint it runs on
	EndpointFlag  string // --endpoint, or ""
}

// Resolved is everything a spawn needs to start a ws child, defaults filled in.
type Resolved struct {
	Target
	EndpointAlias string // for a raw id: the alias whose endpoint was used; else ""
	MaxSteps      int
	Timeout       time.Duration
	MaxTokens     int
	MCPServers    []string
	MCPTools      []string
	MCPDeny       []string
	MCPUpfront    int
	Warnings      []string
}

// runnerTableNames are config.toml's top-level keys the runner reads. They're
// decoded apart from the rest of the file, so a problem in them can't stop
// any other command (ADR 0071).
func runnerTableNames() []string {
	return []string{"runner", "endpoints", "models", "daemon"}
}

var (
	runnerNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	runnerEnvPattern  = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

// runnerFile is the part of config.toml the runner reads.
type runnerFile struct {
	Runner    Runner              `toml:"runner"`
	Endpoints map[string]Endpoint `toml:"endpoints"`
	Models    map[string]Model    `toml:"models"`
	Daemon    Daemon              `toml:"daemon"`
}

// loadRunner reads the runner's tables into c. A problem is kept in
// RunnerErr and never fails Load.
func (c *Config) loadRunner(data []byte) {
	c.roleRunners, c.roleRunnersBad = lenientRoleRunners(data)
	c.RunnerErr = decodeRunner(data, c)
	if c.RunnerErr == nil {
		c.RunnerErr = c.checkRunner()
	}
}

func decodeRunner(data []byte, c *Config) error {
	var file runnerFile
	meta, err := toml.Decode(string(data), &file)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if keys := undecodedNames(meta.Undecoded(), true); len(keys) > 0 {
		return fmt.Errorf("%w: unknown keys %s%s", ErrInvalid, strings.Join(keys, ", "), runnerTokenHint(keys))
	}
	c.Runner, c.Endpoints, c.Models, c.Daemon = file.Runner, file.Endpoints, file.Models, file.Daemon
	return nil
}

// undecodedNames returns the undecoded keys that are in the runner's tables
// when runner is true, and the others when it is false.
func undecodedNames(keys []toml.Key, runner bool) []string {
	var names []string
	for _, key := range keys {
		if slices.Contains(runnerTableNames(), key[0]) == runner {
			names = append(names, key.String())
		}
	}
	return names
}

// runnerTokenHint says where a token goes when a runner table holds one.
func runnerTokenHint(keys []string) string {
	for _, key := range keys {
		if strings.HasSuffix(key, ".token") {
			return "; tokens go in WS_ENDPOINT_TOKEN_<NAME> in the environment or secrets.env, never in config.toml"
		}
	}
	return ""
}

// lenientRoleRunners returns each runner.roles.<role>.runner as written,
// whatever its type, so RunnerFor can answer per role. The second result
// is true when the roles can't be read at all.
func lenientRoleRunners(data []byte) (map[string]any, bool) {
	var file struct {
		Runner struct {
			Roles map[string]struct {
				Runner any `toml:"runner"`
			} `toml:"roles"`
		} `toml:"runner"`
	}
	if _, err := toml.Decode(string(data), &file); err != nil {
		return nil, true
	}
	roles := file.Runner.Roles
	if roles == nil {
		return nil, false
	}
	values := make(map[string]any, len(roles))
	for name, role := range roles {
		values[name] = role.Runner
	}
	return values, false
}

// RunnerFor is runner.roles.<role>.runner, or RunnerClaude. A value that
// can't be read gives RunnerWS, so the spawn fails rather than running on
// Claude.
func (c *Config) RunnerFor(role string) string {
	if role == "" {
		return RunnerClaude
	}
	if c.roleRunnersBad {
		return RunnerWS
	}
	value, ok := c.roleRunners[role]
	if !ok || value == nil {
		return RunnerClaude
	}
	if name, ok := value.(string); ok && (name == "" || name == RunnerClaude) {
		return RunnerClaude
	}
	return RunnerWS
}

// Resolve applies ADR 0064's order: modelFlag (an alias, else a raw id on
// the endpoint of the alias the role or runner.model would choose, with a
// warning), the role's model, runner.model; endpointFlag overrides the
// alias's endpoint. Budgets from the role, else DefaultBudget(role).
func (c *Config) Resolve(role, endpointFlag, modelFlag string) (Resolved, error) {
	if c.RunnerErr != nil {
		return Resolved{}, c.RunnerErr
	}
	if _, ok := c.Models[modelFlag]; ok || modelFlag == "" {
		return c.resolveAlias(role, endpointFlag, modelFlag)
	}
	return c.resolveRaw(role, endpointFlag, modelFlag)
}

func (c *Config) resolveAlias(role, endpointFlag, alias string) (Resolved, error) {
	if alias == "" {
		alias = c.chosenAlias(role)
	}
	if alias == "" {
		return Resolved{}, ErrNoModel
	}
	target, err := c.buildTarget(alias, c.Models[alias], endpointFlag)
	if err != nil {
		return Resolved{}, err
	}
	return c.resolved(role, target, "", nil), nil
}

func (c *Config) resolveRaw(role, endpointFlag, id string) (Resolved, error) {
	base := c.chosenAlias(role)
	if base == "" {
		return Resolved{}, ErrNoModel
	}
	target, err := c.buildTarget("", rawModel(c.Models[base], id), endpointFlag)
	if err != nil {
		return Resolved{}, err
	}
	warning := fmt.Sprintf("%q isn't a model alias; running it as a raw id on the endpoint of %s", id, base)
	return c.resolved(role, target, base, []string{warning}), nil
}

// chosenAlias is the alias steps 2 and 3 of ADR 0064 pick: the role's
// model, else runner.model.
func (c *Config) chosenAlias(role string) string {
	if model := c.Runner.Roles[role].Model; model != "" {
		return model
	}
	return c.Runner.Model
}

// rawModel is the model a raw id runs as: the alias's endpoint and context
// size, with the id given (ADR 0064).
func rawModel(base Model, id string) Model { //nolint:gocritic // hugeParam: a map value, copied once per resolution.
	return Model{Endpoint: base.Endpoint, ID: id, ContextTokens: base.ContextTokens}
}

func (c *Config) resolved(role string, target Target, endpointAlias string, warnings []string) Resolved { //nolint:gocritic // hugeParam: built once per resolution.
	steps, timeout, tokens := c.budgetFor(role)
	setting := c.Runner.Roles[role]
	upfront := defaultMCPUpfront
	if setting.MCPUpfront != nil {
		upfront = *setting.MCPUpfront
	}
	tools := setting.MCPTools
	if tools == nil {
		tools = []string{"*"}
	}
	return Resolved{
		Target:        target,
		EndpointAlias: endpointAlias,
		MaxSteps:      steps,
		Timeout:       timeout,
		MaxTokens:     tokens,
		MCPServers:    setting.MCPServers,
		MCPTools:      tools,
		MCPDeny:       setting.MCPDeny,
		MCPUpfront:    upfront,
		Warnings:      warnings,
	}
}

// budgetFor is the role's budgets, each falling back to DefaultBudget.
func (c *Config) budgetFor(role string) (int, time.Duration, int) {
	steps, timeout, tokens := DefaultBudget(role)
	setting := c.Runner.Roles[role]
	if setting.MaxSteps > 0 {
		steps = setting.MaxSteps
	}
	if setting.Timeout > 0 {
		timeout = setting.Timeout
	}
	if setting.MaxTokens > 0 {
		tokens = setting.MaxTokens
	}
	return steps, timeout, tokens
}

// buildTarget is the target for model m under alias (or "" for a raw id),
// on endpointFlag's endpoint when it's set.
func (c *Config) buildTarget(alias string, m Model, endpointFlag string) (Target, error) { //nolint:gocritic // hugeParam: a map value, copied once per resolution.
	name := m.Endpoint
	if endpointFlag != "" {
		name = endpointFlag
	}
	endpoint, ok := c.Endpoints[name]
	if !ok {
		return Target{}, fmt.Errorf("%w: %s", ErrEndpointUnknown, name)
	}
	temperature, err := parseTemperature(m.Temperature)
	if err != nil {
		return Target{}, err
	}
	return Target{
		ModelAlias:    alias,
		ModelID:       m.ID,
		EndpointName:  name,
		Endpoint:      endpointWithDefaults(endpoint),
		ContextTokens: m.ContextTokens,
		MaxOutput:     maxOutputOf(m.MaxOutput),
		Temperature:   temperature,
		Reasoning:     m.Reasoning,
	}, nil
}

// Key is the RunKey a spawn writes to run.toml for r and endpointFlag.
func (r *Resolved) Key(endpointFlag string) RunKey {
	key := RunKey{ModelAlias: r.ModelAlias, EndpointAlias: r.EndpointAlias, EndpointFlag: endpointFlag}
	if r.ModelAlias == "" {
		key.ModelID = r.ModelID
	}
	return key
}

// Reresolve reads the run's alias (or, for a raw id, EndpointAlias's
// endpoint) as the configuration is now, with EndpointFlag still winning.
// It never chooses another alias. ErrModelGone, ErrEndpointUnknown or
// ErrAPINotImplemented when the key no longer resolves.
func (c *Config) Reresolve(key RunKey) (Target, error) {
	if c.RunnerErr != nil {
		return Target{}, c.RunnerErr
	}
	if key.ModelAlias == "" {
		base, ok := c.Models[key.EndpointAlias]
		if !ok {
			return Target{}, fmt.Errorf("%w: %s", ErrModelGone, key.EndpointAlias)
		}
		return c.buildTarget("", rawModel(base, key.ModelID), key.EndpointFlag)
	}
	model, ok := c.Models[key.ModelAlias]
	if !ok {
		return Target{}, fmt.Errorf("%w: %s", ErrModelGone, key.ModelAlias)
	}
	return c.buildTarget(key.ModelAlias, model, key.EndpointFlag)
}

// ReadFile reads, parses and validates a config.toml the way Load does,
// but never creates one: a missing file is an error. The runner uses it
// to re-resolve.
func ReadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading config: %w", err)
	}
	cfg, err := parse(data)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// KeepsGitTokens is runner.keep_git_tokens, default true.
func (r *Runner) KeepsGitTokens() bool {
	return r.KeepGitTokens == nil || *r.KeepGitTokens
}

// ResumesOnStart is daemon.resume_on_start, default true.
func (d *Daemon) ResumesOnStart() bool {
	return d.ResumeOnStart == nil || *d.ResumeOnStart
}

// DefaultBudget returns a role's budgets: implementer 80 steps, 60m,
// 4_000_000 tokens; reviewer 40, 30m, 2_000_000; verifier 40, 60m,
// 2_000_000; anything else 60, 60m, 4_000_000.
func DefaultBudget(role string) (maxSteps int, timeout time.Duration, maxTokens int) {
	switch role {
	case "implementer":
		return 80, 60 * time.Minute, 4_000_000
	case "reviewer":
		return 40, 30 * time.Minute, 2_000_000
	case "verifier":
		return 40, 60 * time.Minute, 2_000_000
	}
	return 60, 60 * time.Minute, 4_000_000
}

// TokenEnv is WS_ENDPOINT_TOKEN_ + upper(name), '-' → '_'.
func TokenEnv(name string) string {
	return "WS_ENDPOINT_TOKEN_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// RunnerError is RunnerErr: why the runner's tables can't be used, or nil.
// Only a ws start, ws runner, ws daemon, ws config and Settings › Models
// look at it.
func (c *Config) RunnerError() error {
	return c.RunnerErr
}

// checkRunner applies the rules of reference.md's config.toml section and
// returns the first one broken, naming its key.
func (c *Config) checkRunner() error {
	checks := []func() error{c.checkRunnerTable, c.checkEndpoints, c.checkModels, c.checkRoles}
	for _, check := range checks {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) checkRunnerTable() error {
	if c.Runner.Model != "" && !c.hasAlias(c.Runner.Model) {
		return fmt.Errorf("runner.model %q isn't a defined model alias", c.Runner.Model)
	}
	for _, name := range slices.Sorted(maps.Keys(c.Runner.Env)) {
		if err := checkEnvName(name); err != nil {
			return err
		}
	}
	return nil
}

func checkEnvName(name string) error {
	if !runnerEnvPattern.MatchString(name) {
		return fmt.Errorf("runner.env.%s: the name must be uppercase letters, digits, or _", name)
	}
	for _, suffix := range []string{"_TOKEN", "_KEY", "_SECRET", "_PASSWORD"} {
		if strings.HasSuffix(name, suffix) {
			return fmt.Errorf("runner.env.%s: put secrets in secrets.env", name)
		}
	}
	return nil
}

func (c *Config) hasAlias(alias string) bool {
	_, ok := c.Models[alias]
	return ok
}

func (c *Config) checkEndpoints() error {
	for _, name := range slices.Sorted(maps.Keys(c.Endpoints)) {
		if err := checkEntryName("endpoints", name); err != nil {
			return err
		}
		if err := checkEndpoint(name, c.Endpoints[name]); err != nil {
			return err
		}
	}
	return nil
}

func checkEntryName(section, name string) error {
	if !runnerNamePattern.MatchString(name) {
		return fmt.Errorf("%s.%s: the name must be 1–32 lowercase letters, digits, or -, starting with a letter", section, name)
	}
	return nil
}

func checkEndpoint(name string, e Endpoint) error {
	key := "endpoints." + name
	if err := checkURL(e.URL); err != nil {
		return fmt.Errorf("%s.url: %w", key, err)
	}
	if err := checkAPI(key, e.API); err != nil {
		return err
	}
	if !slices.Contains([]string{"", "auto", "ollama", "none"}, e.Probe) {
		return fmt.Errorf("%s.probe %q must be auto, ollama, or none", key, e.Probe)
	}
	return nil
}

func checkURL(raw string) error {
	if raw == "" {
		return errors.New("is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%q must start with http:// or https://", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("%q has no host", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%q must have no user info, query, or fragment", raw)
	}
	return nil
}

func checkAPI(key, api string) error {
	switch {
	case api == "" || api == APIOpenAI:
		return nil
	case slices.Contains(ReservedAPIs(), api):
		return fmt.Errorf("%s.api: %q isn't implemented yet: %w", key, api, ErrAPINotImplemented)
	}
	return fmt.Errorf("%s.api %q must be %s", key, api, APIOpenAI)
}

func (c *Config) checkModels() error {
	for _, alias := range slices.Sorted(maps.Keys(c.Models)) {
		if err := checkEntryName("models", alias); err != nil {
			return err
		}
		if err := c.checkModel(alias, c.Models[alias]); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) checkModel(alias string, m Model) error { //nolint:gocritic // hugeParam: a map value, checked once.
	key := "models." + alias
	if m.Endpoint == "" {
		return fmt.Errorf("%s.endpoint is required", key)
	}
	if _, ok := c.Endpoints[m.Endpoint]; !ok {
		return fmt.Errorf("%s.endpoint %q isn't a defined endpoint", key, m.Endpoint)
	}
	if err := checkModelID(m.ID); err != nil {
		return fmt.Errorf("%s.id: %w", key, err)
	}
	if m.ContextTokens < MinContextTokens {
		return fmt.Errorf("%s.context_tokens %d must be at least %d", key, m.ContextTokens, MinContextTokens)
	}
	if m.MaxOutput < 0 {
		return fmt.Errorf("%s.max_output %d can't be negative", key, m.MaxOutput)
	}
	if _, err := parseTemperature(m.Temperature); err != nil {
		return fmt.Errorf("%s.temperature: %w", key, err)
	}
	if m.Reasoning != "" && !slices.Contains(ReasoningLevels(), m.Reasoning) {
		return fmt.Errorf("%s.reasoning %q must be none, low, medium, or high", key, m.Reasoning)
	}
	return nil
}

// checkModelID accepts any non-empty id without control characters. The
// id is kept exactly as written: it's never trimmed or split.
func checkModelID(id string) error {
	if id == "" {
		return errors.New("is required")
	}
	if strings.IndexFunc(id, unicode.IsControl) >= 0 {
		return fmt.Errorf("%q has a control character", id)
	}
	return nil
}

func (c *Config) checkRoles() error {
	for _, role := range slices.Sorted(maps.Keys(c.Runner.Roles)) {
		if err := checkEntryName("runner.roles", role); err != nil {
			return err
		}
		if err := c.checkRole(role, c.Runner.Roles[role]); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) checkRole(role string, r RunnerRole) error { //nolint:gocritic // hugeParam: a map value, checked once.
	key := "runner.roles." + role
	if !slices.Contains([]string{"", RunnerClaude, RunnerWS}, r.Runner) {
		return fmt.Errorf("%s.runner %q must be claude or ws", key, r.Runner)
	}
	if r.Model != "" && !c.hasAlias(r.Model) {
		return fmt.Errorf("%s.model %q isn't a defined model alias", key, r.Model)
	}
	if r.MCPUpfront != nil && *r.MCPUpfront < 0 {
		return fmt.Errorf("%s.mcp_upfront %d can't be negative", key, *r.MCPUpfront)
	}
	if slices.Contains(r.MCPServers, "") {
		return fmt.Errorf("%s.mcp_servers has an empty name", key)
	}
	return nil
}

// parseTemperature reads a model's temperature, "0.2" by default, and
// checks it's in [0, 2].
func parseTemperature(value string) (float64, error) {
	if value == "" {
		value = defaultTemperature
	}
	t, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("temperature %q is not a number: %w", value, err)
	}
	if t < 0 || t > 2 {
		return 0, fmt.Errorf("temperature %q must be between 0 and 2", value)
	}
	return t, nil
}

func endpointWithDefaults(e Endpoint) Endpoint {
	if e.API == "" {
		e.API = APIOpenAI
	}
	if e.Probe == "" {
		e.Probe = defaultProbe
	}
	if e.RequestTimeout == 0 {
		e.RequestTimeout = defaultRequestTimeout
	}
	if e.IdleTimeout == 0 {
		e.IdleTimeout = defaultIdleTimeout
	}
	return e
}

func maxOutputOf(n int) int {
	if n == 0 {
		return defaultMaxOutput
	}
	return n
}
