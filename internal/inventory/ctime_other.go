//go:build !darwin

package inventory

import (
	"os"
	"time"
)

// changeTime is unavailable on this platform; size and mtime are still checked.
func changeTime(os.FileInfo) time.Time { return time.Time{} }
