// Package power tells whether the machine is fully awake, so ws continue
// doesn't start an agent's turn during a macOS dark wake, which ends
// within a minute (ADR 0047). It is the only package that runs ioreg.
package power

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Reader reads the machine's power state.
type Reader struct{}

// Awake reports whether the machine is fully awake: on macOS, whether it
// is in a full wake rather than a dark wake; elsewhere, always.
func (Reader) Awake(ctx context.Context) (bool, error) {
	return awake(ctx)
}

// capabilityGraphics is IOKit's kIOPMSystemCapabilityGraphics: set in a
// full wake, clear in a dark wake (ADR 0047).
const capabilityGraphics = 0x2

// capabilitiesKey is IOPMrootDomain's property that holds the system's
// power capabilities.
const capabilitiesKey = "System Capabilities"

// errNoCapabilities means ioreg's output had no System Capabilities.
var errNoCapabilities = errors.New("ioreg printed no " + capabilitiesKey)

// awakeFrom reads `ioreg -a`'s property list and reports whether its
// System Capabilities has the graphics bit.
func awakeFrom(plist []byte) (bool, error) {
	caps, err := capabilities(plist)
	if err != nil {
		return false, err
	}
	return caps&capabilityGraphics != 0, nil
}

// capabilities finds the integer after the System Capabilities key in a
// property list.
func capabilities(plist []byte) (int64, error) {
	dec := xml.NewDecoder(bytes.NewReader(plist))
	dec.Strict = false
	afterKey := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return 0, errNoCapabilities
		}
		if err != nil {
			return 0, fmt.Errorf("reading ioreg's output: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "key":
			var key string
			if err := dec.DecodeElement(&key, &start); err != nil {
				return 0, fmt.Errorf("reading ioreg's output: %w", err)
			}
			afterKey = key == capabilitiesKey
		case "integer":
			if afterKey {
				var value string
				if err := dec.DecodeElement(&value, &start); err != nil {
					return 0, fmt.Errorf("reading ioreg's output: %w", err)
				}
				return strconv.ParseInt(strings.TrimSpace(value), 0, 64)
			}
			afterKey = false
		default:
			afterKey = false
		}
	}
}
