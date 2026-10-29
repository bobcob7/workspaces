package power

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rootDomain is `ioreg -r -n IOPMrootDomain -d 1 -a` as macOS 26.6.2 prints
// it, cut down, with System Capabilities caps. A nested dictionary before
// it has a key of the same name, which isn't the root domain's.
func rootDomain(caps string) []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<array>
	<dict>
		<key>IOPowerManagement</key>
		<dict>
			<key>CapabilityFlags</key>
			<integer>2</integer>
		</dict>
		<key>Last Sleep Reason</key>
		<string>Maintenance Sleep</string>
		<key>SleepStatistics</key>
		<array>
			<dict>
				<key>System Capabilities</key>
				<string>not this one</string>
			</dict>
		</array>
		<key>System Capabilities</key>
		<integer>%s</integer>
		<key>Wake Type</key>
		<string>UserActivity Assertion</string>
	</dict>
</array>
</plist>
`, caps))
}

func TestAwakeFrom(t *testing.T) {
	t.Parallel()
	t.Run("a full wake has the graphics bit", func(t *testing.T) {
		awake, err := awakeFrom(rootDomain("15"))
		require.NoError(t, err)
		assert.True(t, awake)
	})
	t.Run("reads ioreg's real output from a full wake", func(t *testing.T) {
		t.Parallel()
		plist, err := os.ReadFile("testdata/full-wake.plist")
		require.NoError(t, err)
		awake, err := awakeFrom(plist)
		require.NoError(t, err)
		assert.True(t, awake)
	})
	t.Run("a dark wake has CPU and network only", func(t *testing.T) {
		awake, err := awakeFrom(rootDomain("9"))
		require.NoError(t, err)
		assert.False(t, awake)
	})
	t.Run("no System Capabilities is an error", func(t *testing.T) {
		_, err := awakeFrom([]byte(`<plist><array><dict><key>Wake Type</key><string>x</string></dict></array></plist>`))
		require.ErrorIs(t, err, errNoCapabilities)
	})
	t.Run("a value that isn't a number is an error", func(t *testing.T) {
		_, err := awakeFrom(rootDomain("lots"))
		require.Error(t, err)
	})
	t.Run("output that isn't XML is an error", func(t *testing.T) {
		_, err := awakeFrom([]byte("<plist><array><dict><key>System"))
		require.Error(t, err)
	})
}

func TestReader_Awake(t *testing.T) {
	t.Parallel()
	_, err := Reader{}.Awake(t.Context())
	require.NoError(t, err)
}
