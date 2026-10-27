package repo

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ID is a canonical repo id, host/owner/name, in lowercase (ADR 0007). Owner
// may hold nested groups separated by "/". Host includes a non-default port,
// as in "git.example.com:2222" (ADR 0020).
type ID struct {
	Host  string
	Owner string
	Name  string
}

// NewID validates and lowercases the parts of a repo id. host may include a
// port.
func NewID(host, owner, name string) (ID, error) {
	id := ID{Host: strings.ToLower(host), Owner: strings.ToLower(owner), Name: strings.ToLower(name)}
	if err := validateHostPort(id.Host); err != nil {
		return ID{}, fmt.Errorf("%w: %w", ErrInvalidSpec, err)
	}
	if err := validatePath(id.Owner); err != nil {
		return ID{}, fmt.Errorf("%w: owner: %w", ErrInvalidSpec, err)
	}
	if err := validateSegment(id.Name); err != nil {
		return ID{}, fmt.Errorf("%w: name: %w", ErrInvalidSpec, err)
	}
	return id, nil
}

// ParseID parses a canonical id, host/owner/name, as String writes it.
func ParseID(s string) (ID, error) {
	host, rest, ok := strings.Cut(s, "/")
	slash := strings.LastIndex(rest, "/")
	if !ok || slash < 0 {
		return ID{}, fmt.Errorf("%w: repo id %q isn't host/owner/name", ErrInvalidSpec, s)
	}
	return NewID(host, rest[:slash], rest[slash+1:])
}

// String returns host/owner/name.
func (id ID) String() string {
	return id.Host + "/" + id.Owner + "/" + id.Name
}

// UpstreamURL is the HTTPS clone URL ws builds for an id (ADR 0019). It's
// only meaningful for ids from host/owner/name specs; a repo cached from a
// URL keeps the URL it was cloned from.
func (id ID) UpstreamURL() string {
	return "https://" + id.String() + ".git"
}

// validateHostPort accepts a host name with an optional ":port".
func validateHostPort(hostPort string) error {
	host, port, hasPort := strings.Cut(hostPort, ":")
	if err := validateHost(host); err != nil {
		return err
	}
	if !hasPort {
		return nil
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return fmt.Errorf("port %q must be a number from 1 to 65535", port)
	}
	return nil
}

// validateHost accepts a bare host name: letters, digits, ".", and "-".
func validateHost(host string) error {
	if host == "" {
		return errors.New("empty host")
	}
	if !onlyChars(host, ".-") {
		return fmt.Errorf("host %q may only contain letters, digits, '.', and '-'", host)
	}
	return nil
}

// validatePath checks each "/"-separated segment of an owner path.
func validatePath(path string) error {
	for seg := range strings.SplitSeq(path, "/") {
		if err := validateSegment(seg); err != nil {
			return err
		}
	}
	return nil
}

// validateSegment accepts one owner, group, or repo name: letters, digits,
// "_", ".", and "-", the characters GitHub, GitLab, and Gitea allow
// (ADR 0007).
func validateSegment(seg string) error {
	switch {
	case seg == "":
		return errors.New("empty path segment")
	case seg == "." || seg == "..":
		return fmt.Errorf("path segment %q is not a name", seg)
	case !onlyChars(seg, "_.-"):
		return fmt.Errorf("%q may only contain letters, digits, '_', '.', and '-'", seg)
	}
	return nil
}

// onlyChars reports whether s holds only ASCII letters, digits, and extra.
func onlyChars(s, extra string) bool {
	for _, r := range s {
		isAlnum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !isAlnum && !strings.ContainsRune(extra, r) {
			return false
		}
	}
	return true
}
