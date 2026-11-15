package mcp

import (
	"slices"
	"strings"
	"unicode"
)

// Search ranks tools by case-insensitive word overlap of query with Name
// (split on _ and -), then Description; ties keep input order; at most n.
func Search(tools []ToolInfo, query string, n int) []ToolInfo {
	words := slices.Compact(slices.Sorted(slices.Values(wordsOf(query))))
	type scored struct {
		tool        ToolInfo
		name, descr int
	}
	var hits []scored
	for _, tool := range tools {
		s := scored{
			tool:  tool,
			name:  overlap(words, wordsOf(tool.Name)),
			descr: overlap(words, wordsOf(tool.Description)),
		}
		if s.name+s.descr > 0 {
			hits = append(hits, s)
		}
	}
	slices.SortStableFunc(hits, func(a, b scored) int {
		if a.name != b.name {
			return b.name - a.name
		}
		return b.descr - a.descr
	})
	out := make([]ToolInfo, 0, min(n, len(hits)))
	for _, h := range hits[:min(n, len(hits))] {
		out = append(out, h.tool)
	}
	return out
}

func wordsOf(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// overlap counts the distinct query words that appear among the words.
func overlap(query, words []string) int {
	count := 0
	for _, q := range query {
		if slices.Contains(words, q) {
			count++
		}
	}
	return count
}
