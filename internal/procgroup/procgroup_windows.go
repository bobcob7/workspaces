package procgroup

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// OwnGroup gives a process a process group of its own, so a Ctrl-C at
// ws's console doesn't reach it.
func OwnGroup() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

// Detached starts a process with no console, in a process group of its own,
// so it outlives ws.
func Detached() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS}
}

// KillTree terminates pid and every descendant of it that started after
// its parent did, leaves first. A process that can't be ended doesn't stop
// the rest; the failures come back joined. Windows has no signals between
// processes, so Signal kills too.
func KillTree(pid int) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return fmt.Errorf("listing processes: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()
	entries, err := processEntries(snapshot)
	if err != nil {
		return fmt.Errorf("listing processes: %w", err)
	}
	root := uint32(pid) //nolint:gosec // Windows process ids fit in 32 bits
	return terminateAll(killOrder(root, descendants(root, entries, creationTime)), terminate)
}

// Signal kills pgid's process tree: Windows has no graceful stop between
// processes.
func Signal(pgid int, _ syscall.Signal) error {
	return KillTree(pgid)
}

// processEntries reads every process in a Toolhelp32 snapshot.
func processEntries(snapshot windows.Handle) ([]windows.ProcessEntry32, error) {
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	var entries []windows.ProcessEntry32
	err := windows.Process32First(snapshot, &entry)
	for ; err == nil; err = windows.Process32Next(snapshot, &entry) {
		entries = append(entries, entry)
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, err
	}
	return entries, nil
}

// descendants returns the processes below root, breadth first. A child is
// kept only if its creation time, which created reports, is not before its
// parent's: a pid reused by an unrelated process started later.
func descendants(root uint32, entries []windows.ProcessEntry32, created func(pid uint32) (windows.Filetime, bool)) []uint32 {
	children := make(map[uint32][]uint32)
	for i := range entries {
		children[entries[i].ParentProcessID] = append(children[entries[i].ParentProcessID], entries[i].ProcessID)
	}
	seen := map[uint32]bool{root: true}
	queue := []uint32{root}
	var found []uint32
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		parentTime, parentOK := created(parent)
		for _, pid := range children[parent] {
			if seen[pid] {
				continue
			}
			seen[pid] = true
			childTime, childOK := created(pid)
			if !childOK || !parentOK || filetimeTicks(childTime) < filetimeTicks(parentTime) {
				continue
			}
			found = append(found, pid)
			queue = append(queue, pid)
		}
	}
	return found
}

// creationTime reports when the process pid started, and false if it has
// exited or can't be inspected.
func creationTime(pid uint32) (windows.Filetime, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return windows.Filetime{}, false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return windows.Filetime{}, false
	}
	return created, true
}

// terminate ends pid. A process that has already exited is not an error.
func terminate(pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	return windows.TerminateProcess(h, 1)
}

// filetimeTicks joins ft's two halves into one count of 100ns ticks.
func filetimeTicks(ft windows.Filetime) uint64 {
	return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
}
