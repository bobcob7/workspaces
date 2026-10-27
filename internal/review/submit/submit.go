// Package submit checks one review document submitted from a review
// workspace: the forked checks, plus what the workspace's record makes
// checkable (ADRs 0037 and 0039). ref may be left out and is filled in from
// the record; if given it must match. profile must be one of the record's.
package submit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/bobcob7/workspaces/internal/review"
	"github.com/bobcob7/workspaces/internal/review/advisory"
	"github.com/bobcob7/workspaces/internal/review/record"
	"github.com/bobcob7/workspaces/internal/review/structural"
	"github.com/bobcob7/workspaces/internal/review/validate"
	"github.com/bobcob7/workspaces/internal/review/verify"
)

// Status is what became of a submission.
type Status string

const (
	// StatusValid is a review to store.
	StatusValid Status = "valid"
	// StatusInvalid is a review to revise and resubmit: review_invalid.
	StatusInvalid Status = "invalid"
	// StatusUnverifiable is a review whose anchors couldn't be checked: the
	// working tree diverged or the commit is missing. Revising it won't
	// help: review_unverifiable.
	StatusUnverifiable Status = "unverifiable"
)

// checkDocumentUnparseable is the check an input that isn't a JSON object
// reports under.
const checkDocumentUnparseable = "document-unparseable"

// Outcome is one checked submission.
type Outcome struct {
	Status Status
	Result *review.Result
	// Profile is the document's profile, empty when it has none.
	Profile string
	// Document is what to store: the source as given, or with ref filled
	// in when it was left out.
	Document []byte
}

// validator runs the forked check tiers.
type validator interface {
	Validate(ctx context.Context, source []byte, options validate.Options) (*review.Result, error)
}

// Checker checks submissions.
type Checker struct {
	validator validator
}

// New returns a Checker running validator.
func New(v validator) *Checker {
	return &Checker{validator: v}
}

// Check checks source, submitted for rec's review, verifying its anchors
// against the repository in repoDir.
func (c *Checker) Check(ctx context.Context, rec *record.Record, repoDir string, source []byte) (Outcome, error) {
	doc, err := review.Parse(source)
	if err != nil {
		if !review.IsDocumentError(err) {
			return Outcome{}, err
		}
		return Outcome{Status: StatusInvalid, Result: unparseable(err)}, nil
	}
	own := recordDiagnostics(doc, rec)
	if !doc.Ref.Present {
		if source, err = withRef(source, rec.Ref); err != nil {
			return Outcome{}, err
		}
	}
	result, err := c.validator.Validate(ctx, source, validate.Options{Dir: repoDir})
	if err != nil {
		return Outcome{}, fmt.Errorf("checking the review: %w", err)
	}
	if !result.Precondition && len(own) > 0 {
		result.Diagnostics = append(result.Diagnostics, own...)
		review.SortDiagnostics(result.Diagnostics)
		result.Valid = false
	}
	profile := ""
	if doc.Profile.OK {
		profile = doc.Profile.Value
	}
	return Outcome{Status: status(result, len(own) > 0), Result: result, Profile: profile, Document: source}, nil
}

// recordDiagnostics are the checks only the record makes possible.
func recordDiagnostics(doc *review.Document, rec *record.Record) []review.Diagnostic {
	var out []review.Diagnostic
	if doc.Ref.OK && doc.Ref.Value != rec.Ref {
		out = append(out, review.Diagnostic{
			Severity: review.SeverityError,
			Name:     checkRef,
			Path:     "/ref",
			Message:  fmt.Sprintf("ref %s isn't %s, the commit this review workspace reviews; leave ref out", doc.Ref.Value, rec.Ref),
		})
	}
	switch {
	case !doc.Profile.Present:
		out = append(out, review.Diagnostic{
			Severity: review.SeverityError,
			Name:     checkProfile,
			Message:  "profile is required; this review's profiles are " + quoted(rec.ProfileNames()),
		})
	case doc.Profile.OK && !rec.HasProfile(doc.Profile.Value):
		out = append(out, review.Diagnostic{
			Severity: review.SeverityError,
			Name:     checkProfile,
			Path:     "/profile",
			Message:  fmt.Sprintf("profile %q isn't one of this review's profiles: %s", doc.Profile.Value, quoted(rec.ProfileNames())),
		})
	}
	return out
}

// status classifies a result. A diverged working tree, a commit the
// repository doesn't have, and anchors git couldn't check are
// unverifiable, unless the document is also wrong about the record, which
// is the reviewer's to fix first.
func status(result *review.Result, recordWrong bool) Status {
	if result.Precondition {
		return StatusUnverifiable
	}
	if result.Valid {
		return StatusValid
	}
	if recordWrong {
		return StatusInvalid
	}
	for _, d := range result.Diagnostics {
		if d.Name == "ref-unknown" || d.Name == "verification-required" {
			return StatusUnverifiable
		}
	}
	return StatusInvalid
}

// withRef returns source with ref set to commit. source is a JSON object
// without ref; numbers are kept exactly as written.
func withRef(source []byte, commit string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(source))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, fmt.Errorf("filling in ref: %w", err)
	}
	obj["ref"] = commit
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("filling in ref: %w", err)
	}
	return out, nil
}

// unparseable is the result for an input that isn't a JSON object: one
// diagnostic, and every other check reported as skipped rather than left
// out, so silence isn't read as checks passing.
func unparseable(err error) *review.Result {
	const reason = "the input is not a review document"
	result := &review.Result{
		Diagnostics: []review.Diagnostic{{
			Severity: review.SeverityError,
			Name:     checkDocumentUnparseable,
			Message:  err.Error(),
		}},
		Verification: review.Verification{Source: "unavailable", Reason: reason},
	}
	for _, tier := range [][]review.Check{structural.Checks(), verify.Checks(), Checks(), advisory.Checks()} {
		for _, check := range tier {
			if check.Name != checkDocumentUnparseable {
				result.Skipped = append(result.Skipped, review.Skipped{Name: check.Name, Reason: reason})
			}
		}
	}
	return result
}

func quoted(names []string) string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, strconv.Quote(name))
	}
	return strings.Join(out, ", ")
}
