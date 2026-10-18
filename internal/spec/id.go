package spec

import (
	"errors"
	"fmt"
	"strings"
)

// ID is a canonical repo id, host/owner/name, in lowercase (ADR 0007). Owner
// may hold nested groups separated by "/".
type ID struct {
	Host  string
	Owner string
	Name  string
}

// NewID validates and lowercases the parts of a repo id.
func NewID(host, owner, name string) (ID, error) {
	id := ID{Host: strings.ToLower(host), Owner: strings.ToLower(owner), Name: strings.ToLower(name)}
	if err := validateHost(id.Host); err != nil {
		return ID{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := validatePath(id.Owner); err != nil {
		return ID{}, fmt.Errorf("%w: owner: %w", ErrInvalid, err)
	}
	if err := validateSegment(id.Name); err != nil {
		return ID{}, fmt.Errorf("%w: name: %w", ErrInvalid, err)
	}
	return id, nil
}

// String returns host/owner/name.
func (id ID) String() string {
	return id.Host + "/" + id.Owner + "/" + id.Name
}

// UpstreamURL is the HTTPS clone URL ws builds for an id (ADR 0019).
func (id ID) UpstreamURL() string {
	return "https://" + id.String() + ".git"
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
