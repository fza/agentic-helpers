package pluginconfig_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
)

func TestSecretReadSkipsParseEachEntryAndNameWhatTheyIgnore(t *testing.T) {
	cases := []struct {
		name     string
		value    string
		entries  []pluginconfig.SecretReadSkip
		problems []string
	}{
		{name: "unset", value: ""},
		{
			name:    "several globs and entries, spaced",
			value:   " ~/code/app = test/fixtures/** ; *.pem , /srv/x=a.yaml,",
			entries: []pluginconfig.SecretReadSkip{{Project: "~/code/app", Globs: []string{"test/fixtures/**", "*.pem"}}, {Project: "/srv/x", Globs: []string{"a.yaml"}}},
		},
		{
			name:     "no glob",
			value:    "~/a=",
			problems: []string{"shell_guard_secret_read_skip: ~/a= ignored; it is no <project>=<glob>[;<glob>] entry"},
		},
		{
			name:     "relative project",
			value:    "app=x",
			problems: []string{"shell_guard_secret_read_skip: app=x ignored; app is neither absolute nor under ~"},
		},
		{
			name:     "absolute glob",
			value:    "~/a=/etc/*",
			problems: []string{"shell_guard_secret_read_skip: ~/a=/etc/* ignored; /etc/* is not relative to the project"},
		},
		{
			name:     "climbing glob",
			value:    "~/a=../b/*",
			problems: []string{"shell_guard_secret_read_skip: ~/a=../b/* ignored; ../b/* climbs through .."},
		},
		{
			name:     "broken glob, the next entry kept",
			value:    "~/a=[x, ~/b=y",
			entries:  []pluginconfig.SecretReadSkip{{Project: "~/b", Globs: []string{"y"}}},
			problems: []string{"shell_guard_secret_read_skip: ~/a=[x ignored; [x is no glob"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries, problems := pluginconfig.SecretReadSkips(tc.value)

			if !reflect.DeepEqual(entries, tc.entries) {
				t.Errorf("each entry that parses should be read as written: got %+v", entries)
			}

			if !slices.Equal(problems, tc.problems) {
				t.Errorf("each entry ignored should be named with why: got %v", problems)
			}
		})
	}
}
