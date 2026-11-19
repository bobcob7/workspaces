package mcp

import (
	"fmt"
	"strings"
)

// UnsetVarError is a ${VAR} with no value in the environment and no default.
// Its message names the variable, never a value.
type UnsetVarError struct{ Server, Var string }

func (e *UnsetVarError) Error() string {
	return fmt.Sprintf("MCP server %q: environment variable %s is not set", e.Server, e.Var)
}

// Expand applies ${VAR} and ${VAR:-default} to Command, Args, Env, URL and
// Headers from getenv. It returns the expanded def and every value it
// substituted plus every literal Headers value (for the redactor). An
// unset ${VAR} without a default is *UnsetVarError naming VAR.
//
//nolint:gocritic // ADR 0069 fixes the by-value signature
func Expand(d ServerDef, getenv func(string) (string, bool)) (ServerDef, []string, error) {
	x := &expander{server: d.Name, getenv: getenv}
	out, err := x.def(&d)
	if err != nil {
		return ServerDef{}, nil, err
	}
	for _, value := range out.Headers {
		x.keep(value)
	}
	return out, x.secrets, nil
}

type expander struct {
	server  string
	getenv  func(string) (string, bool)
	secrets []string
}

func (x *expander) def(d *ServerDef) (ServerDef, error) {
	out := *d
	var err error
	if out.Command, err = x.expand(d.Command); err != nil {
		return ServerDef{}, err
	}
	if out.Args, err = x.expandAll(d.Args); err != nil {
		return ServerDef{}, err
	}
	if out.Env, err = x.expandValues(d.Env); err != nil {
		return ServerDef{}, err
	}
	if out.URL, err = x.expand(d.URL); err != nil {
		return ServerDef{}, err
	}
	if out.Headers, err = x.expandValues(d.Headers); err != nil {
		return ServerDef{}, err
	}
	return out, nil
}

func (x *expander) expandAll(values []string) ([]string, error) {
	out := make([]string, len(values))
	for i, v := range values {
		expanded, err := x.expand(v)
		if err != nil {
			return nil, err
		}
		out[i] = expanded
	}
	return out, nil
}

func (x *expander) expandValues(values map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(values))
	for k, v := range values {
		expanded, err := x.expand(v)
		if err != nil {
			return nil, err
		}
		out[k] = expanded
	}
	return out, nil
}

// expand replaces each ${...} in s. An unclosed "${" is left as written.
func (x *expander) expand(s string) (string, error) {
	var out strings.Builder
	for {
		start := strings.Index(s, "${")
		if start < 0 {
			break
		}
		end := strings.IndexByte(s[start:], '}')
		if end < 0 {
			break
		}
		end += start
		out.WriteString(s[:start])
		value, err := x.reference(s[start+2 : end])
		if err != nil {
			return "", err
		}
		out.WriteString(value)
		s = s[end+1:]
	}
	out.WriteString(s)
	return out.String(), nil
}

// reference resolves the inside of ${...}: NAME or NAME:-default. An empty
// variable uses the default; a variable that is set but empty, with no
// default, is empty.
func (x *expander) reference(expr string) (string, error) {
	name, fallback, hasDefault := strings.Cut(expr, ":-")
	value, set := x.getenv(name)
	if set && value != "" {
		x.keep(value)
		return value, nil
	}
	if hasDefault {
		x.keep(fallback)
		return fallback, nil
	}
	if !set {
		return "", &UnsetVarError{Server: x.server, Var: name}
	}
	return "", nil
}

func (x *expander) keep(value string) {
	if value != "" {
		x.secrets = append(x.secrets, value)
	}
}
