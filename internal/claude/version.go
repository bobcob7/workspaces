package claude

import (
	"fmt"
	"strconv"
	"strings"
)

// minVersion is the oldest Claude Code whose background-session interface
// was measured (ADRs 0015, 0018, 0024). Lower it only with a measurement
// on the older version.
func minVersion() version {
	return version{major: 2, minor: 1, patch: 302}
}

// version is a Claude Code release number.
type version struct {
	major, minor, patch int
}

func (v version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
}

// atLeast reports whether v is the same as or newer than o.
func (v version) atLeast(o version) bool {
	if v.major != o.major {
		return v.major > o.major
	}
	if v.minor != o.minor {
		return v.minor > o.minor
	}
	return v.patch >= o.patch
}

// parseVersion reads `claude --version` output, such as "2.1.303 (Claude
// Code)". A pre-release or build suffix, as in "2.1.304-beta.1", is
// ignored.
func parseVersion(out string) (version, error) {
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return version{}, fmt.Errorf("%w: empty claude --version output", ErrUnexpectedOutput)
	}
	release, _, _ := strings.Cut(fields[0], "-")
	release, _, _ = strings.Cut(release, "+")
	parts := strings.Split(release, ".")
	if len(parts) != 3 {
		return version{}, fmt.Errorf("%w: claude version %q", ErrUnexpectedOutput, fields[0])
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return version{}, fmt.Errorf("%w: claude version %q", ErrUnexpectedOutput, fields[0])
		}
		nums[i] = n
	}
	return version{major: nums[0], minor: nums[1], patch: nums[2]}, nil
}
