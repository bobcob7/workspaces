package mcp

import (
	"path"
	"slices"
)

// ToolInfo is one tool a server offers.
type ToolInfo struct {
	Server, Tool, Name, Description string // Name = ToolName(Server, Tool)
	Schema                          map[string]any
	ReadOnly, Destructive           bool // annotations
}

// Filter keeps tools matching allow (exact names or path.Match globs on
// Name), minus deny; a tool that isn't ReadOnly, or is Destructive, only
// when allow names it exactly. Order: allow's order, then the server's.
func Filter(tools []ToolInfo, allow, deny []string) []ToolInfo {
	picked := make([]bool, len(tools))
	var out []ToolInfo
	for _, pattern := range allow {
		for i := range tools {
			if picked[i] || !allows(pattern, &tools[i]) {
				continue
			}
			picked[i] = true
			out = append(out, tools[i])
		}
	}
	return slices.DeleteFunc(out, func(tool ToolInfo) bool {
		return slices.ContainsFunc(deny, func(pattern string) bool {
			return matches(pattern, tool.Name)
		})
	})
}

func allows(pattern string, tool *ToolInfo) bool {
	if pattern == tool.Name {
		return true
	}
	if !tool.ReadOnly || tool.Destructive {
		return false
	}
	return matches(pattern, tool.Name)
}

func matches(pattern, name string) bool {
	if pattern == name {
		return true
	}
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}
