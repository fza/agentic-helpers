package scratch

import "testing"

func TestIsSession(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{name: "c3fb65eb-dbdf-47a8-9e04-e0477053b2b6", want: true},
		{name: "C3FB65EB-DBDF-47A8-9E04-E0477053B2B6", want: false},
		{name: "c3fb65eb-dbdf-47a8-9e04-e0477053b2b", want: false},
		{name: "c3fb65eb-dbdf-47a8-9e04-e0477053b2b6x", want: false},
		{name: "keep", want: false},
		{name: "", want: false},
		{name: "../c3fb65eb-dbdf-47a8-9e04-e0477053b2b6", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if isSession(tc.name) != tc.want {
				t.Error("only a lowercase session identifier should count as a session")
			}
		})
	}
}
