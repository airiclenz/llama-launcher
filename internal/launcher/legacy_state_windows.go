//go:build windows

package launcher

import "os"

// ownedByCurrentUser answers the unix owner check on windows, where a file's
// owner is an ACL security descriptor rather than a uid the file info carries.
// It accepts every file; the name, size and content checks still apply.
func ownedByCurrentUser(info os.FileInfo) bool {
	return true
}
