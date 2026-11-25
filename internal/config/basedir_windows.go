package config

import "os"

// UserBaseDir is %LocalAppData%, not the roaming %AppData%: the repo cache
// and the workspaces are large and tied to this machine.
func UserBaseDir() (string, error) {
	return os.UserCacheDir()
}
