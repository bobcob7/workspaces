package environment

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"

	"github.com/bobcob7/workspaces/internal/privfile"
)

// writeFileAtomic writes data to a temp file in path's directory with
// perm, syncs, and renames it into place.
func writeFileAtomic(path string, data []byte, perm fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", path, err)
	}
	if err := writeSynced(tmp, data, perm); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("renaming into %s: %w", path, err)
	}
	if err := privfile.RestrictFile(path); err != nil {
		return fmt.Errorf("protecting %s: %w", path, err)
	}
	return nil
}

// writeSynced sets perm, writes and syncs f, then closes it. CreateTemp
// makes files 0600, so the mode is set explicitly.
func writeSynced(f *os.File, data []byte, perm fs.FileMode) error {
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func encodeCert(cert *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
}

func encodeKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshaling private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func readCert(path string) (*x509.Certificate, error) {
	der, err := readPEM(path, "CERTIFICATE")
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parsing certificate %s: %w", path, err)
	}
	return cert, nil
}

func readKey(path string) (*ecdsa.PrivateKey, error) {
	der, err := readPEM(path, "PRIVATE KEY")
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("parsing private key %s: %w", path, err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key %s is not ECDSA: %w", path, errKeyType)
	}
	return key, nil
}

// readPEM returns the DER of the single PEM block of type blockType in
// path. A missing file wraps fs.ErrNotExist.
func readPEM(path, blockType string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != blockType {
		return nil, fmt.Errorf("reading %s: no %s block", path, blockType)
	}
	return block.Bytes, nil
}

// randomSerial returns 128 random bits as a positive integer.
func randomSerial(random io.Reader) (*big.Int, error) {
	buf := make([]byte, 16)
	for {
		if _, err := io.ReadFull(random, buf); err != nil {
			return nil, fmt.Errorf("reading serial number: %w", err)
		}
		if serial := new(big.Int).SetBytes(buf); serial.Sign() > 0 {
			return serial, nil
		}
	}
}

var errKeyType = errors.New("key is not ECDSA")
