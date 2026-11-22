package environment

import "crypto/tls"

// tlsState is what the listeners serve with. It is swapped whole on reload.
type tlsState struct {
	trust  trust
	server tls.Certificate
}

// brokerTLS requires a client certificate signed by a trusted CA. The
// state is read at each handshake.
func brokerTLS(state func() *tlsState) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			s := state()
			return &tls.Config{
				MinVersion:   tls.VersionTLS13,
				Certificates: []tls.Certificate{s.server},
				ClientAuth:   tls.RequireAndVerifyClientCert,
				ClientCAs:    s.trust.pool(),
			}, nil
		},
	}
}

// enrollTLS accepts connections without a client certificate, for enrollment.
func enrollTLS(state func() *tlsState) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			cert := state().server
			return &cert, nil
		},
	}
}
