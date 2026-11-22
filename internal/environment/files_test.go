package environment

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteFileAtomic(t *testing.T) {
	t.Run("writes data with the given mode", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "ca.key")
		require.NoError(t, writeFileAtomic(path, []byte("secret"), 0o600))
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "secret", string(data))
		assertMode(t, path, 0o600)
	})
	t.Run("replaces an existing file and leaves no temp file behind", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "ca.crt")
		require.NoError(t, writeFileAtomic(path, []byte("old"), 0o644))
		require.NoError(t, writeFileAtomic(path, []byte("new"), 0o644))
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Equal(t, "new", string(data))
		assert.Len(t, entries, 1)
	})
	t.Run("fails when the directory is missing", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "missing", "ca.key")
		assert.Error(t, writeFileAtomic(path, []byte("x"), 0o600))
	})
}

func TestEncodeCert(t *testing.T) {
	t.Run("reads back through readCert", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		ca, err := a.currentCert()
		require.NoError(t, err)
		path := filepath.Join(t.TempDir(), "ca.crt")
		require.NoError(t, os.WriteFile(path, encodeCert(ca), 0o644))
		got, err := readCert(path)
		require.NoError(t, err)
		assert.Equal(t, ca.Raw, got.Raw)
	})
}

func TestEncodeKey(t *testing.T) {
	t.Run("reads back through readKey", func(t *testing.T) {
		t.Parallel()
		key := newTestKey(t)
		keyPEM, err := encodeKey(key)
		require.NoError(t, err)
		path := filepath.Join(t.TempDir(), "ca.key")
		require.NoError(t, os.WriteFile(path, keyPEM, 0o600))
		got, err := readKey(path)
		require.NoError(t, err)
		assert.True(t, key.Equal(got))
	})
}

func TestReadCert(t *testing.T) {
	t.Run("wraps fs.ErrNotExist for a missing file", func(t *testing.T) {
		t.Parallel()
		_, err := readCert(filepath.Join(t.TempDir(), "missing.crt"))
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
	t.Run("refuses a file without a certificate block", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "bad.crt")
		require.NoError(t, os.WriteFile(path, []byte("not pem"), 0o644))
		_, err := readCert(path)
		assert.Error(t, err)
	})
	t.Run("refuses a certificate block that does not parse", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "bad.crt")
		block := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("junk")})
		require.NoError(t, os.WriteFile(path, block, 0o644))
		_, err := readCert(path)
		assert.Error(t, err)
	})
}

func TestReadKey(t *testing.T) {
	t.Run("wraps fs.ErrNotExist for a missing file", func(t *testing.T) {
		t.Parallel()
		_, err := readKey(filepath.Join(t.TempDir(), "missing.key"))
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
	t.Run("refuses a file without a key block", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "bad.key")
		require.NoError(t, os.WriteFile(path, []byte("not pem"), 0o600))
		_, err := readKey(path)
		assert.Error(t, err)
	})
	t.Run("refuses a key that is not ECDSA", func(t *testing.T) {
		t.Parallel()
		rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		keyPEM, err := encodeRSAKey(rsaKey)
		require.NoError(t, err)
		path := filepath.Join(t.TempDir(), "rsa.key")
		require.NoError(t, os.WriteFile(path, keyPEM, 0o600))
		_, err = readKey(path)
		assert.ErrorIs(t, err, errKeyType)
	})
	t.Run("refuses a key block that does not parse", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "bad.key")
		block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("junk")})
		require.NoError(t, os.WriteFile(path, block, 0o600))
		_, err := readKey(path)
		assert.Error(t, err)
	})
}

func encodeRSAKey(key *rsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func TestRandomSerial(t *testing.T) {
	t.Run("skips an all-zero read and returns a positive 128-bit value", func(t *testing.T) {
		ones := bytes.Repeat([]byte{0x01}, 16)
		serial, err := randomSerial(bytes.NewReader(append(make([]byte, 16), ones...)))
		require.NoError(t, err)
		assert.Equal(t, new(big.Int).SetBytes(ones), serial)
		assert.Equal(t, 1, serial.Sign())
		assert.LessOrEqual(t, serial.BitLen(), 128)
	})
	t.Run("returns the read error", func(t *testing.T) {
		_, err := randomSerial(iotest.ErrReader(errors.New("boom")))
		assert.Error(t, err)
	})
}
