package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordResultTool(t *testing.T) {
	cfg := testConfig(t)
	record := recordResultTool(&cfg)
	t.Run("requires a report", func(t *testing.T) {
		_, err := record.Call(t.Context(), `{"verdict":"PASS"}`)
		require.EqualError(t, err, "missing report")
	})
	t.Run("rejects a verdict that is not a string", func(t *testing.T) {
		_, err := record.Call(t.Context(), `{"verdict":true,"report":"r"}`)
		require.ErrorContains(t, err, "arguments:")
	})
}

func TestRecordResult(t *testing.T) {
	t.Run("records a verdict and ends the run", func(t *testing.T) {
		cfg := testConfig(t)
		var gotVerdict string
		var gotReport []byte
		recorder := &recorderMock{RecordFunc: func(_ context.Context, verdict string, report []byte) error {
			gotVerdict, gotReport = verdict, report
			return nil
		}}
		cfg.Recorder = recorder
		res, err := recordResult(t.Context(), &cfg, recordResultArgs{Verdict: "pass", Report: "# ok"})
		require.NoError(t, err)
		assert.True(t, res.Done)
		assert.Equal(t, "PASS", res.Verdict)
		assert.Equal(t, "PASS", gotVerdict)
		assert.Equal(t, "# ok", string(gotReport))
		assert.Len(t, recorder.RecordCalls(), 1)
	})
	t.Run("rejects a verdict that is not allowed", func(t *testing.T) {
		cfg := testConfig(t)
		recorder := &recorderMock{RecordFunc: func(context.Context, string, []byte) error { return nil }}
		cfg.Recorder = recorder
		_, err := recordResult(t.Context(), &cfg, recordResultArgs{Verdict: "MAYBE", Report: "?"})
		require.EqualError(t, err, "verdict MAYBE is not one of PASS, FAIL")
		assert.Empty(t, recorder.RecordCalls())
	})
	t.Run("rejects a malformed verdict", func(t *testing.T) {
		_, err := recordResult(t.Context(), new(testConfig(t)), recordResultArgs{Verdict: "1-2", Report: "?"})
		require.ErrorContains(t, err, "invalid machine record")
	})
	t.Run("returns a recorder failure", func(t *testing.T) {
		cfg := testConfig(t)
		cfg.Recorder = &recorderMock{RecordFunc: func(context.Context, string, []byte) error { return errors.New("disk") }}
		_, err := recordResult(t.Context(), &cfg, recordResultArgs{Verdict: "FAIL", Report: "r"})
		require.ErrorContains(t, err, "recording FAIL: disk")
	})
	t.Run("requires a verdict and a report", func(t *testing.T) {
		_, err := recordResult(t.Context(), new(testConfig(t)), recordResultArgs{Report: "r"})
		require.EqualError(t, err, "missing verdict")
		_, err = recordResult(t.Context(), new(testConfig(t)), recordResultArgs{Verdict: "PASS"})
		require.EqualError(t, err, "missing report")
	})
}
