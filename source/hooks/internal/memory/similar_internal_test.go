package memory

import (
	"fmt"
	"testing"
)

func TestStandoutsNeedTheFloorAndTheDeviation(t *testing.T) {
	cases := []struct {
		name  string
		top   float32
		rest  []float32
		found bool
	}{
		{name: "standing out above the floor", top: 0.75, rest: []float32{0.1, 0.12}, found: true},
		{name: "standing out below the floor", top: 0.55, rest: []float32{0.1, 0.12}, found: false},
		{name: "above the floor among peers as close", top: 0.8, rest: []float32{0.7, 0.9}, found: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var peers []*entry

			scores := map[string]float32{}

			for index := range 12 {
				id := fmt.Sprintf("m-peer%02d", index)
				peers = append(peers, &entry{id: id, title: id})
				scores[id] = tc.rest[index%len(tc.rest)]
			}

			peers = append(peers, &entry{id: "m-top", title: "top"})
			scores["m-top"] = tc.top

			found := standouts(peers, scores)
			if tc.found != (len(found) == 1 && found[0].id == "m-top") {
				t.Errorf("the closest peer should be named only when it stands out and reaches the floor, got %v", found)
			}
		})
	}
}
