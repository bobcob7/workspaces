//go:build unix

package config

import "os"

// UserBaseDir is the per-user config directory that CONFIG_DIR is built on.
func UserBaseDir() (string, error) {
	return os.UserConfigDir()
}
