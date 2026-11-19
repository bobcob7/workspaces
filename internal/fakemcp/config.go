// Package fakemcp is a scripted MCP server for tests (ADR 0067). It is
// never shipped.
package fakemcp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"time"

	"go.yaml.in/yaml/v3"
)

// Config is a fake-mcp YAML file (docs/design/ollama-agent/fake-mcp).
type Config struct {
	Server        ServerInfo        `yaml:"server"`         // initialize's serverInfo and instructions
	RequireEnv    map[string]string `yaml:"require_env"`    // stdio: initialize fails unless each is set to the value
	RequireHeader map[string]string `yaml:"require_header"` // http: each request must carry each header with the value
	Faults        Faults            `yaml:"faults"`
	Tools         []Tool            `yaml:"tools"`
	Generate      *Generate         `yaml:"generate"`
}

// ServerInfo is initialize's serverInfo and instructions.
type ServerInfo struct {
	Name         string `yaml:"name"`    // default "fake"
	Version      string `yaml:"version"` // default "0.0.0-fake"
	Instructions string `yaml:"instructions"`
}

// Faults script failures in the server.
type Faults struct {
	SlowInitialize  time.Duration `yaml:"slow_initialize"`
	CrashAfterCalls int           `yaml:"crash_after_calls"` // exit(3) after this many calls; 0 = never
	SlowCall        time.Duration `yaml:"slow_call"`
}

// Tool is a scripted tool.
type Tool struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	InputSchema map[string]any `yaml:"input_schema"` // default {"type":"object"}
	Annotations Annotations    `yaml:"annotations"`
	Replies     []Reply        `yaml:"replies"`
}

// Annotations are the tool's MCP hints.
type Annotations struct {
	ReadOnlyHint    *bool `yaml:"readOnlyHint"`    //nolint:tagliatelle // MCP's hint names, as the fake-mcp configs spell them.
	DestructiveHint *bool `yaml:"destructiveHint"` //nolint:tagliatelle // MCP's hint names, as the fake-mcp configs spell them.
}

// Reply is one scripted answer. The first whose Match (a regexp on the canonical JSON arguments;
// empty matches all) matches is sent.
type Reply struct {
	Match         string `yaml:"match"`
	Text          string `yaml:"text"`           // a text result (Go template over .Args)
	Error         string `yaml:"error"`          // an isError result
	ProtocolError string `yaml:"protocol_error"` // a JSON-RPC error
}

// Generate adds filler tools to imitate a large server.
type Generate struct {
	Count  int    `yaml:"count"`
	Prefix string `yaml:"prefix"` // tools <prefix>001…, read-only, one-line description, replying "ok"
}

// LoadConfig reads a fake-mcp YAML file. Unknown fields are an error.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading config: %w", err)
	}
	return parseConfig(data)
}

// CheckEnv is RequireEnv against getenv; the error names the variable, never a value.
func CheckEnv(cfg Config, getenv func(string) string) error {
	for _, name := range slices.Sorted(maps.Keys(cfg.RequireEnv)) {
		if getenv(name) != cfg.RequireEnv[name] {
			return fmt.Errorf("environment variable %s is missing or wrong", name)
		}
	}
	return nil
}

func parseConfig(data []byte) (Config, error) {
	cfg := Config{Server: ServerInfo{Name: "fake", Version: "0.0.0-fake"}}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("parsing config: %w", err)
	}
	applyDefaults(&cfg)
	return cfg, nil
}

func applyDefaults(cfg *Config) {
	for i := range cfg.Tools {
		if cfg.Tools[i].InputSchema == nil {
			cfg.Tools[i].InputSchema = map[string]any{"type": "object"}
		}
	}
}
