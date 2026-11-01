package home

import (
	"github.com/fza/agentic-helpers/source/hooks/internal/formatsteps"
)

// ErrNewerFormat is a component's directory written by a newer release.
var ErrNewerFormat = formatsteps.ErrNewer

// ComponentFormat is the format the component at dir records in its own
// `format.json`; absent, a directory no release recorded one for, which
// reads as format 1.
func ComponentFormat(dir string) (int, error) {
	return formatsteps.Read(dir)
}

// CheckComponent is the component's format, refusing one newer than current.
func CheckComponent(dir string, current int) (int, error) {
	return formatsteps.Check(dir, dir, current)
}

// WriteComponentFormat records version as the component's format.
func WriteComponentFormat(root string, dir string, version int) error {
	return formatsteps.Write(root, dir, version)
}
