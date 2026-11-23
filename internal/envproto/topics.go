package envproto

import (
	"regexp"
	"slices"
	"strings"
)

// Prefix starts every environment topic.
const Prefix = "ws/v1/env/"

// StreamSuffix ends the client id of an agent's stream connection (D11).
const StreamSuffix = ".t"

// Topic levels under an environment's id.
const (
	LeafStatus   = "status"
	LeafSnapshot = "snapshot"
	LeafReq      = "req"
	LeafRes      = "res"
	LeafUp       = "up"
	LeafStream   = "stream"
)

var validIDPattern = regexp.MustCompile(`^env-[0-9a-f]{8}$`)

// Topic is Prefix + id + "/" + strings.Join(levels, "/").
func Topic(id string, levels ...string) string {
	return Prefix + id + "/" + strings.Join(levels, "/")
}

// ParseTopic splits "ws/v1/env/<id>/<rest...>"; ok is false for any other
// topic, an id that isn't ValidID, or an empty rest. A "+" id is not valid.
func ParseTopic(topic string) (id string, rest []string, ok bool) {
	body, found := strings.CutPrefix(topic, Prefix)
	if !found {
		return "", nil, false
	}
	parts := strings.Split(body, "/")
	if len(parts) < 2 || !ValidID(parts[0]) {
		return "", nil, false
	}
	if slices.Contains(parts[1:], "") {
		return "", nil, false
	}
	return parts[0], parts[1:], true
}

// StreamClientID is the client id of environment id's stream connection:
// id + StreamSuffix.
func StreamClientID(id string) string {
	return id + StreamSuffix
}

// ValidID reports whether id matches ^env-[0-9a-f]{8}$.
func ValidID(id string) bool {
	return validIDPattern.MatchString(id)
}

// Match reports whether an MQTT topic filter matches topic: "+" is one
// level, "#" (last level only) is zero or more levels.
func Match(filter, topic string) bool {
	filterLevels := strings.Split(filter, "/")
	topicLevels := strings.Split(topic, "/")
	for i, level := range filterLevels {
		if level == "#" {
			return i == len(filterLevels)-1
		}
		if i >= len(topicLevels) {
			return false
		}
		if level != "+" && level != topicLevels[i] {
			return false
		}
	}
	return len(filterLevels) == len(topicLevels)
}
