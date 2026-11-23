package artifact

import (
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

const (
	idPrefix      = "art-"
	idRandomBytes = 4
	hintFormat    = `(Artifacts %s: run "ws machine artifact get <id>" for each, then read the file at the "path" it prints.)`
	hintMarker    = `run "ws machine artifact get <id>"`
	hintSeparator = "\n\n"
)

var (
	idPattern     = regexp.MustCompile(`^art-[0-9a-f]{8}$`)
	refPattern    = regexp.MustCompile(`^(?:@artifact:)?(art-[0-9a-f]{8})(?:/r([1-9]\d{0,2}))?$`)
	inTextPattern = regexp.MustCompile(`@artifact:(art-[0-9a-f]{8})(?:/r([1-9]\d{0,2}))?`)
)

// NewID is "art-" and 8 lowercase hex digits from random.
func NewID(random io.Reader) (string, error) {
	buf := make([]byte, idRandomBytes)
	if _, err := io.ReadFull(random, buf); err != nil {
		return "", fmt.Errorf("drawing an artifact id: %w", err)
	}
	return fmt.Sprintf("%s%x", idPrefix, buf), nil
}

// ValidID reports whether id matches ^art-[0-9a-f]{8}$.
func ValidID(id string) bool {
	return idPattern.MatchString(id)
}

// ParseRef accepts "art-…", "@artifact:art-…" and "@artifact:art-…/r<n>";
// rev is 0 when no /r<n> is given. Anything else is ErrInvalid.
func ParseRef(s string) (id string, rev int, err error) {
	m := refPattern.FindStringSubmatch(s)
	if m == nil {
		return "", 0, fmt.Errorf("%w: %q is not an artifact id or reference", ErrInvalid, s)
	}
	return m[1], revisionOf(m[2]), nil
}

// FindRefs is the distinct artifact ids referenced in text, in first-seen
// order.
func FindRefs(text string) []string {
	var ids []string
	seen := make(map[string]bool)
	for _, m := range inTextPattern.FindAllStringSubmatch(text, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		ids = append(ids, m[1])
	}
	return ids
}

// HintLine tells an agent how to fetch the artifacts ids.
func HintLine(ids []string) string {
	return fmt.Sprintf(hintFormat, strings.Join(ids, ", "))
}

// AppendHint returns prompt with a HintLine appended when it references
// artifacts and doesn't already carry one; otherwise prompt is unchanged.
// It also returns the referenced ids.
func AppendHint(prompt string) (string, []string) {
	ids := FindRefs(prompt)
	if len(ids) == 0 || strings.Contains(prompt, hintMarker) {
		return prompt, ids
	}
	return prompt + hintSeparator + HintLine(ids), ids
}

func revisionOf(digits string) int {
	if digits == "" {
		return 0
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return 0
	}
	return n
}
