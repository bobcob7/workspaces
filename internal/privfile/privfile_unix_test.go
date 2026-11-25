//go:build unix

package privfile

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRestrictDir(t *testing.T) {
	assert.NoError(t, RestrictDir(t.TempDir()))
}

func TestRestrictFile(t *testing.T) {
	assert.NoError(t, RestrictFile(t.TempDir()+"/file"))
}

func TestCheckPrivate(t *testing.T) {
	assert.NoError(t, CheckPrivate(t.TempDir()))
}
