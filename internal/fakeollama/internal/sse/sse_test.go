package sse

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errBoom = errors.New("boom")

func readAll(r *Reader) []string {
	var out []string
	for r.Next() {
		out = append(out, string(r.Data()))
	}
	return out
}

func TestNewWriter(t *testing.T) {
	t.Run("sets the event stream content type", func(t *testing.T) {
		rec := httptest.NewRecorder()
		NewWriter(rec)
		assert.Equal(t, ContentType, rec.Header().Get("Content-Type"))
	})
}

func TestWriter_Data(t *testing.T) {
	t.Run("writes and flushes a data event", func(t *testing.T) {
		rec := httptest.NewRecorder()
		require.NoError(t, NewWriter(rec).Data([]byte(`{"a":1}`)))
		assert.Equal(t, "data: {\"a\":1}\n\n", rec.Body.String())
		assert.True(t, rec.Flushed)
	})
	t.Run("reports write failures without flushing", func(t *testing.T) {
		f := &flusherMock{}
		w := &Writer{w: &bodyWriterMock{WriteFunc: func([]byte) (int, error) { return 0, errBoom }}, f: f}
		err := w.Data([]byte("x"))
		require.ErrorIs(t, err, errBoom)
		require.ErrorContains(t, err, "writing event")
		assert.Empty(t, f.FlushCalls())
	})
	t.Run("reports flush failures", func(t *testing.T) {
		w := &Writer{
			w: &bodyWriterMock{WriteFunc: func(p []byte) (int, error) { return len(p), nil }},
			f: &flusherMock{FlushFunc: func() error { return http.ErrNotSupported }},
		}
		err := w.Data([]byte("x"))
		require.ErrorIs(t, err, http.ErrNotSupported)
		assert.ErrorContains(t, err, "flushing event")
	})
}

func TestWriter_JSON(t *testing.T) {
	t.Run("encodes the value as one event", func(t *testing.T) {
		rec := httptest.NewRecorder()
		require.NoError(t, NewWriter(rec).JSON(map[string]int{"a": 1}))
		assert.Equal(t, "data: {\"a\":1}\n\n", rec.Body.String())
	})
	t.Run("reports encoding failures", func(t *testing.T) {
		rec := httptest.NewRecorder()
		assert.ErrorContains(t, NewWriter(rec).JSON(make(chan int)), "encoding event")
	})
}

func TestWriter_Done(t *testing.T) {
	t.Run("writes the done event", func(t *testing.T) {
		rec := httptest.NewRecorder()
		require.NoError(t, NewWriter(rec).Done())
		assert.Equal(t, "data: [DONE]\n\n", rec.Body.String())
	})
}

func TestReader_Next(t *testing.T) {
	t.Run("reads each payload and stops at done", func(t *testing.T) {
		r := NewReader(strings.NewReader("data: a\n\ndata:b\n\ndata: [DONE]\n\ndata: after\n\n"))
		assert.Equal(t, []string{"a", "b"}, readAll(r))
		assert.True(t, r.Done())
		assert.False(t, r.Next())
	})
	t.Run("joins multi-line data and ignores other fields", func(t *testing.T) {
		r := NewReader(strings.NewReader(": comment\nevent: x\ndata: one\r\ndata: two\n\n"))
		assert.Equal(t, []string{"one\ntwo"}, readAll(r))
		assert.False(t, r.Done())
	})
	t.Run("skips blank lines between events", func(t *testing.T) {
		r := NewReader(strings.NewReader("\n\ndata: a\n\n\n"))
		assert.Equal(t, []string{"a"}, readAll(r))
	})
	t.Run("discards an event cut off by the end of the stream", func(t *testing.T) {
		r := NewReader(strings.NewReader("data: a\n\ndata: partial"))
		assert.Equal(t, []string{"a"}, readAll(r))
		assert.False(t, r.Done())
		assert.NoError(t, r.Err())
	})
	t.Run("reports read errors", func(t *testing.T) {
		r := NewReader(io.MultiReader(strings.NewReader("data: a\n\n"), iotest.ErrReader(errBoom)))
		assert.Equal(t, []string{"a"}, readAll(r))
		assert.ErrorIs(t, r.Err(), errBoom)
	})
}

func TestDoneWatcher_Write(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"with space", "data: {}\n\ndata: [DONE]\n\n", true},
		{"without space", "data: {}\n\ndata:[DONE]\n\n", true},
		{"crlf", "data: [DONE]\r\n\r\n", true},
		{"done inside a payload", "data: {\"text\":\"[DONE]\"}\n\n", false},
		{"missing", "data: {}\n\n", false},
		{"unterminated", "data: [DONE]", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d DoneWatcher
			_, err := io.Copy(&d, iotest.OneByteReader(strings.NewReader(tt.body)))
			require.NoError(t, err)
			assert.Equal(t, tt.want, d.Seen())
		})
	}
}
