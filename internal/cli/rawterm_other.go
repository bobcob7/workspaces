//go:build !linux && !darwin

package cli

// makeRaw refuses: remote terminals need Linux or macOS.
func makeRaw(int) (func(), error) {
	return nil, usageErrorf("remote terminals aren't supported on this platform")
}

// terminalSize refuses, as makeRaw does.
func terminalSize(int) (cols, rows int, err error) {
	return 0, 0, usageErrorf("remote terminals aren't supported on this platform")
}
