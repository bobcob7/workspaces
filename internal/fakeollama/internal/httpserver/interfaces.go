package httpserver

import "net"

//go:generate go tool moq -out moq_test.go . listener

// listener is the net.Listener a Server accepts connections from.
type listener interface {
	net.Listener
}
