package cache

import (
	"sync"
)

// maxWorkers bounds how many repos are worked on at once. Each is a git
// process, local for `list` and a network fetch for `update`; 8 overlaps
// enough of git's start-up time and network latency to cut the wall time
// several-fold, without opening more connections to one forge than a person
// running a few terminals would.
const maxWorkers = 8

// forEach calls fn for 0 through n-1, at most maxWorkers at a time, and
// returns once every call has.
func forEach(n int, fn func(i int)) {
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup
	for i := range n {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			fn(i)
		})
	}
	wg.Wait()
}
