//go:build darwin

package inventory

import (
	"os"
	"syscall"
	"time"
)

func changeTime(info os.FileInfo) time.Time {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}
	}
	return time.Unix(stat.Ctimespec.Sec, stat.Ctimespec.Nsec)
}
