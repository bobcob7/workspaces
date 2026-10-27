package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/review/profile"
)

func (a *App) newReviewProfilesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "profiles",
		Short: "List review profiles with their settings",
		Args:  noArgs,
		RunE: runE(a.withConfig(func(*cobra.Command, []string) error {
			return a.runReviewProfiles()
		})),
	}
}

// reviewProfilesJSON is ws review profiles' output (ADR 0039).
type reviewProfilesJSON struct {
	Profiles []reviewProfileInfoJSON `json:"profiles"`
	Warnings []string                `json:"warnings"`
}

// reviewProfileInfoJSON is one profile as config.toml leaves it: when is
// config.toml's override, if any, and model "" is the session's.
type reviewProfileInfoJSON struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	When        profile.When `json:"when"`
	Match       []string     `json:"match"`
	Model       string       `json:"model"`
	Builtin     bool         `json:"builtin"`
}

// runReviewProfiles lists every profile, built-in and the user's, with
// config.toml's when and model applied.
func (a *App) runReviewProfiles() error {
	profiles, err := a.reviewProfiles()
	if err != nil {
		return err
	}
	out := make([]reviewProfileInfoJSON, 0, len(profiles))
	for i := range profiles {
		p := &profiles[i]
		settings := a.config.Review.Profiles[p.Name]
		when := p.When
		if settings.When != "" {
			when = profile.When(settings.When)
		}
		out = append(out, reviewProfileInfoJSON{
			Name: p.Name, Description: p.Description, When: when, Match: nonNil(p.Match), Model: settings.Model, Builtin: p.Builtin,
		})
	}
	if a.global.json {
		return a.writeJSON(reviewProfilesJSON{Profiles: out, Warnings: a.warnings()})
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "NAME\tWHEN\tMATCH\tMODEL\tDESCRIPTION")
	for _, p := range out {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.Name, p.When, dashIfEmpty(strings.Join(p.Match, " ")), dashIfEmpty(p.Model), p.Description)
	}
	_ = tw.Flush()
	a.printWarnings()
	return nil
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
