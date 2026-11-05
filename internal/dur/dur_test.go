package dur

import (
	"testing"
	"time"
)

func TestFormat(t *testing.T) {
	for d, want := range map[time.Duration]string{
		120 * time.Second:       "120s",
		30 * time.Minute:        "30m",
		30 * time.Second:        "30s",
		10 * time.Minute:        "10m",
		200 * time.Second:       "200s",
		50 * time.Millisecond:   "50ms",
		5 * time.Minute:         "300s",
		1500 * time.Millisecond: "1.5s",
	} {
		if got := Format(d); got != want {
			t.Errorf("Format(%v) = %q, want %q", d, got, want)
		}
	}
}
