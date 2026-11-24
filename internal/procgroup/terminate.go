package procgroup

import (
	"errors"
	"fmt"
)

// terminateAll calls terminate for each pid in turn, including after one
// fails, and joins the failures, so one process that can't be ended doesn't
// leave the rest of its tree running.
func terminateAll(pids []uint32, terminate func(uint32) error) error {
	var errs []error
	for _, pid := range pids {
		if err := terminate(pid); err != nil {
			errs = append(errs, fmt.Errorf("terminating process %d: %w", pid, err))
		}
	}
	return errors.Join(errs...)
}

// killOrder is the order to end a tree in: descendants first, deepest first,
// then root. descendants lists breadth first, so reversing it ends the leaves
// before the processes that started them.
func killOrder(root uint32, descendants []uint32) []uint32 {
	order := make([]uint32, 0, len(descendants)+1)
	for i := len(descendants) - 1; i >= 0; i-- {
		order = append(order, descendants[i])
	}
	return append(order, root)
}
