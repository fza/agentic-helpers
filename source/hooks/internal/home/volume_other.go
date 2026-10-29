//go:build !darwin && !linux

package home

// volumeID knows no volume UUID on this platform: a checkout is matched by
// its path and pointer alone.
func volumeID(string) string { return "" }
