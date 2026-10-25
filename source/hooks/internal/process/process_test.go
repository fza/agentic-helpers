package process_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestParseLstart(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("loading a zone: %v", err)
	}

	cases := []struct {
		name     string
		text     string
		want     time.Time
		readable bool
	}{
		{
			name: "C locale text", text: "Sat Oct 24 14:35:50 2026\n",
			want: time.Date(2026, time.October, 24, 14, 35, 50, 0, berlin), readable: true,
		},
		{
			name: "a one-digit day padded with a space", text: "Mon Oct  5 09:01:02 2026",
			want: time.Date(2026, time.October, 5, 9, 1, 2, 0, berlin), readable: true,
		},
		{name: "German text", text: "Sa. 24 Okt. 14:35:50 2026"},
		{name: "nothing", text: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, readable := process.ParseLstart(tc.text, berlin)
			if readable != tc.readable || !got.Equal(tc.want) {
				t.Errorf("the text should read as its instant in the given zone, or not at all, got %v %v", got, readable)
			}
		})
	}
}

func TestStartTimeAsksPsInTheCLocale(t *testing.T) {
	bin := t.TempDir()
	test.WriteFile(t, filepath.Join(bin, "ps"), test.Fixture(t, "process/ps.sh"))

	err := os.Chmod(filepath.Join(bin, "ps"), 0o755)
	if err != nil {
		t.Fatalf("making the fake ps runnable: %v", err)
	}

	t.Setenv("PATH", bin)
	t.Setenv("LC_ALL", "de_DE.UTF-8")

	got, found := process.Table{}.StartTime(context.Background(), os.Getpid())

	want, _ := process.ParseLstart("Sat Oct 24 14:35:50 2026", time.Local)
	if !found || !got.Equal(want) {
		t.Errorf("ps should run under the C locale whatever the caller's, got %v %v", got, found)
	}
}
