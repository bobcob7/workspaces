package cache

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bobcob7/workspaces/internal/repo"
)

func TestAmbiguousError_Error(t *testing.T) {
	err := &AmbiguousError{Spec: "bear", Candidates: []repo.ID{
		{Host: "github.com", Owner: "a", Name: "bear"},
		{Host: "github.com", Owner: "b", Name: "bear"},
	}}
	assert.Equal(t, `"bear" matches several cached repos: github.com/a/bear, github.com/b/bear; name one with owner/name or host/owner/name`, err.Error())
}
