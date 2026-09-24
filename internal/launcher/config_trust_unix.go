//go:build unix

package launcher

import (
	"fmt"
	"os"
	"syscall"
)

// groupOrOtherWritable is the part of a file's mode that lets someone other
// than its owner rewrite it: the group-write and other-write bits.
const groupOrOtherWritable os.FileMode = 0o022

// configTrusted reports whether the config file at path may have its
// api_key_cmd lines run, and returns a refusal naming the file and the fix
// when it may not. A file is trusted when the current user owns it and neither
// its group nor anyone else can write it — exactly the premise that makes
// running its lines through a shell the user's own act: whoever can write the
// file can run any command as this user.
//
// The file is judged through os.Stat, so a symlinked config is judged by the
// file it points at, which is the one whose bytes were read.
func configTrusted(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("config: %s: checking who can write the file before running its api_key_cmd: %w", path, err)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("config: %s: cannot tell who owns the file, so its api_key_cmd is not run", path)
	}

	uid := os.Getuid()
	if int(stat.Uid) != uid {
		return fmt.Errorf("config: %s is owned by uid %d, not by you (uid %d) — api_key_cmd runs as you, so it is "+
			"run only from a config you own and only you can write; make the file yours, then `chmod 600 %s`",
			path, stat.Uid, uid, path)
	}
	if perm := info.Mode().Perm(); perm&groupOrOtherWritable != 0 {
		return fmt.Errorf("config: %s has mode %04o, which lets others rewrite it — api_key_cmd runs as you, so it "+
			"is run only from a config only you can write; fix with `chmod 600 %s`", path, perm, path)
	}
	return nil
}
