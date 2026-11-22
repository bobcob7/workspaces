package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// interruptRotation leaves the CA as a rotation does when it stops after the
// new key is installed and before its certificate is: ca.crt is absent and
// ca.crt.new holds the certificate. It returns the fingerprint of that new CA.
func interruptRotation(t *testing.T, app *App) string {
	t.Helper()
	rot, err := app.envStore().RotateCA(t.Context())
	require.NoError(t, err)
	caDir := filepath.Join(app.paths.Environments, "ca")
	require.NoError(t, os.Rename(filepath.Join(caDir, "ca.crt"), filepath.Join(caDir, "ca.crt.new")))
	return rot.Current.Fingerprint
}

// requireEnvError fails unless err is a cliError with code want.
func requireEnvError(t *testing.T, err error, want errorCode) {
	t.Helper()
	ce, ok := errors.AsType[*cliError](err)
	require.True(t, ok, "error %v has no code", err)
	require.Equal(t, want, ce.code)
}

func TestApp_runEnvCARotate(t *testing.T) {
	t.Run("--force without --finish is usage", func(t *testing.T) {
		app, _, _ := newEnvTestApp(t)
		requireEnvError(t, app.runEnvCARotate(t.Context(), envCARotateOptions{force: true}), codeUsage)
	})
	t.Run("without a CA is usage and creates none", func(t *testing.T) {
		app, _, _ := newEnvTestApp(t)
		requireEnvError(t, app.runEnvCARotate(t.Context(), envCARotateOptions{}), codeUsage)
		status, err := app.envStore().CAStatus(30)
		require.NoError(t, err)
		assert.Nil(t, status.Current)
	})
	t.Run("keeps the old CA trusted beside the new one", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		withPublicName(app)
		enrollTestEnvironment(t, app)
		before, err := app.envStore().CAStatus(30)
		require.NoError(t, err)
		require.NotNil(t, before.Current)
		app.global.json = true
		require.NoError(t, app.runEnvCARotate(t.Context(), envCARotateOptions{}))
		obj := decodeJSONObject(t, stdout)
		old, ok := obj["old"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, before.Current.Fingerprint, old["fingerprint"])
		assert.NotNil(t, obj["current"])
		assert.Equal(t, []any{}, obj["stranded"])
	})
	t.Run("--finish with an environment still on the old CA is ca_in_use", func(t *testing.T) {
		app, _, _ := newEnvTestApp(t)
		withPublicName(app)
		enrollTestEnvironment(t, app)
		require.NoError(t, app.runEnvCARotate(t.Context(), envCARotateOptions{}))
		requireEnvError(t, app.runEnvCARotate(t.Context(), envCARotateOptions{finish: true}), codeCAInUse)
	})
	t.Run("--finish --force drops the old CA and lists the stranded environment", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		withPublicName(app)
		enrollTestEnvironment(t, app)
		require.NoError(t, app.runEnvCARotate(t.Context(), envCARotateOptions{}))
		stdout.Reset()
		app.global.json = true
		require.NoError(t, app.runEnvCARotate(t.Context(), envCARotateOptions{finish: true, force: true}))
		obj := decodeJSONObject(t, stdout)
		assert.Equal(t, []any{"linux-server"}, obj["stranded"])
		assert.Nil(t, obj["old"])
		assert.NotNil(t, obj["dropped"])
	})
	t.Run("--finish without a rotation is usage", func(t *testing.T) {
		app, _, _ := newEnvTestApp(t)
		withPublicName(app)
		enrollTestEnvironment(t, app)
		requireEnvError(t, app.runEnvCARotate(t.Context(), envCARotateOptions{finish: true}), codeUsage)
	})
	t.Run("rotates after finishing a rotation an interruption left unfinished", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		withPublicName(app)
		enrollTestEnvironment(t, app)
		interrupted := interruptRotation(t, app)
		app.global.json = true
		require.NoError(t, app.runEnvCARotate(t.Context(), envCARotateOptions{}))
		obj := decodeJSONObject(t, stdout)
		old, ok := obj["old"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, interrupted, old["fingerprint"])
		assert.Contains(t, obj["warnings"], "finished a CA rotation that an interruption had left unfinished")
	})
	t.Run("a running daemon is asked to reload and no warning is given", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		withPublicName(app)
		enrollTestEnvironment(t, app)
		host := serveRecordingEnvDaemon(t, app)
		app.global.json = true
		require.NoError(t, app.runEnvCARotate(t.Context(), envCARotateOptions{}))
		assert.Equal(t, int32(1), host.reloads.Load())
		obj := decodeJSONObject(t, stdout)
		assert.Empty(t, obj["warnings"])
	})
}

func TestApp_runEnvCAStatus(t *testing.T) {
	t.Run("says there is no CA yet and creates none", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		require.NoError(t, app.runEnvCAStatus(t.Context()))
		assert.Equal(t, "no environments CA yet; ws env add creates one\n", stdout.String())
		status, err := app.envStore().CAStatus(30)
		require.NoError(t, err)
		assert.Nil(t, status.Current)
	})
	t.Run("counts the environments on each CA after a rotation", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		withPublicName(app)
		enrollTestEnvironment(t, app)
		require.NoError(t, app.runEnvCARotate(t.Context(), envCARotateOptions{}))
		stdout.Reset()
		app.global.json = true
		require.NoError(t, app.runEnvCAStatus(t.Context()))
		obj := decodeJSONObject(t, stdout)
		assert.InDelta(t, 0, obj["environments_on_current"], 0)
		assert.InDelta(t, 1, obj["environments_on_old"], 0)
	})
	t.Run("the text names the old CA and how many environments still use it", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		withPublicName(app)
		enrollTestEnvironment(t, app)
		require.NoError(t, app.runEnvCARotate(t.Context(), envCARotateOptions{}))
		stdout.Reset()
		require.NoError(t, app.runEnvCAStatus(t.Context()))
		assert.Contains(t, stdout.String(), "current CA ")
		assert.Contains(t, stdout.String(), "; old CA ")
		assert.Contains(t, stdout.String(), "(1 environment still on it)")
	})
	t.Run("finishes an interrupted rotation before reading the CA", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		withPublicName(app)
		enrollTestEnvironment(t, app)
		interrupted := interruptRotation(t, app)
		app.global.json = true
		require.NoError(t, app.runEnvCAStatus(t.Context()))
		obj := decodeJSONObject(t, stdout)
		current, ok := obj["current"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, interrupted, current["fingerprint"])
		assert.Contains(t, obj["warnings"], "finished a CA rotation that an interruption had left unfinished")
	})
}
