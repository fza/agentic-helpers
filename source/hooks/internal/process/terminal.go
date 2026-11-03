package process

import (
	"fmt"
	"strconv"
)

// darwinPtyMajor is the major number of darwin's pseudo-terminal devices,
// `/dev/ttysNNN`; darwin packs a device as eight bits of major over 24 of
// minor.
const darwinPtyMajor = 16

// Linux's pseudo-terminal devices take majors 136 to 143, 256 minors each,
// and its virtual consoles major 4 below minor 64.
const (
	linuxPtyMajor     = 136
	linuxPtyMajorLast = 143
	linuxConsoleMajor = 4
	linuxConsoleLast  = 63
)

// DarwinTerminal is the device file darwin names a controlling terminal of
// device by, empty for a device no pseudo-terminal is.
func DarwinTerminal(device uint32) string {
	if device>>24 != darwinPtyMajor {
		return ""
	}

	return fmt.Sprintf("/dev/ttys%03d", device&0xffffff)
}

// LinuxTerminal is the device file Linux names a controlling terminal of
// major and minor by, empty for a device that is neither a pseudo-terminal
// nor a virtual console.
func LinuxTerminal(major uint32, minor uint32) string {
	switch {
	case major >= linuxPtyMajor && major <= linuxPtyMajorLast:
		return "/dev/pts/" + strconv.FormatUint(uint64((major-linuxPtyMajor)<<8+minor), 10)
	case major == linuxConsoleMajor && minor <= linuxConsoleLast:
		return "/dev/tty" + strconv.FormatUint(uint64(minor), 10)
	default:
		return ""
	}
}
