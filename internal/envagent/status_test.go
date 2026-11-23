package envagent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusOf(t *testing.T) {
	t.Run("reports not enrolled without an error when nothing is there", func(t *testing.T) {
		status, err := StatusOf(NewFiles(t.TempDir()), 30)
		require.NoError(t, err)
		assert.False(t, status.Enrolled)
		assert.Equal(t, []string{}, status.CAFingerprints)
	})
	t.Run("reports the enrolled identity and its renewal point", func(t *testing.T) {
		files, id := savedFiles(t)
		status, err := StatusOf(files, 30)
		require.NoError(t, err)
		assert.True(t, status.Enrolled)
		assert.Equal(t, id.ID, status.ID)
		assert.Equal(t, id.Name, status.Name)
		assert.Equal(t, id.Broker, status.Broker)
		assert.Equal(t, id.Cert.SerialNumber.Text(16), status.CertSerial)
		assert.True(t, id.Cert.NotAfter.Equal(status.CertNotAfter))
		assert.True(t, id.Cert.NotAfter.AddDate(0, 0, -30).Equal(status.RenewAt))
		assert.Equal(t, id.Fingerprints(), status.CAFingerprints)
	})
}
