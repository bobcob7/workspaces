package agent

import (
	"context"
	"fmt"
	"slices"

	"github.com/bobcob7/workspaces/internal/agent/tools"
	"github.com/bobcob7/workspaces/internal/llm"
)

// StaticToolset offers ts unchanged.
func StaticToolset(ts []tools.Tool) Toolset {
	return staticToolset{items: slices.Clone(ts)}
}

type staticToolset struct {
	items []tools.Tool
}

func (s staticToolset) Definitions() []llm.Tool {
	defs := make([]llm.Tool, 0, len(s.items))
	for _, t := range s.items {
		defs = append(defs, llm.Tool{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}
	return defs
}

func (s staticToolset) Call(ctx context.Context, name, arguments string) (tools.Result, error) {
	t, ok := s.find(name)
	if !ok {
		return tools.Result{}, fmt.Errorf("unknown tool %q", name)
	}
	return t.Call(ctx, arguments)
}

func (s staticToolset) Known(name string) bool {
	_, ok := s.find(name)
	return ok
}

func (s staticToolset) find(name string) (tools.Tool, bool) {
	for _, t := range s.items {
		if t.Name == name {
			return t, true
		}
	}
	return tools.Tool{}, false
}
