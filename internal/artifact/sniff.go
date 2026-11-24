package artifact

import (
	"bytes"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	contentTypeText     = "text/plain; charset=utf-8"
	contentTypeMarkdown = "text/markdown; charset=utf-8"
	contentTypeSVG      = "image/svg+xml"
	allowedTypesMessage = "only PNG, JPEG, GIF, WebP, SVG, Markdown and UTF-8 text are allowed"
	nameMessage         = "a name is 1 to 128 characters, one path element, with no control characters and no leading dot"
	whitespace          = " \t\r\n"
)

// Detect returns the kind and content type of data named name, or
// ErrInvalid (wrapped, with a reason a person can act on) when the type
// isn't allowed, data is empty, or it is over its kind's limit.
func Detect(name string, data []byte) (Kind, string, error) {
	if len(data) == 0 {
		return "", "", fmt.Errorf("%w: the file is empty", ErrInvalid)
	}
	if ct := http.DetectContentType(data); isRasterType(ct) {
		return detectRaster(ct, data)
	}
	return detectText(name, data)
}

// CheckName is nil for a valid artifact name (1-128 runes, one path
// element, no '/', '\\', control characters or leading '.').
func CheckName(name string) error {
	if name == "" || strings.HasPrefix(name, ".") || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("%w: %s", ErrInvalid, nameMessage)
	}
	if utf8.RuneCountInString(name) > MaxNameRunes || hasControl(name) {
		return fmt.Errorf("%w: %s", ErrInvalid, nameMessage)
	}
	return nil
}

// CheckLine is nil for a title or note: up to MaxLineRunes, no newline or
// control character. Empty is allowed.
func CheckLine(what, s string) error {
	if utf8.RuneCountInString(s) > MaxLineRunes || hasControl(s) {
		return fmt.Errorf("%w: %s must be one line of at most %d characters", ErrInvalid, what, MaxLineRunes)
	}
	return nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func isRasterType(ct string) bool {
	switch ct {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}

func detectRaster(ct string, data []byte) (Kind, string, error) {
	if len(data) > MaxImageSize {
		return "", "", fmt.Errorf("%w: images are limited to %d MiB", ErrInvalid, MaxImageSize>>20)
	}
	return KindImage, ct, nil
}

func detectText(name string, data []byte) (Kind, string, error) {
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", "", fmt.Errorf("%w: %s", ErrInvalid, allowedTypesMessage)
	}
	if len(data) > MaxTextSize {
		return "", "", fmt.Errorf("%w: text is limited to %d MiB", ErrInvalid, MaxTextSize>>20)
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".svg":
		return detectSVG(data)
	case ".md", ".markdown":
		return KindMarkdown, contentTypeMarkdown, nil
	}
	return KindText, contentTypeText, nil
}

func detectSVG(data []byte) (Kind, string, error) {
	if !looksLikeSVG(string(data)) {
		return "", "", fmt.Errorf("%w: a .svg file must hold SVG markup", ErrInvalid)
	}
	return KindImage, contentTypeSVG, nil
}

// looksLikeSVG skips the prolog (XML declaration, comments and a doctype)
// and checks that the root element is <svg>.
func looksLikeSVG(s string) bool {
	s = strings.TrimLeft(s, whitespace)
	for {
		rest, ok := skipProlog(s)
		if !ok {
			return isSVGRoot(s)
		}
		s = strings.TrimLeft(rest, whitespace)
	}
}

func skipProlog(s string) (string, bool) {
	if rest, ok := skipPast(s, "<?", "?>"); ok {
		return rest, true
	}
	if rest, ok := skipPast(s, "<!--", "-->"); ok {
		return rest, true
	}
	return skipDoctype(s)
}

func skipPast(s, open, closing string) (string, bool) {
	if !strings.HasPrefix(s, open) {
		return "", false
	}
	i := strings.Index(s[len(open):], closing)
	if i < 0 {
		return "", false
	}
	return s[len(open)+i+len(closing):], true
}

func skipDoctype(s string) (string, bool) {
	const keyword = "<!DOCTYPE"
	if len(s) < len(keyword) || !strings.EqualFold(s[:len(keyword)], keyword) {
		return "", false
	}
	_, rest, found := strings.Cut(s, ">")
	if !found {
		return "", false
	}
	return rest, true
}

func isSVGRoot(s string) bool {
	const tag = "<svg"
	if !strings.HasPrefix(s, tag) || len(s) == len(tag) {
		return false
	}
	return strings.ContainsRune(whitespace+">/", rune(s[len(tag)]))
}
