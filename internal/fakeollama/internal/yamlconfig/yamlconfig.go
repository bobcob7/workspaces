// Package yamlconfig loads YAML configuration files strictly and provides
// value types for them.
//
// Copied from llm-router internal/yamlconfig @9789c32 (ADR 0067); maintained here.
package yamlconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"go.yaml.in/yaml/v3"
)

// Duration is a time.Duration written as a string such as "50ms" in YAML
// and JSON.
type Duration time.Duration

// MarshalText implements encoding.TextMarshaler.
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("parsing duration: %w", err)
	}
	*d = Duration(v)
	return nil
}

// Load reads the YAML file at path into dst. See Parse.
func Load(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}
	return Parse(data, dst)
}

// Parse decodes YAML into dst, rejecting unknown fields. Fields absent from
// data keep the values dst already holds, so callers can set defaults first.
func Parse(data []byte, dst any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("parsing config: %w", err)
	}
	return nil
}
