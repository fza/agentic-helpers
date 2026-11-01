package pluginconfig_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
)

func TestExtraRootsParseEachEntryAndNameWhatTheyIgnore(t *testing.T) {
	cases := []struct {
		name     string
		value    string
		entries  []pluginconfig.ExtraRoot
		problems []string
	}{
		{name: "unset", value: ""},
		{
			name:    "one root",
			value:   "~/Codepot/devops/format-d-fdbox=~/Codepot/sources/sdd-autopilot-python/.tmp",
			entries: []pluginconfig.ExtraRoot{{Project: "~/Codepot/devops/format-d-fdbox", Roots: []string{"~/Codepot/sources/sdd-autopilot-python/.tmp"}}},
		},
		{
			name:  "several roots and entries, spaced and cleaned",
			value: " /a = /b/ ; ~/c/./d , ~/e=/f,",
			entries: []pluginconfig.ExtraRoot{
				{Project: "/a", Roots: []string{"/b", "~/c/d"}}, {Project: "~/e", Roots: []string{"/f"}},
			},
		},
		{
			name:     "no pair",
			value:    "/a, /c=/d",
			entries:  []pluginconfig.ExtraRoot{{Project: "/c", Roots: []string{"/d"}}},
			problems: []string{"shell_guard_extra_roots: /a ignored; it is no <project>=<root>[;<root>] entry"},
		},
		{
			name:     "no project",
			value:    "=/b",
			problems: []string{"shell_guard_extra_roots: =/b ignored; it is no <project>=<root>[;<root>] entry"},
		},
		{
			name:    "an empty root among others",
			value:   "/a=/b;",
			entries: []pluginconfig.ExtraRoot{{Project: "/a", Roots: []string{"/b"}}},
		},
		{
			name:     "no root",
			value:    "/a=",
			problems: []string{"shell_guard_extra_roots: /a= ignored; it is no <project>=<root>[;<root>] entry"},
		},
		{
			name:     "a relative root",
			value:    "/a=/b;c/d",
			problems: []string{"shell_guard_extra_roots: /a=/b;c/d ignored; c/d is neither absolute nor under ~"},
		},
		{
			name:     "a relative project",
			value:    "a=/b",
			problems: []string{"shell_guard_extra_roots: a=/b ignored; a is neither absolute nor under ~"},
		},
		{
			name:     "another user's home",
			value:    "/a=~bob/x",
			problems: []string{"shell_guard_extra_roots: /a=~bob/x ignored; ~bob/x is neither absolute nor under ~"},
		},
		{
			name:     "a root climbing through ..",
			value:    "/a=~/../x",
			problems: []string{"shell_guard_extra_roots: /a=~/../x ignored; ~/../x climbs through .."},
		},
		{
			name:     "the filesystem root",
			value:    "/a=/",
			problems: []string{"shell_guard_extra_roots: /a=/ ignored; / is the filesystem root or the home directory"},
		},
		{
			name:     "the home directory",
			value:    "/a=~/",
			problems: []string{"shell_guard_extra_roots: /a=~/ ignored; ~/ is the filesystem root or the home directory"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries, problems := pluginconfig.ExtraRoots(tc.value)

			if !reflect.DeepEqual(entries, tc.entries) {
				t.Errorf("each well-formed entry should be kept cleaned, got %#v", entries)
			}

			if !slices.Equal(problems, tc.problems) {
				t.Errorf("each ignored entry should be named with why, got %q", problems)
			}
		})
	}
}
