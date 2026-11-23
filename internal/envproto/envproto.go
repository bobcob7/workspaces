// Package envproto is the wire contract between ws's controller and its
// environment agents (ADRs 0074, 0075): topics, payloads, enrollment codes.
package envproto

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"time"
)

// Message is one MQTT 5 application message.
type Message struct {
	Topic           string
	Payload         []byte
	QoS             byte
	Retain          bool
	ResponseTopic   string
	CorrelationData []byte
	Expiry          time.Duration // 0: none
}

// ConnectOptions are what an agent connects with.
type ConnectOptions struct {
	Address   string // host:port of the broker
	TLS       *tls.Config
	ClientID  string
	KeepAlive time.Duration
	Will      *Message
	WillDelay time.Duration
}

// Peer is a connected client as its verified certificate names it.
type Peer struct {
	ClientID string
	CN       string // the environment id
	Serial   string // cert.SerialNumber.Text(16)
	Issuer   string // Fingerprint of the CA that signed it
}

// Access is what a client asks to do with a topic.
type Access int

const (
	// AccessPublish is a client publishing to a topic.
	AccessPublish Access = iota + 1
	// AccessSubscribe is a client subscribing to a topic filter.
	AccessSubscribe
)

// Rules are what a broker asks before it lets a client connect, publish
// or subscribe. The controller's in-process client isn't checked.
type Rules struct {
	Connect func(p Peer) error
	Allow   func(p Peer, access Access, topic string) bool
}

// ErrNoVerifiedChain is returned when a connection has no verified client
// certificate chain of at least two certificates.
var ErrNoVerifiedChain = errors.New("no verified client certificate chain")

// PeerFromState reads the verified leaf (VerifiedChains[0][0]) and its
// issuer (VerifiedChains[0][1]). ClientID is left empty for the broker to
// fill. ErrNoVerifiedChain when there is no chain of at least 2.
func PeerFromState(cs *tls.ConnectionState) (Peer, error) {
	if cs == nil || len(cs.VerifiedChains) == 0 || len(cs.VerifiedChains[0]) < 2 {
		return Peer{}, ErrNoVerifiedChain
	}
	leaf := cs.VerifiedChains[0][0]
	return Peer{
		CN:     leaf.Subject.CommonName,
		Serial: leaf.SerialNumber.Text(16),
		Issuer: Fingerprint(cs.VerifiedChains[0][1]),
	}, nil
}

// Fingerprint is the lowercase hex SHA-256 of cert.Raw.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}
