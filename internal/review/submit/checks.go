package submit

import "github.com/bobcob7/workspaces/internal/review"

// Check names ws adds to the forked registry: what a review workspace's
// record makes checkable (ADR 0037).
const (
	checkRef     = "review-ref"
	checkProfile = "review-profile"
)

// Checks returns the checks submit runs against the review workspace's
// record, for the describe registry.
func Checks() []review.Check {
	return []review.Check{
		{
			Name:    checkRef,
			Tier:    review.TierVerification,
			Summary: "ref is not the commit this review workspace reviews",
			Title:   "Ref differs from the review's commit",
			Body: `Fires when the document sets ref to anything but the commit the review
workspace was started for. ws review submit already knows that commit from
the workspace's record, so ref may be left out: it is filled in.

A ref that differs means the review describes some other commit, and its
anchors would be checked against the wrong tree. Leave ref out, or copy it
from git rev-parse HEAD in the review workspace.`,
			Related: []string{"ref", "ref-unknown"},
		},
		{
			Name:    checkProfile,
			Tier:    review.TierVerification,
			Summary: "profile is missing or not one of this review's profiles",
			Title:   "Profile not in the review",
			Body: `Fires when the document has no profile, or one the review workspace was not
started with. Each profile's submission is stored under its name, and the
report counts a profile as done once it has one, so a review under a name
the review didn't ask for would never be read.

Set profile to the name you were primed with: ws review prime
--profile=NAME. Resubmitting under the same profile replaces your earlier
submission.`,
			Related: []string{"profile", "store"},
		},
	}
}
