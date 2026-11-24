//go:build darwin

package cli

import "golang.org/x/sys/unix"

// makeRaw puts the terminal on fd in raw mode, as cfmakeraw does, and
// returns what restores its previous mode.
func makeRaw(fd int) (func(), error) {
	old, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return nil, usageErrorf("stdin isn't a terminal: %v", err)
	}
	raw := *old
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, &raw); err != nil {
		return nil, usageErrorf("couldn't put the terminal in raw mode: %v", err)
	}
	return func() { _ = unix.IoctlSetTermios(fd, unix.TIOCSETA, old) }, nil
}

// terminalSize is the size of the terminal on fd.
func terminalSize(fd int) (cols, rows int, err error) {
	ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, err
	}
	return int(ws.Col), int(ws.Row), nil
}
