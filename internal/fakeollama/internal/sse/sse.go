// Package sse writes and reads the server-sent event streams used by
// streamed OpenAI chat completions: data-only events ending with a [DONE]
// sentinel.
//
// Copied from llm-router internal/sse @9789c32 (ADR 0067); maintained here.
package sse

import (
	"bufio"
	"bytes"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
)

const (
	// ContentType is the media type of an event stream.
	ContentType = "text/event-stream"
	// DoneData is the payload of the event that ends a stream.
	DoneData = "[DONE]"
	// maxLine bounds the length of one line when reading.
	maxLine = 1 << 20
)

// Writer writes data events, flushing each one.
type Writer struct {
	w bodyWriter
	f flusher
}

// NewWriter sets the event stream content type on w and returns a Writer for
// it. Call it before writing the response status or body.
func NewWriter(w http.ResponseWriter) *Writer {
	w.Header().Set("Content-Type", ContentType)
	return &Writer{w: w, f: http.NewResponseController(w)}
}

// Data writes data as one event.
func (w *Writer) Data(data []byte) error {
	if _, err := fmt.Fprintf(w.w, "data: %s\n\n", data); err != nil {
		return fmt.Errorf("writing event: %w", err)
	}
	if err := w.f.Flush(); err != nil {
		return fmt.Errorf("flushing event: %w", err)
	}
	return nil
}

// JSON writes v, encoded as JSON, as one event.
func (w *Writer) JSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding event: %w", err)
	}
	return w.Data(data)
}

// Done writes the [DONE] event.
func (w *Writer) Done() error {
	return w.Data([]byte(DoneData))
}

// Reader reads the data payloads of an event stream.
type Reader struct {
	scanner *bufio.Scanner
	data    []byte
	done    bool
}

// NewReader returns a Reader over r.
func NewReader(r io.Reader) *Reader {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(nil, maxLine)
	return &Reader{scanner: scanner}
}

// Next advances to the next event's data. It returns false at the [DONE]
// event, at the end of the stream, or on error. An event cut off by the end
// of the stream is discarded, as the SSE specification requires.
func (r *Reader) Next() bool {
	var data [][]byte
	for !r.done && r.scanner.Scan() {
		line := r.scanner.Bytes()
		if len(line) == 0 {
			if data == nil {
				continue
			}
			r.data = bytes.Join(data, []byte("\n"))
			r.done = string(r.data) == DoneData
			return !r.done
		}
		if payload, ok := dataPayload(line); ok {
			data = append(data, bytes.Clone(payload))
		}
	}
	return false
}

// Data returns the current event's data.
func (r *Reader) Data() []byte {
	return r.data
}

// Done reports whether the stream ended with the [DONE] event.
func (r *Reader) Done() bool {
	return r.done
}

// Err returns the error that ended the stream, if any.
func (r *Reader) Err() error {
	return r.scanner.Err()
}

// DoneWatcher watches the bytes of an event stream as they pass, for callers
// that can't read the stream themselves. It is an io.Writer, so it can be fed
// with io.TeeReader.
type DoneWatcher struct {
	partial []byte
	seen    bool
}

// Write scans p for a [DONE] event. It never fails.
func (d *DoneWatcher) Write(p []byte) (int, error) {
	n := len(p)
	for !d.seen {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			d.partial = append(d.partial, p...)
			break
		}
		d.partial = append(d.partial, p[:i]...)
		payload, ok := dataPayload(d.partial)
		d.seen = ok && string(payload) == DoneData
		d.partial = d.partial[:0]
		p = p[i+1:]
	}
	return n, nil
}

// Seen reports whether a [DONE] event has been written.
func (d *DoneWatcher) Seen() bool {
	return d.seen
}

// dataPayload returns the value of a data field line, dropping the optional
// space after the colon and a trailing carriage return.
func dataPayload(line []byte) ([]byte, bool) {
	payload, ok := bytes.CutPrefix(bytes.TrimSuffix(line, []byte("\r")), []byte("data:"))
	if !ok {
		return nil, false
	}
	return bytes.TrimPrefix(payload, []byte(" ")), true
}
