package sddcaps_test

import (
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/sddcaps"
)

func TestAnAttachValueSplitsAsSddSplitsIt(t *testing.T) {
	cases := []struct {
		spec string
		want sddcaps.Attached
	}{
		{spec: "notes/run.log", want: sddcaps.Attached{Path: "notes/run.log", Name: "notes/run.log"}},
		{spec: "notes/run.log:run.log", want: sddcaps.Attached{Path: "notes/run.log", Name: "run.log"}},
		{spec: "c:/odd:dir/run.log:run.log", want: sddcaps.Attached{Path: "c:/odd:dir/run.log", Name: "run.log"}},
		{spec: "-:pasted.txt", want: sddcaps.Attached{Name: "pasted.txt", Stdin: true}},
	}

	for _, tc := range cases {
		t.Run(tc.spec, func(t *testing.T) {
			got := sddcaps.Attachment(tc.spec)
			if got != tc.want {
				t.Errorf("the value should split on its last colon, standard input on a lone dash, got %+v", got)
			}
		})
	}
}
