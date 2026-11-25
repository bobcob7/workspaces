package artifact

// syncDirectory does nothing: NTFS journals the rename, and FlushFileBuffers
// refuses a directory handle.
func syncDirectory(string) error {
	return nil
}
