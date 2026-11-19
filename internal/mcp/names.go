package mcp

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

const (
	maxToolNameLen = 64
	toolNameKeep   = 55
)

// ToolName is mcp__<server>__<tool>, characters outside [A-Za-z0-9_-]
// replaced by "_"; over 64 characters: the first 55, "_", and 8 hex digits
// of SHA-256 of the full name.
func ToolName(server, tool string) string {
	name := strings.Map(replaceNonNameRune, "mcp__"+server+"__"+tool)
	if len(name) <= maxToolNameLen {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	return fmt.Sprintf("%s_%x", name[:toolNameKeep], sum[:4])
}

func replaceNonNameRune(r rune) rune {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		return r
	default:
		return '_'
	}
}
