package agent

import (
	_ "embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed preamble.md
var preambleText string

// PreambleData fills the system prompt (README, "Preamble").
type PreambleData struct {
	Dir                    string
	StepsLeft, MinutesLeft int
	Verdicts               string
	MCP                    bool     // MCP tools are offered
	Deferred               string   // "server: a, b, c" lines, ≤ 2 KiB (T12 fills it)
	Unavailable            []string // MCP servers that failed to start
	Instructions           string
}

// Preamble renders the system prompt.
//
//nolint:gocritic // the signature is the spec; the data is filled once here
func Preamble(d PreambleData) (string, error) {
	tmpl, err := template.New("preamble").Parse(preambleText)
	if err != nil {
		return "", fmt.Errorf("parsing preamble: %w", err)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, d); err != nil {
		return "", fmt.Errorf("rendering preamble: %w", err)
	}
	return b.String(), nil
}
