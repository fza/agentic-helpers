package shellwalk

import "testing"

func TestDirnameReadsAPathAsPOSIXDoes(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{name: "scripts/x.sh", want: "scripts"},
		{name: "a/b/x.sh", want: "a/b"},
		{name: "a//x.sh", want: "a"},
		{name: "x.sh", want: "."},
		{name: "./x.sh", want: "."},
		{name: "/x.sh", want: "/"},
		{name: "/opt/x.sh", want: "/opt"},
		{name: "scripts/x.sh/", want: "scripts"},
		{name: "/", want: "/"},
		{name: "", want: "."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := posixDirname(tc.name)
			if got != tc.want {
				t.Errorf("the folder should be what dirname prints, got %q", got)
			}
		})
	}
}
