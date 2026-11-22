package environment

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/bobcob7/workspaces/internal/envproto"
)

const (
	leafIn  = "in"
	leafOut = "out"
	wildOne = "+"
)

var (
	errClientIDMismatch = errors.New("client id isn't the certificate's common name")
	errInvalidID        = errors.New("common name isn't an environment id")
	errNotEnrolled      = errors.New("environment isn't enrolled")
	errRevoked          = errors.New("certificate is revoked")
	errUntrustedCA      = errors.New("certificate isn't issued by a trusted CA")
)

// policy decides what the broker admits. trust returns the roots the
// listeners accept right now.
type policy struct {
	store *Store
	trust func() trust
}

// connect refuses (errors, never panics) a peer whose ClientID != CN, whose
// CN isn't envproto.ValidID, that has no enrolled record, whose serial is
// in Revoked(), or whose Issuer isn't trusted.
func (p *policy) connect(peer envproto.Peer) error {
	if peer.ClientID != peer.CN {
		return errClientIDMismatch
	}
	if !envproto.ValidID(peer.CN) {
		return errInvalidID
	}
	rec, err := p.store.ByID(peer.CN)
	if err != nil {
		return fmt.Errorf("looking up environment %s: %w", peer.CN, err)
	}
	if rec.State != StateEnrolled {
		return errNotEnrolled
	}
	revoked, err := p.store.Revoked()
	if err != nil {
		return err
	}
	if revoked[peer.Serial] {
		return errRevoked
	}
	if !p.trust().contains(peer.Issuer) {
		return errUntrustedCA
	}
	return nil
}

// rules are the broker's checks: connect and allowed.
func (p *policy) rules() envproto.Rules {
	return envproto.Rules{Connect: p.connect, Allow: allowed}
}

// allowed is ADR 0074's ACL plus "up", for the peer's own id (= CN) only.
// Publish: <id>/status, <id>/snapshot, <id>/up, <id>/res/<x>,
// <id>/stream/<x>/out, where <x> is one level with no "+" or "#".
// Subscribe: <id>/req and <id>/stream/<x>/in where <x> may be "+".
// Everything else, any "#", and any topic outside ws/v1/env/ is refused.
func allowed(peer envproto.Peer, access envproto.Access, topic string) bool {
	id, rest, ok := envproto.ParseTopic(topic)
	if !ok || id != peer.CN {
		return false
	}
	switch access {
	case envproto.AccessPublish:
		return publishAllowed(rest)
	case envproto.AccessSubscribe:
		return subscribeAllowed(rest)
	default:
		return false
	}
}

// publishAllowed reports whether a peer may publish to the levels after its id.
func publishAllowed(rest []string) bool {
	switch {
	case len(rest) == 1:
		return slices.Contains([]string{envproto.LeafStatus, envproto.LeafSnapshot, envproto.LeafUp}, rest[0])
	case len(rest) == 2 && rest[0] == envproto.LeafRes:
		return plainLevel(rest[1])
	case len(rest) == 3 && rest[0] == envproto.LeafStream && rest[2] == leafOut:
		return plainLevel(rest[1])
	default:
		return false
	}
}

// subscribeAllowed reports whether a peer may subscribe to the levels after its id.
func subscribeAllowed(rest []string) bool {
	switch {
	case len(rest) == 1:
		return rest[0] == envproto.LeafReq
	case len(rest) == 3 && rest[0] == envproto.LeafStream && rest[2] == leafIn:
		return rest[1] == wildOne || plainLevel(rest[1])
	default:
		return false
	}
}

// plainLevel reports whether level is a topic level with no wildcard.
func plainLevel(level string) bool {
	return level != "" && !strings.ContainsAny(level, wildOne+"#")
}
