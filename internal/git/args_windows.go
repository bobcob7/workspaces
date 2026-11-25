package git

// platformArgs lets checkouts go past MAX_PATH (260 characters) unless the
// machine's LongPathsEnabled policy already does.
func platformArgs() []string {
	return []string{"-c", "core.longpaths=true"}
}
