// Package mcp is the runner's MCP client (ADR 0069).
package mcp

import (
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	json "encoding/json/v2"
)

const (
	sourceProject = "project"
	sourceUser    = "user"
	typeStdio     = "stdio"
	typeHTTP      = "http"
	typeSSE       = "sse"
)

// ServerDef is one server definition, Claude Code's shape.
type ServerDef struct {
	Name    string
	Source  string // "project" or "user"
	Type    string // "stdio" (default when Command is set), "http", "sse"; others skipped
	Command string
	Args    []string
	Env     map[string]string
	URL     string
	Headers map[string]string
}

type serverJSON struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	OAuth   jsontext.Value    `json:"oauth"`
}

type configFile struct {
	MCPServers map[string]serverJSON `json:"mcpServers"` //nolint:tagliatelle // Claude Code's file format
}

// ClaudeConfigPath is $CLAUDE_CONFIG_DIR/.claude.json when set, else
// $HOME/.claude.json, or "" when neither is set.
func ClaudeConfigPath(environ []string) string {
	if dir := lookup(environ, "CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	home := userHome(environ)
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".claude.json")
}

// LoadDefs reads <repoDir>/.mcp.json (project) and claudeJSON's top-level
// mcpServers (user); project wins on a name. Unsupported types (ws, and
// definitions with an "oauth" block) are left out with a warning. Missing
// files are fine; an unparsable one is an error naming the file.
func LoadDefs(repoDir, claudeJSON string) (map[string]ServerDef, []string, error) {
	projectServers, err := readServers(filepath.Join(repoDir, ".mcp.json"))
	if err != nil {
		return nil, nil, err
	}
	userServers, err := readServers(claudeJSON)
	if err != nil {
		return nil, nil, err
	}
	defs := make(map[string]ServerDef)
	warnings := addDefs(defs, nil, projectServers, sourceProject, nil)
	warnings = addDefs(defs, warnings, userServers, sourceUser, projectServers)
	return defs, warnings, nil
}

// Select returns the defs named in names, in that order, each once. A name
// with no def is a warning, not an error.
func Select(defs map[string]ServerDef, names []string) ([]ServerDef, []string) {
	var selected []ServerDef
	var warnings []string
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		def, ok := defs[name]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("unknown MCP server %q", name))
			continue
		}
		selected = append(selected, def)
	}
	return selected, warnings
}

// readServers returns the mcpServers of the JSON file at path, or nothing
// when the file doesn't exist or path is empty.
func readServers(path string) (map[string]serverJSON, error) {
	var file configFile
	if path == "" {
		return file.MCPServers, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return file.MCPServers, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	// The parser's message can quote a byte of the file, so it isn't wrapped.
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parsing %s: invalid JSON", path)
	}
	return file.MCPServers, nil
}

// addDefs adds the supported servers to defs in name order and returns the
// warnings with one line for each skipped server. A name in shadow is left
// out, because a higher-precedence source already claims it.
func addDefs(defs map[string]ServerDef, warnings []string, servers map[string]serverJSON, source string, shadow map[string]serverJSON) []string {
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		if _, claimed := shadow[name]; claimed {
			continue
		}
		s := servers[name]
		def, warning := newDef(name, source, &s)
		if warning != "" {
			warnings = append(warnings, warning)
			continue
		}
		defs[name] = def
	}
	return warnings
}

// newDef converts one JSON definition, or returns a warning saying why it is
// skipped.
func newDef(name, source string, s *serverJSON) (ServerDef, string) {
	typ := s.Type
	if typ == "" && s.Command != "" {
		typ = typeStdio
	}
	if hasOAuth(s.OAuth) {
		return ServerDef{}, fmt.Sprintf("skipping %s MCP server %q: OAuth is not supported", source, name)
	}
	switch typ {
	case typeStdio:
		if s.Command == "" {
			return ServerDef{}, fmt.Sprintf("skipping %s MCP server %q: no command", source, name)
		}
	case typeHTTP, typeSSE:
	default:
		return ServerDef{}, fmt.Sprintf("skipping %s MCP server %q: unsupported type %q", source, name, typ)
	}
	return ServerDef{
		Name:    name,
		Source:  source,
		Type:    typ,
		Command: s.Command,
		Args:    s.Args,
		Env:     s.Env,
		URL:     s.URL,
		Headers: s.Headers,
	}, ""
}

func hasOAuth(raw jsontext.Value) bool {
	return len(raw) > 0 && string(raw) != "null"
}

func lookup(environ []string, key string) string {
	prefix := key + "="
	for _, kv := range environ {
		if value, ok := strings.CutPrefix(kv, prefix); ok {
			return value
		}
	}
	return ""
}
