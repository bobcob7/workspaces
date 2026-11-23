package daemon

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNopEnvironments_Status(t *testing.T) {
	t.Run("reports no listeners and an empty list, not null", func(t *testing.T) {
		st := NopEnvironments{}.Status()
		assert.False(t, st.Listening)
		assert.Empty(t, st.Listen)
		require.NotNil(t, st.Environments)
		assert.Empty(t, st.Environments)
	})
}

func TestNopEnvironments_Warnings(t *testing.T) {
	t.Run("has no warnings without a reason", func(t *testing.T) {
		assert.Equal(t, []string{}, NopEnvironments{}.Warnings())
	})
	t.Run("its reason is its one warning", func(t *testing.T) {
		assert.Equal(t, []string{"no broker"}, NopEnvironments{Reason: "no broker"}.Warnings())
	})
}

func TestNopEnvironments_lifecycle(t *testing.T) {
	t.Run("start, reload and close do nothing", func(t *testing.T) {
		var envs NopEnvironments
		require.NoError(t, envs.Start(t.Context()))
		require.NoError(t, envs.Reload(t.Context()))
		require.NoError(t, envs.Close())
	})
}

func TestNopEnvironments_Call(t *testing.T) {
	t.Run("is offline, not listening", func(t *testing.T) {
		_, err := NopEnvironments{}.Call(t.Context(), "linux", CallRequest{Op: "workspace.list", RequestID: "0123456789abcdef"})
		require.ErrorIs(t, err, ErrEnvironmentOffline)
		hostErr, ok := errors.AsType[*HostError](err)
		require.True(t, ok)
		assert.Equal(t, "not_listening", hostErr.Details["reason"])
		assert.Equal(t, "linux", hostErr.Details["environment"])
	})
}
