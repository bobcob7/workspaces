package cli

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/environment/envtest"
	"github.com/bobcob7/workspaces/internal/envproto"
)

// newEnvTestApp is newDaemonTestApp with the stub stores that a command's
// warnings read.
func newEnvTestApp(t *testing.T) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	app, stdout, stderr := newDaemonTestApp(t)
	s := app.newStores(app.paths, app.config)
	app.cache, app.workspaces, app.lifetime = s.cache, s.workspaces, s.lifetime
	return app, stdout, stderr
}

// withPublicName gives app an environments config that can issue codes.
func withPublicName(app *App) {
	app.config.Environments = config.Environments{PublicName: "ws.example.com"}
}

// enrollTestEnvironment adds an environment called name and enrolls it
// through the store, as an agent's enroll would.
func enrollTestEnvironment(t *testing.T, app *App) environment.Record {
	t.Helper()
	_, code, _, err := app.envStore().Add(t.Context(), "linux-server", "ws.example.com:8884")
	require.NoError(t, err)
	parsed, err := envproto.ParseCode(code)
	require.NoError(t, err)
	_, err = app.envStore().Enroll(t.Context(), parsed.Secret, testCSR(t), "ws.example.com:8883", "ws.example.com")
	require.NoError(t, err)
	rec, err := app.envStore().Get("linux-server")
	require.NoError(t, err)
	return rec
}

// testCSR is a PEM certificate request for a new P-256 key.
func testCSR(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "agent"}}, key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

func TestEnvError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want errorCode
	}{
		{"unknown environment", environment.ErrUnknown, codeEnvironmentUnknown},
		{"existing environment", environment.ErrExists, codeEnvironmentExists},
		{"invalid name", environment.ErrNameInvalid, codeNameInvalid},
		{"CA in use", environment.ErrCAInUse, codeCAInUse},
		{"no CA", environment.ErrNoCA, codeUsage},
		{"no rotation", environment.ErrNoRotation, codeUsage},
		{"no public name", config.ErrNoPublicName, codeUsage},
		{"enrollment used", environment.ErrEnrollmentUsed, codeEnrollmentUsed},
		{"enrollment failed", environment.ErrEnrollmentFailed, codeEnrollmentFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name+" maps to "+string(tc.want), func(t *testing.T) {
			ce, ok := errors.AsType[*cliError](envError(tc.err))
			require.True(t, ok)
			assert.Equal(t, tc.want, ce.code)
		})
	}
	t.Run("other errors are returned as they are", func(t *testing.T) {
		require.ErrorIs(t, envError(errTest), errTest)
		_, ok := errors.AsType[*cliError](envError(errTest))
		assert.False(t, ok)
	})
}

func TestApp_runEnvAdd(t *testing.T) {
	t.Run("without public_name is usage and writes nothing", func(t *testing.T) {
		app, _, _ := newEnvTestApp(t)
		err := app.runEnvAdd(t.Context(), "linux-server")
		ce, ok := errors.AsType[*cliError](err)
		require.True(t, ok)
		assert.Equal(t, codeUsage, ce.code)
		recs, err := app.envStore().List()
		require.NoError(t, err)
		assert.Empty(t, recs)
	})
	t.Run("prints a code that parses to the enroll host and the CA's fingerprint", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		withPublicName(app)
		require.NoError(t, app.runEnvAdd(t.Context(), "linux-server"))
		assert.Contains(t, stdout.String(), "Code (works once, shown once):")
		assert.Contains(t, stdout.String(), "ws env agent enroll ws-enroll-v1.")
		recs, err := app.envStore().List()
		require.NoError(t, err)
		require.Len(t, recs, 1)
		assert.Equal(t, environment.StatePending, recs[0].State)
	})
	t.Run("json has the code and the pending state", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		withPublicName(app)
		app.global.json = true
		require.NoError(t, app.runEnvAdd(t.Context(), "linux-server"))
		obj := decodeJSONObject(t, stdout)
		assert.Equal(t, "pending", obj["state"])
		assert.Equal(t, true, obj["ca_created"])
		code, ok := obj["code"].(string)
		require.True(t, ok)
		parsed, err := envproto.ParseCode(code)
		require.NoError(t, err)
		assert.Equal(t, "ws.example.com:8884", parsed.Host)
		assert.Contains(t, obj, "warnings")
	})
	t.Run("a name already taken is environment_exists", func(t *testing.T) {
		app, _, _ := newEnvTestApp(t)
		withPublicName(app)
		require.NoError(t, app.runEnvAdd(t.Context(), "linux-server"))
		err := app.runEnvAdd(t.Context(), "linux-server")
		ce, ok := errors.AsType[*cliError](err)
		require.True(t, ok)
		assert.Equal(t, codeEnvironmentExists, ce.code)
	})
	t.Run("the name local is name_invalid", func(t *testing.T) {
		app, _, _ := newEnvTestApp(t)
		withPublicName(app)
		err := app.runEnvAdd(t.Context(), "local")
		ce, ok := errors.AsType[*cliError](err)
		require.True(t, ok)
		assert.Equal(t, codeNameInvalid, ce.code)
	})
	t.Run("with no daemon it warns that remote environments can't connect", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		withPublicName(app)
		app.global.json = true
		require.NoError(t, app.runEnvAdd(t.Context(), "linux-server"))
		obj := decodeJSONObject(t, stdout)
		assert.Contains(t, obj["warnings"], noDaemonWarning)
	})
}

func TestApp_runEnvRemove(t *testing.T) {
	t.Run("an unknown name is environment_unknown", func(t *testing.T) {
		app, _, _ := newEnvTestApp(t)
		withPublicName(app)
		err := app.runEnvRemove(t.Context(), "nope")
		ce, ok := errors.AsType[*cliError](err)
		require.True(t, ok)
		assert.Equal(t, codeEnvironmentUnknown, ce.code)
	})
	t.Run("an enrolled environment's serial is revoked and listed", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		withPublicName(app)
		rec := enrollTestEnvironment(t, app)
		app.global.json = true
		require.NoError(t, app.runEnvRemove(t.Context(), "linux-server"))
		obj := decodeJSONObject(t, stdout)
		assert.Equal(t, []any{rec.CertSerial}, obj["revoked_serials"])
		_, err := app.envStore().Get("linux-server")
		assert.ErrorIs(t, err, environment.ErrUnknown)
	})
}

func TestApp_runEnvRotate(t *testing.T) {
	t.Run("gives a new code and revokes an enrolled environment's serial", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		withPublicName(app)
		before := enrollTestEnvironment(t, app)
		app.global.json = true
		require.NoError(t, app.runEnvRotate(t.Context(), "linux-server"))
		obj := decodeJSONObject(t, stdout)
		code, ok := obj["code"].(string)
		require.True(t, ok)
		assert.Equal(t, "pending", obj["state"])
		assert.Equal(t, []any{before.CertSerial}, obj["revoked_serials"])
		after, err := app.envStore().Get("linux-server")
		require.NoError(t, err)
		assert.Equal(t, environment.StatePending, after.State)
		_, err = envproto.ParseCode(code)
		require.NoError(t, err)
	})
	t.Run("an unknown name is environment_unknown", func(t *testing.T) {
		app, _, _ := newEnvTestApp(t)
		withPublicName(app)
		err := app.runEnvRotate(t.Context(), "nope")
		ce, ok := errors.AsType[*cliError](err)
		require.True(t, ok)
		assert.Equal(t, codeEnvironmentUnknown, ce.code)
	})
}

func TestApp_runEnvList(t *testing.T) {
	t.Run("lists a pending environment as unknown with a warning when no daemon runs", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		withPublicName(app)
		require.NoError(t, app.runEnvAdd(t.Context(), "linux-server"))
		stdout.Reset()
		app.global.json = true
		require.NoError(t, app.runEnvList(t.Context()))
		obj := decodeJSONObject(t, stdout)
		envs, ok := obj["environments"].([]any)
		require.True(t, ok)
		require.Len(t, envs, 1)
		env, ok := envs[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "unknown", env["status"])
		assert.Nil(t, env["last_seen"])
		assert.Nil(t, env["workspaces"])
		assert.Contains(t, obj["warnings"], noDaemonWarning)
	})
	t.Run("reports the workspaces in a stored snapshot", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		withPublicName(app)
		enrollTestEnvironment(t, app)
		snapshot, err := json.Marshal(envproto.Snapshot{Workspaces: json.RawMessage(`[{"id":"a"},{"id":"b"}]`)})
		require.NoError(t, err)
		require.NoError(t, app.envStore().WriteSnapshot("linux-server", snapshot))
		app.global.json = true
		require.NoError(t, app.runEnvList(t.Context()))
		obj := decodeJSONObject(t, stdout)
		envs, ok := obj["environments"].([]any)
		require.True(t, ok)
		env, ok := envs[0].(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 2, env["workspaces"], 0)
		assert.Equal(t, "current", env["cert_ca"])
	})
	t.Run("takes the status from a running daemon", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		app.newEnvBroker = func(*slog.Logger) (envBroker, error) { return envtest.NewBroker(), nil }
		withPublicName(app)
		require.NoError(t, app.runEnvAdd(t.Context(), "linux-server"))
		app.config.Environments.Listen, app.config.Environments.EnrollListen = "127.0.0.1:0", "127.0.0.1:0"
		serveEnvDaemon(t, app)
		stdout.Reset()
		require.NoError(t, app.runEnvList(t.Context()))
		assert.Contains(t, stdout.String(), "linux-server")
		assert.Contains(t, stdout.String(), "pending")
		assert.NotContains(t, stdout.String(), "no ws daemon runs")
	})
	t.Run("prints a header even with no environments", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		require.NoError(t, app.runEnvList(t.Context()))
		assert.Equal(t, "no environments\n", stdout.String())
	})
}
