package home

import (
	"encoding/hex"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// volumeID asks getattrlist(2) for the UUID of the volume holding path, which
// stays the volume's across mounts and reboots where st_dev does not. The
// reply is a length, then the UUID's sixteen bytes; x/sys carries the
// constants and layout but no wrapper for the call.
func volumeID(path string) string {
	request := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Volattr: unix.ATTR_VOL_INFO | unix.ATTR_VOL_UUID}
	reply := make([]byte, 4+16+4)

	name, err := syscall.BytePtrFromString(path)
	if err != nil {
		return ""
	}

	_, _, errno := syscall.Syscall6(unix.SYS_GETATTRLIST, uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(&request)), uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)), 0, 0)
	if errno != 0 {
		return ""
	}

	uuid := reply[4:20]
	if strings.Trim(hex.EncodeToString(uuid), "0") == "" {
		return ""
	}

	return formatUUID(uuid)
}

func formatUUID(raw []byte) string {
	text := strings.ToUpper(hex.EncodeToString(raw))

	return strings.Join([]string{text[0:8], text[8:12], text[12:16], text[16:20], text[20:32]}, "-")
}
