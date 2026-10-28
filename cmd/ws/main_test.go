package main

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildVersion(t *testing.T) {
	t.Parallel()
	info := func(version string, settings ...debug.BuildSetting) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Version: version}, Settings: settings}, true
		}
	}
	rev := debug.BuildSetting{Key: "vcs.revision", Value: "f513b70aa6af6e60c47201d34fcf8c89b2328d89"}
	tests := []struct {
		name string
		set  string
		read func() (*debug.BuildInfo, bool)
		want string
	}{
		{name: "ldflags win", set: "v0.3.0-4-gdc313a2", read: info("v9"), want: "v0.3.0-4-gdc313a2"},
		{name: "go install's module version", set: "dev", read: info("v0.0.0-20261028-f513b70aa6af"), want: "v0.0.0-20261028-f513b70aa6af"},
		{name: "a local build's commit", set: "dev", read: info("(devel)", rev), want: "dev-f513b70"},
		{name: "a modified local build", set: "dev", read: info("(devel)", rev, debug.BuildSetting{Key: "vcs.modified", Value: "true"}), want: "dev-f513b70-dirty"},
		{name: "nothing recorded", set: "dev", read: info("(devel)"), want: "dev"},
		{name: "no build info", set: "dev", read: func() (*debug.BuildInfo, bool) { return nil, false }, want: "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, buildVersion(tt.set, tt.read))
		})
	}
}
