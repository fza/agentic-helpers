package sddcaps

import "strings"

// Attached is one `sdd new --attach` value as sdd reads it: `path`,
// `source:target`, or `-:target` for standard input. Path is the file sdd
// reads, empty for standard input; Name is the file's name in the entry.
type Attached struct {
	Path  string
	Name  string
	Stdin bool
}

// Attachment splits an `--attach` value the way sdd does: on the last colon,
// so a source path may hold one.
func Attachment(spec string) Attached {
	index := strings.LastIndex(spec, ":")
	if index < 0 {
		return Attached{Path: spec, Name: spec}
	}

	source, target := spec[:index], spec[index+1:]
	if source == "-" {
		return Attached{Name: target, Stdin: true}
	}

	return Attached{Path: source, Name: target}
}
