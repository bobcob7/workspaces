// Package repo parses repo specs, repo[@ref] (ADR 0007), into canonical
// ids and upstream URLs. It doesn't resolve short names; that needs the
// cache and config.
package repo

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ErrInvalidSpec marks a spec or id that can't be parsed.
var ErrInvalidSpec = errors.New("invalid repo spec")

// Kind is the form a spec's repo part takes.
type Kind int

// The repo forms of ADR 0007's grammar.
const (
	KindName      Kind = iota + 1 // name
	KindOwnerName                 // owner/name
	KindQualified                 // host/owner/name, with nested owner groups
	KindURL                       // a clone URL, scp-style or with a scheme
)

func (k Kind) String() string {
	switch k {
	case KindName:
		return "name"
	case KindOwnerName:
		return "owner/name"
	case KindQualified:
		return "host/owner/name"
	case KindURL:
		return "url"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Spec is a parsed repo spec. Host, Owner, and Name are lowercase.
type Spec struct {
	// Raw is the spec as given.
	Raw  string
	Kind Kind
	// Host is empty for KindName and KindOwnerName.
	Host string
	// Owner is empty for KindName; nested groups are joined by "/".
	Owner string
	Name  string
	// URL is the clone URL as given, for KindURL only.
	URL string
	// Ref is empty when the spec names none, meaning the default branch.
	Ref string
}

// ParseSpec parses a spec. The ref separator is the first "@" after the repo
// part, so a URL's user@ stays in the URL and a ref may contain "@".
func ParseSpec(s string) (Spec, error) {
	repo, ref, hasRef := split(s)
	if hasRef {
		if err := validateRef(ref); err != nil {
			return Spec{}, fmt.Errorf("%w %q: %w", ErrInvalidSpec, redact(s), err)
		}
	}
	sp, err := parseRepo(repo)
	if err != nil {
		return Spec{}, fmt.Errorf("%w %q: %w", ErrInvalidSpec, redact(s), err)
	}
	sp.Raw, sp.Ref = s, ref
	return sp, nil
}

// ID returns the canonical id when the spec names the host, so no lookup
// is needed: KindQualified and KindURL.
func (s *Spec) ID() (ID, bool) {
	if s.Kind != KindQualified && s.Kind != KindURL {
		return ID{}, false
	}
	return ID{Host: s.Host, Owner: s.Owner, Name: s.Name}, true
}

// Upstream returns the URL to clone from: a URL spec's URL as given, or the
// HTTPS URL built from a qualified spec's id (ADR 0019). Short specs have
// none until they're resolved.
func (s *Spec) Upstream() (string, bool) {
	if s.Kind == KindURL {
		return s.URL, true
	}
	id, ok := s.ID()
	if !ok {
		return "", false
	}
	return id.UpstreamURL(), true
}

// split separates the repo part from the ref.
func split(s string) (repo, ref string, hasRef bool) {
	start := pathStart(s)
	i := strings.Index(s[start:], "@")
	if i < 0 {
		return s, "", false
	}
	return s[:start+i], s[start+i+1:], true
}

// pathStart is where a spec's path begins: after a scheme URL's
// authority, or after an scp-style URL's colon. An "@" before it belongs to
// the URL.
func pathStart(s string) int {
	if rest, ok := schemeEnd(s); ok {
		slash := strings.Index(s[rest:], "/")
		if slash < 0 {
			return len(s)
		}
		return rest + slash
	}
	if colon, ok := scpColon(s); ok {
		return colon + 1
	}
	return 0
}

// schemeEnd finds a URL scheme such as "https://" at the start of s and
// returns the index just past it.
func schemeEnd(s string) (int, bool) {
	i := strings.Index(s, "://")
	if i <= 0 {
		return 0, false
	}
	for j, r := range s[:i] {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !isLetter && (j == 0 || !strings.ContainsRune("0123456789+.-", r)) {
			return 0, false
		}
	}
	return i + len("://"), true
}

// scpColon finds the colon of an scp-style URL, [user@]host:path: a ":"
// before any "/", as git recognizes it, with a host containing ".". Without
// the "." rule, a spec such as name@ref:path, whose ref is invalid, would
// read as user@host:path, and an ssh alias or insteadOf shorthand such as
// gh:owner/name would get a wrong canonical id.
func scpColon(s string) (int, bool) {
	colon := strings.Index(s, ":")
	if colon <= 0 {
		return 0, false
	}
	if slash := strings.Index(s, "/"); slash >= 0 && slash < colon {
		return 0, false
	}
	left := s[:colon]
	if !strings.Contains(left[strings.LastIndex(left, "@")+1:], ".") {
		return 0, false
	}
	return colon, true
}

// validateRef applies the rules ADR 0007 relies on: git forbids the bare
// ref "@", the sequence "@{", and ":". It also rejects a leading "-", which
// git refuses in branch names and which would read as an option when the
// ref is passed to git. Everything else is left to git when the ref is
// resolved.
func validateRef(ref string) error {
	switch {
	case ref == "":
		return errors.New("empty ref after @")
	case ref == "@":
		return errors.New(`ref can't be "@"`)
	case strings.HasPrefix(ref, "-"):
		return fmt.Errorf(`ref %q can't start with "-"`, ref)
	case strings.Contains(ref, "@{"):
		return fmt.Errorf(`ref %q can't contain "@{"`, ref)
	case strings.Contains(ref, ":"):
		return fmt.Errorf(`ref %q can't contain ":"`, ref)
	}
	return nil
}

// errLocal rejects local repos: ws clones from forges only (ADR 0020).
var errLocal = errors.New("local paths and file:// URLs aren't repo specs; use a URL or host/owner/name")

// isLocalPath reports whether repo looks like a filesystem path rather than
// a forge repo.
func isLocalPath(repo string) bool {
	for _, prefix := range []string{"/", "./", "../", "~"} {
		if strings.HasPrefix(repo, prefix) {
			return true
		}
	}
	return repo == "." || repo == ".."
}

// redact masks a password in a URL spec, so errors never repeat it.
func redact(s string) string {
	if _, ok := schemeEnd(s); !ok {
		return s
	}
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	return u.Redacted()
}

// parseRepo parses the repo part of a spec.
func parseRepo(repo string) (Spec, error) {
	if isLocalPath(repo) {
		return Spec{}, errLocal
	}
	if _, ok := schemeEnd(repo); ok {
		return parseSchemeURL(repo)
	}
	if colon, ok := scpColon(repo); ok {
		return parseSCP(repo, colon)
	}
	return parsePath(repo)
}

// defaultPorts are the URL schemes git can clone from a forge with, and the
// port each uses when a URL names none.
func defaultPorts() map[string]string {
	return map[string]string{"https": "443", "http": "80", "ssh": "22", "git": "9418"}
}

// parseSchemeURL parses a URL such as https://host/owner/name.git or
// ssh://git@host:2222/owner/name.
func parseSchemeURL(repo string) (Spec, error) {
	u, err := url.Parse(repo)
	if err != nil {
		return Spec{}, fmt.Errorf("parsing URL: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "file" {
		return Spec{}, errLocal
	}
	defaultPort, ok := defaultPorts()[scheme]
	if !ok {
		return Spec{}, fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	if _, has := u.User.Password(); has {
		return Spec{}, errors.New("URL can't contain a password; use a git credential helper")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return Spec{}, errors.New("URL can't have a query or fragment")
	}
	sp, err := urlSpec(repo, u.Hostname(), u.Path)
	if err != nil {
		return Spec{}, err
	}
	if port := u.Port(); port != "" && port != defaultPort {
		sp.Host += ":" + port
	}
	return sp, nil
}

// parseSCP parses an scp-style URL, [user@]host:owner/name.
func parseSCP(repo string, colon int) (Spec, error) {
	left := repo[:colon]
	host := left[strings.LastIndex(left, "@")+1:]
	return urlSpec(repo, host, repo[colon+1:])
}

func urlSpec(repo, host, path string) (Spec, error) {
	if err := validateHost(host); err != nil {
		return Spec{}, err
	}
	segs, err := segments(path)
	if err != nil {
		return Spec{}, err
	}
	if len(segs) < 2 {
		return Spec{}, errors.New("URL path needs an owner and a name")
	}
	last := len(segs) - 1
	return Spec{
		Kind:  KindURL,
		Host:  strings.ToLower(host),
		Owner: strings.Join(segs[:last], "/"),
		Name:  segs[last],
		URL:   repo,
	}, nil
}

// parsePath parses name, owner/name, or host/owner/name. With three or more
// segments, the first must contain "." to be the host, and the segments
// between host and name are nested owner groups.
func parsePath(repo string) (Spec, error) {
	if strings.HasSuffix(repo, "/") {
		return Spec{}, errors.New(`repo can't end with "/"`)
	}
	segs, err := segments(repo)
	if err != nil {
		return Spec{}, err
	}
	last := len(segs) - 1
	switch {
	case len(segs) == 1:
		return Spec{Kind: KindName, Name: segs[0]}, nil
	case len(segs) == 2:
		return Spec{Kind: KindOwnerName, Owner: segs[0], Name: segs[1]}, nil
	case !strings.Contains(segs[0], "."):
		return Spec{}, fmt.Errorf("%q isn't a host name (it has no '.'); use owner/name or host/owner/name", segs[0])
	}
	if err := validateHost(segs[0]); err != nil {
		return Spec{}, err
	}
	return Spec{Kind: KindQualified, Host: segs[0], Owner: strings.Join(segs[1:last], "/"), Name: segs[last]}, nil
}

// segments lowercases a repo path, drops one leading "/" and a trailing
// ".git", splits it on "/", and validates each segment.
func segments(path string) ([]string, error) {
	path = strings.TrimPrefix(strings.ToLower(path), "/")
	path = strings.TrimSuffix(path, ".git")
	if path == "" {
		return nil, errors.New("empty repo")
	}
	segs := strings.Split(path, "/")
	for _, seg := range segs {
		if err := validateSegment(seg); err != nil {
			return nil, err
		}
	}
	return segs, nil
}
