package mqttclient

import "net"

// SetDialHook makes c call fn with the connection Connect dials.
func (c *Client) SetDialHook(fn func(net.Conn)) { c.dialHook = fn }
