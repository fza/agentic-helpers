package slots

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// twinsOf is the header, the pair holding key and the side holding its newest
// value.
func twinsOf(t *testing.T, file *File, key string) (Header, int, int) {
	t.Helper()

	opened, err := os.Open(file.Path)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}

	defer func() { _ = opened.Close() }()

	header, err := readHeader(opened)
	if err != nil {
		t.Fatalf("reading the header: %v", err)
	}

	pair, twins, found, err := find(opened, header, key)
	if err != nil || !found {
		t.Fatalf("finding %s: %v", key, err)
	}

	winner := newest(twins)

	return header, pair, winner
}

func TestATornWriteLeavesTheTwinReadable(t *testing.T) {
	cases := []struct {
		name string
		tear func(slot []byte) []byte
	}{
		{name: "cut after its head", tear: func(slot []byte) []byte { return slot[:slotHead+10] }},
		{name: "cut inside its head", tear: func(slot []byte) []byte { return slot[:12] }},
		{name: "a value byte flipped", tear: func(slot []byte) []byte {
			torn := bytes.Clone(slot)
			torn[len(torn)-3] ^= 0x40

			return torn
		}},
		{name: "a head byte flipped", tear: func(slot []byte) []byte {
			torn := bytes.Clone(slot)
			torn[9] ^= 0x01

			return torn
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			file := Open(dir, filepath.Join(dir, "sessions.slots"), 4096)
			older := bytes.Repeat([]byte("older;"), 30)

			err := file.Put(context.Background(), "ladder/s1", []byte("oldest"))
			if err == nil {
				err = file.Put(context.Background(), "ladder/s1", older)
			}

			if err != nil {
				t.Fatalf("putting: %v", err)
			}

			header, pair, winner := twinsOf(t, file, "ladder/s1")
			next := bytes.Repeat([]byte("newer;"), 30)
			slot := encodeSlot("ladder/s1", next, 99, time.Now())

			opened, err := os.OpenFile(file.Path, os.O_RDWR, 0)
			if err != nil {
				t.Fatalf("opening: %v", err)
			}

			_, err = opened.WriteAt(tc.tear(slot), twinOffset(header, pair, 1-winner))
			_ = opened.Close()

			if err != nil {
				t.Fatalf("tearing: %v", err)
			}

			value, found, err := file.Get(context.Background(), "ladder/s1")
			if err != nil || !found || !bytes.Equal(value, older) {
				t.Errorf("a torn write should read as the twin before it, got %q, %v", value, err)
			}

			err = file.Put(context.Background(), "ladder/s1", []byte("after"))
			if err != nil {
				t.Fatalf("putting after the tear: %v", err)
			}

			value, _, _ = file.Get(context.Background(), "ladder/s1")
			if string(value) != "after" {
				t.Errorf("a write after a torn one should read back, got %q", value)
			}
		})
	}
}
