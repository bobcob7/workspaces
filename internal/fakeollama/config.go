package fakeollama

import (
	"errors"
	"fmt"
	"slices"

	"github.com/bobcob7/workspaces/internal/fakeollama/internal/yamlconfig"
)

// Capability is a feature a fake model supports.
type Capability string

// Capabilities a model can be configured with.
const (
	CapabilityTools     Capability = "tools"
	CapabilityReasoning Capability = "reasoning"
)

const (
	defaultListen        = ":11434"
	defaultControlListen = ":11435"
	ephemeralListen      = "127.0.0.1:0"
)

var errInvalidConfig = errors.New("invalid config")

// Config configures a Server.
type Config struct {
	// Listen is the API address. Empty means an ephemeral loopback port.
	Listen string `yaml:"listen"`
	// ControlListen is the control API address. Empty means an ephemeral
	// loopback port.
	ControlListen string  `yaml:"control_listen"`
	Models        []Model `yaml:"models"`
	Timing        Timing  `yaml:"timing"`
	Rules         []Rule  `yaml:"rules"`
	// Faults are active from startup.
	Faults []Fault `yaml:"faults"`
}

// Model is a model the fake serves.
type Model struct {
	Name         string       `yaml:"name"`
	Capabilities []Capability `yaml:"capabilities"`
}

func (m Model) has(c Capability) bool {
	return slices.Contains(m.Capabilities, c)
}

// Timing controls how fast replies are generated.
type Timing struct {
	// FirstToken is the delay before the first chunk.
	FirstToken yamlconfig.Duration `yaml:"first_token"`
	// ChunkInterval is the delay between chunks.
	ChunkInterval yamlconfig.Duration `yaml:"chunk_interval"`
	// WordsPerChunk is the number of words per streamed chunk. Defaults to 1.
	WordsPerChunk int `yaml:"words_per_chunk"`
}

// Rule replies to user messages matching a pattern, either with text or by
// calling a tool. Set exactly one of Reply and Tool.
type Rule struct {
	// Match is a regular expression matched against the last user message.
	Match string `yaml:"match"`
	// Reply is a text/template producing the reply. See templateData for the
	// fields available to it.
	Reply string `yaml:"reply"`
	// Tool names a tool to call. The rule only applies when the request
	// offers the tool and the model has the tools capability; otherwise the
	// next matching rule is tried.
	Tool string `yaml:"tool"`
	// Arguments is a text/template producing the tool call's JSON arguments,
	// e.g. {"order_id":"{{ index .Match 1 }}"}. Empty fills the tool's
	// required parameters from its schema.
	Arguments string `yaml:"arguments"`
}

// LoadConfig reads a YAML config file. See ParseConfig.
func LoadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	if err := yamlconfig.Load(path, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// ParseConfig parses a YAML config, rejecting unknown fields. Listen
// addresses default to Ollama's port and the port after it. The config is
// validated by New.
func ParseConfig(data []byte) (Config, error) {
	cfg := defaultConfig()
	if err := yamlconfig.Parse(data, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func defaultConfig() Config {
	return Config{Listen: defaultListen, ControlListen: defaultControlListen}
}

func validateModels(models []Model) error {
	if len(models) == 0 {
		return fmt.Errorf("%w: at least one model is required", errInvalidConfig)
	}
	seen := make(map[string]bool, len(models))
	for _, m := range models {
		if m.Name == "" {
			return fmt.Errorf("%w: model name is required", errInvalidConfig)
		}
		if seen[m.Name] {
			return fmt.Errorf("%w: duplicate model %q", errInvalidConfig, m.Name)
		}
		seen[m.Name] = true
		for _, c := range m.Capabilities {
			if c != CapabilityTools && c != CapabilityReasoning {
				return fmt.Errorf("%w: model %q: unknown capability %q", errInvalidConfig, m.Name, c)
			}
		}
	}
	return nil
}

func validateTiming(t Timing) error {
	if t.FirstToken < 0 || t.ChunkInterval < 0 || t.WordsPerChunk < 0 {
		return fmt.Errorf("%w: timing values must not be negative", errInvalidConfig)
	}
	return nil
}
