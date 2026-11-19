package fakemcp

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"sync"
	"time"
)

// recorder appends one JSON line per tool call, for test assertions. A nil
// recorder records nothing.
type recorder struct {
	mu sync.Mutex
	w  io.Writer
}

type callLine struct {
	Time      time.Time      `json:"time"`
	Tool      string         `json:"tool"`
	Arguments jsontext.Value `json:"arguments"`
}

func (r *recorder) write(tool string, canonicalArgs []byte) error {
	if r == nil {
		return nil
	}
	line, err := json.Marshal(callLine{Time: time.Now(), Tool: tool, Arguments: canonicalArgs})
	if err != nil {
		return fmt.Errorf("encoding call record: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.w.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("writing call record: %w", err)
	}
	return nil
}
