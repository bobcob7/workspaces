package git

import (
	"fmt"
	"strconv"
	"strings"
)

// minVersion is the oldest supported git: 2.23 added `git switch` (ADR 0003).
// Raise it only with an ADR naming the feature that needs it.
func minVersion() Version {
	return Version{Major: 2, Minor: 23}
}

// Version is a git release number.
type Version struct {
	Major, Minor, Patch int
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// atLeast reports whether v is the same as or newer than o.
func (v Version) atLeast(o Version) bool {
	if v.Major != o.Major {
		return v.Major > o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor > o.Minor
	}
	return v.Patch >= o.Patch
}

// parseVersion reads `git --version` output, such as "git version 2.50.1
// (Apple Git-155)" or "git version 2.44.0.windows.1". A missing patch
// number is 0.
func parseVersion(out string) (Version, error) {
	fields := strings.Fields(out)
	if len(fields) < 3 || fields[0] != "git" || fields[1] != "version" {
		return Version{}, fmt.Errorf("unrecognized git --version output %q", strings.TrimSpace(out))
	}
	parts := strings.SplitN(fields[2], ".", 4)
	if len(parts) < 2 {
		return Version{}, fmt.Errorf("unrecognized git version %q", fields[2])
	}
	nums := make([]int, 3)
	for i := 0; i < len(nums) && i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return Version{}, fmt.Errorf("unrecognized git version %q: %w", fields[2], err)
		}
		nums[i] = n
	}
	return Version{Major: nums[0], Minor: nums[1], Patch: nums[2]}, nil
}
