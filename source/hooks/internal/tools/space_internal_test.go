package tools

import (
	"math"
	"testing"
)

func TestHashedBytesStaysWithinTheToolCap(t *testing.T) {
	cases := []struct {
		name   string
		sizes  []int64
		want   int64
		reason string
	}{
		{name: "recorded sizes", sizes: []int64{100, 200}, want: 300, reason: "sizes within the cap should sum"},
		{name: "a negative size", sizes: []int64{100, -1 << 40}, want: 100, reason: "a negative size should count nothing"},
		{name: "a size past the cap", sizes: []int64{1 << 40}, want: toolLimit, reason: "a version should never count past the cap"},
		{
			name: "sizes that would overflow", sizes: []int64{math.MaxInt64, math.MaxInt64}, want: toolLimit,
			reason: "a sum should saturate, never wrap",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hashes := make([]fileHash, 0, len(tc.sizes))
			for _, size := range tc.sizes {
				hashes = append(hashes, fileHash{Size: size})
			}

			if got := hashedBytes(hashes); got != tc.want {
				t.Errorf("%s, got %d", tc.reason, got)
			}
		})
	}
}
