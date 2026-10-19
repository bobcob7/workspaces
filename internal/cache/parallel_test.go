package cache

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestForEach(t *testing.T) {
	t.Run("calls fn once per index", func(t *testing.T) {
		var mu sync.Mutex
		seen := map[int]int{}
		forEach(20, func(i int) {
			mu.Lock()
			defer mu.Unlock()
			seen[i]++
		})
		assert.Len(t, seen, 20)
		for i := range 20 {
			assert.Equal(t, 1, seen[i], i)
		}
	})
	t.Run("no calls for zero", func(t *testing.T) {
		called := false
		forEach(0, func(int) { called = true })
		assert.False(t, called)
	})
	t.Run("runs at most maxWorkers at once", func(t *testing.T) {
		var active, peak atomic.Int32
		forEach(maxWorkers*3, func(int) {
			n := active.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			active.Add(-1)
		})
		assert.LessOrEqual(t, peak.Load(), int32(maxWorkers))
		assert.Greater(t, peak.Load(), int32(1))
	})
}
