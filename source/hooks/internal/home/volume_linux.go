package home

import (
	"os"
	"path/filepath"
	"syscall"
)

// volumeID finds the UUID of the volume holding path among the links udev
// keeps under /dev/disk/by-uuid: the one whose device is the volume's. A
// volume udev names no UUID for, as a tmpfs, has none.
func volumeID(path string) string {
	var held syscall.Stat_t

	err := syscall.Stat(path, &held)
	if err != nil {
		return ""
	}

	links, err := os.ReadDir("/dev/disk/by-uuid")
	if err != nil {
		return ""
	}

	for _, link := range links {
		var device syscall.Stat_t

		err = syscall.Stat(filepath.Join("/dev/disk/by-uuid", link.Name()), &device)
		if err == nil && uint64(device.Rdev) == uint64(held.Dev) {
			return link.Name()
		}
	}

	return ""
}
