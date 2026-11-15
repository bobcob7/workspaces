package tools

import (
	"slices"
	"strings"
)

// Environ is environ without secrets (ADR 0066): names matching
// WS_ENDPOINT_TOKEN_*, WS_FORGEJO_TOKEN_*, *_API_KEY, *_TOKEN, *_SECRET,
// *_PASSWORD, OTEL_EXPORTER_OTLP_HEADERS, OTEL_EXPORTER_OTLP_*_HEADERS, or
// in secretNames (the names in secrets.env), except
// GH_TOKEN and GITHUB_TOKEN when keepGit; WS_RUNNER=ws is set.
func Environ(environ []string, keepGit bool, secretNames []string) []string {
	kept := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		if !isSecretEntry(kv, keepGit, secretNames) && !hasName(kv, "WS_RUNNER") {
			kept = append(kept, kv)
		}
	}
	return append(kept, "WS_RUNNER=ws")
}

// Secrets is the values Environ removed, for the redactor.
func Secrets(environ []string, keepGit bool, secretNames []string) []string {
	var values []string
	for _, kv := range environ {
		if isSecretEntry(kv, keepGit, secretNames) {
			_, value, _ := strings.Cut(kv, "=")
			values = append(values, value)
		}
	}
	return values
}

func isSecretEntry(kv string, keepGit bool, secretNames []string) bool {
	name, _, _ := strings.Cut(kv, "=")
	if keepGit && (name == "GH_TOKEN" || name == "GITHUB_TOKEN") {
		return false
	}
	return slices.Contains(secretNames, name) || isSecretName(name)
}

// isSecretName reports whether name matches one of the patterns of ADR 0066.
func isSecretName(name string) bool {
	switch {
	case strings.HasPrefix(name, "WS_ENDPOINT_TOKEN_"), strings.HasPrefix(name, "WS_FORGEJO_TOKEN_"):
		return true
	case name == "OTEL_EXPORTER_OTLP_HEADERS":
		return true
	case strings.HasPrefix(name, "OTEL_EXPORTER_OTLP_") && strings.HasSuffix(name, "_HEADERS"):
		return true
	}
	for _, suffix := range []string{"_API_KEY", "_TOKEN", "_SECRET", "_PASSWORD"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func hasName(kv, name string) bool {
	entry, _, _ := strings.Cut(kv, "=")
	return entry == name
}
