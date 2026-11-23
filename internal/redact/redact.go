// Package redact removes secrets from text an environment agent returns (ADR 0078).
package redact

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"
)

// Marker replaces each secret.
const Marker = "[redacted]"

// minValue is the shortest exact value redacted.
const minValue = 8

// ErrInvalid is returned by JSON for input that isn't JSON.
var ErrInvalid = errors.New("redact: not JSON")

// pattern is a token regexp and the replacement for each match.
type pattern struct {
	re          *regexp.Regexp
	keepsPrefix bool // group 1 stays and only the rest is replaced
}

// Redactor replaces exact secret values and well-known token patterns.
// Immutable after New; safe for concurrent use.
type Redactor struct {
	values   []string // longest first
	patterns []pattern
}

// New returns a Redactor for the given exact values (those shorter than 8 bytes are ignored).
func New(values ...string) *Redactor {
	kept := make([]string, 0, len(values))
	for _, v := range values {
		if len(v) >= minValue && !slices.Contains(kept, v) {
			kept = append(kept, v)
		}
	}
	slices.SortStableFunc(kept, func(a, b string) int { return len(b) - len(a) })
	return &Redactor{values: kept, patterns: compilePatterns()}
}

func compilePatterns() []pattern {
	specs := []struct {
		expr        string
		keepsPrefix bool
	}{
		{`gh[pousr]_[A-Za-z0-9]{36,255}`, false},
		{`github_pat_[A-Za-z0-9_]{22,255}`, false},
		{`sk-ant-[A-Za-z0-9_-]{20,}`, false},
		{`glpat-[A-Za-z0-9_-]{20,}`, false},
		{`xox[abprs]-[A-Za-z0-9-]{10,}`, false},
		{`AKIA[0-9A-Z]{16}`, false},
		{`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`, false},
		{`(?i)(authorization:\s*(?:bearer|token)\s+)[A-Za-z0-9._~+/=-]{8,}`, true},
	}
	patterns := make([]pattern, len(specs))
	for i, s := range specs {
		patterns[i] = pattern{re: regexp.MustCompile(s.expr), keepsPrefix: s.keepsPrefix}
	}
	return patterns
}

// SecretValues returns the values to redact from secrets (secrets.env's
// NAME=value pairs) and environ: every secrets value, and every environ
// value whose name is GH_TOKEN, GITHUB_TOKEN or CLAUDE_CODE_OAUTH_TOKEN,
// starts with WS_FORGEJO_TOKEN_ or WS_ENDPOINT_TOKEN_, or ends with
// _TOKEN, _SECRET, _API_KEY or _PASSWORD.
func SecretValues(secrets map[string]string, environ []string) []string {
	var out []string
	add := func(v string) {
		if v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	for _, v := range secrets {
		add(v)
	}
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if ok && secretName(name) {
			add(value)
		}
	}
	slices.Sort(out)
	return out
}

func secretName(name string) bool {
	switch name {
	case "GH_TOKEN", "GITHUB_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN":
		return true
	}
	for _, p := range []string{"WS_FORGEJO_TOKEN_", "WS_ENDPOINT_TOKEN_"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	for _, s := range []string{"_TOKEN", "_SECRET", "_API_KEY", "_PASSWORD"} {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

// String returns s with every secret replaced, and how many were.
func (r *Redactor) String(s string) (string, int) {
	s, count := r.exact(s)
	for _, p := range r.patterns {
		var n int
		s, n = p.apply(s)
		count += n
	}
	return s, count
}

// authHeader is the trailing header name a value may swallow.
const authHeader = "authorization"

// apply replaces each match of the pattern in s.
//
// The Authorization value class includes "=", so "Authorization: Bearer x=Authorization: Bearer y"
// would swallow the second header's name and leave y. A trailing header
// name is therefore handed back and scanned again.
func (p pattern) apply(s string) (string, int) {
	var b strings.Builder
	count := 0
	for {
		loc := p.re.FindStringSubmatchIndex(s)
		if loc == nil {
			break
		}
		keep, end := loc[0], loc[1]
		if p.keepsPrefix {
			keep = loc[3]
			if end-keep >= len(authHeader)+minValue && strings.EqualFold(s[end-len(authHeader):end], authHeader) {
				end -= len(authHeader)
			}
		}
		b.WriteString(s[:keep])
		b.WriteString(Marker)
		s = s[end:]
		count++
	}
	if count == 0 {
		return s, 0
	}
	b.WriteString(s)
	return b.String(), count
}

type span struct{ start, end int }

// exact replaces every occurrence of every value in one pass, so a
// replacement is never searched again and overlapping occurrences merge.
func (r *Redactor) exact(s string) (string, int) {
	var spans []span
	for _, v := range r.values {
		for from := 0; ; {
			i := strings.Index(s[from:], v)
			if i < 0 {
				break
			}
			spans = append(spans, span{from + i, from + i + len(v)})
			from += i + 1
		}
	}
	if len(spans) == 0 {
		return s, 0
	}
	slices.SortFunc(spans, func(a, b span) int { return a.start - b.start })
	var b strings.Builder
	count, pos := 0, 0
	cur := spans[0]
	flush := func() {
		b.WriteString(s[pos:cur.start])
		b.WriteString(Marker)
		pos = cur.end
		count++
	}
	for _, sp := range spans[1:] {
		if sp.start <= cur.end {
			cur.end = max(cur.end, sp.end)
			continue
		}
		flush()
		cur = sp
	}
	flush()
	b.WriteString(s[pos:])
	return b.String(), count
}

// JSON returns raw with every string value (not keys) redacted, re-encoded
// compactly, and the count. raw must be valid JSON (ErrInvalid otherwise).
func (r *Redactor) JSON(raw []byte) ([]byte, int, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out bytes.Buffer
	count := 0
	if err := r.walk(dec, &out, &count); err != nil {
		return nil, 0, ErrInvalid
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, 0, ErrInvalid
	}
	return out.Bytes(), count, nil
}

// walk copies one JSON value from dec to out, redacting string values.
func (r *Redactor) walk(dec *json.Decoder, out *bytes.Buffer, count *int) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch t := tok.(type) {
	case json.Delim:
		return r.walkContainer(dec, out, count, t)
	case string:
		s, n := r.String(t)
		*count += n
		return writeString(out, s)
	case json.Number:
		out.WriteString(t.String())
	case bool:
		out.WriteString(boolText(t))
	case nil:
		out.WriteString("null")
	}
	return nil
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func (r *Redactor) walkContainer(dec *json.Decoder, out *bytes.Buffer, count *int, open json.Delim) error {
	isObject := open == '{'
	out.WriteString(open.String())
	for first := true; dec.More(); first = false {
		if !first {
			out.WriteByte(',')
		}
		if isObject {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return ErrInvalid
			}
			if err := writeString(out, name); err != nil {
				return err
			}
			out.WriteByte(':')
		}
		if err := r.walk(dec, out, count); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	if isObject {
		out.WriteByte('}')
	} else {
		out.WriteByte(']')
	}
	return nil
}

// writeString writes s as a JSON string without HTML escaping.
func writeString(out *bytes.Buffer, s string) error {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return err
	}
	out.Truncate(out.Len() - 1)
	return nil
}
