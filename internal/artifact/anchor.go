package artifact

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrAnchorNotFound is a quote that isn't in the text, or isn't unique.
var ErrAnchorNotFound = errors.New("quote not found")

// stateAnchored and stateDetached are an anchor's State (ADR 0079).
const (
	stateAnchored = "anchored"
	stateDetached = "detached"
)

// AnchorError is ErrAnchorNotFound with the occurrences, for details.
type AnchorError struct {
	Occurrences int
	Lines       []int // the 1-based line each occurrence starts on
}

// Error names the occurrences and their lines, or that the quote is gone.
func (e *AnchorError) Error() string {
	if e.Occurrences == 0 {
		return "the quote isn't in the latest revision"
	}
	lines := make([]string, len(e.Lines))
	for i, line := range e.Lines {
		lines[i] = strconv.Itoa(line)
	}
	return fmt.Sprintf("the quote occurs %d times, on lines %s; pass --line", e.Occurrences, strings.Join(lines, ", "))
}

// Is reports whether target is ErrAnchorNotFound.
func (*AnchorError) Is(target error) bool {
	return target == ErrAnchorNotFound
}

// AnchorRequest is how a caller names the text a new thread is about.
type AnchorRequest struct {
	Quote  string
	Line   int // 0: any
	Prefix string
	Suffix string
}

// Locate anchors req in text, revision rev (ADR 0079 › Making an anchor):
// the only occurrence; else the one starting on req.Line; else the one
// with the strictly highest context score; else *AnchorError.
func Locate(text string, rev int, req AnchorRequest) (Anchor, error) {
	if err := checkQuote(req.Quote); err != nil {
		return Anchor{}, err
	}
	spans := exactSpans(text, req.Quote)
	span, ok := pickSpan(text, spans, &req)
	if !ok {
		return Anchor{}, newAnchorError(text, spans)
	}
	return anchorAt(text, rev, span[0], span[1]), nil
}

// Move re-anchors a into text, revision rev (ADR 0079 › New revisions
// move anchors). ok is false when the quote is gone; the caller then
// keeps a with State "detached".
//
//nolint:gocritic // a is passed by value, as ADR 0079 names the signature.
func Move(text string, rev int, a Anchor) (moved Anchor, ok bool) {
	if a.Quote == "" {
		return a, false
	}
	spans := exactSpans(text, a.Quote)
	if len(spans) == 0 {
		spans = normalizedOccurrences(text, a.Quote)
	}
	if len(spans) == 0 {
		return a, false
	}
	span := closestSpan(text, spans, &a)
	moved = anchorAt(text, rev, span[0], span[1])
	moved.Quote = a.Quote
	return moved, true
}

func checkQuote(quote string) error {
	switch {
	case strings.TrimSpace(quote) == "":
		return fmt.Errorf("%w: the quote is empty", ErrInvalid)
	case utf8.RuneCountInString(quote) > MaxQuoteRunes:
		return fmt.Errorf("%w: a quote is at most %d characters", ErrInvalid, MaxQuoteRunes)
	}
	return nil
}

func pickSpan(text string, spans [][2]int, req *AnchorRequest) ([2]int, bool) {
	if len(spans) == 1 {
		return spans[0], true
	}
	if span, ok := spanOnLine(text, spans, req.Line); ok {
		return span, true
	}
	return uniqueBestSpan(text, spans, req.Prefix, req.Suffix)
}

func newAnchorError(text string, spans [][2]int) *AnchorError {
	lines := make([]int, 0, len(spans))
	for _, span := range spans {
		lines = append(lines, lineAt(text, span[0]))
	}
	return &AnchorError{Occurrences: len(spans), Lines: lines}
}

// spanOnLine is the only span starting on line; line 0 matches nothing.
func spanOnLine(text string, spans [][2]int, line int) ([2]int, bool) {
	if line == 0 {
		return [2]int{}, false
	}
	var found [2]int
	matches := 0
	for _, span := range spans {
		if lineAt(text, span[0]) == line {
			found = span
			matches++
		}
	}
	return found, matches == 1
}

// uniqueBestSpan is the span with the strictly highest context score.
func uniqueBestSpan(text string, spans [][2]int, prefix, suffix string) ([2]int, bool) {
	var best [2]int
	bestScore, ties := -1, 0
	for _, span := range spans {
		score := contextScore(text, span[0], span[1], prefix, suffix)
		switch {
		case score > bestScore:
			best, bestScore, ties = span, score, 1
		case score == bestScore:
			ties++
		}
	}
	return best, ties == 1
}

// closestSpan is the highest context score; a tie goes to the span whose
// line is closest to a's, then the earliest (spans are in text order).
func closestSpan(text string, spans [][2]int, a *Anchor) [2]int {
	best := spans[0]
	bestScore, bestDistance := -1, 0
	for _, span := range spans {
		score := contextScore(text, span[0], span[1], a.Prefix, a.Suffix)
		distance := absInt(lineAt(text, span[0]) - a.LineStart)
		if score > bestScore || (score == bestScore && distance < bestDistance) {
			best, bestScore, bestDistance = span, score, distance
		}
	}
	return best
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// exactSpans is every exact occurrence of quote as [start, end) offsets.
func exactSpans(text, quote string) [][2]int {
	starts := occurrences(text, quote)
	spans := make([][2]int, 0, len(starts))
	for _, start := range starts {
		spans = append(spans, [2]int{start, start + len(quote)})
	}
	return spans
}

// occurrences is every byte offset where quote starts, overlaps allowed.
func occurrences(text, quote string) []int {
	if quote == "" {
		return nil
	}
	var starts []int
	for offset := 0; offset < len(text); {
		i := strings.Index(text[offset:], quote)
		if i < 0 {
			break
		}
		starts = append(starts, offset+i)
		_, size := utf8.DecodeRuneInString(text[offset+i:])
		offset += i + size
	}
	return starts
}

// normalizedOccurrences matches quote in text with every run of whitespace
// collapsed to one space on both sides, as [start, end) offsets in text.
func normalizedOccurrences(text, quote string) [][2]int {
	want := strings.Join(strings.Fields(quote), " ")
	if want == "" {
		return nil
	}
	norm, offsets := normalizeWhitespace(text)
	var spans [][2]int
	for _, p := range occurrences(norm, want) {
		spans = append(spans, [2]int{offsets[p], offsets[p+len(want)]})
	}
	return spans
}

// normalizeWhitespace collapses runs of Unicode whitespace to one space. The
// result has one offset per byte, plus len(text) at the end, mapping each
// normalized byte back to its byte in text.
func normalizeWhitespace(text string) (string, []int) {
	var norm []byte
	offsets := make([]int, 0, len(text)+1)
	inSpace := false
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if unicode.IsSpace(r) {
			if !inSpace {
				norm = append(norm, ' ')
				offsets = append(offsets, i)
			}
			inSpace = true
		} else {
			norm = append(norm, text[i:i+size]...)
			for range size {
				offsets = append(offsets, i)
			}
			inSpace = false
		}
		i += size
	}
	return string(norm), append(offsets, len(text))
}

// contextScore counts the runes of the common suffix of prefix and the text
// before start, plus the runes of the common prefix of suffix and the text
// from end.
func contextScore(text string, start, end int, prefix, suffix string) int {
	return commonSuffixRunes(prefix, text[:start]) + commonPrefixRunes(suffix, text[end:])
}

func commonSuffixRunes(a, b string) int {
	count := 0
	for a != "" && b != "" {
		ra, sa := utf8.DecodeLastRuneInString(a)
		rb, sb := utf8.DecodeLastRuneInString(b)
		if ra != rb {
			break
		}
		count++
		a, b = a[:len(a)-sa], b[:len(b)-sb]
	}
	return count
}

func commonPrefixRunes(a, b string) int {
	count := 0
	for a != "" && b != "" {
		ra, sa := utf8.DecodeRuneInString(a)
		rb, sb := utf8.DecodeRuneInString(b)
		if ra != rb {
			break
		}
		count++
		a, b = a[sa:], b[sb:]
	}
	return count
}

// lineAt is the 1-based line that offset falls on.
func lineAt(text string, offset int) int {
	return 1 + strings.Count(text[:offset], "\n")
}

// contextAround is up to ContextRunes runes before start and after end.
func contextAround(text string, start, end int) (string, string) {
	return lastRunes(text[:start], ContextRunes), firstRunes(text[end:], ContextRunes)
}

func lastRunes(s string, n int) string {
	start := len(s)
	for range n {
		if start == 0 {
			break
		}
		_, size := utf8.DecodeLastRuneInString(s[:start])
		start -= size
	}
	return s[start:]
}

func firstRunes(s string, n int) string {
	end := 0
	for range n {
		if end == len(s) {
			break
		}
		_, size := utf8.DecodeRuneInString(s[end:])
		end += size
	}
	return s[:end]
}

// anchorAt is the anchor for text[start:end] in revision rev.
func anchorAt(text string, rev, start, end int) Anchor {
	prefix, suffix := contextAround(text, start, end)
	lineStart := lineAt(text, start)
	lineEnd := lineStart
	if end > start {
		lineEnd = lineAt(text, end-1)
	}
	return Anchor{
		Rev:       rev,
		Start:     start,
		End:       end,
		LineStart: lineStart,
		LineEnd:   lineEnd,
		Quote:     text[start:end],
		Prefix:    prefix,
		Suffix:    suffix,
		State:     stateAnchored,
	}
}
