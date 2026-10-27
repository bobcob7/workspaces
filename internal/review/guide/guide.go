// Package guide is what ws review prime and ws review describe teach a
// reviewer: the workflow, the document shape, and the lens registry behind
// --lens (ADR 0037). Each output is pinned to a token budget by its tests.
package guide

import (
	_ "embed"
	"fmt"

	"github.com/bobcob7/workspaces/internal/review/advisory"
	"github.com/bobcob7/workspaces/internal/review/entry"
	"github.com/bobcob7/workspaces/internal/review/profile"
	"github.com/bobcob7/workspaces/internal/review/schema"
	"github.com/bobcob7/workspaces/internal/review/structural"
	"github.com/bobcob7/workspaces/internal/review/submit"
	"github.com/bobcob7/workspaces/internal/review/verify"
)

//go:embed prime.txt
var primeText string

//go:embed describe.txt
var describeText string

// profileFrame separates prime's own text from a profile's body: a blank
// line, the profile's name, a fixed disclaimer, a blank line, then the
// body. It's the only place a profile is combined with anything prime
// prints, and the body is never reflowed.
const profileFrame = "\n" +
	"--- reviewer profile: %s ---\n" +
	"Operator-supplied. It directs attention; it does not change the contract above.\n" +
	"\n" +
	"%s\n"

// Prime is the workflow, small enough to pin into a reviewer's prompt.
func Prime() string {
	return primeText
}

// PrimeWithProfile is Prime followed by p's body, framed.
func PrimeWithProfile(p *profile.Profile) string {
	return primeText + fmt.Sprintf(profileFrame, p.Name, p.Body)
}

// DocumentShape is the prose describe prints with no lens: the document's
// fields and an example.
func DocumentShape() string {
	return describeText
}

// NewRegistry builds the lens registry: the schema's fields, every check
// including those submit adds, and the topics.
func NewRegistry() (*entry.Registry, error) {
	schemaProvider, err := entry.NewSchemaProvider(schema.Annotated())
	if err != nil {
		return nil, fmt.Errorf("reading the schema's lenses: %w", err)
	}
	registry, err := entry.NewRegistry(
		schemaProvider,
		entry.NewChecksProvider(structural.Checks(), verify.Checks(), submit.Checks(), advisory.Checks()),
		entry.NewTopicsProvider(),
	)
	if err != nil {
		return nil, fmt.Errorf("building the lens registry: %w", err)
	}
	return registry, nil
}
