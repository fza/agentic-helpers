package home

import (
	"golang.org/x/sys/unix"
)

// BackupExclusion is the extended attribute Time Machine skips an item by,
// the sticky mark `tmutil addexclusion` sets without `-p`: it travels with the
// folder through a move or a restore, where a path exclusion needs root and
// breaks once the folder moves.
const BackupExclusion = "com.apple.metadata:com_apple_backup_excludeItem"

// backupExclusionValue is the value `tmutil addexclusion` writes: the string
// `com.apple.backupd` as a binary property list.
var backupExclusionValue = []byte("bplist00_\x10\x11com.apple.backupd\x08\x00\x00\x00\x00\x00\x00\x01\x01" +
	"\x00\x00\x00\x00\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x1c")

func excludedFromBackups(path string) bool {
	size, err := unix.Getxattr(path, BackupExclusion, nil)

	return err == nil && size > 0
}

func excludeFromBackups(path string) error {
	return unix.Setxattr(path, BackupExclusion, backupExclusionValue, 0)
}
