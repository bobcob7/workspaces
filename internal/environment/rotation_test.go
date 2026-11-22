package environment

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
)

func TestStore_CAStatus(t *testing.T) {
	t.Run("has no CA and no warnings before the first add", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		status, err := store.CAStatus(30)
		require.NoError(t, err)
		assert.Nil(t, status.Current)
		assert.Nil(t, status.Old)
		assert.Equal(t, []string{}, status.Warnings)
	})
	t.Run("counts enrolled environments on the current CA", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		enrolledEnvironment(t, store)
		status, err := store.CAStatus(30)
		require.NoError(t, err)
		require.NotNil(t, status.Current)
		assert.Equal(t, 1, status.OnCurrent)
		assert.Equal(t, 0, status.OnOld)
	})
	t.Run("warns when the CA expires within renewBeforeDays", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		addEnvironment(t, store, "linux-server")
		status, err := store.CAStatus(400)
		require.NoError(t, err)
		assert.Len(t, status.Warnings, 1)
	})
}

func TestStore_RotateCA(t *testing.T) {
	t.Run("is ErrNoCA without a CA", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		_, err := store.RotateCA(t.Context())
		require.ErrorIs(t, err, ErrNoCA)
	})
	t.Run("makes the old CA the previous one", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		addEnvironment(t, store, "linux-server")
		before, err := store.currentCert()
		require.NoError(t, err)
		rotation, err := store.RotateCA(t.Context())
		require.NoError(t, err)
		require.NotNil(t, rotation.Current)
		require.NotNil(t, rotation.Old)
		assert.Equal(t, envproto.Fingerprint(before), rotation.Old.Fingerprint)
		assert.NotEqual(t, rotation.Old.Fingerprint, rotation.Current.Fingerprint)
		assert.Nil(t, rotation.Dropped)
		assert.Equal(t, []string{}, rotation.Stranded)
	})
	t.Run("drops the root from the previous rotation and lists who holds it", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		enrolled := enrolledEnvironment(t, store)
		_, err := store.RotateCA(t.Context())
		require.NoError(t, err)
		rotation, err := store.RotateCA(t.Context())
		require.NoError(t, err)
		require.NotNil(t, rotation.Dropped)
		assert.Equal(t, enrolled.CertCA, rotation.Dropped.Fingerprint)
		assert.Equal(t, []string{"linux-server"}, rotation.Stranded)
	})
}

func TestStore_FinishCA(t *testing.T) {
	t.Run("is ErrNoRotation without an old CA", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		addEnvironment(t, store, "linux-server")
		_, err := store.FinishCA(t.Context(), false)
		require.ErrorIs(t, err, ErrNoRotation)
	})
	t.Run("is ErrCAInUse while environments hold the old CA", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		enrolledEnvironment(t, store)
		_, err := store.RotateCA(t.Context())
		require.NoError(t, err)
		_, err = store.FinishCA(t.Context(), false)
		require.ErrorIs(t, err, ErrCAInUse)
		assert.Contains(t, err.Error(), "1 environments")
	})
	t.Run("succeeds once each environment has renewed onto the new CA", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		enrolled := enrolledEnvironment(t, store)
		_, err := store.RotateCA(t.Context())
		require.NoError(t, err)
		status, err := store.CAStatus(30)
		require.NoError(t, err)
		assert.Equal(t, 1, status.OnOld)
		_, err = store.Renew(t.Context(), enrolled.ID, newTestCSR(t, newTestKey(t)))
		require.NoError(t, err)
		status, err = store.CAStatus(30)
		require.NoError(t, err)
		assert.Equal(t, 0, status.OnOld)
		rotation, err := store.FinishCA(t.Context(), false)
		require.NoError(t, err)
		assert.Nil(t, rotation.Old)
		require.NotNil(t, rotation.Dropped)
		assert.Equal(t, []string{}, rotation.Stranded)
		old, err := store.oldCert()
		require.NoError(t, err)
		assert.Nil(t, old)
	})
	t.Run("force drops the old CA and lists the stranded environments", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		enrolledEnvironment(t, store)
		_, err := store.RotateCA(t.Context())
		require.NoError(t, err)
		rotation, err := store.FinishCA(t.Context(), true)
		require.NoError(t, err)
		assert.Equal(t, []string{"linux-server"}, rotation.Stranded)
		require.NotNil(t, rotation.Dropped)
	})
	t.Run("a code added after RotateCA carries the new CA's fingerprint", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		addEnvironment(t, store, "linux-server")
		_, err := store.RotateCA(t.Context())
		require.NoError(t, err)
		_, code, _, err := store.Add(t.Context(), "second", testBroker)
		require.NoError(t, err)
		parsed, err := envproto.ParseCode(code)
		require.NoError(t, err)
		current, err := store.currentCert()
		require.NoError(t, err)
		assert.Equal(t, envproto.Fingerprint(current), parsed.Fingerprint)
	})
}
