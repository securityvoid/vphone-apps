// Package dur formats deadlines for `timed out after <d>` messages.
package dur

import (
	"fmt"
	"time"
)

// Format renders whole minutes when d is a multiple of 10 minutes (30m), else
// whole seconds (120s), else Duration.String (50ms).
func Format(d time.Duration) string {
	switch {
	case d >= 10*time.Minute && d%(10*time.Minute) == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	case d >= time.Second && d%time.Second == 0:
		return fmt.Sprintf("%ds", d/time.Second)
	}
	return d.String()
}
