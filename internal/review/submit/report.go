package submit

import "github.com/bobcob7/workspaces/internal/review"

// Report is a checked submission as ws prints it (ADR 0016): every field
// present, null when it has no value.
type Report struct {
	Valid        bool         `json:"valid"`
	Verification Verification `json:"verification"`
	Counts       Counts       `json:"counts"`
	Diagnostics  []Diagnostic `json:"diagnostics"`
	Skipped      []Skipped    `json:"skipped"`
	// Lenses are the names to open with ws review describe --lens, one per
	// check that fired.
	Lenses []string `json:"lenses"`
}

// Verification says whether the anchors were checked, and against what.
type Verification struct {
	Source   string  `json:"source"`
	Reason   *string `json:"reason"`
	Anchors  int     `json:"anchors"`
	Verified int     `json:"verified"`
}

// Counts are the submission's totals.
type Counts struct {
	Comments   int `json:"comments"`
	Errors     int `json:"errors"`
	Advisories int `json:"advisories"`
	Skipped    int `json:"skipped"`
}

// Diagnostic is one finding about the document.
type Diagnostic struct {
	Severity string  `json:"severity"`
	Name     string  `json:"name"`
	Comment  *string `json:"comment"`
	Path     *string `json:"path"`
	Message  string  `json:"message"`
}

// Skipped groups the checks that couldn't run for one reason.
type Skipped struct {
	Reason string   `json:"reason"`
	Checks []string `json:"checks"`
}

// NewReport describes result.
func NewReport(result *review.Result) Report {
	r := Report{
		Valid: result.Valid,
		Verification: Verification{
			Source:   result.Verification.Source,
			Reason:   orNull(result.Verification.Reason),
			Anchors:  result.Verification.Anchors,
			Verified: result.Verification.Verified,
		},
		Counts: Counts{
			Comments:   result.Comments,
			Errors:     result.Errors(),
			Advisories: result.Advisories(),
			Skipped:    len(result.Skipped),
		},
		Diagnostics: make([]Diagnostic, 0, len(result.Diagnostics)),
		Skipped:     groupSkipped(result.Skipped),
		Lenses:      result.Lenses(),
	}
	for _, d := range result.Diagnostics {
		r.Diagnostics = append(r.Diagnostics, Diagnostic{
			Severity: string(d.Severity),
			Name:     d.Name,
			Comment:  orNull(d.Comment),
			Path:     orNull(d.Path),
			Message:  d.Message,
		})
	}
	return r
}

// groupSkipped collects checks by reason, in first-seen order.
func groupSkipped(skipped []review.Skipped) []Skipped {
	out := []Skipped{}
	index := map[string]int{}
	for _, skip := range skipped {
		i, seen := index[skip.Reason]
		if !seen {
			i = len(out)
			index[skip.Reason] = i
			out = append(out, Skipped{Reason: skip.Reason})
		}
		out[i].Checks = append(out[i].Checks, skip.Name)
	}
	return out
}

func orNull(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
