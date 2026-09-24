//go:build unix

package launcher

import (
	"os"
	"syscall"
)

// ownedByCurrentUser reports whether info describes a file the current user
// owns, so the legacy state cleanup never deletes a file another account put
// in the config directory.
func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return int(stat.Uid) == os.Getuid()
}
